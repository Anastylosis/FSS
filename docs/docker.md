# Docker

FSS ships as a multi-arch container image on the GitHub Container Registry. Built from a single static Go binary on Alpine — final image is ~33 MB.

## Image

| Registry | Path |
|----------|------|
| GHCR | `ghcr.io/anastylosis/fss` |

Architectures: `linux/amd64`, `linux/arm64` (Pi 4/5, Apple Silicon Linux VMs, ARM cloud instances).

### Tags

| Tag | Tracks |
|-----|--------|
| `latest` | Latest released version (updated only when a `v*` tag is pushed and the manual smoke-test gate is approved) |
| `X.Y.Z`, `X.Y`, `X` | Specific release / minor / major track, without the tag's `v` (also gated by `manual-smoke-gate`) |
| `master` | Tip of the default branch (development build, not gated) |
| `sha-<short>` | Specific commit (development) |

Pin to `X.Y` for stable systems; `latest` is fine for personal use.

## Quick start

```bash
docker run --rm ghcr.io/anastylosis/fss:latest list-scrapers
```

The default `ENTRYPOINT` is `fss`, so any subcommand and flags work directly:

```bash
docker run --rm ghcr.io/anastylosis/fss:latest scrape --help
```

## Volumes

The image declares two volumes:

| Path | Purpose |
|------|---------|
| `/data` | Scrape output (JSON, CSV, SQLite). The container's working directory. |
| `/config` | Configuration. The image sets `XDG_CONFIG_HOME=/config`, so put your YAML at `/config/fss/config.yaml`. |

### The bind-mount UID gotcha

The image runs as a non-root user (`fss`, uid 100). When you bind-mount a host directory, **the host directory's owner has to match** — otherwise the container can't write to it.

The portable fix is to override the user at run time:

```bash
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/data:/data" \
  ghcr.io/anastylosis/fss:latest \
  scrape https://www.manyvids.com/Profile/1000001/jane-eyre/Store/Videos
```

If you use Docker named volumes instead of bind mounts, ownership is handled automatically and `--user` is not needed:

```bash
docker volume create fss-data
docker run --rm -v fss-data:/data ghcr.io/anastylosis/fss:latest scrape <url>
```

## Configuration

```bash
mkdir -p ./config/fss
cat > ./config/fss/config.yaml <<'YAML'
workers: 5
output: json,csv
out_dir: /data

stash:
  url: http://stash:9999
  tag: fss_import
YAML

docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/data:/data" \
  -v "$PWD/config:/config" \
  ghcr.io/anastylosis/fss:latest \
  scrape https://example.com/...
```

The Stash API key should never go in the YAML — pass it via env var:

```bash
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/data:/data" \
  -v "$PWD/config:/config" \
  -e FSS_STASH_API_KEY \
  ghcr.io/anastylosis/fss:latest \
  stash import --apply
```

## Talking to a dockerized Stash

Put both containers on the same Docker network and use the Stash service name as the host:

```yaml
# compose.yaml
services:
  stash:
    image: stashapp/stash:latest
    ports:
      - "9999:9999"
    volumes:
      - ./stash-config:/root/.stash
      - ./stash-data:/data
    networks:
      - stashnet

  fss:
    image: ghcr.io/anastylosis/fss:latest
    user: "1000:1000"
    volumes:
      - ./fss-data:/data
      - ./fss-config:/config
    environment:
      - FSS_STASH_API_KEY=${FSS_STASH_API_KEY}
    networks:
      - stashnet
    # FSS is a CLI — no long-running process. Override the command per task:
    entrypoint: ["sleep", "infinity"]

networks:
  stashnet:
```

Then:

```bash
# scrape on demand
docker compose exec fss fss scrape https://www.manyvids.com/Profile/...

# import to Stash (use the service name as the URL)
docker compose exec fss fss stash import \
  --url http://stash:9999 \
  --dir /data \
  --apply
```

## Scheduled scraping (cron)

The simplest pattern is a host cron entry that runs `docker run --rm`:

```cron
# Every day at 03:00 — refresh metadata for one studio
0 3 * * * docker run --rm --user 1000:1000 \
  -v /srv/fss/data:/data \
  -v /srv/fss/config:/config \
  ghcr.io/anastylosis/fss:X.Y \
  scrape --refresh https://www.manyvids.com/Profile/1000001/jane-eyre/Store/Videos \
  >> /var/log/fss.log 2>&1
```

For systemd-based hosts, a `.timer` + `.service` pair is more discoverable than cron — see `systemctl --user` if you'd rather not touch root crontabs.

## Building locally

```bash
docker build \
  --build-arg VERSION=dev \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t fss:dev .
```

### Build arguments

| Arg | Default | Purpose |
|-----|---------|---------|
| `GO_VERSION` | `1.26.5` | Builder image: `golang:${GO_VERSION}-alpine`. Bump when `go.mod` requires a newer Go. |
| `ALPINE_VERSION` | `3.21` | Runtime image: `alpine:${ALPINE_VERSION}`. Bump for security updates to the runtime base. |
| `VERSION` | `dev` | Embedded in `fss version` output. CI sets this to the git tag. |
| `COMMIT` | `none` | Embedded in `fss version` output. CI sets this to the short SHA. |
| `DATE` | `unknown` | Embedded in `fss version` output. CI sets this to the build timestamp. |

Example with overrides:

```bash
docker build --build-arg GO_VERSION=1.26 --build-arg ALPINE_VERSION=3.22 -t fss:dev .
```

For multi-arch local builds you need buildx and QEMU:

```bash
docker buildx create --use --name fss-builder
docker buildx build --platform linux/amd64,linux/arm64 -t fss:dev .
```

## CI/CD

Two workflows handle the image, split by purpose so a release can never ship a docker image without the manual smoke-test approval. Both are thin callers of `Anastylosis/.github/.github/workflows/docker-publish.yml@v1`, which differs between them only in `mode` — the QEMU/buildx setup, build-args, SLSA attestation and Trivy scan are one shared implementation.

| Workflow | Trigger | Tags pushed |
|----------|---------|-------------|
| `.github/workflows/docker.yml` (`mode: dev`) | Push to `main`/`master` | `master`, `sha-<short>` |
| `.github/workflows/docker.yml` (`mode: dev`) | Pull request touching the Dockerfile | _(builds, no push)_ |
| `.github/workflows/docker.yml` (`mode: dev`) | Manual `workflow_dispatch` | `master`, `sha-<short>` |
| `.github/workflows/release.yml` (`docker` job, after `release`) | Push of `v*` tag, **after `manual-smoke-gate` approval** | `X.Y.Z`, `X.Y`, `X`, `latest` |

Release image tags carry **no `v`**: `docker/metadata-action`'s semver patterns strip it, so tag `v1.28.1` publishes `ghcr.io/anastylosis/fss:1.28.1`. Only the archive names and `go install` keep the `v`.

The release flow is sequential: tag push → `build` (tests + binaries) → wait for `manual-smoke-gate` approval → `release` (GitHub Release) → `docker` (multi-arch image to GHCR with the version tags). Reject the gate and neither the GitHub Release nor the docker image lands.

Build cache is stored in GitHub Actions cache (`type=gha`) so subsequent builds reuse downloaded modules and compiled stdlib. Multi-arch builds run via QEMU emulation.

## Troubleshooting

**`Permission denied` on `/data`** — see the bind-mount UID gotcha above. Add `--user "$(id -u):$(id -g)"` or switch to a named volume.

**`No FSS scenes found in /data`** — the Stash import looks at `--dir` (defaults to the working directory `/data`). Make sure you mounted the directory containing your `*.json` scrape output.

**Stash connection refused** — when both run in Docker, FSS must use the Stash *service name* on the shared network (`http://stash:9999`), not `localhost:9999`. Localhost inside a container is the container itself.

**Image is not being updated by `docker pull`** — Docker caches by tag. For `latest` and `master`, use `docker pull ghcr.io/anastylosis/fss:latest` explicitly before `docker run`, or pin to an `X.Y.Z` tag and bump it deliberately.

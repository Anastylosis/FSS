# Installing FSS

Pick one — Homebrew or a system package for auto-updates, a pre-built binary if you would rather not add a tap, Docker if you prefer containers, source if you want to hack on it.

Already installed? `fss version` reports the build and checks for a newer release; `fss version --update` installs it, or names the package-manager command to run when your copy is managed by one. See [usage.md](usage.md#fss-version).

## Homebrew (macOS and Linux)

```bash
brew install anastylosis/tap/fss
fss version
```

`brew upgrade` picks up new releases. The formula installs the same pre-built
binary as the archives below (macOS and Linux, arm64 and amd64), so it needs
no Go toolchain, and it installs bash/zsh/fish completions for you. Every
release re-renders the formula in
[`anastylosis/homebrew-tap`](https://github.com/Anastylosis/homebrew-tap),
where the tap's own CI runs `brew audit`, `brew install` and `brew test` on
both platforms before you ever see it.

## Pre-built binary

Download the archive for your platform from the [latest release](https://github.com/Anastylosis/FSS/releases/latest), extract, and put the binary on your `PATH`. All binaries are static (no runtime dependencies).

Asset naming: `fss-<version>-<os>-<arch>.tar.gz` (or `.zip` for Windows). Available platforms: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`.

### Linux

```bash
# Resolves to the latest tag (e.g. v1.11.0) by following the GitHub redirect.
VERSION=$(curl -sIL -o /dev/null -w '%{url_effective}' https://github.com/Anastylosis/FSS/releases/latest | sed 's|.*/||')
ARCH=amd64    # or arm64 for Raspberry Pi 4/5, ARM cloud instances, etc.

curl -LO https://github.com/Anastylosis/FSS/releases/download/${VERSION}/fss-${VERSION}-linux-${ARCH}.tar.gz
tar xzf fss-${VERSION}-linux-${ARCH}.tar.gz
sudo install -m 0755 fss /usr/local/bin/fss
fss version
```

If you don't have sudo, drop `fss` into `~/.local/bin/` (already on your `PATH` on most distros) instead.

### macOS

```bash
VERSION=$(curl -sIL -o /dev/null -w '%{url_effective}' https://github.com/Anastylosis/FSS/releases/latest | sed 's|.*/||')
ARCH=arm64    # Apple Silicon (M1+); use amd64 for Intel Macs

curl -LO https://github.com/Anastylosis/FSS/releases/download/${VERSION}/fss-${VERSION}-darwin-${ARCH}.tar.gz
tar xzf fss-${VERSION}-darwin-${ARCH}.tar.gz
sudo install -m 0755 fss /usr/local/bin/fss

# Gatekeeper will block unsigned binaries the first time. Either:
xattr -d com.apple.quarantine /usr/local/bin/fss
# or: System Settings → Privacy & Security → "Open anyway" after the first failed run.

fss version
```

### Windows (PowerShell)

```powershell
$Version = (Invoke-RestMethod -Uri "https://api.github.com/repos/Anastylosis/FSS/releases/latest").tag_name

Invoke-WebRequest -Uri "https://github.com/Anastylosis/FSS/releases/download/$Version/fss-$Version-windows-amd64.zip" -OutFile fss.zip
Expand-Archive -Path fss.zip -DestinationPath .

# Move into a folder that's on your PATH, e.g.:
New-Item -ItemType Directory -Force -Path "$env:USERPROFILE\bin" | Out-Null
Move-Item -Force fss.exe "$env:USERPROFILE\bin\fss.exe"

# Add %USERPROFILE%\bin to your PATH (one-time):
[Environment]::SetEnvironmentVariable("Path", "$env:Path;$env:USERPROFILE\bin", "User")
# Restart your shell, then:
fss version
```

## System package (.deb / .rpm / AUR)

Each release also publishes `.deb` and `.rpm` packages. Download them from the [latest release](https://github.com/Anastylosis/FSS/releases/latest).

```bash
TAG=$(curl -sIL -o /dev/null -w '%{url_effective}' https://github.com/Anastylosis/FSS/releases/latest | sed 's|.*/||')
VERSION=${TAG#v}    # asset filenames drop the leading "v"
ARCH=amd64          # or arm64

# Debian / Ubuntu
curl -LO "https://github.com/Anastylosis/FSS/releases/download/${TAG}/fss_${VERSION}_${ARCH}.deb"
sudo dpkg -i "fss_${VERSION}_${ARCH}.deb"

# Fedora / RHEL (rpm names the architecture differently)
RPM_ARCH=$([ "$ARCH" = arm64 ] && echo aarch64 || echo x86_64)
curl -LO "https://github.com/Anastylosis/FSS/releases/download/${TAG}/fss-${VERSION}-1.${RPM_ARCH}.rpm"
sudo rpm -i "fss-${VERSION}-1.${RPM_ARCH}.rpm"
```

### Arch Linux (AUR)

```bash
yay -S fss
```

## Docker (multi-arch image on GHCR)

```bash
docker pull ghcr.io/anastylosis/fss:latest
docker run --rm ghcr.io/anastylosis/fss:latest list-scrapers
```

See [docker.md](docker.md) for volume conventions, the bind-mount UID gotcha, and a `docker compose` example with Stash.

## Build from source

Requires Go 1.26+ (matches the `go` directive in `go.mod`).

```bash
git clone https://github.com/Anastylosis/FSS
cd FSS
go build -o fss .
./fss version
```

Or via `go install` (binary lands in `$GOBIN`, typically `~/go/bin/`):

```bash
go install github.com/Anastylosis/FSS@latest
```

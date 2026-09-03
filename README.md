# FSS — FullStudioScraper

[![CI](https://github.com/Anastylosis/FSS/actions/workflows/ci.yml/badge.svg)](https://github.com/Anastylosis/FSS/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/Anastylosis/FSS/graph/badge.svg)](https://codecov.io/gh/Anastylosis/FSS)
[![Go Reference](https://pkg.go.dev/badge/github.com/Anastylosis/FSS.svg)](https://pkg.go.dev/github.com/Anastylosis/FSS)
[![Release](https://img.shields.io/github/v/release/Anastylosis/FSS)](https://github.com/Anastylosis/FSS/releases/latest)
[![License](https://img.shields.io/github/license/Anastylosis/FSS)](LICENSE)
[![Translation status](https://hosted.weblate.org/widget/fss/language-badge.svg?threshold=0)](https://hosted.weblate.org/engage/fss/)

Scrapes all scenes and metadata from a studio URL. Designed to be easily extended to new sites. Can push scraped metadata into a local [Stash](https://stashapp.cc/) instance.

## Supported sites

**1776 sites** across 30+ platforms — [full list with URL patterns →](docs/scrapers.md) · [searchable A-Z domain list →](docs/sites.md)

| Platform | Examples |
|----------|---------|
| Adult Prime | Private, DDF Network, Her Limit, Hands On Hardcore, … |
| Aylo/Juan | Babes, BangBros, Brazzers, Bromo, FakeHub, Men, MileHigh, Reality Kings, Sean Cody, SpiceVids, … |
| Czech AV | Czech Casting, Czech Massage, Czech Streets, Czech Amateurs, Czech Couples, … |
| FTV | FTV Girls, FTV MILFs |
| FYC/PornPros | Passion HD, Tiny4K, POVD, Casting Couch X, Lubed, SpyFam, PORN+, … |
| Full Porn Network | Analized, James Deen, Bad Daddy POV, Porn Force, DTF Sluts, … |
| Gamma/Algolia | Adult Time, Burning Angel, Dogfart Network, Evil Angel, Girlsway, Pure Taboo, Wicked, … |
| Grooby | Grooby Girls, TGirls.xxx, Brazilian Transsexuals, TGirl Japan, Black TGirls, … |
| Jules Jordan Network | Jules Jordan, Manuel Ferrara, The Score, … |
| MetArt Network | MetArt, SexArt, ALS Scan, Viv Thomas, MetArt X, Errotica Archives, … |
| ModelCentro | Penny Barber, The Jerky Girls, Mugur Porn, The Lion XXX, RosellaExtrem, … |
| New Sensations | New Sensations, FamilyXXX, Hot Wife XXX, Girl Girl XXX, … |
| POVR/WankzVR | BrasilVR, MilfVR, TranzVR, WankzVR |
| Score Group | Scoreland, XL Girls, 18Eighteen, Leg Sex, Naughty Mag, 50 Plus MILFs, … |
| SexMex Pro CMS | Exposed Latinas, SexMex, Trans Queens |
| SpiceVids collections | AdamAndEveVOD, Crunchboy, 1111Customs, AdultTime, NewSensations, … |
| Spizoo | Spizoo, Jessica Jaymes, Kelly Madison, Sara Jay, … |
| TeamSkeet/PSM | TeamSkeet, MYLF, Family Strokes, Freeuse, Shoplyfter, SayUncle, Dad Crush, … |
| Teen Mega World | Anal Angels, Beauty Angels, Creampie Angels, Dirty Flix, Nubile Girls HD, … |
| Up-Timely CMS | Attackers, DAS!, Idea Pocket, Madonna, MOODYZ, S1 NO.1 STYLE |
| Vixen MG | Vixen, Blacked, Tushy, Deeper, Slayed, Milfy, … |
| WP video-elements | DaughterSwap, MommysBoy, PervMom, SisLovesMe, … |
| Wank It Now | Wank It Now, Wank It Now VR |
| Wankz | Wankz, Lethal Pass |
| Wow Network | WowGirls, Ultra Films, angels.love, sensual.love |
| Flourish | Flourish, NaughtyNatural, Cherry Pop, … |
| Glamose/UTG | UTG, Glamose, Prestige, … |
| Railway/Express | Smoking Erotica, Smoking Models, Spanking Glamour |
| Real Spankings | Real Spankings, Real Spankings Institute |
| WordPress | Anal Therapy, Family Therapy, Sonia Marek, … |
| Stashbox | StashDB, any stashbox instance (config-driven) |
| Standalone | Anal Vids, APClips, Bare Back Studios, Clips4Sale, Kink, ManyVids, Pornbox, Pornhub, SexLikeReal, … |

## Install

Pick one — Homebrew or a system package for auto-updates, pre-built binary if you would rather not add a tap, Docker if you prefer containers, source if you want to hack on it.

### Option 1 — Homebrew (macOS and Linux)

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

### Option 2 — pre-built binary

Download the archive for your platform from the [latest release](https://github.com/Anastylosis/FSS/releases/latest), extract, and put the binary on your `PATH`. All binaries are static (no runtime dependencies).

Asset naming: `fss-<version>-<os>-<arch>.tar.gz` (or `.zip` for Windows). Available platforms: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`.

#### Linux

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

#### macOS

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

#### Windows (PowerShell)

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

### Option 3 — system package (.deb / .rpm / AUR)

Each release also publishes `.deb` and `.rpm` packages. Download them from the [latest release](https://github.com/Anastylosis/FSS/releases/latest).

```bash
TAG=$(curl -sIL -o /dev/null -w '%{url_effective}' https://github.com/Anastylosis/FSS/releases/latest | sed 's|.*/||')
VERSION=${TAG#v}    # asset filenames drop the leading "v"
ARCH=amd64          # or arm64

# Debian / Ubuntu
curl -LO "https://github.com/Anastylosis/FSS/releases/download/${TAG}/fss_${VERSION}_${ARCH}.deb"
sudo dpkg -i "fss_${VERSION}_${ARCH}.deb"

# Fedora / RHEL
curl -LO "https://github.com/Anastylosis/FSS/releases/download/${TAG}/fss-${VERSION}-1.x86_64.rpm"
sudo rpm -i "fss-${VERSION}-1.x86_64.rpm"
```

#### Arch Linux (AUR)

```bash
yay -S fss
```

### Option 4 — Docker (multi-arch image on GHCR)

```bash
docker pull ghcr.io/anastylosis/fss:latest
docker run --rm ghcr.io/anastylosis/fss:latest list-scrapers
```

See [docs/docker.md](docs/docker.md) for volume conventions, the bind-mount UID gotcha, and a `docker compose` example with Stash.

### Option 5 — build from source

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

## Quick start

```bash
# Scrape a studio — outputs JSON by default
fss scrape https://www.manyvids.com/Profile/590705/bettie-bondage/Store/Videos

# Incremental mode (default): only fetches new scenes
fss scrape <url>

# Full re-scrape from scratch
fss scrape --full <url>

# Re-fetch all metadata, soft-delete removed scenes
fss scrape --refresh <url>

# Output both JSON and CSV
fss scrape --output json,csv --out-dir ./data <url>

# Use SQLite as the store (default: ~/.local/share/fss/fss.db)
fss scrape --db --name "Bettie Bondage" <url>

# Use SQLite at a custom path
fss scrape --db=/path/to/fss.db <url>

# Move existing JSON files into the database, and back out again
fss import --db ./data/
fss export --db --out-dir ./data -o json,csv

# See supported sites
fss list-scrapers

# Check if a URL is supported
fss check <url>

# Debug output (stackable: -d, -dd, -ddd)
fss scrape -d <url>
```

### Creators — one person, several storefronts

A creator sells the same catalogue on Clips4Sale, ManyVids, LoyalFans and their
own site, at different prices. Define the grouping once and work in that unit:

```bash
# Bootstrap creators.d from the studios you already scrape
fss creators suggest --db --write

# Scrape every storefront one creator sells on
fss scrape --creator "Mara Vance"

# Cron-friendly: everything not scraped in the last week
fss scrape --all-creators --stale 7d --db

# Where is each clip cheapest, and what is exclusive to one store?
fss compare --db --from-creator "Mara Vance"
```

Scraping a store listed in a creator file also folds that storefront's own
branding out of the performer credits, so a shop crediting itself does not file
one person as several performers. It needs no configuration beyond the file
itself and leaves co-stars alone.

Definitions are one YAML file per creator and carry no credentials, so a
directory of them can be kept in git and shared. See
[docs/creators.md](docs/creators.md) and [docs/compare.md](docs/compare.md).

### NFO sidecar files

```bash
# Dry-run: match video files against FSS metadata
fss identify /path/to/videos --dir ./data

# Write .nfo files next to matched videos
fss identify /path/to/videos --dir ./data --apply

# Optional: install ffprobe (FFmpeg) for duration-based disambiguation
sudo apt install ffmpeg  # or: brew install ffmpeg
```

### Stash integration

```bash
# List unmatched scenes in Stash
fss stash unmatched

# Dry-run: show what would be imported
fss stash import --dir ./data

# Apply changes
fss stash import --dir ./data --apply
```

## Config file

Optional YAML config at the platform-specific path:

| Platform | Path |
|----------|------|
| Linux    | `~/.config/fss/config.yaml` |
| macOS    | `~/Library/Application Support/fss/config.yaml` |
| Windows  | `%APPDATA%\fss\config.yaml` |

See [`config.example.yaml`](config.example.yaml) for all available options with descriptions. To get started, grab it directly and drop it into place:

```bash
# Linux
mkdir -p ~/.config/fss
curl -fsSL https://raw.githubusercontent.com/Anastylosis/FSS/master/config.example.yaml \
  -o ~/.config/fss/config.yaml

# macOS
mkdir -p ~/Library/Application\ Support/fss
curl -fsSL https://raw.githubusercontent.com/Anastylosis/FSS/master/config.example.yaml \
  -o ~/Library/Application\ Support/fss/config.yaml
```

Or if you already have the repo cloned: `cp config.example.yaml ~/.config/fss/config.yaml`

CLI flags always override config values. See [docs/usage.md](docs/usage.md#config-file) for the full reference.

## Documentation

**New here?** [Quick start](#quick-start) above, then [docs/usage.md](docs/usage.md) for the full CLI.

**Using FSS**

| Document | Contents |
|----------|----------|
| [docs/usage.md](docs/usage.md) | CLI reference for every command and flag, config file, output formats, SQLite schema |
| [docs/scrapers.md](docs/scrapers.md) | Supported sites, URL patterns, shared packages |
| [docs/sites.md](docs/sites.md) | Searchable A-Z list of every covered domain |
| [docs/docker.md](docs/docker.md) | Running FSS in Docker — image tags, volumes, compose examples |
| [docs/translations.md](docs/translations.md) | Help text in other languages — using one, and adding one |

**Working with the data you scrape**

| Document | Contents |
|----------|----------|
| [docs/creators.md](docs/creators.md) | Grouping one person's storefronts: the creators.d format, bootstrapping it, scraping and filtering by creator, cron with `--stale` |
| [docs/compare.md](docs/compare.md) | Comparing a creator's catalogue across storefronts: overlap, cheapest source, exclusives |
| [docs/storage.md](docs/storage.md) | JSON vs SQLite: measured trade-offs, which to pick, inspecting a database without SQL, moving between the two |
| [docs/metadata.md](docs/metadata.md) | The scene model: schema versioning, first-seen tracking, external IDs, what a re-scrape can and cannot change |
| [docs/stash.md](docs/stash.md) | Stash integration: scene sources, matching, cross-site merging, import and revert |
| [docs/identify.md](docs/identify.md) | NFO sidecar files for Kodi/Jellyfin: matching videos, posters, expiring thumbnails |

**Extending FSS**

| Document | Contents |
|----------|----------|
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to add a new scraper, reference implementations |
| [docs/architecture.md](docs/architecture.md) | System design, plugin registry, streaming model, store abstraction |
| [docs/library.md](docs/library.md) | Using FSS as a Go library — the eight public packages |
| [docs/platform-detection.md](docs/platform-detection.md) | Per-platform detection signals and which `*util` package a site belongs to |
| [docs/enhancements.md](docs/enhancements.md) | Known gaps and ideas not yet built |
| [SECURITY.md](SECURITY.md) | Credential handling, network policy, vulnerability reporting |

## License

Copyright (C) 2024-2026 Wasylq

[GPL-3.0-only](LICENSE).

# FSS — FullStudioScraper

[![CI](https://github.com/Anastylosis/FSS/actions/workflows/ci.yml/badge.svg)](https://github.com/Anastylosis/FSS/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/Anastylosis/FSS/graph/badge.svg)](https://codecov.io/gh/Anastylosis/FSS)
[![Go Reference](https://pkg.go.dev/badge/github.com/Anastylosis/FSS.svg)](https://pkg.go.dev/github.com/Anastylosis/FSS)
[![Release](https://img.shields.io/github/v/release/Anastylosis/FSS)](https://github.com/Anastylosis/FSS/releases/latest)
[![License](https://img.shields.io/github/license/Anastylosis/FSS)](LICENSE)
[![Translation status](https://hosted.weblate.org/widget/fss/language-badge.svg?threshold=0)](https://hosted.weblate.org/engage/fss/)

Give FSS a studio URL and it collects every scene in that studio's catalogue —
titles, dates, performers, tags, durations, prices — and keeps the result up to
date on later runs.

- **Scrapes whole catalogues**, then only what is new on each re-run.
- **Tracks prices** over time, and compares one creator's catalogue across the
  several storefronts they sell on.
- **Feeds your library**: pushes metadata into [Stash](https://stashapp.cc/),
  or writes `.nfo` sidecars for Kodi and Jellyfin.
- **Stores** JSON files by default, or a SQLite database; exports CSV.

**1936 sites** are supported, from the big networks (Aylo, Gamma/Adult Time,
Vixen, TeamSkeet, MetArt, Score, Jules Jordan, …) to clip stores (Clips4Sale,
ManyVids, IWantClips, …) and hundreds of independent sites —
[by platform →](docs/scrapers.md) · [A–Z domain list →](docs/sites.md)

## Install

```bash
brew install anastylosis/tap/fss                  # macOS / Linux
yay -S fss                                        # Arch (AUR)
docker run --rm ghcr.io/anastylosis/fss:latest    # Docker
go install github.com/Anastylosis/FSS@latest      # from source, Go 1.26+
```

Static binaries for Linux, macOS and Windows, plus `.deb` and `.rpm` packages,
are on the [releases page](https://github.com/Anastylosis/FSS/releases/latest).
[docs/install.md](docs/install.md) has step-by-step commands for each.
`fss version --update` keeps a copy up to date.

## Quick start

```bash
fss check <url>                        # is this site supported?
fss scrape <url>                       # first run: the whole catalogue
fss scrape <url>                       # later runs: only new scenes
fss scrape --refresh <url>             # re-fetch everything, mark removed scenes
fss scrape --output json,csv <url>     # also write a CSV
fss scrape --db <url>                  # store in SQLite instead of JSON
```

Output lands in the current directory, one file per studio. A config file can
set defaults such as the output directory and per-site delays — see
[usage.md](docs/usage.md#config-file).

**One creator, several storefronts.** Group the stores one person sells on and
treat them as a unit:

```bash
fss creators suggest --db --write           # propose groupings from what you scrape
fss scrape --creator "Jane Eyre"            # scrape all of their stores
fss compare --db --from-creator "Jane Eyre" # where is each clip cheapest?
```

**Your own library.** Match video files on disk against scraped metadata:

```bash
fss stash import --dir ./data          # dry run against Stash; add --apply to write
fss identify /path/to/videos --apply   # write .nfo files next to matched videos
```

## Documentation

| Using FSS | |
|---|---|
| [install.md](docs/install.md) | Every install method, step by step |
| [usage.md](docs/usage.md) | Every command and flag, config file, output formats |
| [docker.md](docs/docker.md) | Image tags, volumes, compose with Stash |
| [creators.md](docs/creators.md) · [compare.md](docs/compare.md) | Grouping a creator's storefronts; comparing them |
| [stash.md](docs/stash.md) · [identify.md](docs/identify.md) | Stash import and revert; NFO sidecars |
| [storage.md](docs/storage.md) · [metadata.md](docs/metadata.md) | JSON vs SQLite; what a scene records and how re-scrapes change it |
| [translations.md](docs/translations.md) | Help text in other languages |

| Extending FSS | |
|---|---|
| [CONTRIBUTING.md](CONTRIBUTING.md) | Adding a scraper, tests, releases |
| [architecture.md](docs/architecture.md) | Registry, streaming model, stores |
| [platform-detection.md](docs/platform-detection.md) | Which shared package a new site belongs to |
| [library.md](docs/library.md) | Using FSS as a Go library |
| [enhancements.md](docs/enhancements.md) | Known gaps and ideas |
| [SECURITY.md](SECURITY.md) | Credentials, network policy, reporting a vulnerability |

## License

Copyright (C) 2024-2026 Wasylq · [GPL-3.0-only](LICENSE)

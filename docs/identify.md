# Identify — NFO Sidecar Files

FSS can match a directory of video files against scraped metadata and write `.nfo` sidecar files alongside each matched video. The `.nfo` files can be picked up by Stash via the [community NFO scraper](https://github.com/stashapp/CommunityScrapers/tree/master/scrapers/nfo), or by any media manager that reads Kodi-style NFO metadata.

## Workflow

1. Scrape studios as usual: `fss scrape <url>` — produces JSON files
2. Point `fss identify` at a directory of video files and the JSON metadata
3. Review the dry-run output
4. Run again with `--apply` to write `.nfo` files
5. In Stash: run the **Identify** task with the NFO scraper as a source to bulk-import the metadata

## Quick start

```bash
# Dry-run — see what would match
fss identify /path/to/videos --json studio.json

# Write .nfo files
fss identify /path/to/videos --json studio.json --apply

# Cross-site merge: load multiple JSON files for richer metadata
fss identify /path/to/videos --json manyvids.json --json clips4sale.json --apply

# Load all JSON files from a directory
fss identify /path/to/videos --dir ./data --apply

# Overwrite existing .nfo files
fss identify /path/to/videos --json studio.json --apply --force

# Suppress the report file
fss identify /path/to/videos --json studio.json --apply --no-report
```

## Flags

### `fss identify <video-dir>`

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--json` | []string | _(none)_ | Specific FSS JSON files to load |
| `--dir` | string | config `out_dir` | Directory of FSS JSON files (loads all `*.json`) |
| `--db` | string | _(from config)_ | Load scenes from the SQLite store instead of JSON. Cannot be combined with `--json`/`--dir` |
| `--from-studio` | []string | _(none)_ | Only match against scenes from these studios — URL, studio name, or sub-brand. Repeatable |
| `--from-performer` | []string | _(none)_ | Only match against scenes featuring these performers. Repeatable |
| `--include-deleted` | bool | `false` | Also match scenes a `--refresh` marked as removed from the site (skipped by default) |
| `--apply` | bool | `false` | Actually write `.nfo` files (default is dry-run) |
| `--force` | bool | `false` | Overwrite existing `.nfo` files |
| `--no-report` | bool | `false` | Do not write `fss-report.txt` |
| `--poster` | bool | `false` | Download each matched scene's thumbnail beside the video and reference it from the `.nfo` (see [Posters and expiring thumbnails](#posters-and-expiring-thumbnails)) |
| `--poster-allow-private` | bool | `false` | Allow poster URLs that resolve to private/loopback IPs, for local media servers; off by default to prevent SSRF |

The positional argument `<video-dir>` is required — the directory of video files to scan.

Scene sources work exactly as in `fss stash import` — same flags, same
precedence, same filter semantics. See
[Scene sources](usage.md#scene-sources-fss-stash-import-fss-identify-fss-compare-fss-creators-suggest) for the
full rules.

In short: `--json` loads specific files, `--dir` loads every `*.json` in a
directory, `--db` reads the SQLite store. With **nothing specified**, the
configured `out_dir` is scanned for JSON — a `db:` entry in your config does not
change that. Every run prints which source it resolved to.

## Video discovery

The video directory is scanned **recursively**. Files are identified by extension:

`.mp4`, `.mkv`, `.avi`, `.wmv`, `.mov`, `.flv`, `.webm`, `.m4v`, `.mpg`, `.mpeg`, `.ts`

No magic-byte sniffing — extension-based detection is fast, works on network shares, and is consistent across platforms.

## Duration filtering

If `ffprobe` (part of FFmpeg) is installed and on `PATH`, `fss identify` probes each video file for its duration and uses it to disambiguate same-title scenes. This is optional — matching works without it, but duration filtering reduces false positives when multiple scenes share similar titles.

Install FFmpeg to enable this:

```bash
# Debian/Ubuntu
sudo apt install ffmpeg

# macOS
brew install ffmpeg

# Windows (winget)
winget install FFmpeg
```

## Matching

The same three-pass matching engine used by `fss stash import` matches each video filename against FSS scene titles. See [stash.md — Matching strategy](stash.md#matching-strategy) for details.

When multiple JSON files are loaded, **cross-site merging** applies: if the same scene title appears in both ManyVids and Clips4Sale JSON files, the metadata is merged — URLs are unioned, the earliest date is picked, performers and tags are combined.

## NFO format

Each `.nfo` file is written next to the video with the same basename (`scene.mp4` → `scene.nfo`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<movie>
  <title>Scene Title</title>
  <url>https://manyvids.com/Video/123/...</url>
  <url>https://clips4sale.com/studio/456/...</url>
  <premiered>2024-01-15</premiered>
  <plot>Scene description if available.</plot>
  <studio>Studio Name</studio>
  <thumb aspect="poster">https://example.com/cover.jpg</thumb>
  <actor>
    <name>Performer One</name>
  </actor>
  <actor>
    <name>Performer Two</name>
  </actor>
  <tag>Tag One</tag>
  <tag>Tag Two</tag>
</movie>
```

**Field mapping:**

| FSS field | NFO element | Notes |
|-----------|-------------|-------|
| `Title` | `<title>` | |
| `URL` | `<url>` | Multiple if cross-site merged |
| `Date` | `<premiered>` | |
| `Description` | `<plot>` | Empty if the scraper didn't produce one |
| `Studio` | `<studio>` | |
| `Thumbnail` | `<thumb aspect="poster">` | Remote URL, or a local filename with `--poster`. Omitted when the URL is known-dead — see below |
| `Performers` | `<actor><name>` | One `<actor>` block per performer |
| `Tags` | `<tag>` | One element per tag |

## Posters and expiring thumbnails

Scraped thumbnails are usually hotlinks, and many sites hand out **signed, short-lived CDN URLs**:

```
https://c758cac692.mjedge.net/…/6932-1x.jpg?expires=1783066465&l=40&token=2036bf…
```

That `expires` value is the moment of the scrape plus a few hours. Days later the URL returns `410 Gone`, so a `<thumb>` pointing at it is a dead link the media manager fetches and fails on.

**By default** (no network access at all), FSS reads the expiry out of the URL and simply omits `<thumb>` when it has already passed. Recognised forms are `expires` / `exp` / `expiry` / `valid_until` holding a Unix timestamp, and AWS SigV4 presigning (`X-Amz-Date` + `X-Amz-Expires`). The check is deliberately conservative: a URL it cannot decide about is kept.

**With `--poster`**, the image is downloaded to `<basename>-poster.<ext>` next to the video — the local-artwork convention Kodi, Jellyfin and Emby all recognise — and `<thumb>` points at that filename instead of a URL. A local file is present by definition, so the reference cannot rot.

```bash
fss identify /path/to/videos --json studio.json --apply --poster
```

It is opt-in because it is one network request per matched scene, against the same hosts a scrape rate-limits.

When `--poster` is set and a download fails, `<thumb>` is omitted rather than falling back to the URL: the failure is proof the URL is dead. The scene is still identified and the `.nfo` still written — a missing poster never fails an identification. Responses that are not images (a login page, say) are refused rather than saved under a `.jpg` name. The run prints `N poster(s) saved, M unavailable`.

Either way the rule is the same: **`<thumb>` is only written when the image is actually there.** If posters come out empty, re-scrape the studio first so the URLs are freshly signed, then run `identify --poster`.

## Existing `.nfo` files

If an `.nfo` file already exists for a video, the default behavior is to **warn and skip** it. The skipped file is logged in the console output and in the report. Pass `--force` to overwrite.

## Report file

By default, `fss identify` writes `fss-report.txt` in the video directory listing:
- **Unmatched** — video files with no FSS match
- **Skipped** — video files where an `.nfo` already existed, or whose NFO name is shared with an earlier video in the run (`scene.mp4` and `scene.mkv` both map to `scene.nfo`; only the first is written and the other is skipped as "same NFO name as ...", even with `--force`)

The report is not written if all files matched successfully. Pass `--no-report` to disable.

## Stash setup

To use the `.nfo` files in Stash:

1. **Install the NFO scraper**: In Stash, go to **Settings > Metadata Providers** and install "NFO Metadata Reader" from the community scrapers.
2. **Run the Identify task**: Go to **Settings > Tasks > Identify**, add "NFO Metadata Reader" as a source, and run it. Stash processes each scene in bulk — it finds the `.nfo` file by matching the filename and imports the metadata.

The NFO scraper requires Python on the Stash machine. The community scrapers package manager handles the `py_common` dependency automatically.

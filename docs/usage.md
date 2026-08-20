# Usage Manual

Full technical reference. Use the section links to jump to what you need.

- [CLI flags](#cli-flags)
- [Config file](#config-file)
- [Data model](#data-model)
- [Output formats](#output-formats)
- [Modifying a scraper](#modifying-a-scraper)
- [Resume and update behaviour](#resume-and-update-behaviour)
- [SQLite](#sqlite)

For grouping one person's several storefronts, see [creators.md](creators.md).
For comparing a creator's catalogue across those storefronts, see [compare.md](compare.md).
For Stash integration, see [stash.md](stash.md).
For NFO sidecar file generation, see [identify.md](identify.md).
For the metadata model and store contract, see [metadata.md](metadata.md).
For choosing a store, inspecting a database, and moving between the two, see [storage.md](storage.md).

---

## CLI flags

### Global flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--debug`, `-d` | count | 0 | Increase debug verbosity. Stackable: `-d` = high-level ops, `-dd` = HTTP requests, `-ddd` = parsing details |
| `--lang` | string | _(ambient locale)_ | Language for the help text, e.g. `ko`. Also `FSS_LANG` or the config `language:` key. Only help text is translated — see [translations.md](translations.md) |

### `fss scrape <studio-url>`

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--workers`, `-w` | int | 3 | Max parallel metadata fetchers |
| `--full` | bool | false | Full traversal (no early-stop); preserves price history; drops scenes no longer on the site |
| `--refresh` | bool | false | Re-fetch metadata for all known scenes; soft-delete missing ones |
| `--force` | bool | false | Allow a destructive `--full`/`--refresh` against a populated studio without being asked — covers both the 0-scene case and a coverage collapse (see [Broken-scraper detection](#broken-scraper-detection)) |
| `--no-preserve` | bool | false | Let a re-scrape blank fields it no longer returns. By default a fresh scene inherits any field it left empty from the stored one, so a broken parser cannot wipe metadata — see [metadata.md](metadata.md) |
| `--output`, `-o` | string | `json` | Export format(s): `json`, `csv`, or `json,csv` |
| `--out-dir` | string | `.` | Output directory |
| `--db` | string | _(from config)_ | Store selector. `--db` alone uses the database named in `db:`, or `~/.local/share/fss/fss.db`; `--db=/path` uses a specific file; `--db=""` forces the flat JSON store even when `db:` is set. Note the `=` — a space-separated value is not parsed |
| `--delay` | int | `500` | Milliseconds to sleep between page requests (default from config; `--delay 0` disables) |
| `--site-delay` | []string | _(none)_ | Per-scraper delay overrides as `name=ms` pairs, e.g. `--site-delay manyvids=0,pornhub=2000` |
| `--name` | string | _(none)_ | Human-readable label for this studio (stored when `--db` is set) |
| `--performer` | []string | _(none)_ | Replace the performers on every scene this run scrapes. Repeat the flag, or comma-separate, for several |
| `--studio` | string | _(none)_ | Replace the studio on every scene this run scrapes |
| `--creator` | []string | _(none)_ | Scrape every storefront defined for this creator in `creators.d`. Repeatable — see [creators.md](creators.md) |
| `--all-creators` | bool | false | Scrape every storefront of every defined creator |
| `--stale` | string | _(none)_ | Only scrape studios not scraped within this window, e.g. `12h`, `7d`, `2w`. Needs `--db` |
| `--creators-dir` | string | _(from config)_ | Directory of creator YAML files |

`--full` and `--refresh` are mutually exclusive.

The studio URL argument is optional when `--creator` or `--all-creators` selects
one; passing none of the three is an error. URLs reachable more than one way are
scraped once.

Any scrape of a store listed in `creators.d` also folds storefront branding
credited as a performer into that creator's name, so a shop crediting itself
does not file one person as several performers. It needs no configuration beyond
the creator file itself, leaves co-stars alone, and happens whether the store was
reached by URL or by `--creator`; `--performer` overrides it. See
[creators.md](creators.md#storefront-branding-in-performer-credits).

**Per-site delay precedence:** `--site-delay <id>=N` (CLI) > `site_delays.<id>: N` (config) > `--delay`/`delay` (global). A site explicitly set to `0` disables delay even when the global default is non-zero. `--full` re-fetches every scene (carrying price history forward) and drops scenes no longer on the site. `--refresh` traverses the full scene list but re-uses existing IDs to update metadata in place and detect deletions.

### Relabelling a scrape: `--performer` / `--studio`

One performer's catalogue is often spread over several sites that each credit
her differently. Scraping them all under one name files every scene together
instead of leaving the store with an alias per site:

```bash
fss scrape https://siteA.com https://siteB.com https://siteC.com \
  --performer "Jodi West" --studio "Jodi West"
```

Both flags apply to **every URL in the invocation**, and both **replace** rather
than merge — the point is to drop the site's spelling, and a scene that kept
both would hold one person under two names. On a scene the site credited to
several people, the co-stars are replaced too, so this fits solo-performer sites
and is lossy elsewhere. Pass every name you want kept:

```bash
fss scrape https://siteA.com --performer "Jodi West" --performer "Marcello Bravo"
fss scrape https://siteA.com --performer "Jodi West, Marcello Bravo"   # same thing
```

The override applies to the scenes a run **scrapes**, not to everything already
stored. An incremental run therefore relabels only what it re-collects; use
`--full` or `--refresh` to relabel a whole catalogue. The run prints what it is
doing (`overriding: performers → …`) so the relabel is not invisible.

`--studio` is not `--name`: `--name` is the studio's label in the SQLite
`studios` table, while `--studio` is the per-scene `Studio` field. On a network
scraper whose scenes carry sub-brands (Gamma, Empire Stores, Trix Video),
`--studio` flattens them all to the one value.

**Effect on `fss stash import`:** performers are safe — the import seeds a
scene's performer list from what Stash already has and only appends, so a name
dropped here is never removed from Stash, merely no longer added by FSS. A
brand-new Stash scene, though, gets only the names you supplied. `--studio`
does replace the Stash studio, since a scene has exactly one.

### `fss list-scrapers`

Prints all registered scrapers and the URL patterns each one handles.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--markdown` | bool | false | Output as a markdown table (useful for docs) |

### `fss list-studios`

Lists all studios in the SQLite database with scene counts and last-scraped timestamps. Needs a database: pass `--db`, or set `db:` in the config file and omit the flag.

### `fss creators`

Lists the creators defined in `creators.d`, with each store's last scrape date when a database is configured. See [creators.md](creators.md).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--creators-dir` | string | _(from config)_ | Directory of creator YAML files |

### `fss creators suggest`

Proposes `creators.d` files by clustering the studios you already scrape, on shared names and shared dominant performers. Prints for review by default.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--write` | bool | false | Write the proposals into the creators directory instead of printing them |
| `--force` | bool | false | With `--write`, overwrite creator files that already exist |
| `--include-single` | bool | false | Also propose creators whose catalogue is on a single storefront |

Plus the shared [scene-source flags](#scene-sources-fss-stash-import-fss-identify-fss-compare-fss-creators-suggest).

### `fss compare`

Compares each creator's catalogue across the storefronts they sell it on: what each store holds, which titles overlap, where each shared title is cheapest, and what only one store carries. Full detail in [compare.md](compare.md).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--top` | int | 10 | How many of the widest price gaps to list per creator |
| `--exclusives` | bool | false | Also report, per store, how many titles only it carries |
| `--csv` | string | _(none)_ | Write every shared title to this CSV file |

Plus the shared [scene-source flags](#scene-sources-fss-stash-import-fss-identify-fss-compare-fss-creators-suggest).

### `fss import <file-or-dir> ...`

Loads studio JSON files into the SQLite database. Directories contribute their `*.json` entries (not recursive). Each file's own `studioUrl` decides which studio it belongs to — filenames are never parsed, since `Slugify` is lossy.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--db` | string | _(from config)_ | Target database (`--db` alone uses the default path; `--db=/path` for a custom one) |
| `--replace` | bool | false | Make each file authoritative: delete stored scenes it does not contain |
| `--dry-run` | bool | false | Report what would be imported without writing. Creates nothing, including the database itself |

The database schema is a superset of the JSON layout, so nothing is lost. By default the file is *merged* into the database: scenes present in both take the file's values, price history is carried forward, fields the file omits keep their stored values, and scenes only in the database are left alone.

**Files are processed oldest-first by modification time.** Merging is last-write-wins per field, so processing order decides which version of a re-scraped studio survives — and ordering by *name* made that depend on filenames. A second download saved as `studio (1).json` sorts before `studio.json`, so the newer file would have been overwritten by the older one. When two files in one run describe the same studio, both are named in a warning.

```bash
fss import --db ./out/                      # every studio file in ./out
fss import --db=/custom/path.db studio.json # one file into a custom database
```

### `fss export [studio-url ...]`

Writes studio JSON/CSV files out of the SQLite database, named exactly as the flat store names them. With no arguments, every tracked studio is exported.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--db` | string | _(from config)_ | Source database |
| `--output`, `-o` | string | `json` | Export format(s): `json`, `csv`, or `json,csv` |
| `--out-dir` | string | `.` | Output directory |

```bash
fss export --db --out-dir ./out -o json,csv
```

A JSON → database → JSON round trip is lossless except for sub-second timestamp precision, which SQLite has never stored (times are written as RFC 3339 to the second).

### Scene sources (`fss stash import`, `fss identify`, `fss compare`, `fss creators suggest`)

These commands read previously-scraped scenes. They share one set of flags for choosing where those scenes come from:

| `--json` | []string | _(none)_ | Specific JSON files to load |
| `--dir` | string | _(config `out_dir`)_ | Directory of FSS JSON files |
| `--db` | string | _(from config)_ | Load scenes from the SQLite store instead of JSON (`--db` alone uses the default path; `--db=/path` for a custom one). Cannot be combined with `--json`/`--dir` |
| `--from-studio` | []string | _(none)_ | Only use scenes from these studios. Accepts a studio URL, a studio display name, or a per-scene studio/sub-brand name. Repeatable — any match |
| `--from-performer` | []string | _(none)_ | Only use scenes featuring these performers. Repeatable — any match |
| `--from-creator` | []string | _(none)_ | Only use scenes from this creator's storefronts, as defined in `creators.d`. Repeatable — any match |
| `--creators-dir` | string | _(from config)_ | Directory of creator YAML files |

**Precedence:** `--json` → `--db` → `--dir` → the config's `out_dir`. Passing `--db` together with `--json` or `--dir` is an error rather than a silent winner. Each run prints the source it resolved to.

**JSON remains the default source**, even if you have `db:` set in your config. That setting says where `fss scrape` *writes*; it does not change where these commands *read*. Reading the database is an explicit `--db` until the [announced default switch](storage.md#making-sqlite-the-default) — until then nothing about your existing workflow changes.

**How `--from-studio` resolves**, most specific first:

1. A value containing `://` matches the scene's studio URL exactly.
2. A value naming a per-scene studio matches **that sub-brand only**. Scrapers record a per-scene studio name, which for a network is the sub-brand — one scrape of `sexlikereal.com` carries 705 distinct values (`SLR Originals`, `perVRt`, …). This gives you one level of grouping even though FSS records no studio hierarchy.
3. Otherwise it is matched against the studios table's display names and selects every scene under those URLs.

Step 2 comes first deliberately: a studio's display name is often *derived* from its first scene, so it is frequently a sub-brand itself. Matching display names first would make `--from-studio "SLR Originals"` select the entire network.

Names are matched canonically — case-folded with whitespace collapsed — so `"ABC "`, `"abc"` and `"ABC"` are the same studio. This is the same rule `MergeScenes` uses to deduplicate names, so filtering and merging always agree.

A filter matching nothing is an **error listing the available names**, not an empty run.

**Combining filters:** repeated values of one flag are OR; different flags are AND. `--from-studio X --from-performer A` means scenes from X that also feature A.

`--from-creator` resolves to that creator's store URLs and then behaves exactly like listing them under `--from-studio` — see [creators.md](creators.md).

Note `--from-studio`/`--from-performer`/`--from-creator` filter the **FSS metadata**, while `fss stash import`'s `--studio`/`--performer` filter which **Stash scenes** are queried. They apply to opposite sides of the match and combine as AND.

### `fss check <url>`

Checks whether a URL is supported by any registered scraper. Prints the scraper ID and its URL patterns if matched. If unsupported, prints a pre-filled link to open a new-scraper request issue on GitHub.

```bash
$ fss check https://www.brazzers.com/videos
Scraper:  brazzers
Patterns: brazzers.com, brazzers.com/pornstar/{id}/{slug}, ...

$ fss check https://example.com/unknown
Not supported: https://example.com/unknown

Request support: https://github.com/Anastylosis/FSS/issues/new?template=new_scraper.yml&url=...
```

The scheme is optional — `fss check brazzers.com` and `fss check www.brazzers.com/videos` are read as `https://`. The same applies to `fss detect` and `fss scrape`. Pass `http://` explicitly for the handful of sites that are http-only.

### `fss detect <url>`

Fetches a URL once and checks the response for known platform signals (Aylo `instance_token`, Algolia API, `psmcdn.net`, ModelCentro, etc.). Reports the detected platform and corresponding util package. Useful when deciding whether a new site needs a standalone scraper or belongs to an existing shared package.

```bash
$ fss detect https://www.teenmegaworld.net
Platform: Teen Mega World
Package:  tmwutil
```

If the URL is already supported by a registered scraper, it reports that instead of fetching.

### `fss doctor`

Checks environment health: config file, scraper registry, database writability, Stash connectivity, `ffprobe` availability, and network egress.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--prune` | bool | false | Delete performer/tag/category rows no scene references any more |

The `vocabulary` check counts entity rows nothing links to. `Save` deletes a scene's junction rows but never the shared `performers`/`tags`/`categories` rows they pointed at — the entity may still belong to another studio, and proving otherwise costs a full scan of the junction table, which is exactly what `Save`'s content-hash short-circuit exists to avoid. So they accumulate, and this is where they get cleared.

A non-zero count is worth reading before pruning: a cluster of near-miss names (`Tockings`, `Triptease`, `Eductress`) is the fingerprint of a parser that briefly truncated what it wrote, long after the junction rows were corrected.

### `fss completion <bash|zsh|fish|powershell>`

Generates shell completion scripts. Usage:

```bash
source <(fss completion bash)           # bash
fss completion zsh > "${fpath[1]}/_fss" # zsh
fss completion fish | source            # fish
```

### `fss config init`

Creates a default config file at the XDG config path (e.g. `~/.config/fss/config.yaml`). Fails if the file already exists.

### `fss config path`

Prints the config file path for the current platform.

### `fss version`

Prints the build version, commit hash, and build date. Checks for newer releases on GitHub.

When a newer release is available and its tag carried an annotation, that message is shown
with the notice — it is where a release says what its commit list cannot:

```
fss v1.28.1 (a1b2c3d, 2026-07-30)
Update available: v1.28.1 → v1.29.0

  maintenance only, no new scrapers

  Existing scrapers are unaffected; no config or store changes.

https://github.com/Anastylosis/FSS/releases/latest
```

Releases cut from a lightweight tag simply have no such message and print the notice alone.
The annotation is not repeated once you are running that release.

The update check is best-effort and never fails the command: a network error prints
`Could not check for updates: …`, and a response that carries no release tag prints
`Could not determine the latest release.` rather than advertising an update with nothing
after the arrow.

For `fss identify`, see [identify.md](identify.md).
For `fss stash` subcommands, see [stash.md](stash.md).

---

## Config file

Located at the XDG config path for your platform (see [README](../README.md)). All keys are optional — missing keys use the defaults shown below. A fully commented example is available at [`config.example.yaml`](../config.example.yaml) in the repo root.

```yaml
workers: 3        # int   — parallel metadata fetchers
output: json      # str   — json | csv | json,csv
out_dir: .        # str   — output directory path
db: ""            # str   — "" or absent = flat store (the default, and staying so);
                  #         "default" = ~/.local/share/fss/fss.db; or a path, absolute or
                  #         relative to the working directory (e.g. "./fss.db").
                  #         Omitting the key entirely is not the same as "" — see storage.md
delay: 500        # int   — ms between page requests; 0 disables
user_agent: ""    # str   — "firefox" (default), "chrome", or a custom UA string
notices: true     # bool  — advisory `[notice]` lines that explain rather than report;
                  #         false (or FSS_NO_NOTICES=1) silences them. Warnings and
                  #         errors are unaffected
language: ""      # str   — help-text language, e.g. "ko". "" (or a language with no
                  #         catalog) = English. Overridden by FSS_LANG and --lang.
                  #         Only help text is translated — see translations.md

creators_dir: ""  # str   — directory of one-YAML-per-creator definitions.
                  #         "" = ~/.config/fss/creators.d. Point at a clone to use a
                  #         shared set — see creators.md

site_delays:      # map[string]int — per-scraper delay overrides (overrides `delay` for matching sites)
  # manyvids: 0
  # pornhub: 2000
  # brazzers: 500

stashbox:         # list — stashbox instances for the stashbox scraper
  # - url: "https://stashdb.org/graphql"       # GraphQL endpoint URL
  #   api_key: "your-api-key-here"             # API key for this instance
  # - url: "https://pmvstash.org/graphql"
  #   api_key: "another-api-key"

stash:
  url: "http://localhost:9999"    # str   — Stash server URL
  api_key: ""                     # str   — API key (prefer FSS_STASH_API_KEY env var)
  tag: "fss_import"               # str   — import marker tag
  stashbox_tag: "fss_stashbox_override"  # str — tag for StashDB override tracking
  resolution_tags: true           # bool  — add 4K/FHD/HD Available tags
```

CLI flags take precedence over config values. Config values take precedence over built-in defaults.

---

## Data model

### Scene

All fields that FSS stores per scene. Fields marked _optional_ are omitted from JSON output when empty.

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `id` | string | yes | Site-specific unique ID |
| `siteId` | string | yes | Scraper ID (e.g. `manyvids`) |
| `studioUrl` | string | yes | Studio URL passed on the command line |
| `title` | string | yes | |
| `url` | string | yes | Full URL to the scene page |
| `date` | time | yes | Release / launch date |
| `description` | string | optional | |
| `thumbnail` | string | optional | URL to the main preview image |
| `preview` | string | optional | URL to the preview video clip |
| `performers` | []string | optional | |
| `director` | string | optional | |
| `studio` | string | optional | Creator / brand name |
| `tags` | []string | optional | |
| `categories` | []string | optional | Broader groupings; some sites distinguish from tags |
| `series` | string | optional | |
| `seriesPart` | int | optional | |
| `duration` | int | optional | Seconds |
| `resolution` | string | optional | e.g. `4K`, `HD`, `SD` |
| `width` | int | optional | Pixels |
| `height` | int | optional | Pixels |
| `format` | string | optional | e.g. `MP4` |
| `views` | int | optional | At time of scrape |
| `likes` | int | optional | At time of scrape |
| `comments` | int | optional | At time of scrape |
| `priceHistory` | []PriceSnapshot | optional | See below |
| `lowestPrice` | float64 | optional | Lowest effective price seen across all scrapes |
| `lowestPriceDate` | time | optional | When that lowest price was recorded |
| `scrapedAt` | time | yes | When this record was last written |
| `deletedAt` | time | optional | Set when scene is no longer found on re-scrape; never removed |

### PriceSnapshot

One entry is appended to `priceHistory` on each scrape run.

| Field | Type | Notes |
|-------|------|-------|
| `date` | time | When this snapshot was taken |
| `regular` | float64 | Full price |
| `discounted` | float64 | Sale price; 0 if not on sale |
| `isFree` | bool | |
| `isOnSale` | bool | |
| `discountPercent` | int | e.g. `50` for 50% off |

**Effective price** = `discounted` if on sale, `0` if free, otherwise `regular`.

---

## Output formats

### File naming

Output files are named by sanitising the studio URL into a slug:

```
https://www.manyvids.com/Profile/590705/bettie-bondage/Store/Videos
→ manyvids.com-profile-590705-bettie-bondage-store-videos.json
→ manyvids.com-profile-590705-bettie-bondage-store-videos.csv
```

Two distinct studio URLs can sanitise to the same slug (e.g. `/foo-bar` and `/foo/bar`, or `/Foo` and `/foo`). The full `studioUrl` is stored inside the JSON, and the store refuses to load or overwrite a file whose stored URL doesn't match the one you're scraping — you'll see a `slug collision` error. Rename or move one of the studio files to resolve.

### JSON

The JSON file wraps the scene list with a small header:

```json
{
  "studioUrl": "https://www.manyvids.com/Profile/590705/bettie-bondage/Store/Videos",
  "scrapedAt": "2026-04-22T10:00:00Z",
  "sceneCount": 700,
  "scenes": [ ... ]
}
```

Each scene is a JSON object with the fields listed in the [Data model](#data-model) section. Optional fields are omitted when empty.

JSON is **always written** by the flat store — it is the backing format for incremental updates. Even if you request `--output csv` only, a JSON file is also created alongside it.

**Important:** all scenes are collected in memory first, then the entire JSON file is written at the end of the scrape. If you cancel mid-scrape (Ctrl+C), no output file is produced. For large sites (e.g. ~1750 pages), a scrape can take several minutes — use `--delay` to throttle requests and avoid being blocked. The progress line (`fetching: N / total scenes`) and the final `Done:` / `Partial save complete:` line both include elapsed wall-clock time. Note that `--delay` paces each worker individually, not the aggregate request rate — a high `--workers` count can still overwhelm a rate-limited site even with a non-zero delay, so lower `--workers` first if a site starts timing out mid-scrape.

### CSV

One row per scene. Column order matches the table below exactly.

Multi-value fields (`performers`, `tags`, `categories`) use `|` as a separator — e.g. `Alice|Bob`.

`priceHistory` is serialised as a JSON string within its column. Use a JSON-aware tool (e.g. `jq`, DuckDB, Python) to query it; otherwise treat it as opaque.

| Column | Type | Notes |
|--------|------|-------|
| `id` | string | Site-specific unique ID |
| `siteId` | string | e.g. `manyvids` |
| `studioUrl` | string | |
| `title` | string | |
| `url` | string | Full scene page URL |
| `date` | RFC3339 | Release date |
| `description` | string | |
| `thumbnail` | string | URL |
| `preview` | string | URL to preview clip |
| `performers` | string | `\|`-separated |
| `director` | string | |
| `studio` | string | |
| `tags` | string | `\|`-separated |
| `categories` | string | `\|`-separated |
| `series` | string | |
| `seriesPart` | int | |
| `duration` | int | Seconds |
| `resolution` | string | e.g. `4K`, `HD` |
| `width` | int | Pixels |
| `height` | int | Pixels |
| `format` | string | e.g. `MP4` |
| `views` | int | At time of scrape |
| `likes` | int | At time of scrape |
| `comments` | int | At time of scrape |
| `lowestPrice` | float | Lowest effective price seen |
| `lowestPriceDate` | RFC3339 | When that price was recorded |
| `priceHistory` | JSON string | Array of PriceSnapshot objects |
| `scrapedAt` | RFC3339 | |
| `deletedAt` | RFC3339 | Empty if active |

---

## Modifying a scraper

### Adding a new field to `models.Scene`

1. **`models/scene.go`** — add the field with JSON tag
2. **The scraper** — populate the field in `toScene()` (or equivalent mapping function)
3. **`internal/store/export_csv.go`** — add the column name to `csvHeaders` and the value to `sceneToRow()`
4. **`docs/usage.md`** — add a row to the CSV column table and the data model table (this file)
5. If using SQLite — add the column to the `CREATE TABLE` statement and the insert/select queries in `internal/store/sqlite.go`

### Adding a new URL pattern to an existing scraper

Update `Patterns()` to return the new pattern and update `MatchesURL()` to recognise it. Add a test case to `TestMatchesURL`. No other files change.

---

## Resume and update behaviour

FSS supports three modes, selected by flags on `fss scrape`.

### Default — incremental (no flag)

The fastest option. Suitable for routine daily runs.

1. Load all scene IDs already stored for this studio.
2. Paginate the site's scene list (newest-first). As soon as an already-known ID appears, stop — everything behind it is already in the store.
3. Fetch full metadata only for the newly discovered scenes.
4. Merge new scenes in front of the existing set and save.

**Trade-off:** if the site re-orders or back-fills scenes you may miss them. Use `--refresh` periodically to catch that.

### `--full`

Full traversal — no early-stop hint. Fetches every page and every scene.

**Price history is preserved** — existing price snapshots are carried forward and the new snapshot is appended.

Differs from `--refresh` in one way: scenes that no longer appear on the site are dropped from the store rather than soft-deleted. Use when you want a clean slate of "what's currently on the site" without losing pricing history.

### `--refresh`

Full traversal (every page, every scene) but preserves history:

- **Price history** from prior scrapes is carried forward — each re-fetched scene gets the new price snapshot appended to its existing history.
- **Soft-delete** — any scene that was in the store but is no longer returned by the site has its `deletedAt` timestamp set. It is never removed from the store.

Use periodically (e.g. weekly) to catch deletions and accumulate accurate price history.

### Broken-scraper detection

Sites change. A scraper can keep running, report no errors and still return a
fraction of what it used to — a site drops its pagination, or a redesign leaves
only one of two card templates matching. That looks like a clean success to
every other check, and under `--full`/`--refresh` the authoritative `Save` will
happily delete everything the scrape no longer reaches.

So every scrape compares what it re-collected against what the store already
holds. When a **completed** traversal re-sees **less than 50%** of the stored
scenes, `fss` says so:

```
[warn] auntjudys: this scrape re-saw 9 of 412 stored scenes (2%) — the site or the scraper may have changed
       this is an authoritative save: 403 stored scene(s) would be dropped
       proceed anyway? [y/N]
```

- **`--full` / `--refresh`** — prompts before saving. Answering anything but
  `y`/`yes` skips the save and leaves the store untouched. With no terminal
  attached (cron, CI, a pipe) it does not hang: it skips the save and tells you
  to pass `--force`.
- **`--force`** — proceeds without asking, for when you know the catalogue
  really did shrink.
- **Incremental (the default)** — reports the same numbers but always
  continues, because incremental merges and cannot lose scenes.

The check deliberately stays quiet in three cases, each of which explains a
small result on its own:

| Case | Why it is exempt |
|---|---|
| Fewer than 10 stored scenes | One or two scenes swing the ratio past any threshold |
| Incomplete traversal | Fetch errors already force non-destructive merge semantics |
| `KnownIDs` early-stop fired | A partial fresh set is the *point* of incremental mode |

A 0-scene `--full`/`--refresh` is handled separately and more strictly — it is
refused outright rather than prompted, since nothing distinguishes it from a
totally broken parser.

---

## SQLite

### Enabling

Pass `--db` to any scrape command, or set `db` in your config file:

```bash
fss scrape --db <studio-url>                  # uses default path: ~/.local/share/fss/fss.db
fss scrape --db=/custom/path.db <studio-url>  # uses a custom path — the `=` is required
```

Or in `config.yaml`:

```yaml
db: "default"           # uses ~/.local/share/fss/fss.db
db: "/custom/path.db"   # uses a custom path
```

The data directory is created automatically if it doesn't exist. When `--db` is set, SQLite is the source of truth. JSON/CSV files are exported from it if `--output` requests them.

### Schema

Eleven tables (three core + six junction/lookup + one metadata + `schema_version`),
at schema version 9. Inspect with any SQLite client (`sqlite3`, DB Browser for
SQLite, DBeaver, Datasette, …). Migrations run automatically on open.

**`scenes`** — one row per scene. Primary key is **`(id, site_id, studio_url)`**:
the same scene reachable from two studio URLs is stored once per URL, so the
studio URL is part of the identity.

| Column | Type | Notes |
|--------|------|-------|
| `id` | TEXT | Site-specific ID (part of the primary key) |
| `site_id` | TEXT | e.g. `manyvids` (part of the primary key) |
| `studio_url` | TEXT | Part of the primary key; indexed |
| `title` | TEXT | |
| `url` | TEXT | |
| `date` | TEXT | RFC3339 |
| `description` | TEXT | |
| `thumbnail` | TEXT | Remote URL; often a short-lived signed CDN link |
| `preview` | TEXT | |
| `performers` | TEXT | **Legacy JSON array — no longer read or written.** Use `scene_performers` |
| `director` | TEXT | |
| `studio` | TEXT | |
| `tags` | TEXT | **Legacy JSON array — no longer read or written.** Use `scene_tags` |
| `categories` | TEXT | **Legacy JSON array — no longer read or written.** Use `scene_categories` |
| `series` | TEXT | |
| `series_part` | INTEGER | |
| `duration` | INTEGER | Seconds |
| `resolution` | TEXT | |
| `width` | INTEGER | |
| `height` | INTEGER | |
| `format` | TEXT | |
| `views` | INTEGER | |
| `likes` | INTEGER | |
| `comments` | INTEGER | |
| `lowest_price` | REAL | |
| `lowest_price_date` | TEXT | RFC3339, nullable |
| `first_seen_at` | TEXT | RFC3339 — when the scene entered the store; never moves once set |
| `scraped_at` | TEXT | RFC3339 — when it was last fetched |
| `deleted_at` | TEXT | RFC3339, nullable — NULL means active |
| `content_hash` | TEXT | Internal. Fingerprint of everything above except `scraped_at`/`first_seen_at`, so `Save` can skip unchanged rows. **Do not hand-edit** — a stale value makes a scene invisible to updates. Setting it to `''` forces a rewrite |

**`price_history`** — one row per price snapshot per scene:

| Column | Type | Notes |
|--------|------|-------|
| `id` | INTEGER | autoincrement |
| `scene_id` | TEXT | with `site_id`, `studio_url` identifies the scene |
| `site_id` | TEXT | |
| `studio_url` | TEXT | |
| `date` | TEXT | RFC3339 |
| `regular` | REAL | |
| `discounted` | REAL | |
| `is_free` | INTEGER | 0/1 |
| `is_on_sale` | INTEGER | 0/1 |
| `discount_percent` | INTEGER | |

**`studios`** — one row per studio URL:

| Column | Type | Notes |
|--------|------|-------|
| `url` | TEXT | Primary key |
| `site_id` | TEXT | e.g. `manyvids` |
| `name` | TEXT | Label via `--name`; never cleared by a scrape that omits `--name` |
| `added_at` | TEXT | RFC3339 — when first scraped |
| `last_scraped_at` | TEXT | RFC3339, nullable |

**Normalized lookup tables** — performers, tags and categories live in their own
tables with junction tables linking them to scenes, so the data is queryable
without JSON parsing. The lookup tables are **global**: one row per distinct
name, shared across every studio. That is deliberate deduplication.

**`performers`** / **`tags`** / **`categories`**:

| Column | Type |
|--------|------|
| `id` | INTEGER (autoincrement) |
| `name` | TEXT (unique) |

**`scene_performers`** / **`scene_tags`** / **`scene_categories`** — all three have
the same shape, including `position`:

| Column | Type | Notes |
|--------|------|-------|
| `scene_id` | TEXT | FK to scenes |
| `site_id` | TEXT | FK to scenes |
| `studio_url` | TEXT | FK to scenes — always join on all three |
| `<entity>_id` | INTEGER | FK to `performers` / `tags` / `categories` |
| `position` | INTEGER | Source order (0 = first). Scrapers emit meaningful order and it is preserved |

**`scene_external_ids`** — cross-site identity, e.g. a StashDB UUID:

| Column | Type | Notes |
|--------|------|-------|
| `scene_id` | TEXT | FK to scenes |
| `site_id` | TEXT | FK to scenes |
| `studio_url` | TEXT | FK to scenes |
| `source` | TEXT | `stashdb`, `tpdb`, `iafd`, `indexxx`, or a stashbox site ID |
| `external_id` | TEXT | The ID in that database |

Indexed on `(source, external_id)`, so "which stored scenes are this StashDB
UUID" is answerable across every site and studio.

**`schema_version`** — tracks migration state (single `version INTEGER` column).

> **Joining scene child rows:** always match on **all three** of `scene_id`,
> `site_id` *and* `studio_url`. Dropping `studio_url` silently merges rows from
> different studios that happen to share a scene ID.

### Listing studios

```bash
fss list-studios --db              # uses default db location
fss list-studios --db ./fss.db     # uses a custom path
fss list-studios                   # uses the config file's `db:` value
```

### Example queries

Placeholders in ALL CAPS — substitute your own values. Run with
`sqlite3 -header -column ~/.local/share/fss/fss.db` (or `.mode box`) for readable
output.

> **Joining child rows:** `scene_performers`, `scene_tags`, `scene_categories`,
> `price_history` and `scene_external_ids` are keyed on **`scene_id`, `site_id`
> *and* `studio_url`**. Match on all three. Dropping `studio_url` appears to work
> until a scene ID is reused across two studios, at which point rows silently
> multiply.

**Browsing a studio**

```sql
-- Everything currently on a studio
SELECT title, date, duration, lowest_price
FROM scenes
WHERE studio_url = 'STUDIO_URL'
  AND deleted_at IS NULL
ORDER BY date DESC;

-- Every studio, with how much is stored and when it was last scraped
SELECT st.name, st.site_id, st.last_scraped_at, COUNT(sc.id) AS scenes
FROM studios st
LEFT JOIN scenes sc ON sc.studio_url = st.url AND sc.deleted_at IS NULL
GROUP BY st.url
ORDER BY scenes DESC;

-- Studios not scraped in the last 30 days
SELECT url, site_id, last_scraped_at
FROM studios
WHERE last_scraped_at < date('now', '-30 day')
ORDER BY last_scraped_at;
```

**Following a performer across sites**

This is the query the flat JSON store cannot answer at all — it spans every
studio and site in one pass.

```sql
-- Every scene featuring a performer, with the studio it came from
SELECT s.date,
       s.title,
       COALESCE(NULLIF(s.studio, ''), NULLIF(st.name, ''), s.studio_url) AS studio,
       s.site_id
FROM scenes s
JOIN scene_performers sp
  ON sp.scene_id = s.id AND sp.site_id = s.site_id AND sp.studio_url = s.studio_url
JOIN performers p ON p.id = sp.performer_id
LEFT JOIN studios st ON st.url = s.studio_url
WHERE p.name = 'PERFORMER NAME' COLLATE NOCASE
  AND s.deleted_at IS NULL
ORDER BY s.date DESC;

-- Which studios they appear in, and how often
SELECT COALESCE(NULLIF(s.studio, ''), s.studio_url) AS studio, COUNT(*) AS scenes
FROM scenes s
JOIN scene_performers sp
  ON sp.scene_id = s.id AND sp.site_id = s.site_id AND sp.studio_url = s.studio_url
JOIN performers p ON p.id = sp.performer_id
WHERE p.name = 'PERFORMER NAME' COLLATE NOCASE
GROUP BY studio
ORDER BY scenes DESC;
```

`COLLATE NOCASE` handles capitalisation, but sites also differ on *spelling*.
Merging folds case for deduplication while storing each site's own spelling, so
check for variants before trusting a total:

```sql
SELECT p.name, COUNT(*) AS scenes
FROM performers p
JOIN scene_performers sp ON sp.performer_id = p.id
WHERE p.name LIKE '%SURNAME%'
GROUP BY p.name
ORDER BY scenes DESC;
```

**Combining performers**

The difference between *any* and *all* is `IN` versus a `GROUP BY … HAVING`
count — a common thing to get wrong, since the obvious `p.name = 'A' AND
p.name = 'B'` matches nothing (one row cannot hold two names).

```sql
-- Scenes featuring ANY of these performers
SELECT DISTINCT s.date, s.title,
       COALESCE(NULLIF(s.studio, ''), s.studio_url) AS studio
FROM scenes s
JOIN scene_performers sp
  ON sp.scene_id = s.id AND sp.site_id = s.site_id AND sp.studio_url = s.studio_url
JOIN performers p ON p.id = sp.performer_id
WHERE p.name COLLATE NOCASE IN ('PERFORMER A', 'PERFORMER B')
  AND s.deleted_at IS NULL
ORDER BY s.date DESC;

-- Scenes featuring ALL of them together. Raise the HAVING count to match how
-- many names you listed.
SELECT s.date, s.title,
       COALESCE(NULLIF(s.studio, ''), s.studio_url) AS studio
FROM scenes s
JOIN scene_performers sp
  ON sp.scene_id = s.id AND sp.site_id = s.site_id AND sp.studio_url = s.studio_url
JOIN performers p ON p.id = sp.performer_id
WHERE p.name COLLATE NOCASE IN ('PERFORMER A', 'PERFORMER B')
  AND s.deleted_at IS NULL
GROUP BY s.id, s.site_id, s.studio_url
HAVING COUNT(DISTINCT p.name COLLATE NOCASE) = 2
ORDER BY s.date DESC;

-- Who one performer appears with most often
SELECT co.name AS co_performer, COUNT(*) AS shared_scenes
FROM performers p
JOIN scene_performers sp  ON sp.performer_id = p.id
JOIN scene_performers sp2 ON sp2.scene_id  = sp.scene_id
                         AND sp2.site_id   = sp.site_id
                         AND sp2.studio_url = sp.studio_url
                         AND sp2.performer_id <> sp.performer_id
JOIN performers co ON co.id = sp2.performer_id
WHERE p.name = 'PERFORMER NAME' COLLATE NOCASE
GROUP BY co.name
ORDER BY shared_scenes DESC
LIMIT 25;

-- Solo scenes: exactly one credited performer
SELECT s.date, s.title
FROM scenes s
JOIN scene_performers sp
  ON sp.scene_id = s.id AND sp.site_id = s.site_id AND sp.studio_url = s.studio_url
WHERE s.deleted_at IS NULL
GROUP BY s.id, s.site_id, s.studio_url
HAVING COUNT(*) = 1
ORDER BY s.date DESC;
```

**Tags and cast**

```sql
-- Scenes carrying a tag
SELECT s.title, s.date
FROM scenes s
JOIN scene_tags j
  ON j.scene_id = s.id AND j.site_id = s.site_id AND j.studio_url = s.studio_url
JOIN tags t ON t.id = j.tag_id
WHERE t.name = 'TAG NAME'
  AND s.deleted_at IS NULL
ORDER BY s.date DESC;

-- The cast of one scene, in billing order
SELECT p.name
FROM scene_performers sp
JOIN performers p ON p.id = sp.performer_id
WHERE sp.scene_id = 'SCENE_ID'
  AND sp.site_id = 'SITE_ID'
  AND sp.studio_url = 'STUDIO_URL'
ORDER BY sp.position;

-- Most used tags overall
SELECT t.name, COUNT(*) AS scenes
FROM scene_tags j
JOIN tags t ON t.id = j.tag_id
GROUP BY t.name
ORDER BY scenes DESC
LIMIT 25;

-- One tag but not another
SELECT s.date, s.title
FROM scenes s
JOIN scene_tags j
  ON j.scene_id = s.id AND j.site_id = s.site_id AND j.studio_url = s.studio_url
JOIN tags t ON t.id = j.tag_id
WHERE t.name = 'WANTED TAG'
  AND s.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM scene_tags j2
    JOIN tags t2 ON t2.id = j2.tag_id
    WHERE j2.scene_id = s.id AND j2.site_id = s.site_id AND j2.studio_url = s.studio_url
      AND t2.name = 'UNWANTED TAG')
ORDER BY s.date DESC;

-- A performer and a tag together
SELECT s.date, s.title
FROM scenes s
JOIN scene_performers sp
  ON sp.scene_id = s.id AND sp.site_id = s.site_id AND sp.studio_url = s.studio_url
JOIN performers p ON p.id = sp.performer_id
JOIN scene_tags j
  ON j.scene_id = s.id AND j.site_id = s.site_id AND j.studio_url = s.studio_url
JOIN tags t ON t.id = j.tag_id
WHERE p.name = 'PERFORMER NAME' COLLATE NOCASE
  AND t.name = 'TAG NAME'
  AND s.deleted_at IS NULL
ORDER BY s.date DESC;
```

**What changed**

```sql
-- Scenes that entered the store since a date (see firstSeenAt in metadata.md)
SELECT title, first_seen_at, studio_url
FROM scenes
WHERE first_seen_at >= '2026-01-01'
ORDER BY first_seen_at DESC;

-- Scenes that disappeared from their site (soft-deleted by --refresh)
SELECT title, studio_url, deleted_at
FROM scenes
WHERE deleted_at IS NOT NULL
ORDER BY deleted_at DESC;
```

**Pricing**

```sql
-- Deepest discounts ever recorded
SELECT s.title, ph.regular, ph.discounted, ph.discount_percent, ph.date
FROM scenes s
JOIN price_history ph
  ON ph.scene_id = s.id AND ph.site_id = s.site_id AND ph.studio_url = s.studio_url
WHERE ph.is_on_sale = 1
ORDER BY ph.discount_percent DESC
LIMIT 25;

-- Price timeline for one scene
SELECT date, regular, discounted, is_on_sale, discount_percent
FROM price_history
WHERE scene_id = 'SCENE_ID' AND site_id = 'SITE_ID' AND studio_url = 'STUDIO_URL'
ORDER BY date ASC;
```

Price history is a log of *changes*, not of scrapes: a snapshot identical to the
previous one is dropped, so consecutive rows always differ.

**Cross-site identity**

```sql
-- Scenes that different sites agree are the same, via a shared external ID
SELECT e.source, e.external_id, COUNT(*) AS copies
FROM scene_external_ids e
GROUP BY e.source, e.external_id
HAVING copies > 1
ORDER BY copies DESC;
```

Returns nothing until something populates `scene_external_ids` — today the
stashbox scraper is the only producer. See
[metadata.md](metadata.md#cross-site-identity).

Without external IDs, matching titles is the rough approximation — useful for
spotting the same release sold through several studios:

```sql
SELECT title, COUNT(DISTINCT studio_url) AS studios
FROM scenes
WHERE deleted_at IS NULL
GROUP BY lower(title)
HAVING studios > 1
ORDER BY studios DESC, title
LIMIT 50;
```

Titles are not identifiers, so expect both misses and coincidences. `match`
normalises far more aggressively than `lower()` when it does this properly — see
[stash.md](stash.md#matching-strategy).

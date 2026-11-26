---
name: fss
description: Drive the fss (FullStudioScraper) command-line tool, which scrapes adult studio and clip-store catalogues (2,000+ sites) into JSON or SQLite and pushes the metadata into Stash or .nfo sidecars. Use this whenever the user wants to check whether a site is supported, scrape or update a studio's scene list, find what is new or removed, compare a creator's prices across storefronts, match video files to scene metadata, or import scene metadata into Stash, even if they do not name fss.
---

# fss

fss is a local command-line program. Run it in a shell and read its results.
It never needs a browser, and it does not log in to sites.

## Always ask for JSON

Every command you are likely to run takes `--format json`. With it, stdout holds
JSON only and everything meant for people goes to stderr, so parse stdout and
show stderr to the user only when something went wrong.

Most commands print one object:

```json
{"formatVersion":1,"command":"check","ok":true,"result":{},"error":null}
```

`fss scrape` instead prints one line per studio as each finishes, then a line with
`"type":"summary"`. Read the lines as they arrive; a large catalogue can take an hour.

- `ok` is true exactly when the exit code is 0.
- On failure, branch on `error.code`, not on the wording of `error.message`.
- Ignore fields you do not recognise; new ones get added.

| `error.code` | What it means | What to do |
|---|---|---|
| `invalid_args` | Bad flag or argument | Fix the command; `fss <command> --help` lists the flags |
| `config` | The config file did not load | Run `fss config path`, show the user the error |
| `unsupported_url` | No scraper handles that URL | Run `fss check`, then `fss detect`; offer `result.requestSupportUrl` |
| `transport` | A page did not arrive. `httpStatus` may say why (429, 403, 5xx) | Wait and retry once; do not lower `--delay` |
| `parse` | The site answered but fss could not read it, usually a redesign | Do not retry; tell the user the scraper needs fixing |
| `refused` | fss declined to overwrite stored data. `reason` is `zero_scenes` or `coverage_collapse` | Stop and ask the user (see below) |
| `interrupted` | The run was stopped. Partial results were saved | Tell the user; see "No resume" |
| `stash_unreachable` | Stash did not answer | Check the URL and API key with the user |
| `error` | Anything else | Show `error.message` |

## Common tasks

**Is a site supported?** `fss check <url> --format json`. An unsupported URL
is a normal answer (`ok` true, `supported` false). For an unsupported site,
`fss detect <url> --format json` fetches the page once and names the platform
it runs on, which helps whoever writes the scraper.

**Scrape a studio.** `fss scrape <url> --format json`. The first run fetches the
whole catalogue; later runs fetch only what is new. Each target line has a
`status` (`saved`, `partial`, `refused`, `failed`, `interrupted`, `skipped`),
`scenesSaved`, `coverage` against what was stored before, `complete`, page
`failures` by kind, and `written`, the files it wrote.

`complete: false` or non-zero `failures` with status `saved` means some pages
failed but what arrived was kept. Say so; do not present it as the full catalogue.

**Read what was scraped.** Each studio is one JSON file (path in `written`).
Its layout is described by the JSON Schema at
https://github.com/Anastylosis/FSS/blob/master/docs/schema/studio-file.schema.json.
Only `id`, `siteId`, `title`, `url` and `scrapedAt` are always filled in;
a missing key means unknown, not zero or false. `deletedAt` marks a scene the
site has removed. If the user stores data in SQLite (`--db`), read it with
`fss list-studios --db --format json` or with SQL against the database file.

**Several storefronts of one creator.** `fss compare --format json` (with
`--from-creator "<name>"` to narrow it) reports overlap, the cheapest store
per shared title and the widest price gaps. Creator groupings live in
`creators.d`; `fss creators suggest` proposes them.

**Push metadata into Stash.** Always run the dry run first:
`fss stash import --format json`. It changes nothing and lists, per scene,
the field changes it would make (`scenes[].changes`) and the performers, tags
and studios it would create (`wouldCreate`). Summarise that for the user. Run
again with `--apply` only after they say yes. `--fields` limits what is
touched, and `--top` limits how many scenes.

**Write .nfo files for Kodi or Jellyfin.** `fss identify <video-dir>` is a dry
run; `--apply` writes the files. It has no JSON output; read its text.

**Check the setup.** `fss doctor --format json` checks the config, database,
output directory, Stash connection and network.

## Ask before you do any of these

These lose or overwrite data, or act on the user's behalf. Explain what will
happen and wait for a yes.

- `scrape --full` or `scrape --refresh`. They make the stored catalogue match
  the site: `--full` deletes scenes the site no longer lists, `--refresh` marks
  them removed. An ordinary incremental `fss scrape <url>` only adds, so prefer it.
- `--force`. fss refuses (`refused`) when a full scrape returned nothing, or
  far less than was stored. That is usually a broken scraper or a site change,
  not a shrunken catalogue. `--force` overrides the guard and can wipe the stored
  catalogue. Show the user the `coverage` numbers and let them decide.
- `stash import --apply`, `identify --apply`, `fss stash revert`.
- `fss doctor --prune`, which deletes unused performer, tag and category rows.
- `fss version --update`, which replaces the fss binary.
- `--site-cookie`. Only use a cookie the user gives you. Do not take one from a
  browser yourself, and do not try to get past a bot check or login wall.

## Be gentle with sites

- Leave `--delay` alone unless the user asks; it spaces out requests so sites
  do not block the user. Raising `--workers` does not speed up a rate-limited site.
- Do not run two scrapes of the same studio at once. The second one waits for
  the first.

## No resume

An interrupted first scrape keeps the newest scenes it reached, and the next
ordinary run stops early as soon as it sees them, so the older part of the
catalogue is never fetched. If a first full scrape of a large catalogue was
interrupted, finish it with `fss scrape --refresh <url>` (ask first, as above),
and in general let first runs finish in one go.

## Where things are

- Config: `fss config path` lists where fss looks. `fss config init` creates one.
- Scrape output: the current directory, or `out_dir` from the config, or `--out-dir`.
- Full flag lists: `fss <command> --help`.
- Every JSON shape in detail: https://github.com/Anastylosis/FSS/blob/master/docs/json-output.md

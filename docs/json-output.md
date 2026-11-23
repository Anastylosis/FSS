# JSON output

Commands that answer a question or report on a run take `--format json`. stdout
then carries JSON only. Anything meant for a person, such as progress lines,
notices and the final `Error:` line, goes to stderr. It is meant for scripts and
for LLM agents driving fss as a subprocess.

The flag is `--format`, not `--json`. `--json` already names input files on
`stash import`, `compare`, `identify` and `creators suggest`. It is also unrelated
to `scrape --output json`, which picks the format of the files a scrape writes.

| Command | Shape |
|---|---|
| `check`, `detect`, `list-scrapers`, `list-studios`, `doctor`, `version` | one envelope |

For the files a scrape writes, see [schema/studio-file.schema.json](schema/studio-file.schema.json)
and [metadata.md](metadata.md).

## The envelope

```json
{"formatVersion":1,"command":"check","ok":true,"result":{…},"error":null}
```

- `formatVersion` versions these shapes. It is bumped only for a change that
  breaks an existing reader. New fields can appear at any time, so ignore keys
  you do not know. It is not the `schemaVersion` inside studio files, which is a
  separate number.
- `command` is the command path without `fss`, e.g. `"stash import"`.
- `ok` is true exactly when the process exits 0.
- `result` is the command's own object. A failing command still fills it when it
  has something to report (`doctor` does). Otherwise it is `null`.
- `error` is `null` or `{"code", "message", "reason"?, "httpStatus"?}`.

Exit codes are unchanged: `0` success, `1` failure, `2` a save was refused or
declined, `3` interrupted.

Errors that happen before a command runs still produce an envelope on stdout
when `--format json` is on the command line. That covers an unknown flag, a bad
`--format` value and an unreadable config file. Usage text goes to stderr.

## Error codes

Branch on `code`. The `message` is for people and its wording can change.

| `code` | Meaning | Usual next step |
|---|---|---|
| `invalid_args` | Bad flag, argument or flag combination | Fix the invocation |
| `config` | The config file could not be loaded | Check `fss config path` |
| `unsupported_url` | No scraper handles the URL | `fss check` / `fss detect` |
| `transport` | A page never arrived (network, 5xx, 403, 429). See `httpStatus` | Retry later, raise `--delay` |
| `parse` | A page arrived but could not be read; the site probably changed | Report it, retrying will not help |
| `refused` | fss declined to save. `reason` is `zero_scenes` or `coverage_collapse` | Inspect, then `--force` if the site really shrank |
| `interrupted` | SIGINT/SIGTERM. Partial results were saved | Re-run; see the no-resume caveat in usage.md |
| `stash_unreachable` | Stash did not answer | Check `stash_url` / API key |
| `error` | Anything else | Read `message` |

`httpStatus` is set when the failing request returned an HTTP status. FSS does
not try to tell a WAF block from an ordinary 403.

## check

```json
{"url":"https://brazzers.com","supported":true,"scraper":"brazzers","patterns":["brazzers.com/videos", "…"]}
{"url":"https://example.org","supported":false,"requestSupportUrl":"https://github.com/Anastylosis/FSS/issues/new?…"}
```

An unsupported URL is an answer, not an error: `ok` is true and the exit code is 0.

## detect

```json
{"url":"https://example.org","supported":false,"platforms":[{"platform":"WordPress","package":"wputil (standalone)","detail":"WordPress detected"}]}
```

If a scraper already claims the URL, `supported` is true, `scraper` and
`patterns` are set, and nothing is fetched. `platforms: []` means no known
signal matched. A failed fetch is a `transport` error, and `result` still
carries `url` and an empty `platforms`.

## list-scrapers

```json
{"count":2045,"scrapers":[{"id":"a1c","patterns":["a1c.jp/~{brand}/","…"],"languages":["en","ja"]}]}
```

`languages` appears only for scrapers that serve several content languages. The
first entry is the default. `--format` cannot be combined with `--markdown`.

## list-studios

```json
{"count":1,"studios":[{"url":"https://…","siteId":"clips4sale","name":"…","lastScrapedAt":"2026-08-27T12:34:47Z"}]}
```

Needs `--db`. `lastScrapedAt` is `null` for a studio that has never finished a scrape.

## doctor

```json
{"allOk":false,"checks":[{"section":"Config","name":"config file","ok":true,"detail":"/home/…/config.yaml"}]}
```

When a check fails, the result is still filled in, `ok` is false and the exit
code is 1. `--prune` still deletes, and the `vocabulary` check's `detail` says
what was removed.

## version

```json
{"version":"v1.40.0","commit":"…","date":"…","channel":"release","latest":"v1.41.0","updateAvailable":true}
```

A failed update check is not an error: `latest` is `null` and
`updateCheckError` says why. `--update` cannot be combined with `--format json`.

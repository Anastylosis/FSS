# Storage: how FSS holds your data

What FSS actually does with a scrape, which store to use today, how to see
what's inside one, and what has to be true before SQLite becomes the default.

For the field-level model (`schemaVersion`, `firstSeenAt`, `externalIds`, merge
rules), see [metadata.md](metadata.md).

---

## The lifecycle

FSS is three stages, and only the middle one is storage:

```
   scrape                    store                     consume
┌────────────┐        ┌────────────────┐        ┌────────────────────┐
│ ~290 site  │───────▶│  Flat (JSON)   │───────▶│ fss stash import   │
│  scrapers  │        │       or       │        │ fss identify (nfo) │
│            │        │  SQLite (--db) │        │ fss export (csv)   │
└────────────┘        └────────────────┘        └────────────────────┘
```

1. **Scrape.** `fss scrape <studio-url>` runs one site scraper and collects
   `models.Scene` values. Three modes: incremental (default, stops early at a
   known ID), `--full`, and `--refresh` (also soft-deletes what vanished).
2. **Store.** The results are merged with what you already had and written.
   This is where the two implementations differ, and nowhere else — both satisfy
   the same `store.Store` contract and the same contract tests.
3. **Consume.** Push metadata into Stash, write `.nfo` sidecars for Kodi/Jellyfin,
   or export CSV for a spreadsheet.

The important consequence: **storage is an implementation detail of stage 2.**
Nothing about a scraper changes when you switch stores.

Every consumer reads either source, so switching stores changes nothing
downstream.

---

## The two stores

|  | Flat (default) | SQLite (`--db`) |
|---|---|---|
| Layout | one `<slug>.json` per studio | one file, all studios |
| Human-readable | yes, directly | via `fss export` or a viewer |
| Concurrent studios | one file each | one database, row-level keys |
| Queryable | no | yes |

### Measured cost

On the largest real catalogue to hand — **59,254 scenes, a 104 MB studio file** —
one incremental round trip (`Load`, add a single new scene, `Save`):

| | Flat | SQLite |
|---|---|---|
| Load + Save | 2.0 s | **2.2 s** |
| ↳ of which Save | 1.3 s | **0.5 s** |
| ↳ of which Load | 0.7 s | 1.7 s |
| Peak RSS | 964 MB | **634 MB** |
| On disk | **104 MB** | 248 MB |
| Initial ingest | — | 13 s (one-off) |

The two are now within ~25% on wall clock, and SQLite uses a third less memory.

- **Flat** parses and re-marshals the entire file on every save. `encoding/json`
  is fast at that, but it costs *memory*: ~964 MB peak for a 104 MB file. On a
  small VPS that is an OOM waiting to happen, and it grows with the catalogue.
- **SQLite** writes only what changed. `Save` fingerprints each scene
  (`content_hash`) and skips any whose stored fingerprint matches, so an
  incremental scrape touches a handful of rows instead of all 59,254.

The remaining one-off cost is the **initial ingest** (~13 s for 59k scenes),
paid once when you `fss import` an existing catalogue.

### Which should I use today

- **You want to query your data** — scenes by performer across every site, price
  history over time, per-studio counts: `--db`. The flat store cannot answer any
  of these, at all.
- **Large catalogue, or a memory-constrained host:** `--db`. A third less peak
  RSS, and the gap widens as the catalogue grows.
- **You want `fss list-studios` or `--name`:** `--db`. `Flat.UpsertStudio` and
  `Flat.ListStudios` are no-ops — studio tracking only exists in SQLite.
- **A couple of small studios and you want to read the raw file:** flat is
  simpler and there is no reason to move.

---

## Seeing what is inside a database

The common objection to SQLite is "now I need a SQL viewer to see my data."
In practice the opposite is closer to true — a 104 MB JSON file cannot be
opened in most editors, while a database answers a question instantly.

Four ways, no SQL required:

**1. Export it back to files.** The round trip is lossless (bar sub-second
timestamps, which SQLite has never stored):

```bash
fss export --db --out-dir ./data -o json,csv   # every tracked studio
fss export --db --out-dir ./data <studio-url>  # just one
```

**2. List what is tracked.**

```bash
fss list-studios --db
```

**3. Export CSV and open it in a spreadsheet.** `-o csv` gives one row per
scene with every field as a column — the most approachable view there is.

**4. Use a viewer if you want one.** The database is a single ordinary file:

- [DB Browser for SQLite](https://sqlitebrowser.org/) — free GUI, all platforms
- `sqlite3 ~/.local/share/fss/fss.db` — the official CLI
- [Datasette](https://datasette.io/) — browse and query in a web UI

The schema is small and readable: `scenes`, `studios`, `price_history`,
`scene_external_ids`, plus `performers`/`tags`/`categories` and their junction
tables. See [usage.md](usage.md#sqlite) for the full column list and
[example queries](usage.md#example-queries) covering studios, following a
performer across sites, tags, price history and what changed since a date.

---

## How the SQLite store stays fast

Four things carry the performance in the table above. Each is easy to undo by
accident, so they are written down rather than left to be rediscovered.

**`Save` writes only what changed.** Each scene is fingerprinted into
`content_hash`; a scene whose stored fingerprint matches skips the row upsert,
all three relation syncs and the price-history diff. When only `scraped_at`
moved, it issues one narrow `UPDATE`.

Two properties make that correct:

- The hash is computed over the **stored** representation, using the same
  `timeStr` conversions as the writer. Hashing in-memory values would make a
  freshly scraped scene (nanosecond timestamps) differ from the same scene
  loaded back (second precision), and nothing would ever be skipped.
- `ScrapedAt` and `FirstSeenAt` are excluded — the first changes on every scrape
  by definition, and the second is store-owned and never changes once set.

> **Invariant:** anything writing scene state outside `upsertScene` must
> invalidate `content_hash`. `MarkDeleted` sets it to `''` for exactly this
> reason — without that, re-saving a soft-deleted scene with `DeletedAt == nil`
> would be skipped and the delete would never lift.

**`Load` groups relations in SQL and orders them in Go.** A 59k-scene studio has
1.4M `scene_tags` rows; returning one driver row per name put ~70% of `Load` in
`database/sql`'s `Rows.Next`. The query now aggregates names into JSON arrays,
one row per scene, and the ordering is applied in Go from the stored `position`.

Do not add `ORDER BY` back to these queries: it makes SQLite build temp B-trees
over every row in the studio to order lists that are a handful of entries each.

**The child tables are indexed by `(studio_url, scene_id, site_id, position, …)`**
(migration 8). Their primary keys start with `scene_id`, so a `studio_url`
predicate cannot use them; without an index every `Load` scans the whole table.
The index leads with `studio_url` for the filter and continues with the grouping
columns so the `GROUP BY` streams instead of building a temp B-tree — with a
narrow `studio_url`-only index the aggregate is *slower* than returning flat
rows (0.72 s vs 0.46 s), and with the covering index it is faster (0.33 s).

That is the opposite of the right answer before the query grouped, which is why
migration 8 supersedes migration 7's narrow indexes. The cost is real: on a
40-studio database the covering indexes add ~29 MB to ~303 MB, almost all of it
`idx_scene_tags_studio` over those 1.4M rows.

**Large saves share a `saveSession`** (`internal/store/savesession.go`), caching
prepared statements by SQL text and entity name→id lookups. Without it a first
ingest spent a third of its CPU re-parsing SQL and resolved every
performer/tag/category name with its own round trip. Its statements are bound to
the transaction, so the session is closed before `Commit`.

## What is still not optimised

**Single-studio `Load` (1.7 s for 59k scenes)** is still ~2.5× the flat store's
0.7 s. What remains is SQLite executing the query — `sqlite3VdbeExec` is 46% of
the profile — plus decoding the grouped JSON. There is no obvious next step that
does not trade correctness for speed.

One structural option is left: reorder the junction primary keys to lead with
`studio_url`. The PK index would then serve both the filter and the grouping,
the separate `idx_scene_*_studio` indexes could be dropped entirely, and the
~29 MB they cost would come back. It needs a table rebuild of the junction
tables, which is why it has not been done for a ~20% gain on one query.

## Making SQLite the default

`fss doctor` compares the two stores before you commit to the switch. It reports
which store is *active* — distinct from whether a database merely exists — and
names any studio that lives only as JSON:

```
  active store           ok    (SQLite)
  store contents         FAIL  (12 studio(s) in JSON, 11 in the database
    only in JSON (1):
      https://example.com/studio/4021/mara-vance
      run `fss import` to bring these into the database)
```

That is the failure the switch creates: a studio the database has never seen is
invisible to `stash import` and `identify` once they read from it. The file is
still on disk and nothing is lost, but the catalogue reads as empty, which is
indistinguishable from the upgrade having eaten it.

Only *missing from the database* fails the check. A studio present in the
database but not on disk is normal after exporting or tidying JSON away, and a
scene-count mismatch is reported so it can be re-imported. An unreadable studio
file is reported separately — `fss import` cannot fix a corrupt file, so
counting it as "absent" would send you after the wrong remedy.

Nothing technical blocks it now: both consumers read either source, and the
database is competitive on the numbers above. What is left is the rollout.

Two shapes were considered and rejected. `--no-db` is a double negative that
strands every script already passing `--db`. A separate `--store files|sqlite`
selector was the replacement plan until it turned out to be redundant: `--output`
is a different axis (which *files* to produce, not where data *lives*), and the
one thing `--store` was needed for — saying "JSON this time" on the command line
when `db:` is set — `--db` already covers.

So there is no new flag. `--db` carries every case, because "not passed" and
"passed as empty" are distinguished:

| | Result |
|---|---|
| *(no `--db`)* | the config's `db:` decides |
| `--db=""` | no database, explicitly — overrides the config |
| `--db` | the database named in `db:`, or the default location |
| `--db=/path` | exactly that file |

The flip is then a one-line change to the config default, plus a release note.

Changing a default silently reorganises where people's data lives. The migration
path has to exist and be documented before the default moves, which is what
`fss import` and `fss export` are for.

### Staying on JSON

The flat store is not going away. An explicit `db: ""` in your config pins it,
and keeps doing so after the default flips — the config distinguishes "set to
empty" from "never set", precisely so that opt-out survives the change.

### Choosing where the database lives

`db:` accepts three kinds of value:

| Value | Meaning |
|---|---|
| absent | not configured (today: flat store) |
| `""` | flat store, explicitly — survives the default flip |
| `"default"` | the XDG data path, `~/.local/share/fss/fss.db` |
| any path | exactly that file, absolute or **relative to the working directory** |

So `db: "./fss.db"` keeps the database next to your JSON output rather than
under `~/.local/share`, which is what you want for a self-contained directory or
a bind-mounted container volume. The same values work as `--db=<value>`, and a
bare `--db` uses whatever you configured — it only falls back to the XDG path
when `db:` is unset.

Until then, `fss scrape` prints a one-line notice when it uses the flat store,
so the change is announced well before it happens rather than discovered
afterwards. It appears once per process, on stderr, and is silenced with
`notices: false` in config or `FSS_NO_NOTICES=1`.

---

## Moving between stores

Both directions already work and are lossless:

```bash
fss import --db ./data/                  # JSON files → database (merges)
fss import --db --replace ./data/        # JSON files → database (authoritative)
fss export --db --out-dir ./data -o json # database → JSON files
```

`import` keys on each file's own `studioUrl`, never the filename — `Slugify` is
lossy and its hash suffix is not reversible. Files are processed oldest-first by
modification time so a re-scrape wins over the original it sits beside, and two
files describing one studio are reported rather than silently resolved. It also derives the `studios` row,
which JSON does not carry, from the scenes themselves.

Nothing in the database schema needed to change to support this: it is a strict
superset of the JSON layout.

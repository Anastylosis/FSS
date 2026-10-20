# Metadata model & storage

How FSS represents scene metadata, what it guarantees across re-scrapes, and how
the two stores relate.

## Stores

| | Flat (default) | SQLite (`--db`) |
|---|---|---|
| Layout | one `<slug>.json` per studio | one database, all studios |
| Read cost | full parse of the whole file | indexed query per studio |
| Write cost | full rewrite of the whole file | upsert of changed scenes only |
| Queryable across studios | no | yes |

Both implement `store.Store` and are covered by the same contract tests
(`internal/store/contract_test.go`), so behaviour is identical apart from cost.

**`Save` is authoritative over the full scene set** in both stores, but only
SQLite is incremental about honouring that: it fingerprints each scene into
`content_hash` and rewrites only what changed. The flat store re-marshals the
whole JSON file every time.

On a 59k-scene studio that makes SQLite's `Save` ~2.5× faster than flat's and
its peak memory a third lower, at the cost of 2.4× the disk. See
[storage.md](storage.md) for the full measurements.

**Invariant:** anything writing scene state outside `upsertScene` must clear
`content_hash`, or `Save` will skip the row as unchanged. `MarkDeleted` does.

### Moving between them

The SQLite schema is a strict superset of the JSON layout, so the conversion is
just a loop over studio files:

```bash
fss import --db ./out/                     # JSON files → database
fss export --db --out-dir ./out -o json    # database → JSON files
```

A round trip is lossless except for sub-second timestamp precision, which SQLite
has never stored. `import` merges by default and takes `--replace` to make the
file authoritative; see [usage.md](usage.md).

Studio rows (`name`, `added_at`) are not in the JSON, so `import` derives them:
site ID and name from the scenes, `added_at` from the earliest `firstSeenAt`.

## Schema versioning

`models.StoreSchemaVersion` is stamped into every `StudioFile` as
`"schemaVersion"`. It exists so a reader can *refuse* a file it does not
understand: `Save` is authoritative and rewrites the whole file, so parsing a
newer layout on a guess would silently drop whatever that layout added.

- Bump it only when a change alters how an **existing** file must be interpreted.
- Do **not** bump for additive optional fields — old readers ignore them, new
  readers see zero values.
- Version `0` means "written before versioning existed". Those files load
  normally and are stamped on the next save.
- A version **greater** than this build's is a hard error naming the file.

SQLite has had the equivalent since v0: a `schema_version` table plus numbered
migrations in `internal/store/sqlite.go`.

## Scene lifetime fields

| Field | Meaning | Written by |
|---|---|---|
| `firstSeenAt` | when the scene first entered the store | the store, once |
| `scrapedAt` | when the scene was last fetched | the scraper, every run |
| `deletedAt` | soft-delete marker set by `--refresh` | the cmd layer |

`Save` writes every field verbatim **except `firstSeenAt`**, which is sticky: an
existing value is never overwritten, and a scene arriving without one is stamped
from its `scrapedAt`. Scrapers do not set it; both stores enforce it, so any
caller of `Save` gets the same behaviour.

It is not recoverable after the fact, which is why it is stamped at write time
rather than derived later. Scenes stored before the field existed were
backfilled from `scraped_at` (SQLite migration 4) or are backfilled on their
next save (flat store). That is an upper bound on the true first sighting, not
a measurement.

`firstSeenAt` is also a CSV column, placed before `scrapedAt`.

## Re-scrapes never blank a field

`Save` is authoritative: it writes what it is given and hard-deletes the rest.
That made a parser regression destructive — a site redesign that left the
scraper returning correct IDs but empty tags, performers and descriptions would
replace good stored metadata with nothing on the next `--refresh`.

So a freshly-scraped scene that matches a stored one now inherits every field it
left empty (`preserveEnrichment` in `cmd/scrape.go`). Price history was already
protected this way; the rest of the record now is too. `externalIds` are unioned
rather than replaced, since different sources learn a scene at different times.

Two things this deliberately does *not* do:

- **Detect partial loss.** A scrape returning 2 of 3 tags wins outright. Only
  wholly-empty fields fall back.
- **Allow genuine removals.** A field the site really emptied stays populated.
  That is the accepted cost; losing a catalogue to a parser bug is worse.

`fss scrape --no-preserve` restores the old behaviour for a real rebuild.

`--performer`/`--studio` are unaffected by any of this. They replace those
fields on every scene a run scrapes *before* the fresh scene reaches
`preserveEnrichment`, and what they write is non-empty, so there is nothing for
the fallback to restore — the operator's value wins over both the site's and the
stored one. See [usage.md](usage.md#relabelling-a-scrape---performer----studio).

## Cross-site identity

`(id, siteId)` is site-local: the same scene scraped from two sites has two
unrelated keys, so matching them falls back to normalising titles. `externalIds`
carries the IDs that *are* shared — a map from metadata database to this scene's
ID in it:

```json
"externalIds": { "stashdb": "0ec8a4bd-…", "tpdb": "…" }
```

Well-known keys are `models.ExternalStashDB`, `ExternalTPDB`, `ExternalIAFD`,
`ExternalIndexxx`. A stashbox instance uses its own site ID, so a second
configured instance (`pmvstash`) contributes its own key.

The stashbox scraper populates its instance's key from the scene UUID it already
returns. Other scrapers should set a key only when the site genuinely publishes
one — a guess is worse than an absent entry.

SQLite stores these in `scene_external_ids`, indexed on `(source, external_id)`
so "which stored scenes are this StashDB UUID" is answerable across every site
and studio.

## Name normalisation in cross-site merges

`Performers`, `Tags` and `Categories` are plain `[]string`, so the display name
*is* the join key everywhere downstream — `fss stash import` looks entities up in
Stash by exact name.

`match.MergeScenes` deduplicates on a canonical key (`NormalizeName`:
case-folded, whitespace-collapsed) while storing the first contributing site's
spelling (`cleanName`: trimmed, internal whitespace collapsed, case untouched).
So `"Nikki Nuttz"` and `"nikki  nuttz"` merge to one entry written as
`Nikki Nuttz`. Which spelling wins follows the order scenes are passed in —
deterministic, but not "best".

Keeping both spellings instead would create two Stash performers for one person,
which is why the key is folded even though the stored value is not.

Scrapers should still avoid emitting stray whitespace; normalising here also
repairs catalogues already on disk, which no scraper fix can do without a full
re-scrape.

### Storefront branding, folded before the store sees it

The merge key above resolves spelling differences. It cannot resolve a credit
that is not a person's name at all: storefronts routinely list *themselves* as
the performer, so a shop trading as `Emma Woodhouse Films` credits `Emma Woodhouse Films`
on every scene. That is a different string from the person's name by any
canonical key, so merging keeps them apart — correctly, since nothing in the
scene says they are the same.

What resolves it is knowing the storefront belongs to that person, which is
exactly what a [creator file](creators.md) records. So a scrape of a listed store
rewrites branding credits to the creator's name in `collectScenes`, before merge,
`preserveEnrichment` or `Save` ever see the scene. A credit counts as branding
when it matches the scene's own `Studio` value — a shop crediting itself fills
both fields with the same string — or a spelling declared in the creator file.

Two properties this relies on:

- It is a **rewrite, not a replacement**. Only the branding credit changes;
  co-stars are stored exactly as published. This is what separates it from
  `--performer`, which replaces the whole list.
- It only affects what a scrape **stores**. Scenes already saved keep the credits
  they were saved with, so folding an existing duplicate needs a `--refresh` of
  that store.

Studios are deliberately left alone — see
[Studios are not merged](creators.md#studios-are-not-merged-and-should-not-be).

## Merge provenance

`MergedScene.Sites` said *which* sites contributed, but not what each one
contributed. Merging picks a winner per scalar field — first non-empty title,
longest description, earliest date, largest duration — and the losing values
were unrecoverable.

`MergedScene.Sources` now maps each scalar field to a `FieldSource`:

```go
type FieldSource struct {
    Site      string   // site ID whose value was kept ("" if it came from Stash)
    Discarded []string // competing values that lost, as "siteID: value"
}
```

Tracked fields: `title`, `description`, `date`, `studio`, `thumbnail`,
`duration`, `resolution`. A field no site supplied has no entry. Identical
values from two sites are agreement, not a conflict, so `Conflicted()` stays
false.

`fss stash import` prints conflicts during a dry run:

```
  ~ date: kept siteb, dropped sitea: 2026-01-05
```

This is what separates a merge from a guess — you can see that two sites
disagreed instead of being shown one value as if it were uncontested.

## Compatibility

Changes to the metadata layout are additive. Concretely:

- **Old files, new binary** — a missing `schemaVersion` reads as 0 and loads;
  `first_seen_at` is backfilled by SQLite migration 4 and on the next save for
  the flat store.
- **New files, old binary** — JSON decoding ignores unknown keys, and both the
  SQLite `SELECT` and `INSERT` name their columns explicitly, so an older build
  opens a migrated database without error. It will not *populate* the new
  fields, and because `Save` is authoritative, an old build that rewrites a new
  studio file drops them. The `schemaVersion` guard prevents this from v2
  onward; it cannot retroactively teach older builds to refuse.
- **CSV** — new columns are appended, never inserted, so positional readers keep
  working. `firstSeenAt` and `externalIds` are the last two columns.

## Scraping multiple studios

Studios never overwrite each other:

- SQLite keys scenes on `(id, site_id, studio_url)`, and `Save` deletes only
  rows matching the studio URL being saved. Junction and price-history rows are
  studio-qualified too.
- The flat store gives each studio its own file; `Slugify` appends a hash of the
  raw URL so two URLs that sanitize alike still get distinct filenames, and a
  stored-URL mismatch is an error rather than a silent overwrite.
- `performers`, `tags` and `categories` are global tables with `UNIQUE(name)`.
  Sharing them across studios is deliberate deduplication.

The one cross-studio effect is **duplication, not loss**: a scene reachable from
two studio URLs is stored once per URL.

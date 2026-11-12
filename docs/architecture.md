# Architecture

This document describes the high-level design of FSS for contributors. For usage, see [usage.md](usage.md). For adding a scraper, see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Overview

```
main.go                      ← blank-imports each scraper to trigger init()
  │
  ├── cmd/scrape.go           ← CLI entry point, orchestrates scraping
  │     │
  │     ├── scraper.ForURL()  ← registry lookup: URL → scraper
  │     │
  │     ├── scraper.ListScenes() → chan SceneResult
  │     │                          ↑ goroutine streams results
  │     │
  │     ├── collectScenes()   ← drains channel, displays progress
  │     │
  │     └── store.Save()      ← persists to JSON or SQLite
  │
  ├── cmd/stash_import.go     ← matches FSS scenes → Stash scenes
  │     ├── match.BuildIndex()
  │     ├── idx.Match()
  │     └── stash.Client.UpdateScene()
  │
  ├── cmd/stash_unmatched.go  ← queries Stash for unmatched scenes
  ├── cmd/stash_revert.go     ← undoes a previous stash import
  ├── cmd/identify.go         ← matches video files → NFO sidecars
  └── cmd/version.go          ← prints version and checks for updates
```

## Plugin Registry

**Files:** `scraper/registry.go`, `scraper/interface.go`

Scrapers register themselves at import time — no config, no discovery:

```
init() → scraper.Register(New()) → appends to global slice
main.go blank-imports each scraper package → triggers init()
```

At runtime, `scraper.ForURL(url)` iterates registered scrapers and returns the first whose `MatchesURL()` matches. `scraper.ForID(id)` looks up by stable identifier. `scraper.All()` returns everything (used by `list-scrapers`). See [docs/library.md](library.md) for using these as a Go library.

The `StudioScraper` interface has four methods: `ID()`, `Patterns()`, `MatchesURL()`, and `ListScenes()`. All scrapers implement this directly — there's no base struct or embedding.

## Channel-Based Streaming

**Files:** `scraper/interface.go`, `cmd/scrape.go`

`ListScenes()` returns `<-chan SceneResult` immediately. A background goroutine paginates the site and sends results. Each result carries a `Kind` field (`ResultKind`) that determines which other fields are populated:

| Kind | Meaning |
|------|---------|
| `KindScene` | A scraped scene (`result.Scene`) — the normal case |
| `KindError` | A non-fatal error (`result.Err`) — logged, scraping continues |
| `KindTotal` | Progress hint (`result.Total`) sent once after the first page |
| `KindStoppedEarly` | Incremental mode hit a known ID — stop signal |

The consumer (`collectScenes()` in `cmd/scrape.go`) switches on `result.Kind` and drains until the channel closes. Prefer the constructor functions: `scraper.Scene(s)`, `scraper.Error(err)`, `scraper.Progress(n)`, `scraper.StoppedEarly()`.

**Critical invariant:** the goroutine must `defer close(out)` as its first line, and every send must be wrapped in `select` with `case <-ctx.Done()` to prevent goroutine leaks on cancellation.

That invariant is enforced, not merely stated, by three checks — worth knowing because
they fail in places you may not expect:

| Check | What it proves | Scope |
|-------|----------------|-------|
| `internal/scrapers/all/ctxsend_test.go` | every send of a `SceneResult` sits in a `select` with `ctx.Done()` | all send sites, statically |
| `internal/scrapers/all/cancel_test.go` | no scraper hangs when handed an already-dead context | every registered scraper |
| `scraper/paginate_test.go` | `Paginate`'s own sends unblock on cancellation *while a send is blocked* | every scraper built on `Paginate` |

The third matters because most scrapers delegate their whole paging walk to
`scraper.Paginate`, which owns the sends — so mid-flight cancellation is Paginate's
responsibility rather than each scraper's, and proving it once covers all of them.
Scrapers that page themselves (worker pools, cursor walks) own their sends and can
additionally use `testutil.AssertCancellable` for a runtime check.

A subtlety worth internalising if you write a cancellation test: **a buffered channel
makes the test vacuous.** Sends into a buffered channel never block, so the `select`
around them is never exercised, and deleting the `ctx.Done()` case leaves the test green.
Use an unbuffered channel and stop reading from it.

## Store Abstraction

**Files:** `internal/store/interface.go`, `internal/store/flat.go`, `internal/store/sqlite.go`

The `Store` interface decouples scraping from persistence:

```go
type Store interface {
    Lock(studioURL string) (io.Closer, error)
    LockContext(ctx context.Context, studioURL string) (io.Closer, error)
    Load(studioURL string) ([]models.Scene, error)
    Save(studioURL string, scenes []models.Scene) error
    MarkDeleted(studioURL, siteID string, ids []string) error
    Export(format, path, studioURL string) error
    UpsertStudio(studio models.Studio) error
    ListStudios() ([]models.Studio, error)
}
```

Both implementations are held to one suite, `internal/store/contract_test.go`, so
behaviour is identical apart from cost. Scrapers never know which store is active.

`Save` is **authoritative**: scenes absent from the passed slice are deleted.
`FirstSeenAt` is the single exception to verbatim writing — the store preserves
an existing value and stamps an absent one. See [metadata.md](metadata.md).

**Flat store** (default): one JSON file per studio. The file _is_ the backing
store — `Load()` parses it, `Save()` re-marshals and atomically replaces it. Every
save rewrites the whole file. CSV is written alongside as an export.

**SQLite store** (`--db`): auto-migrates on open via the `schema_version` table
(currently 7). Scenes, price history, studios and external IDs live in relational
tables; performers, tags and categories are normalised into global lookup tables
with junction tables carrying a `position` column. The legacy JSON columns in
`scenes` are kept but never read or written.

Two things make it more than a swap-in:

- **`Save` is diff-aware.** Each scene is fingerprinted into `content_hash`
  (everything except `scraped_at`/`first_seen_at`), and a scene whose stored
  fingerprint matches skips the row upsert, all three relation syncs and the
  price-history diff. When only `scraped_at` moved it issues one narrow `UPDATE`.
  Anything writing scene state outside `upsertScene` must clear `content_hash` —
  `MarkDeleted` does, or a soft-delete would never lift.
- **`Load` groups relations in SQL and orders them in Go.** Names come back as
  JSON arrays, one row per scene rather than one per name — a 59k-scene studio
  has 1.4M `scene_tags` rows and crossing `database/sql` that many times cost
  more than the query. `ORDER BY` is deliberately absent: it made SQLite build
  temp B-trees over every row in the studio.
- **The child tables carry covering indexes** leading with `studio_url`
  (migration 8). Their primary keys start with `scene_id`, so a studio filter
  cannot use them, and the trailing columns let the `GROUP BY` stream instead of
  sorting.
- **Large saves share a `saveSession`**, caching prepared statements and entity
  name→id lookups for the lifetime of one `Save`.

`Export()` regenerates JSON/CSV from the database; `fss import` goes the other
way. `SetMaxOpenConns(1)` because SQLite has no concurrent writers.

Measured trade-offs, and which store to use, are in [storage.md](storage.md).

**Process-level locking:** `Lock(studioURL)` takes an exclusive `flock(2)` on a
`<slug>.lock` sidecar so two concurrent `fss scrape` runs of the same URL cannot
race on Load+Save. It is re-entrant within a process (`MarkDeleted` locks
internally and would otherwise self-deadlock) and a no-op on Windows.
`LockContext(ctx, …)` is what `fss scrape` uses: the wait polls a non-blocking
`flock`, so Ctrl+C ends it, and after about a second it prints
`waiting for lock on <studio> (held by another fss)`. `Flat` never holds its
internal mutex while waiting on another process, and closing a handle twice
releases once.

**SQLite concurrency:** the pragmas travel in the DSN (`busy_timeout=60000`,
`foreign_keys=1`, WAL, `synchronous=NORMAL`, `_txlock=immediate`) so every
pooled connection has them. `_txlock=immediate` matters as much as the timeout:
a deferred transaction that reads first and upgrades after another process
commits fails with `SQLITE_BUSY_SNAPSHOT`, which the busy handler never retries.
Two `fss` processes saving different studios into one database therefore queue
instead of failing and losing the scrape, as long as the holder commits within
60 seconds; a longer wait still fails with `database is locked`. Opening a
database also takes a `<db>.migrate.lock` sidecar lock, so two processes opening
a fresh database cannot both run the migrations. Migrations that need foreign keys off
toggle the pragma on the single pooled connection and now report a failure to
turn it back on. `Load` runs its five queries inside one deferred read
transaction, so a concurrent `Save` cannot yield scenes from one state and
relations from another.

## Shared Scraper Packages

`*util` packages eliminate duplication for sites that share a platform. Key examples:

### ayloutil

**File:** `internal/scrapers/ayloutil/`

For Aylo/MindGeek sites (Babes, BangBros, Brazzers, Digital Playground, Mofos, PropertySex, Reality Kings, TransAngels, Twistys). Uses the `/api/2/releases` REST endpoint with `instance_token` cookie. Individual scrapers are thin wrappers supplying site config.

### gammautil

**File:** `internal/scrapers/gammautil/`

For Gamma Entertainment sites (Burning Angel, Evil Angel, Filthy Kings, Gangbang Creampie, Girlfriends Films, Gloryhole Secrets, Lethal Hardcore, Mommy Blows Best, Pure Taboo, Rocco Siffredi, Taboo Heat, Wicked). Uses Algolia search with a rotating API key scraped from the site HTML. Individual scrapers supply a `SiteConfig` and delegate to `gammautil.Scraper.Run()`.

### veutil

**File:** `internal/scrapers/veutil/`

For WordPress video-elements theme sites (BoyfriendSharing, BrattyFamily, GoStuckYourself, HugeCockBreak, LittleFromAsia, MommysBoy, MomXXX, MyBadMILFs, DaughterSwap, PervMom, SisLovesMe, YoungerLoverOfMine). Uses `/wp-json/` API with the `flavor` parameter. Individual scrapers register with site-specific config.

### wputil

**File:** `internal/scrapers/wputil/`

For WordPress-based sites (Anal Therapy, Family Therapy, Mom Comes First, Perfect Girlfriend, Tara Tainton). Provides XML sitemap parsing, OpenGraph/JSON-LD extraction, and a worker pool for parallel page fetching. Individual scrapers implement a `parsePage` callback.

### povrutil

**File:** `internal/scrapers/povrutil/`

For POVR/WankzVR VR platform sites (BrasilVR, MilfVR, TranzVR, WankzVR). Uses the POVR API for scene listing and metadata.

### sexmexutil

**File:** `internal/scrapers/sexmexutil/`

For SexMex Pro CMS sites (Exposed Latinas, SexMex, Trans Queens). Handles the `/es/`/`/en/` locale prefix and HTTP 500 responses with valid HTML (intentional — the CMS returns HTTP 500 with a valid page body on some endpoints).

### scoregrouputil

**File:** `internal/scrapers/scoregrouputil/`

For Score Group sites (50 Plus MILFs). HTML listing + detail page worker pool for dates/tags.

### railwayutil

**File:** `internal/scrapers/railwayutil/`

For Railway/Express/MongoDB platform sites (Smoking Erotica, Smoking Models, Spanking Glamour). Single JSON API call returns all videos in one response, no auth, no dates, performer extraction from title.

### uptimelyutil

**File:** `internal/scrapers/uptimelyutil/`

For Up-Timely CMS platform sites (DAS!, Idea Pocket, Madonna, MOODYZ, S1 NO.1 STYLE). HTML listing + detail page worker pool, Japanese metadata extraction, cross-page dedup.

## HTTP Layer

**File:** `internal/httpx/httpx.go`

All scrapers share a single HTTP transport (`MaxIdleConnsPerHost: 10`) for connection pooling across requests to the same host.

`httpx.Do()` wraps requests with:
- **Retry with backoff**: network errors, 429, 5xx are retried up to 3 times with `attempt * 2s` sleep
- **Fail-fast**: non-retryable 4xx errors return immediately as `*StatusError`
- **Centralized UA strings**: `UserAgentFirefox` and `UserAgentChrome` constants, versioned in one place. Overridable via `user_agent` config field (`"firefox"`, `"chrome"`, or a custom string); applied at startup by `httpx.SetDefaultUA()`

`httpx.NewClient(timeout)` creates a client using the shared transport.

## Config System

**File:** `internal/config/config.go`

YAML config at the XDG path (`~/.config/fss/config.yaml` on Linux). Loaded once at startup in `rootCmd.PersistentPreRunE`.

Resolution order: CLI flags > config file > hardcoded defaults.

No validation or dynamic reload. The Stash API key can also come from `FSS_STASH_API_KEY` env var (checked at flag parse time in `cmd/stash.go`).

## Stash Integration

**Files:** `cmd/stash.go`, `match/match.go`, `match/merge.go`

Three-layer design:

1. **Client**: [stash-go](https://github.com/Anastylosis/stash-go), the shared GraphQL client, with `FindScenes`, `UpdateScene`, `EnsureTag`, `EnsurePerformer`, `EnsureStudio`. `cmd/stash.go`'s `newStashClient` supplies fss's pooled, retrying HTTP client (`httpx.NewRetryClient`) so Stash calls retry like every other request fss makes. Cover images are fss's own: `internal/mediafetch` validates and encodes them, the client just carries the data URI.

2. **Matcher** (`match.go`): builds a `SceneIndex` from previously-scraped scenes — loaded from JSON files *or* the SQLite store via the shared `loadFSSScenes` in `cmd/scenesource.go` — indexed by normalized title. Matches Stash filenames against the index with two passes (primary + sanitized/noise-stripped). Returns confidence levels: Exact, Substring, Ambiguous, None. Duration filtering rejects false positives.

3. **Merger** (`merge.go`): when a title appears in more than one source (cross-site), combines metadata: union of URLs/performers/tags, earliest date, longest description, highest resolution. Names are deduplicated by canonical key (`NormalizeName`: case-folded, whitespace-collapsed) while keeping the first contributing site's spelling, and per-field provenance is recorded in `MergedScene.Sources`.

The import command (`cmd/stash_import.go`) orchestrates: load index → query Stash → match → merge → dry-run or apply.

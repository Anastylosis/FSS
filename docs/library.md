# Using FSS as a Go Library

FSS can be imported as a Go module. The scraper engine, matching, merging, Stash integration, and NFO generation are all available to external code.

## Install

```bash
go get github.com/Anastylosis/FSS@latest # Or use tag for stable release
```

The module path changed from `github.com/Wasylq/FSS` when the repository moved to
the `Anastylosis` organisation. Update the import paths in your code and drop the
old `require` line — GitHub redirects the repository, but Go verifies the module
path declared in `go.mod`, so the old path resolves only for tags up to `v1.28.1`.

## Public Packages

| Package | Import path | Purpose |
|---------|------------|---------|
| `scrapers/all` | `github.com/Anastylosis/FSS/scrapers/all` | Blank-import to register all scrapers |
| `scraper` | `github.com/Anastylosis/FSS/scraper` | Registry API, `StudioScraper` interface, `SceneResult` channel protocol |
| `models` | `github.com/Anastylosis/FSS/models` | `Scene`, `PriceSnapshot` — the core data model |
| `match` | `github.com/Anastylosis/FSS/match` | Filename→title matching, cross-site merging, JSON loading |
| `output` | `github.com/Anastylosis/FSS/output` | `WriteJSON`, `WriteCSV`, `Slugify` — write FSS output files |
| `parseutil` | `github.com/Anastylosis/FSS/parseutil` | `ParseDurationColon`, `ParseDurationISO`, `StripOrdinalSuffix`, `OpenGraph`, `TryParseDate`, `ExtractVideoObject`, `ExtractVideoObjects`, `DecodeXML` — shared parsing helpers. `thumbnailUrl` in a VideoObject may be a string or an array; both decode, keeping the first entry |
| `nfo` | `github.com/Anastylosis/FSS/nfo` | Kodi-style NFO XML generation |
| `identify` | `github.com/Anastylosis/FSS/identify` | Video directory scan + match + NFO write |

**Registering scrapers:** The individual scraper implementations live under `internal/scrapers/`, but a public aggregator package re-exports them all:

```go
import _ "github.com/Anastylosis/FSS/scrapers/all"  // registers all scrapers
```

This is all you need to populate the registry for scraping from external code.

## Quick Start — Scraping

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Anastylosis/FSS/scraper"
	_ "github.com/Anastylosis/FSS/scrapers/all"
)

func main() {
	ctx := context.Background()

	s, err := scraper.ForURL("https://www.manyvids.com/Profile/590705/bettie-bondage/Store/Videos")
	if err != nil {
		log.Fatal(err)
	}

	ch, err := s.ListScenes(ctx, "https://www.manyvids.com/Profile/590705/bettie-bondage/Store/Videos", scraper.ListOpts{})
	if err != nil {
		log.Fatal(err)
	}

	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			fmt.Printf("%-40s %s\n", r.Scene.Title, r.Scene.URL)
		case scraper.KindError:
			log.Printf("error: %v", r.Err)
		}
	}
}
```

## Quick Start — Matching & Merging

Load FSS JSON output files and match filenames against them (no scraper registration needed).

```go
package main

import (
	"fmt"
	"time"

	"github.com/Anastylosis/FSS/match"
)

func main() {
	// Load scenes from FSS JSON files (produced by `fss scrape`).
	scenes, err := match.LoadJSONDir("./data")
	if err != nil {
		panic(err)
	}

	// Build a title index.
	idx := match.BuildIndex(scenes)

	// Match a filename (duration in seconds, 0 = unknown).
	result := idx.Match("Fostering the Bully.mp4", 605.0)

	switch result.Confidence {
	case match.MatchExact:
		fmt.Println("Exact match:", result.Scenes[0].Title)
	case match.MatchSubstring:
		fmt.Println("Substring match:", result.Scenes[0].Title)
	case match.MatchAmbiguous:
		fmt.Printf("Ambiguous: %d candidates\n", result.Candidates)
	case match.MatchNone:
		fmt.Println("No match")
	}

	// Merge cross-site scenes into a single metadata record.
	if result.Confidence == match.MatchExact || result.Confidence == match.MatchSubstring {
		merged := match.MergeScenes(result.Scenes, time.Time{})
		fmt.Println(merged.Title, merged.URLs, merged.Performers)
	}
}
```

## Selective Scraper Registration

If you only need a few scrapers (to reduce binary size), you can blank-import individual packages from within a fork or custom build inside this repo:

```go
// Only works within the FSS module (forks, custom builds).
_ "github.com/Anastylosis/FSS/internal/scrapers/manyvids"
_ "github.com/Anastylosis/FSS/internal/scrapers/clips4sale"
```

From external modules, use `_ "github.com/Anastylosis/FSS/scrapers/all"` to register all scrapers at once.

## Registry API

After importing `scrapers/all`, the registry is populated and these functions work:

```go
// Find a scraper by URL.
s, err := scraper.ForURL("https://www.manyvids.com/...")

// Find a scraper by its stable ID (e.g. "manyvids", "clips4sale").
s, err := scraper.ForID("clips4sale")

// List all registered scrapers.
for _, s := range scraper.All() {
    fmt.Printf("%-20s %s\n", s.ID(), s.Patterns())
}
```

Use `fss list-scrapers` (or iterate `scraper.All()`) to see all available IDs and URL patterns.

### Studio URL aliases

Some sites serve one studio at more than one URL — YourVids answers both
`yourvids.com/creators/leina-sex` and the bare `yourvids.com/leina-sex`. Since a
studio is keyed on its URL, storing both spellings would make one creator into
two studios. `scraper.PreferredStudioURL` asks the matching scraper which
spelling to key on:

```go
url := scraper.PreferredStudioURL("https://yourvids.com/leina-sex")
// https://yourvids.com/creators/leina-sex
```

A URL no scraper claims, or one whose scraper expresses no preference, is
returned unchanged. Scrapers opt in by implementing
`scraper.StudioURLCanonicalizer`; most do not, and they need no change.

This is a *path* rewrite the scraper vouches for, unlike
`output.CanonicalStudioURL`, which normalises scheme and host generically and
never touches the path.

## Controlling Scrape Behaviour

`ListOpts` configures how the scraper paginates:

```go
opts := scraper.ListOpts{
    // Number of concurrent detail-page workers (for scrapers that fetch
    // detail pages). Zero uses the scraper's default (usually 4).
    Workers: 2,

    // Delay between page fetches. Useful for rate-limiting.
    Delay: 500 * time.Millisecond,

    // Incremental mode: stop as soon as any of these IDs are encountered.
    // Scrapers that sort newest-first will stop at the first known scene,
    // skipping older pages that are already in your store.
    KnownIDs: map[string]bool{
        "existing-scene-id": true,
    },
}
```

## Reading Results

`ListScenes` returns a channel of `SceneResult`. Each result carries a `Kind` field — switch on it:

```go
for r := range ch {
    switch r.Kind {
    case scraper.KindTotal:
        // Progress hint (sent once). Use for display, then skip.
    case scraper.KindStoppedEarly:
        // Incremental mode hit a known ID. No more scenes coming.
    case scraper.KindError:
        // Non-fatal error (r.Err). Log and continue.
    case scraper.KindScene:
        // r.Scene is a valid models.Scene.
    }
}
```

The channel is always closed when the scraper finishes (or is cancelled via context).

### Classifying errors

A `KindError` result is non-fatal, but not every one of them means data went missing.
`scraper.Classify` sorts them into a `FailureKind`:

| Kind | Meaning | Cost |
|------|---------|------|
| `FailureTransport` | the page never arrived — network, timeout, 5xx, 429, 403 | scenes missing |
| `FailureParse` | the page arrived but could not be read | scenes missing |
| `FailureAbsent` | an optional resource the scraper was probing isn't there | nothing missing |
| `FailureUnknown` | unclassified | assume scenes missing |

```go
case scraper.KindError:
    if scraper.Classify(r.Err).MissingData() {
        incomplete = true // this traversal is not the site's full state
    }
```

`MissingData()` is true for everything except `FailureAbsent`, so an unclassified error stays
conservative. This matters for callers doing an authoritative write: a traversal that lost a
page must not be treated as the complete scene set, while an optional sub-listing that 404s
should not block one.

Scrapers annotate their own errors with `scraper.ParseError(url, err)`,
`TransportError`, or `AbsentError`; the returned `*ScrapeError` unwraps to the cause, so
`errors.Is`/`errors.As` still work.

`FailureAbsent` is **opt-in only**. Errors from `httpx` always report missing data, 404
included: a status code cannot say whether the missing resource mattered. A 404 on an
optional sub-listing costs nothing, while a 404 on the detail page of a scene the listing
already returned means a known scene went uncollected. Only the call site knows which,
so downgrade it deliberately with `AbsentError` and never by status alone.

## The Scene Model

`models.Scene` has everything a scraper can extract. Fields vary by site — only `ID`, `SiteID`, `Title`, `URL`, and `ScrapedAt` are guaranteed.

| Group | Fields |
|-------|--------|
| Identity | `ID`, `SiteID`, `StudioURL`, `ExternalIDs` |
| Core | `Title`, `URL`, `Date`, `Description` |
| Media | `Thumbnail`, `Preview` |
| People | `Performers`, `Director`, `Studio` |
| Classification | `Tags`, `Categories` |
| Series | `Series`, `SeriesPart` |
| Technical | `Duration` (seconds), `Resolution`, `Width`, `Height`, `Format` |
| Engagement | `Views`, `Likes`, `Comments` |
| Pricing | `PriceHistory`, `LowestPrice`, `LowestPriceDate` |
| Housekeeping | `FirstSeenAt`, `ScrapedAt`, `DeletedAt` |

Scenes serialize cleanly to JSON (all fields have `json` tags with `omitempty` where appropriate).

`ExternalIDs` maps a metadata database to this scene's ID in it — `(ID, SiteID)` is
site-local, so these are the only keys that identify the same scene across sites. Use the
`models.ExternalStashDB` / `ExternalTPDB` / `ExternalIAFD` / `ExternalIndexxx` constants;
a stashbox instance uses its own site ID as the key.

`FirstSeenAt` is stamped by the store on first write and never moved afterwards, so it
survives re-scrapes. `models.StoreSchemaVersion` is the layout version written into every
`models.StudioFile`; a reader should refuse a file whose version exceeds its own. See
[metadata.md](metadata.md).

## Cancellation

Pass a cancellable context to stop scraping early:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

ch, _ := s.ListScenes(ctx, url, scraper.ListOpts{})
for r := range ch {
    // ...
    if haveEnough {
        cancel() // scraper stops, channel drains and closes
    }
}
```

All scrapers respect `ctx.Done()` on every page fetch and channel send — cancellation is immediate with no goroutine leaks.

## Matching & Merging (`match`)

The `match` package provides filename-to-title matching and cross-site scene merging — the same engine used by `fss stash import` and `fss identify`.

```go
import (
    "github.com/Anastylosis/FSS/match"
    _ "github.com/Anastylosis/FSS/internal/scrapers/manyvids"
)

// Load scenes from FSS JSON files.
scenes, err := match.LoadJSONFiles([]string{"manyvids.json", "clips4sale.json"})
// or: scenes, err := match.LoadJSONDir("./data")

// Build a title index.
idx := match.BuildIndex(scenes)

// Match a filename (duration in seconds, 0 = unknown).
result := idx.Match("Fostering the Bully.mp4", 605.0)

switch result.Confidence {
case match.MatchExact:
    fmt.Println("Exact match:", result.Scenes[0].Title)
case match.MatchSubstring:
    fmt.Println("Substring match:", result.Scenes[0].Title)
case match.MatchAmbiguous:
    fmt.Printf("Ambiguous: %d candidates\n", result.Candidates)
case match.MatchNone:
    fmt.Println("No match")
}

// Merge cross-site scenes into a single metadata record.
merged := match.MergeScenes(result.Scenes, time.Time{})
fmt.Println(merged.Title, merged.URLs, merged.Performers)
```

**Key types:** `SceneIndex`, `MatchResult`, `MatchConfidence`, `MergedScene`, `FieldSource`.

`MergeScenes` normalises performer, tag, category and studio names — surrounding
whitespace removed, internal runs collapsed — and drops entries that reduce to nothing.
This is not cosmetic: `fss stash import` looks entities up in Stash **by name**, so an
untrimmed `"Nikki Nuttz "` both survives as a second performer and creates a duplicate in
Stash. Deduplication is by a case-folded key, while the stored value keeps the first
contributing site's spelling.

`MergedScene.Sources` records provenance per scalar field:

```go
merged := match.MergeScenes(result.Scenes, time.Time{})
if src := merged.Sources["date"]; src.Conflicted() {
    fmt.Printf("kept %s's date, dropped %v\n", src.Site, src.Discarded)
}
```

Tracked fields are `title`, `description`, `date`, `studio`, `thumbnail`, `duration` and
`resolution`. `FieldSource.Site` is the site whose value was kept (empty when the value
came from outside the scene set, e.g. an existing Stash date) and `Discarded` lists the
competing values as `"siteID: value"`.

## NFO Generation (`nfo`)

The `nfo` package generates Kodi-style `.nfo` XML files from merged scene metadata.

```go
import "github.com/Anastylosis/FSS/nfo"

mov := nfo.FromMergedScene(merged) // merged is a match.MergedScene
data, err := nfo.Marshal(mov)
os.WriteFile("scene.nfo", data, 0o644)
```

**Key types:** `Movie`, `Thumb`, `Actor`.

## Identify (`identify`)

The `identify` package scans a directory of video files, matches them against an FSS scene index, and optionally writes `.nfo` sidecar files.

```go
import "github.com/Anastylosis/FSS/identify"

videos, _ := identify.FindVideos("/path/to/videos")
results := identify.Run(videos, idx, identify.Options{
    Apply: true,  // write .nfo files (false = dry-run)
    Force: false, // don't overwrite existing .nfo
})
stats := identify.Summarize(results)
fmt.Printf("%d matched, %d unmatched\n", stats.Matched, stats.Unmatched)
```

`RunContext` is the same with cancellation, which `Options.Poster` needs since it
performs network fetches:

```go
results := identify.RunContext(ctx, videos, idx, identify.Options{
    Apply:  true,
    Poster: true, // download thumbnails to <basename>-poster.<ext> beside each video
})
for _, r := range results {
    if r.PosterError != nil {
        fmt.Printf("%s: no poster (%v)\n", r.VideoPath, r.PosterError)
    }
}
```

`Poster` is off by default — it is one request per matched scene. Without it a
scene's `<thumb>` stays a remote URL, and scraped CDN URLs are usually signed and
short-lived. Either way `<thumb>` is only written when the image is really there:
`nfo.FromMergedScene` drops a thumbnail whose signed expiry has already passed,
and a failed poster download drops it too rather than falling back to the URL.

**Key types:** `Result`, `Options`, `Stats`.

## Stash Client

Talking to Stash is [stash-go](https://github.com/Anastylosis/stash-go), a
separate module — fss used to carry its own copy, and the client is useful to
anything that talks to a Stash server, not just to fss.

```go
import stash "github.com/Anastylosis/stash-go"

client := stash.NewClient("http://localhost:9999", stash.WithAPIKey(key))

scenes, total, err := client.FindScenes(ctx, stash.SceneFilter{
    PerformerName: "Bettie Bondage",
}, 1, 25)
```

See that repository's docs for the full API. Two things are fss's rather than
the library's:

- `cmd/stash.go`'s `newStashClient` passes `httpx.NewRetryClient`, so Stash
  requests retry and pool connections like everything else fss does. The
  library's own default is a plain client with no retry.
- Cover images are fetched by `internal/mediafetch`, which validates the URL
  against SSRF, caps the size and rejects an already-expired signed link. The
  library takes the finished data URI and does not fetch anything itself.

## Output Files (`output`)

`CanonicalStudioURL` reduces a studio URL to its storage identity — https scheme,
lowercase host, no default port, fragment or trailing slash — so that
`http://x.com`, `https://x.com` and `https://x.com/` are one studio. It is
identity only: the URL passed to a scraper must stay as the operator typed it,
because http-only sites exist.


The `output` package writes FSS-format JSON and CSV files, and provides URL-to-filename slugification.

```go
import (
    "github.com/Anastylosis/FSS/models"
    "github.com/Anastylosis/FSS/output"
)

// Write scenes as JSON (atomic file replacement — safe on crash).
sf := models.StudioFile{
    StudioURL:  "https://www.manyvids.com/...",
    ScrapedAt:  time.Now().UTC(),
    SceneCount: len(scenes),
    Scenes:     scenes,
}
output.WriteJSON(sf, "studio.json")

// Write scenes as CSV.
output.WriteCSV(scenes, "studio.csv")

// Generate a safe filename from a URL.
slug := output.Slugify("https://www.manyvids.com/Profile/590705/bettie-bondage/Store/Videos")
// → "www-manyvids-com-profile-590705-bettie-bondage-store-videos"
```

**Key functions:** `WriteJSON`, `WriteCSV`, `Slugify`. **Key var:** `CSVHeaders` (column order).

`Slugify` appends a short hash of the full URL, so two URLs that sanitize to the same
stem never collide, and the result is **capped at 250 bytes** — `<slug>.json` must fit
the 255-byte filename limit. Only the human-readable prefix is truncated; the hash is
taken over the whole URL, so uniqueness is unaffected.

`WriteCSV` prefixes `'` to any cell beginning `=`, `+`, `-`, `@`, tab or CR. Scene titles
and descriptions are scraped, so they are attacker-controlled, and spreadsheets evaluate
such cells as formulas on open. The guard lives in `WriteCSV` rather than in the row
builder so a new column cannot bypass it.

## Parsing helpers (`parseutil`)

The `parseutil` package exposes the parsing primitives FSS's own scrapers
share. Public so external callers can reuse the same logic — the helpers
are stable enough that duplicating them would just diverge.

```go
import "github.com/Anastylosis/FSS/parseutil"

// Video duration strings commonly emitted on adult sites.
parseutil.ParseDurationColon("30:00")    // → 1800 (seconds)
parseutil.ParseDurationColon("01:02:03") // → 3723
parseutil.ParseDurationISO("PT1H2M3S")   // → 3723
parseutil.ParseDurationISO("PT30M")      // → 1800

// English ordinal suffixes — strip before time.Parse against a bare-day
// layout like "2 January 2006".
parseutil.StripOrdinalSuffix("8th May 2026")       // → "8 May 2026"
parseutil.StripOrdinalSuffix("22nd September 2024") // → "22 September 2024"

// OpenGraph metadata — pulls every `<meta property="og:*" content="…">`
// pair into a map. Handles both attribute orderings; values are raw,
// caller decides on html.UnescapeString.
og := parseutil.OpenGraph(htmlBody)
title := og["og:title"]
image := og["og:image"]
```

```go
// Lenient XML — sitemaps and feeds routinely carry an unescaped `&`
xml := parseutil.DecodeXML(body, &urlset)
```

`DecodeXML` is `xml.Unmarshal` with strict mode off. Sitemaps in the wild carry
unescaped `&` in titles, which strict `encoding/xml` rejects outright — one bare
ampersand costs the whole document rather than the entry containing it. Input
that parses strictly parses identically here.

The duration parsers return 0 for empty or unparseable input.
`StripOrdinalSuffix` only touches digit-then-suffix runs, so plain
words containing `st`/`nd`/`rd`/`th` (e.g. `"northwest"`) are safe.
`OpenGraph` returns a non-nil empty map when no tags are present;
repeated `og:foo` tags collapse to the last occurrence in source order.

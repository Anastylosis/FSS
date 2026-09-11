# Contributing

## Adding a New Scraper

Each site scraper is a self-contained package under `internal/scrapers/<site>/`. The system uses a plugin-based registry — no central config to edit beyond a one-line import.

### 1. Create the package

Create `internal/scrapers/<site>/<site>.go`. Your scraper must implement the `scraper.StudioScraper` interface:

```go
type StudioScraper interface {
    ID() string
    Patterns() []string
    MatchesURL(url string) bool
    ListScenes(ctx context.Context, studioURL string, opts ListOpts) (<-chan SceneResult, error)
}
```

- **`ID()`** — stable lowercase identifier, e.g. `"pornhub"`
- **`Patterns()`** — human-readable URL patterns shown by `fss list-scrapers`, e.g. `"pornhub.com/pornstar/{slug}"`
- **`MatchesURL()`** — returns true if this scraper handles the given URL (use a compiled regex)
- **`ListScenes()`** — starts a goroutine that sends results on a channel, returns the channel immediately

### 2. Register via init()

```go
func init() {
    scraper.Register(New())
}
```

This is called automatically at startup when the package is imported.

### 2a. Host regexes

`MatchesURL` decides which scraper claims a URL, and `scraper.ForURL` returns the
**first** match. A regex that anchors the start but not the end of the host is a
prefix match, so `^https?://(?:www\.)?example\.com` also claims
`https://example.com.evil.invalid/`. Because most scrapers fetch the studio URL
verbatim, that look-alike host's content would be scraped and stored under the
legitimate studio's key.

Terminate the host:

```go
regexp.MustCompile(`^https?://(?:www\.)?example\.com(?:/|$)`)
```

- `\b` is **not** a terminator — there is a word boundary between `com` and
  `.evil`, so the look-alike still matches.
- A regex that continues into a path (`example\.com/videos`) is already
  terminated by the `/`; adding another is wrong.
- In an alternation, each branch that ends at a host needs its own terminator:
  `(?:a\.com(?:/|$)|b\.com/tour/…)`.
- Don't match with `strings.Contains(u, "://"+domain)` — it accepts any host
  that merely contains the domain, and any URL with the domain in its query
  string. Use `scraper.HostMatches(u, domain)`, which compares parsed hosts and
  ignores a leading `www.` on either side.

`TestRegistryMatchesURLTerminatesTheHost` builds a look-alike from every
registered scraper's own advertised host and fails if it matches, so a new
scraper cannot reintroduce this.

### 3. Add blank import in main.go

```go
_ "github.com/Anastylosis/FSS/internal/scrapers/<site>"
```

This triggers `init()` and registers the scraper. Without this line, the scraper won't be available.

### 4. Implement the run() goroutine

The `ListScenes` method launches a goroutine that sends `SceneResult` values on a channel. See `internal/scrapers/pornhub/pornhub.go` for the simplest complete example.

**Use `scraper.Paginate`** for page-numbered pagination — it handles delay, ctx cancellation, progress reporting, KnownIDs early-stop, and scene sending. Your callback just fetches and parses one page:

```go
func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
    defer close(out) // MUST be the first line
    now := time.Now().UTC()

    scraper.Paginate(ctx, opts, "mysite", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
        items, total, err := s.fetchPage(ctx, studioURL, page)
        if err != nil {
            return scraper.PageResult{}, err
        }
        scenes := make([]models.Scene, len(items))
        for i, item := range items {
            scenes[i] = toScene(item, studioURL, now)
        }
        return scraper.PageResult{
            Scenes: scenes,
            Total:  total,                    // set on first page for progress display
            Done:   len(items) == 0,          // or page >= totalPages
        }, nil
    })
}
```

For scrapers that can't use `Paginate` (worker pools, cursor-based pagination), use the manual loop pattern — every channel send must be wrapped in `select` with `case <-ctx.Done(): return` to prevent goroutine leaks. See `manyvids` or `kink` for examples.

**Critical rules:**

- `defer close(out)` must be the first line in `run()` — the consumer blocks on this channel
- `Paginate` handles ctx cancellation, delay, and progress internally — no manual select needed in the callback

### 4a. Add debug logging

All scrapers must include level-1 debug logging with `scraper.Debugf(1, ...)`. `Paginate` handles page-fetch and total-count logging automatically; add only what it doesn't cover:

```go
// URL mode detection (if your scraper supports multiple URL types):
scraper.Debugf(1, "mysite: scraping model page")

// Worker pool launch (if applicable):
scraper.Debugf(1, "mysite: fetching %d details with %d workers", n, workers)
```

Debug levels by convention: **1** = high-level operations, **2** = HTTP requests (handled by `httpx.Do` automatically), **3** = parsing details.

### 4b. Check for URL filtering modes

Most sites support more than just the main video listing. Check whether the site offers filtered views and add support for each:

- **Model/performer page** (`/model/{slug}`, `/pornstar/{slug}`) — scenes for one performer
- **Channel/series page** (`/channel/{slug}`, `/series/{slug}`) — sub-studio scenes
- **Tag/category page** (`/tag/{slug}`, `/categories/{name}`)

Add each supported URL pattern to `Patterns()` so `fss list-scrapers` shows them. See `kink` (channel/model/tag/series) or `nubiles` (model profile) for examples.

**Pitfall**: model pages often mix videos and galleries — only return video entries.

### 5. Build the Scene

Populate `models.Scene` with as many fields as the site provides. Required fields:

| Field | Description |
|-------|-------------|
| `ID` | Unique identifier from the site |
| `SiteID` | Your scraper's `ID()` value |
| `StudioURL` | The input studio URL |
| `Title` | Scene title |
| `URL` | Direct link to the scene |
| `ScrapedAt` | `time.Now().UTC()` |

Optional but recommended: `Date`, `Description`, `Thumbnail`, `Preview`, `Performers`, `Tags`, `Categories`, `Duration`, `Width`, `Height`, `Resolution`, `Format`, `Studio`.

For sites with pricing, call `scene.AddPrice()`:

```go
scene.AddPrice(models.PriceSnapshot{
    Date:            now,
    Regular:         19.99,
    Discounted:      9.99,
    IsFree:          false,
    IsOnSale:        true,
    DiscountPercent: 50,
})
```

For free sites (e.g. Pornhub):

```go
scene.AddPrice(models.PriceSnapshot{Date: now, IsFree: true})
```

### 6. Check for an existing platform

Before building a standalone scraper, check [docs/platform-detection.md](docs/platform-detection.md) — your site may belong to an existing `*util` package (Aylo, Gamma, Adult Prime, etc.). If so, you only need a thin wrapper with a site config entry. If no match, build a standalone scraper.

### 7. WordPress sites — use wputil

For WordPress-based sites, the `internal/scrapers/wputil` package provides shared helpers:

- `wputil.BrowserHeaders()` — common browser headers to avoid WAF blocks
- `wputil.FetchSitemap()` / `wputil.FetchAllSitemaps()` — XML sitemap parsing
- `wputil.FetchPage()` — fetch a single page
- `wputil.ParseMeta(body, titleSuffix)` — extract OpenGraph, `article:tag`, `article:published_time`, shortlink post ID, JSON-LD `VideoObject` width/height, and `articleSection` categories
- `wputil.RunWorkerPool()` — sitemap discovery + parallel page fetching with a `PageParser` callback
- `wputil.SlugFromURL()`, `wputil.ParseDuration()`, `wputil.VideoWidth()` — utility helpers

See `taratainton` and `momcomesfirst` for examples. Your scraper only needs to implement the site-specific `parsePage` callback and registration.

### 8. Use the shared HTTP layer

All scrapers should use the shared HTTP client:

```go
import "github.com/Anastylosis/FSS/internal/httpx"

// In New():
client: httpx.NewClient(30 * time.Second)

// In fetch methods:
resp, err := httpx.Do(ctx, s.client, httpx.Request{
    URL: pageURL,
    Headers: map[string]string{
        "User-Agent": httpx.UserAgentFirefox,
    },
})
```

This gives you connection pooling, automatic retries with backoff (0s/2s/4s), and fail-fast on non-retryable 4xx errors.

### 9. Write tests

Create `internal/scrapers/<site>/<site>_test.go`. Tests should be offline — use `httptest.NewServer` to serve fixture HTML/JSON responses:

```go
func TestParsing(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write(fixtureHTML)
    }))
    defer ts.Close()
    // test against ts.URL
}
```

What to test:

- URL matching (`MatchesURL` with valid and invalid URLs)
- HTML/JSON parsing (page parsing, edge cases, missing fields)
- Pagination (multi-page responses, empty last page)
- `KnownIDs` early stopping

#### Golden fixtures: pin the wire format, not your own struct

If your scraper decodes JSON, add a **golden fixture** — a response captured from
the live API and committed under `testdata/`.

The reason is specific. A test that builds a production type in Go and encodes it
shares the struct tag on both sides of the round trip:

```go
// Renaming `json:"thumb"` to `json:"thumbnail"` on the struct keeps this GREEN,
// while every live scrape silently loses its thumbnails.
body, _ := json.Marshal(apiScene{Thumb: "https://…"})
```

Only a fixture the API produced can catch that. Rules that have earned their place:

- **Capture verbatim.** Slice raw bytes out of the response; do not re-serialise
  through an encoder. Re-encoding normalises key order, escaping (`\/`, `\u0027`)
  and number formatting — exactly what the fixture exists to detect. Add a
  companion `…IsRawCapture` test asserting a distinctive raw form is still present.
- **Trim by whole keys, never by editing values.** Dropping an unused blob to keep
  the file small is fine (`gammautil` drops Algolia's `_highlightResult`); rewriting
  a retained value is not. Say in the comment what was dropped and why.
- **Check for credentials before committing.** This has bitten repeatedly, in three
  different forms: an AWS presigned URL with `AWSAccessKeyId=AKIA…` in a public
  response (`puremature`), JWTs (`ayloutil`, `visitx`), and CSRF tokens in embedded
  page state (`faphouse`, `legsemporium`). Add a `…CarriesNoToken` test asserting
  the markers are absent, so a future re-capture cannot quietly reintroduce one.
- **Document the shapes the fixture pins.** The value is in what a hand-written
  fixture would have got wrong: numeric-looking strings (`"542"` cents, `"148"`
  seconds), objects where a string is expected (`{"en": …}` titles, `created_at`
  as a struct), two ids side by side (`set_id` vs `set_path`, `_id` vs `id`), and
  Elasticsearch's `{"value":…,"relation":"gte"}` total.
- **Mutation-verify it.** Rename a tag in the production struct and confirm the
  golden test fails while the struct-built tests still pass. If nothing fails, the
  fixture is decorative.

A `*util` fixture covers every wrapper that builds its types — pinning
`mymemberutil.VideosPage` covers `rubberpassion`, `rachelsteele` and `kingnoirexxx`
too, so check for that before adding a redundant one.

#### Offline means offline: sitemap fixtures leak to the live site

Pointing the scraper's base URL at `ts.URL` is **not** enough to make a
sitemap-driven test offline. Sitemap fixtures are captured verbatim, so their
`<loc>` entries are absolute URLs on the real site:

```xml
<loc>https://honeytrans.com/scenes/1310/latina-transsexual-returns-to-toy-ass</loc>
```

A scraper that fetches those URLs as given (`sceneRef{url: u.Loc}`) walks
straight out of the test. Overriding `siteBase` / `s.base` redirects only the
sitemap request; every detail fetch that follows still hits production, the
`httptest` handler never serves `detail.html`, and the assertions grade live
markup instead of the fixture.

Such a test passes on a dev machine and fails on a sandboxed CI runner, on a
page nobody touched. It also means a green run tells you nothing about your
parser. Use `testutil.SitemapServer`, which rewrites the fixture's host to the
test server before serving it:

```go
// Rewrites https://honeytrans.com → the test server; serves the sitemap for
// any "sitemap"-ish path and detail for everything else.
srv := testutil.SitemapServer(t, "https://honeytrans.com",
    readFixture(t, "sitemap.xml"), readFixture(t, "detail.html"))
```

Then assert the fetches stayed local. `scene.URL` is the URL that was actually
requested, so this is a direct check:

```go
if !strings.HasPrefix(sc.URL, srv.URL) {
    t.Errorf("scene %s fetched %q, which is not the test server", sc.ID, sc.URL)
}
```

`SitemapServer` also fails the test if the fixture no longer contains the host
you passed — otherwise a refreshed fixture would silently turn the rewrite into
a no-op and re-enable live fetches. Scrapers that extract only the slug from
`<loc>` and rebuild it against their base (`joybear`, `producersfun`) are not
exposed to this; ones that follow `u.Loc` directly (`arx`, `kristenbjorn`,
`frenchtwinks`, `lustreality`) are.

A quick way to spot the problem in an existing test: an offline `TestListScenes`
should run in ~0.00s. If it takes hundreds of milliseconds, it is talking to
something.

For live integration smoke tests that hit the real site, use the shared `testutil` helper. Each scraper has an `integration_test.go` like:

```go
//go:build integration

package <site>

import (
    "testing"
    "github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// liveStudioURL — pick a stable studio. Update if it 404s.
const liveStudioURL = "https://example.com/profile/123/some-name"

func TestLive<Site>(t *testing.T) {
    testutil.SkipIfPlaceholder(t, liveStudioURL)
    testutil.RunLiveScrape(t, New(), liveStudioURL, 2)
}
```

`testutil.RunLiveScrape` fetches the first 2 scenes, validates each via `testutil.ValidateScene` (non-empty `ID`/`Title`/`URL`/`Date`, plausible `Duration`, etc.), and logs the first scene's full struct so you can eyeball field mappings on `-v`. `SkipIfPlaceholder` skips cleanly when `liveStudioURL` still contains `REPLACE-ME` — use it for new scrapers until you find a stable URL.

#### A dead site skips; a broken scraper fails

A scrape that returns nothing is either a scraper that broke or a site that
stopped serving, and the suite is only a useful health signal if it tells the
two apart. When `RunLiveScrape` collects no scenes it decides which it is
before failing:

- **The scrape's own errors come first.** They name the URL the scraper could
  not fetch, which is often not the studio URL — HumiliationPOV's front page
  answers 200 while the WordPress REST API underneath it 500s. Errors that are
  *all* site-side (HTTP 5xx, a domain that does not resolve, a refused
  connection) skip the test. A single error that is not — a parse failure, a
  4xx — keeps the whole run a failure.
- **With no errors at all**, the scraper walked a page and found nothing in it,
  which is usually a parser regression. The studio URL is then probed directly,
  and only an unreachable host, a 5xx, or a redirect onto a *different* domain
  (a parked or rebranded site) skips.

Two deliberate exclusions. **A 4xx never skips** — it is far more often a URL
the scraper built wrongly than a site that has gone away. **A timeout never
skips** either: it is as likely to be a client deadline set below the origin's
own ceiling, which is our bug to fix, not the site's.

The check runs *only after* a scrape has already failed, never as a gate before
one. That ordering matters — some CMSes serve real content under a 5xx (SexMex
does), and pre-checking the status would skip scrapers that work perfectly well.

A skip means the scraper is **untested, not known-good**. Re-run when the site
is back rather than reading the skip as a pass.

Run all of them:

```bash
make smoke              # all scrapers + Stash
make smoke-one SCRAPER=<site>   # one scraper
make smoke-stash        # Stash integration only
```

These are **never run in CI** (Cloudflare blocks shared GitHub-runner IP ranges, and they hit live sites / local services). They're a manual pre-release check.

#### Stash integration tests

The Stash integration tests (`cmd/stash_integration_test.go`) verify fss's Stash layer — client construction, the flag-to-filter mapping, and the lookups `import` and `revert` make — against your real Stash instance. They are **read-only**: nothing is created, updated or deleted. The GraphQL client itself is tested in [stash-go](https://github.com/Anastylosis/stash-go), which has its own read-only live suite.

```bash
# Default: connects to http://localhost:9999, no auth
make smoke-stash

# Custom URL and/or API key
FSS_STASH_URL=http://192.168.1.50:9999 make smoke-stash
FSS_STASH_URL=http://192.168.1.50:9999 FSS_STASH_API_KEY=yourkey make smoke-stash
```

If Stash isn't reachable, all tests skip gracefully — no failures.

### 10. Update docs and tracking files

- Add a row to [docs/scrapers.md](docs/scrapers.md) with the site name, URL pattern, platform, and notes.
- Update the site count in `README.md` (auto-verified by `TestReadmeScraperCount` — run `go test ./internal/scrapers/all/...` and it will auto-fix).
- If the new scraper covers a stashdb studio tree, remove the completed entries from all three tracking files:
  - `docs/stashdb-studios.json` — remove entries where the entire tree (parent + all children) is now covered
  - `docs/stashdb-scene-counts.json` — remove the same entries
  - `docs/partially-covered.json` — remove or update completion counts

### 11. Verify

```bash
go build ./...                           # compiles
go vet ./...                             # static analysis
go test -race -count=1 ./...             # all tests pass
go build -o fss . && ./fss list-scrapers # new scraper appears
```

#### Registry-wide checks your scraper must satisfy

Several tests in `internal/scrapers/all` run against **every** registered scraper, so a
new one can fail tests in a package you never touched. What they check, and what a
failure means:

| Test | Requires | Typical fix |
|------|----------|-------------|
| `TestRegistryIDsAreWellFormed` | `ID()` is lowercase `[a-z0-9-]+` and unique | the ID becomes a store filename and a config key — no spaces, dots or capitals |
| `TestRegistryPatternsArePresent` | `Patterns()` non-empty, no blank entries | a blank entry usually means a derivation like `strings.TrimPrefix(base, …)` returned `""` |
| `TestRegistryPatternsAreNotGlued` | a bare-host pattern is not run into a path | you concatenated a path onto `SiteBase` without a leading `/`, giving `example.comtour/…` |
| `TestRegistryMatchesURLIsNotOverBroad` | `MatchesURL` rejects `example.com` and friends | anchor the regex (`^https?://…`) and escape dots (`\.`) |
| `TestScrapersExitOnCancelledContext` | the channel closes on an already-dead context | check `ctx.Err()` before the first fetch |
| `TestScraperSendsAreGuardedByContext` | every `out <-` send is in a `select` with `ctx.Done()` | see [architecture.md](docs/architecture.md#channel-based-streaming) |
| `TestSitesMdInSync` | `docs/sites.md` matches the registry | **it rewrites the file and fails once** — just commit the regenerated `docs/sites.md` |

The over-broad check is the one worth understanding rather than just satisfying:
`scraper.ForURL` returns the **first** registered match, so a loose regex takes over every
scraper after it in registration order. The scrape then appears to work and quietly
produces a different studio's catalogue.

`ForURL` does notice the ambiguity — but it reports it via `Debugf(1, …)`, so nothing is
printed unless the user passes `-d`. If a URL is scraping the wrong site, run
`fss scrape <url> -d` and look for `registry: … also matched by [...]`.

Go's RE2 has no negative lookahead, so where two scrapers legitimately overlap (a network
catch-all and its sub-studios), registration **order** is how the overlap is resolved —
register the specific ones first. `bronetwork` is the worked example: its sub-studios live
at `thebronetwork.com/categories/<slug>`, and until they were registered ahead of the
network catch-all, pasting a sub-studio URL scraped the entire network.

### Reference implementations

| Scraper | Complexity | Good example of |
|---------|-----------|-----------------|
| `pornhub` | Simple | HTML scraping, minimal fields, free content |
| `momcomesfirst` | Simple | WordPress site using `wputil` shared package, JSON-LD VideoObject |
| `babes` | Simple | Thin wrapper around `ayloutil` for an Aylo/Juan site |
| `digitalplayground` | Simple | Thin wrapper around `ayloutil` for an Aylo/Juan site |
| `mofos` | Simple | Thin wrapper around `ayloutil` for an Aylo/Juan site |
| `realitykings` | Simple | Thin wrapper around `ayloutil` for an Aylo/Juan site |
| `tabooheat` | Simple | Thin wrapper around `gammautil` for a Gamma Entertainment site |
| `naughtyamerica` | Medium | Open JSON API, paginated, multi-domain (6 sister sites), VR support, thumbnail URL construction from trailer paths |
| `nubiles` | Medium | EdgeCms HTML scraping, 20+ network domains, detail page worker pool, model/category URL filtering |
| `bangbros` | Medium | Aylo/Juan REST API with slug-to-ID resolution for `/websites/` and `/category/` URLs, uses `ayloutil` |
| `brazzers` | Medium | Aylo/Juan REST API, instance token auth, multi-filter URL parsing, series support, uses `ayloutil` |
| `loyalfans` | Medium | POST-based JSON API, cursor pagination (`page_token`), session init, owner filtering |
| `apclips` | Medium | HTML scraping, listing + detail pages for dates/tags, price tracking |
| `faphouse` | Medium | HTML listing + detail pages with embedded JSON (`view-state-data`), model/studio URL types, price tracking |
| `apovstory` | Medium | PHP tour site, HTML listing + detail pages, category extraction |
| `manyvids` | Medium | JSON API, pricing, detail-page worker pool |
| `clips4sale` | Medium | Multi-page HTML, categories, pricing |
| `iwantclips` | Medium | JSON API, double HTML-unescaping |
| `mydirtyhobby` | Medium | JSON API with auth headers |
| `taratainton` | Medium | WordPress/sitemap-driven discovery, HTML meta parsing, worker pool, uses `wputil` |
| `missax` | Medium | HTML scraping, listing + detail page worker pool, no API |
| `puretaboo` | Medium | Algolia search API, session API key extraction, rich structured JSON, uses `gammautil` |
| `rachelsteele` | Medium | MyMember.site SaaS platform, JSON list API + HTML detail pages, JSON-LD keywords parsing |

---

## Translations

The CLI's help text is translatable. A language is one flat JSON file under
`internal/i18n/locales/`, keyed on the English source string, and no Go is
involved: copy `en.json`, fill in what you're sure of, leave the rest
as `""` (empty falls back to English), run `make test`, open a PR.

Translating in the browser works too:
<https://hosted.weblate.org/projects/fss/help-text/> writes the catalog and
opens the pull request for you.

Reword an English help string and you change a key, orphaning its
translations — `make i18n-extract` regenerates the template and the test
suite names every catalog that went stale.

Full rules and the resolution order in [docs/translations.md](docs/translations.md).

---

## Cutting a release

Releases are tagged with `vMAJOR.MINOR.PATCH`. Pushing the tag triggers `.github/workflows/release.yml`, which builds the cross-platform binaries and `.deb`/`.rpm` packages automatically and then **pauses for manual approval** before publishing.

### Steps

```bash
git tag -a v1.7.0 -m "v1.7.0"
git push origin v1.7.0
```

The tag's annotation is rendered as a callout at the **top of the release page**,
above the commit list — use it for what the commits cannot say on their own:

```bash
git tag -a v1.7.1 -m "maintenance release only, no new scrapers

Existing scrapers are unaffected; no config or store changes."
git push origin v1.7.1
```

The first line becomes the bold heading and the rest the body. A lightweight tag
(`git tag v1.7.1`) simply omits the callout — it has no message of its own, and
the commit it points at is not a release note. Signed tags (`git tag -s`) are
fine; the signature block is stripped.

Every release also opens a linked **Discussion** in the *Announcements* category,
seeded with the release body, so users have somewhere to reply that is not an
issue. Two consequences worth knowing:

- The category is named in `release.yml`. Renaming or deleting *Announcements* on
  GitHub breaks the release job — and it breaks it late, after the binaries are
  built and the smoke gate is approved.
- A release's discussion cannot be changed once created, so re-running the
  `release` job for a tag that already published will fail at that step. Cut a new
  patch tag rather than re-running it. (This does not affect the `aur` and
  `docker` jobs, which are still safe to re-run on their own.)

The same message is shown by `fss version` when it reports an available update,
so it reaches users who never open the release page. It is read back out of the
release body's leading blockquote rather than from the tag object, which keeps
the update check to a single request — see `releaseNote` in `cmd/version.go`. If
you ever change how the callout is rendered above, check that parser: its failure
mode is to show no annotation at all rather than to show it wrongly, so a
mismatch is silent.

Then go to the **Actions → Release** run on GitHub, click *Review deployments*, tick `manual-smoke-gate`, and approve. Once the `release` job finishes, two parallel jobs run automatically: `aur` publishes to the AUR (with one retry on transient SSH failure), and `docker` builds and pushes the image to `ghcr.io`. Both sit behind the single approval gate — if either fails you can re-run *just that job* from the Actions UI without re-cutting the GitHub Release.

### What the release produces

| Artifact | Platforms |
|----------|-----------|
| `.tar.gz` binaries | linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 |
| `.zip` binary | windows/amd64 |
| `.deb` package | linux/amd64, linux/arm64 |
| `.rpm` package | linux/amd64, linux/arm64 |
| AUR `fss` package | auto-published after release |
| Docker image (`ghcr.io/anastylosis/fss`) | linux/amd64, linux/arm64 |

The `.deb`/`.rpm` packages are built by [nfpm](https://nfpm.goreleaser.com/) using `nfpm.yaml`. To test locally:

```bash
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
GOOS=linux GOARCH=amd64 go build -o dist/fss .
GOARCH=amd64 VERSION=1.7.0 nfpm package --packager deb --target dist/
```

### Approver checklist

Before clicking approve, confirm:

- [ ] `make smoke` (scrapers + Stash integration) passed locally — CI cannot run these because Cloudflare blocks shared GitHub runner IP ranges and Stash is a local service.
- [ ] `CHANGELOG`/release notes accurately describe user-visible changes.
- [ ] No known regressions in any of the high-severity checks you track.
- [ ] The new binary's `fss version` shows the expected tag when run locally.

The gate is a **trust-me** check — nothing verifies that you actually ran the smoke tests. Its only job is to force a pause-and-think before a release goes public.

### CI tool version pins

These pins live inside `run:` shell strings (not `go.mod`) so Dependabot can't bump them automatically — review them when you cut a release. They now live in `Anastylosis/.github`, as input defaults on the reusable workflows, and a repo overrides one by passing the input rather than by editing a `go install` line:

| Tool | Pin | Input | Reusable workflow |
|------|-----|-------|-------------------|
| `gotestsum` | `v1.13.0` | `gotestsum-version` | `go-ci.yml` (test job) |
| `golangci-lint` | `v2.11.4` | `golangci-lint-version` | `go-ci.yml` (lint job) |
| `govulncheck` | `v1.3.0` | `govulncheck-version` | `go-ci.yml` (vulncheck job) |
| `nfpm` | `v2.46.3` | `nfpm-version` | `go-release.yml` (build job) |

Bump the default in the shared repo when every project should move together; pass the input from this repo's caller when only FSS should. Check the upstream changelog for breaking format changes — `gotestsum` in particular produces the `junit.xml` uploaded to Codecov. (The coverage summary reads `coverage.out` directly, so it is unaffected by `gotestsum`'s output format.)

The actions themselves (`actions/checkout`, `codecov/codecov-action`, …) are pinned to exact patch versions in the shared workflows, not to moving major tags, so an unrelated `v7.x` release cannot turn CI red. One Dependabot PR in `Anastylosis/.github` bumps them for every repo at once; nothing needs changing here, because this repo's workflows reference `…@v1`, a major tag that is re-pointed as the shared workflows move.

### Cross-platform testing

The `Test` job runs on Linux only. `Cross-build` compiles for darwin and windows
but runs nothing, so a separate **`Test (windows-latest)` / `Test (macos-latest)`**
matrix job executes the suite on the other two platforms.

This is not belt-and-braces. Several invariants are stated in terms of POSIX
behaviour and have a different — sometimes absent — meaning elsewhere:

- `internal/store/flock_windows.go` makes the process lock a **no-op** on Windows,
  so the "two concurrent `fss scrape <same-url>` cannot race" guarantee simply
  does not hold there.
- `output.atomicWriteFile` depends on rename-over-existing, whose failure modes
  differ on Windows when the destination is open.
- The 250-byte `Slugify` cap is derived from the POSIX 255-byte *filename* limit;
  Windows constrains the whole path instead.

The job runs **without `-race`** on purpose: races are OS-independent and the
Linux job already covers them, while requiring the race detector would pull in a
C toolchain dependency on the Windows image for no benefit to the bug class this
job exists to find.

### CodeQL and dependency review

- **CodeQL** — taint analysis of *this repository's own code*, weekly and on every
  push/PR. `govulncheck` and Trivy both look at dependencies; neither reads our
  code. Findings land in the Security tab. CodeQL's Go pack is weaker than its
  JS/Java packs, so treat a quiet run as weak evidence rather than proof.

  It runs from GitHub's **default setup**, configured in Settings → Code security,
  not from a workflow in this repository. There is deliberately no `codeql.yml`:
  GitHub rejects the SARIF upload from an advanced workflow while default setup is
  enabled (`CodeQL analyses from advanced configurations cannot be processed when
  the default setup is enabled`), and default setup already covers Go with the same
  query suite plus `actions`, `python` and `ruby`. Adding a workflow means turning
  default setup off first — worth it only for something it cannot express, such as
  the `security-extended` suite, custom queries or path filters.
- **`dependency-review.yml`** — blocks a PR that adds a dependency with a
  high-or-worse advisory. It only fires on `pull_request`, so it does nothing while
  the maintainer pushes straight to master; it is in place so the first outside
  contribution gets reviewed on arrival.

### CI security checks

`govulncheck` runs on every CI build but is **informational only** — it never fails the pipeline, and there is no release-time gate that fails on findings.

Why: the Go vulnerability database reports findings against the Go toolchain itself (not just third-party deps), so a "fail on any finding" policy would force a Go toolchain bump every time a new Go point release lands — even for low-severity issues that don't affect this codebase. The trade-off is intentional: vulnerabilities surface as workflow warnings, and the maintainer decides when to bump `go.mod` and the runtime.

When triaging a new govulncheck warning:

1. Read the finding in the CI logs (`Vulncheck (informational)` job) — note the GO-YYYY-NNNN ID and which symbol triggers it.
2. If it's a `stdlib` finding, decide whether the call path is actually reachable in this codebase (govulncheck's symbol-level analysis already filters most of these).
3. Bump `go.mod`'s `go` directive and re-run `go mod tidy`. The next workflow run should clear the warning.
4. If you can't bump immediately, link the issue ID in a TODO so a future release can clear it.

### AUR and Homebrew

Packaging files live in `packaging/`:

- `packaging/aur/PKGBUILD` — Arch Linux AUR package. **Automatically published** to the AUR after the GitHub Release is created (via `KSXGitHub/github-actions-deploy-aur` action). Requires `AUR_SSH_PRIVATE_KEY` secret in the repository.
- `packaging/homebrew/fss.rb` — Reference Homebrew formula. For a proper tap, create a `homebrew-fss` repository and publish the formula there after each release.

Every release artifact also carries a SLSA v1 build attestation (`actions/attest-build-provenance` in the `release` job), verifiable with `gh attestation verify <file> --owner Anastylosis`. `SHA256SUMS` proves a download arrived intact; the attestation is what proves it came from this pipeline, since the checksums are generated by the same job that builds the binaries.

Both AUR and Homebrew support system-level updates (`yay -Syu` / `brew upgrade`). For `.deb`/`.rpm` auto-updates via `apt upgrade`/`dnf upgrade`, a hosted package repository (e.g. Packagecloud, Cloudsmith, or Gemfury) is needed — see `docs/enhancements.md`.

### One-time setup (per maintainer / per fork)

The `manual-smoke-gate` environment must exist in the GitHub repository before the workflow can pause on it. To create it:

1. Repository → **Settings → Environments → New environment**, name it `manual-smoke-gate`.
2. Under **Deployment protection rules**, tick *Required reviewers* and add yourself (and any co-maintainers).
3. Save. No environment secrets are needed.

Without this, the release workflow will fail with `Environment "manual-smoke-gate" not found` on the first tag push. Environment protection rules with required reviewers are free for public repositories.

// Package adultdoorwayutil scrapes sites on the Adult Doorway / FacialAbuse
// network. They run a variant of the Elevated X CMS — same "Modern" template
// family as Cherry Pimps but with three structural differences:
//
//  1. Every path is rooted at `/tour/` (e.g. `/tour/categories/movies_1_d.html`).
//  2. Trailer URLs are bare-slug (`/tour/trailers/{slug}.html`), with no
//     numeric scene ID. We use the slug itself as the stable scene ID.
//  3. The listing page omits the air date. Each scene needs a detail fetch to
//     pick up `Added: <date>`, runtime, description, and tags — done via a
//     bounded worker pool.
//
// Card markup (listing):
//
//	<div class="item-update no-overlay col-...">
//	  <div class="item-thumb [item-thumb-videothumb]">
//	    <a href=".../tour/trailers/{slug}.html" title="…">…</a>
//	    <img src="..." class="stdimage update_thumb thumbs" />     (plain)
//	     OR data-videoposter="..." on the wrapper div          (videothumb)
//	  </div>
//	  <div class="item-footer">
//	    <div class="item-title"><a title="…">Title</a></div>
//	    <div class="item-date">Runtime: 01:02:47 | 991 Photos</div>
//	  </div>
//	</div><!--//item-update-->
//
// Detail markup:
//
//	<div class="update-info-block">
//	  <h1 class="highlight">Title</h1>
//	  <div class="update-info-row text-gray"><strong>Added:</strong> May 26, 2026 | Runtime: 01:02:47 | 991 Photos</div>
//	  <div class="update-info-block">Description text…</div>
//	  <div class="update-info-block">
//	    <ul class="tags">
//	      <li><a href=".../tour/categories/Anal_1_d.html">Anal</a></li>
//	      …
//	    </ul>
//	  </div>
//	</div>
package adultdoorwayutil

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one Adult Doorway sister site. SiteBase is the canonical
// `https://<host>` URL; the util internally rewrites all listing/detail paths
// under `/tour/`.
type SiteConfig struct {
	ID       string
	SiteBase string
	Studio   string
	Patterns []string
	MatchRe  *regexp.Regexp
}

// maxListingPages bounds the listing walk. The largest tour on this platform
// is a few hundred pages; this is well above that and exists only so a
// misbehaving origin cannot spin.
const maxListingPages = 1000

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// Listing parser regexes. The card wrapper is `item-update`; we don't require
// the `item-video` subclass (Cherry Pimps does) because some Adult Doorway
// sites use the plain-thumbnail variant `item-thumb` without `videothumb`.
var (
	cardStartRe = regexp.MustCompile(`<div class="item-update[^"]*"`)
	titleRe     = regexp.MustCompile(`(?s)<div class="item-title">\s*<a[^>]+title="([^"]+)"`)
	sceneURLRe  = regexp.MustCompile(`(?s)<div class="item-title">\s*<a\s+href="([^"]+)"`)
	posterRe    = regexp.MustCompile(`data-videoposter="([^"]+)"`)
	imgThumbRe  = regexp.MustCompile(`<img[^>]+class="[^"]*stdimage[^"]*"[^>]+src="([^"]+)"`)
	imgThumbAlt = regexp.MustCompile(`<img[^>]+src="([^"]+)"[^>]+class="[^"]*stdimage[^"]*"`)
	durationRe  = regexp.MustCompile(`Runtime:\s*([0-9:]+)`)
	// Not every site on this template labels the runtime. himeros.tv writes the
	// item-date row as "August 17, 2026 | 22:44 | 1,780 views", so a bare
	// HH:MM(:SS) between pipes is the fallback — only consulted when the
	// labelled form is absent, so the labelled sites are unaffected.
	itemDateRowRe = regexp.MustCompile(`(?s)<div class="item-date">(.*?)</div>`)
	bareRuntimeRe = regexp.MustCompile(`(?:^|\|)\s*(\d{1,2}:\d{2}(?::\d{2})?)\s*(?:\||$)`)
	// Case-insensitive: Adult Doorway's own sites slug in lowercase, but the
	// same template is used with mixed-case slugs (himeros.tv publishes
	// `/tour/trailers/The-Good-Boys.html`), and a lowercase-only pattern
	// matched none of them — the listing parsed to zero scenes.
	slugFromURL  = regexp.MustCompile(`/trailers/([A-Za-z0-9][A-Za-z0-9-]*)\.html`)
	categorySlug = regexp.MustCompile(`/categories/([^_/.]+)`)
	maxPageRe    = regexp.MustCompile(`movies_(\d+)_d\.html`)
)

const cardEnd = "<!--//item-update-->"

// Detail-page regexes.
var (
	detailTitleRe = regexp.MustCompile(`<h1 class="highlight"[^>]*>([^<]+)</h1>`)
	detailMetaRe  = regexp.MustCompile(`<strong>Added:</strong>\s*([A-Z][a-z]+ \d{1,2}, \d{4})(?:\s*\|\s*Runtime:\s*([0-9:]+))?`)
	infoBlockRe   = regexp.MustCompile(`(?s)<div class="update-info-block"[^>]*>(.*?)</div>`)
	tagListRe     = regexp.MustCompile(`(?s)<ul class="tags">(.*?)</ul>`)
	tagItemRe     = regexp.MustCompile(`<a[^>]+href="[^"]*/categories/[^"]+"[^>]*>([^<]+)</a>`)
)

type sceneItem struct {
	id    string // slug from /tour/trailers/{slug}.html
	title string
	url   string
	thumb string
	// Filled in by detail fetch:
	date        time.Time
	duration    int
	description string
	tags        []string
}

// parseListing extracts scene cards from one paginated listing page. The
// returned items have ID / Title / URL / Thumb populated; date, runtime,
// description, and tags require a detail fetch.
func parseListing(body []byte) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))

	seen := make(map[string]bool, len(starts))
	for _, loc := range starts {
		rest := page[loc[0]:]
		endIdx := strings.Index(rest, cardEnd)
		if endIdx < 0 {
			continue
		}
		block := rest[:endIdx]

		var item sceneItem

		// Scene URL is the canonical anchor for both ID and link.
		if sm := sceneURLRe.FindStringSubmatch(block); sm != nil {
			item.url = sm[1]
			if slug := slugFromURL.FindStringSubmatch(item.url); slug != nil {
				item.id = slug[1]
			}
		}
		if item.id == "" || seen[item.id] {
			// "More Updates" carousels at the bottom of detail pages re-emit
			// cards; dedupe by slug.
			continue
		}
		seen[item.id] = true

		if sm := titleRe.FindStringSubmatch(block); sm != nil {
			item.title = html.UnescapeString(strings.TrimSpace(sm[1]))
		}

		// Thumbnail — try the videothumb poster first, then the plain image.
		if sm := posterRe.FindStringSubmatch(block); sm != nil {
			item.thumb = sm[1]
		} else if sm := imgThumbRe.FindStringSubmatch(block); sm != nil {
			item.thumb = sm[1]
		} else if sm := imgThumbAlt.FindStringSubmatch(block); sm != nil {
			item.thumb = sm[1]
		}

		// Listing-side runtime if available; detail fetch may overwrite.
		if sm := durationRe.FindStringSubmatch(block); sm != nil {
			item.duration = parseutil.ParseDurationColon(sm[1])
		} else if row := itemDateRowRe.FindStringSubmatch(block); row != nil {
			if sm := bareRuntimeRe.FindStringSubmatch(row[1]); sm != nil {
				item.duration = parseutil.ParseDurationColon(sm[1])
			}
		}

		items = append(items, item)
	}
	return items
}

// estimateTotal reads the pagination block on a listing page and returns
// max-page * items-on-this-page, giving a rough scene-count hint.
func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func enrichFromDetail(body []byte, item *sceneItem) {
	if sm := detailTitleRe.FindStringSubmatch(string(body)); sm != nil {
		t := html.UnescapeString(strings.TrimSpace(sm[1]))
		if t != "" {
			item.title = t
		}
	}
	if sm := detailMetaRe.FindStringSubmatch(string(body)); sm != nil {
		if d, err := time.Parse("January 2, 2006", sm[1]); err == nil {
			item.date = d.UTC()
		}
		if sm[2] != "" {
			item.duration = parseutil.ParseDurationColon(sm[2])
		}
	}
	// Description: the second `update-info-block` is the prose. The first
	// contains the <h1>, the third is the tag <ul>. Pick the first block whose
	// content is plain text (no nested <div> or <ul>).
	for _, m := range infoBlockRe.FindAllSubmatch(body, -1) {
		inner := strings.TrimSpace(string(m[1]))
		if inner == "" {
			continue
		}
		if strings.Contains(inner, "<h1") || strings.Contains(inner, "<ul") || strings.Contains(inner, "<div") {
			continue
		}
		item.description = html.UnescapeString(inner)
		break
	}
	if sm := tagListRe.FindSubmatch(body); sm != nil {
		for _, tm := range tagItemRe.FindAllSubmatch(sm[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(string(tm[1])))
			if name == "" {
				continue
			}
			item.tags = append(item.tags, name)
		}
	}
}

// URL-mode plumbing — what kind of listing did the user point us at?

type listMode int

const (
	modeFullCatalog listMode = iota
	modeCategory
)

type listConfig struct {
	mode listMode
	slug string
}

func parseStudioURL(studioURL string) listConfig {
	if m := categorySlug.FindStringSubmatch(studioURL); m != nil {
		slug := m[1]
		// `/categories/movies.html` is the full catalog under a different name.
		if slug != "movies" && slug != "" {
			return listConfig{mode: modeCategory, slug: slug}
		}
	}
	return listConfig{mode: modeFullCatalog}
}

func (lc listConfig) pageURL(base string, page int) string {
	switch lc.mode {
	case modeCategory:
		return fmt.Sprintf("%s/tour/categories/%s_%d_d.html", base, lc.slug, page)
	default:
		return fmt.Sprintf("%s/tour/categories/movies_%d_d.html", base, page)
	}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	lc := parseStudioURL(studioURL)
	if lc.mode == modeCategory {
		scraper.Debugf(1, "%s: scraping category %q", s.cfg.ID, lc.slug)
	} else {
		scraper.Debugf(1, "%s: scraping full catalog", s.cfg.ID)
	}

	// Stage 1: paginate the listing, collecting (id, title, url, thumb) for
	// every scene up to the first KnownID. Bail on ctx at each loop iteration.
	items, sentTotal := s.collectListing(ctx, lc, opts, out)
	if items == nil || ctx.Err() != nil {
		return
	}
	if !sentTotal && len(items) > 0 {
		select {
		case out <- scraper.Progress(len(items)):
		case <-ctx.Done():
			return
		}
	}

	// Stage 2: bounded worker pool fetches detail pages and emits the
	// enriched Scene to the consumer.
	s.fetchDetails(ctx, items, opts, out)
}

// collectListing walks pages until either a known ID is hit (early stop), the
// page yields zero scenes, or the context is cancelled. Returns nil on a fatal
// fetch error after surfacing it on `out`.
func (s *Scraper) collectListing(ctx context.Context, lc listConfig, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []sceneItem, sentTotal bool) {
	// The listing has no page count to end on, and parseListing only
	// deduplicates within a single page. An origin that clamps `?page=N` back
	// to page 1 — or a tour that repeats its last page rather than 404ing —
	// would otherwise loop forever, appending the same cards until the process
	// ran out of memory. A page that yields no id the walk has not already
	// seen is that clamp; maxListingPages is the backstop.
	acrossPages := map[string]bool{}
	for page := 1; page <= maxListingPages; page++ {
		if ctx.Err() != nil {
			return items, sentTotal
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return items, sentTotal
			}
		}

		pageURL := lc.pageURL(s.cfg.SiteBase, page)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", s.cfg.ID, page, pageURL)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return nil, sentTotal
		}

		scenes := parseListing(body)
		if len(scenes) == 0 {
			return items, sentTotal
		}

		if !sentTotal {
			total := estimateTotal(body, len(scenes))
			scraper.Debugf(1, "%s: %d total scenes (estimated)", s.cfg.ID, total)
			if total > 0 {
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return items, sentTotal
				}
			}
			sentTotal = true
		}

		fresh := 0
		for _, item := range scenes {
			if acrossPages[item.id] {
				continue
			}
			acrossPages[item.id] = true
			fresh++
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, item.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, sentTotal
			}
			items = append(items, item)
		}
		if fresh == 0 {
			scraper.Debugf(1, "%s: page %d repeats a page already walked, stopping", s.cfg.ID, page)
			return items, sentTotal
		}
	}
	scraper.Debugf(1, "%s: stopped at the %d-page cap", s.cfg.ID, maxListingPages)
	return items, sentTotal
}

// fetchDetails runs a bounded worker pool over the collected items, fetching
// detail pages and emitting enriched Scenes. Workers is capped at 4 (default)
// or the user's opts.Workers; either way the cleanup pattern
// `defer wg.Wait(); defer close(work)` guarantees no goroutine leak on
// context cancellation (see AUDIT.md §Concurrency #1 for prior art).
func (s *Scraper) fetchDetails(ctx context.Context, items []sceneItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(items), workers)

	work := make(chan sceneItem, workers)
	var wg sync.WaitGroup
	// LIFO defers: close(work) runs first so workers' `range work` exits,
	// then wg.Wait() blocks until they're all gone.
	defer wg.Wait()
	defer close(work)

	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				body, err := s.fetchPage(ctx, item.url)
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, err)):
					case <-ctx.Done():
						return
					}
					continue
				}
				enrichFromDetail(body, &item)
				select {
				case out <- scraper.Scene(item.toScene(s.cfg.ID, s.cfg.SiteBase, s.cfg.Studio, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, item := range items {
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
}

func (item sceneItem) toScene(siteID, siteBase, studio string, now time.Time) models.Scene {
	url := item.url
	if strings.HasPrefix(url, "/") {
		url = siteBase + url
	}
	return models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   siteBase,
		Title:       item.title,
		URL:         url,
		Thumbnail:   item.thumb,
		Date:        item.date,
		Duration:    item.duration,
		Description: item.description,
		Tags:        item.tags,
		Studio:      studio,
		ScrapedAt:   now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := checkCMSError(body); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	return body, nil
}

// dbErrorRe matches the error page this CMS serves when its database is
// unreachable. It comes back as HTTP 200 with a short body, so without this
// check a site-side outage reads as a page with no scenes on it — every
// listing parses to zero results and the run looks like an empty catalogue
// rather than a failure.
var dbErrorRe = regexp.MustCompile(`(?i)Error</B></span> connecting to database server|PDO Error Message`)

func checkCMSError(body []byte) error {
	if dbErrorRe.Match(body) {
		return errors.New("site database is unreachable (CMS returned its DB-error page with HTTP 200)")
	}
	return nil
}

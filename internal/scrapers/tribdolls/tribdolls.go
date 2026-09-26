// Package tribdolls scrapes Trib Dolls (trib-dolls.com), a custom PHP site on
// Zurb Foundation 6.
//
// Note the hyphen: `tribdolls.com` redirects to the hyphenated host but
// presents a certificate that does not match it, so requests always go to
// `www.trib-dolls.com`.
//
// The listing is path-paginated (`/all-trib-dolls-videos/{N}/`, 30 per page)
// and the last page number is printed in the page-1 nav, so the end is read
// rather than probed for.
//
// **The site publishes no absolute date anywhere** — listing and detail both
// show only "N days ago". It is always in whole days, even at the far end of
// the archive ("5203 days ago"), so the date is computed as
// `scrapeDay - N days`. That is exact to the day and stable across re-scrapes:
// a run a day later sees N incremented by one, giving the same result.
//
// The card carries id, title, URL, duration and thumbnail. Performers and
// categories need the detail page. There is **no per-scene description** — the
// `<meta name="description">` is the same site-wide boilerplate on every page,
// so none is recorded.
package tribdolls

import (
	"context"
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

const (
	siteID        = "tribdolls"
	studioName    = "Trib Dolls"
	detailWorkers = 4
)

var siteBase = "https://www.trib-dolls.com"

// Scraper implements scraper.StudioScraper for Trib Dolls.
type Scraper struct {
	Client *http.Client
}

// New constructs a Trib Dolls scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"trib-dolls.com",
		"trib-dolls.com/all-trib-dolls-videos/{N}/",
		"trib-dolls.com/movies/{slug}-{id}/",
		"trib-dolls.com/girls/{slug}-{id}/",
	}
}

// The unhyphenated domain redirects here but serves a mismatched certificate,
// so it is matched for convenience while requests still go to the hyphenated
// host.
var matchRe = regexp.MustCompile(`^https?://(?:www\.)?trib-?dolls\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// Relative ages are resolved against the day the scrape started, so every
	// scene in a run shares one reference point.
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)

	seen := make(map[string]bool)
	maxPage := 0
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/all-trib-dolls-videos/%d/", siteBase, page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		// Page 1's nav prints the last page number, so the end is read rather
		// than discovered by overshooting.
		if page == 1 {
			maxPage = parseMaxPage(body)
			scraper.Debugf(1, "%s: %d pages in the listing nav", siteID, maxPage)
		}

		items := parseListing(body, today)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{
			Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay),
			Done:   maxPage > 0 && page >= maxPage,
		}, nil
	})
}

// ---- listing ----

var (
	cardRe = regexp.MustCompile(`<div class="card"`)
	// The numeric id is the trailing slug segment and also the /data/movies/
	// image directory.
	linkRe = regexp.MustCompile(`<a href="(/movies/([^"/]*?)-(\d+)/)" title="([^"]*)"`)
	// "TD2248 / 25:04" — the studio code and runtime share one span.
	codeRe    = regexp.MustCompile(`<span>([A-Z]{2}\d+)\s*/\s*(\d{1,2}:\d{2}(?::\d{2})?)</span>`)
	thumbRe   = regexp.MustCompile(`<img src="(/data/movies/\d+/[^"]+)"`)
	agoRe     = regexp.MustCompile(`(\d+)\s+days?\s+ago`)
	navPageRe = regexp.MustCompile(`/all-trib-dolls-videos/(\d+)/`)
)

type listItem struct {
	id, url, title, code, thumb string
	date                        time.Time
	duration                    int
}

// parseMaxPage reads the highest page number out of the listing nav.
func parseMaxPage(body []byte) int {
	maxPage := 0
	for _, m := range navPageRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func parseListing(body []byte, today time.Time) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			id:    m[3],
			url:   siteBase + m[1],
			title: cleanText(m[4]),
		}
		if c := codeRe.FindStringSubmatch(card); c != nil {
			it.code = c[1]
			it.duration = parseutil.ParseDurationColon(c[2])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = siteBase + th[1]
		}
		if a := agoRe.FindStringSubmatch(card); a != nil {
			if n, err := strconv.Atoi(a[1]); err == nil {
				it.date = today.AddDate(0, 0, -n)
			}
		}

		items = append(items, it)
	}
	return items
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

var (
	// Categories sit in the heading block alongside the relative age.
	categoriesRe = regexp.MustCompile(`(?s)<div class="categories">(.*?)</div>`)
	categoryRe   = regexp.MustCompile(`<a href="/category/[^"]*">([^<]+)</a>`)
	// Cast entries are <h3> links to a girl's profile, with her age in a span.
	girlRe = regexp.MustCompile(`<a href="/girls/[^"]*" title="([^"]+)"`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Date:      it.date,
		Duration:  it.duration,
		Thumbnail: it.thumb,
		Studio:    studioName,
		// The studio code (e.g. "TD2248") is the site's own catalogue number.
		Series:    it.code,
		ScrapedAt: now,
	}

	// Only cast and categories need the detail page; a failure there still
	// leaves a complete scene.
	if body, err := s.fetchPage(ctx, it.url); err == nil {
		applyDetail(&scene, string(body))
	}
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if cb := categoriesRe.FindStringSubmatch(detail); cb != nil {
		seen := make(map[string]bool)
		for _, m := range categoryRe.FindAllStringSubmatch(cb[1], -1) {
			cat := cleanText(m[1])
			if cat == "" || seen[cat] {
				continue
			}
			seen[cat] = true
			scene.Categories = append(scene.Categories, cat)
		}
	}
	seen := make(map[string]bool)
	for _, m := range girlRe.FindAllStringSubmatch(detail, -1) {
		name := cleanText(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		scene.Performers = append(scene.Performers, name)
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

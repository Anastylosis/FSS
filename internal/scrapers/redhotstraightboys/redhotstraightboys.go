// Package redhotstraightboys scrapes Red Hot Straight Boys
// (redhotstraightboys.com) and its sibling Spanking Straight Boys
// (spankingstraightboys.com), two Elevated X classic tours on the
// `update_details` card skin.
//
// The skin is the one girlsrimming and auntjudys use, but the routes differ —
// `/tour/categories/updates_{N}_d.html` for the listing and
// `/tour/updates/{Slug}.html` for scenes — which is why these two live in their
// own package, per the reasoning in the goldwinpass package doc. Between
// themselves the markup and routes are identical, so they are table-driven
// here.
//
// The card carries id, title, URL, performers, duration, date and thumbnail.
// Only the description and tags need the detail page, which a worker pool
// fetches.
//
// Three things the markup forces:
//
//   - The date cell opens with an HTML comment before its value, so the cell is
//     captured first and the date picked out of it.
//   - Runtime is written as "12&nbsp;min&nbsp;of video" inside an
//     `update_counts` block that on other sites in this family also carries a
//     photo count, so the count block is scoped before reading minutes.
//   - **Thumbnail URLs are signed**, carrying `expires` and `token` query
//     parameters that change on every request. They are stored as served, so a
//     re-scrape will show a differing thumbnail URL for an unchanged scene.
//
// The detail page has no description body — og:description is the only source,
// and the CMS truncates it. `<meta name="keywords">` is site-wide boilerplate,
// not per-scene, so tags come from the scene's own category links instead.
package redhotstraightboys

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	detailWorkers = 4
	// Card dates are US-format.
	dateLayout = "01/02/2006"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []siteConfig{
	{"redhotstraightboys", "redhotstraightboys.com", "Red Hot Straight Boys"},
	{"spankingstraightboys", "spankingstraightboys.com", "Spanking Straight Boys"},
}

// Scraper implements scraper.StudioScraper for one Straight Boys site.
type Scraper struct {
	cfg     siteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

func newScraper(cfg siteConfig) *Scraper {
	escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/tour/categories/updates_{N}_d.html",
		s.cfg.Domain + "/tour/updates/{slug}.html",
		s.cfg.Domain + "/tour/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		// `_d` is the skin's date-descending sort. The catalogue runs ~39 pages
		// of 8; a page past the end comes back with no cards.
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/tour/categories/updates_%d_d.html", s.base, page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
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
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// ---- listing ----

var (
	// Cards are delimited by the Elevated X data-setid marker.
	cardRe = regexp.MustCompile(`class="update_details" data-setid="(\d+)"`)
	// Only the update filename is captured so the URL is rebuilt against
	// siteBase — cards link absolutely.
	updateRe = regexp.MustCompile(`href="[^"]*/tour/updates/([^"/]+\.html)"[^>]*>([^<]*)</a>`)
	thumbRe  = regexp.MustCompile(`src0_(?:4x|3x|1x)="([^"]+)"`)
	// The date cell opens with an HTML comment before the value, so the cell is
	// captured first and the date picked out of it.
	dateCellRe  = regexp.MustCompile(`(?s)class="cell update_date">(.*?)</div>`)
	dateValueRe = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	// "12&nbsp;min&nbsp;of video" — sites on this skin also put a photo count
	// here, so the block is scoped before minutes are read.
	countsRe    = regexp.MustCompile(`(?s)class="update_counts">(.*?)</div>`)
	durationRe  = regexp.MustCompile(`(\d+)\s*(?:&nbsp;)*\s*min`)
	modelsRe    = regexp.MustCompile(`(?s)class="update_models">(.*?)</span>`)
	modelLinkRe = regexp.MustCompile(`href="[^"]*/models/[^"]*">([^<]+)</a>`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
	performers            []string
}

func (s *Scraper) parseListing(body []byte) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := page[loc[0]:end]

		it := listItem{id: page[loc[2]:loc[3]]}

		m := updateRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it.url = s.base + "/tour/updates/" + m[1]
		// The first update anchor wraps the thumbnail and has no text; the
		// second is the title link. Take the first non-empty one.
		for _, tm := range updateRe.FindAllStringSubmatch(card, -1) {
			if t := cleanText(tm[2]); t != "" {
				it.title = t
				break
			}
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}
		if cell := dateCellRe.FindStringSubmatch(card); cell != nil {
			if d := dateValueRe.FindStringSubmatch(cell[1]); d != nil {
				if ts, err := time.Parse(dateLayout, d[1]); err == nil {
					it.date = ts.UTC()
				}
			}
		}
		if c := countsRe.FindStringSubmatch(card); c != nil {
			if d := durationRe.FindStringSubmatch(c[1]); d != nil {
				it.duration = atoi(d[1]) * 60
			}
		}
		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			for _, pm := range modelLinkRe.FindAllStringSubmatch(mb[1], -1) {
				if name := cleanText(pm[1]); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}

		items = append(items, it)
	}
	return items
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)
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
	ogDescRe = regexp.MustCompile(`property="og:description"\s+content="([^"]*)"`)
	// Tags are the scene's own category links. The listing routes
	// (`updates_{N}_d`) and the tag index (`tags`) live under the same path and
	// are not tags.
	tagRe = regexp.MustCompile(`/tour/categories/([A-Za-z0-9_-]+)\.html"[^>]*>([^<]+)</a>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Date:       it.date,
		Duration:   it.duration,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     s.cfg.StudioName,
		ScrapedAt:  now,
	}

	// Only the description and tags need the detail page; a failure there still
	// leaves a complete scene.
	if body, err := s.fetchPage(ctx, it.url); err == nil {
		applyDetail(&scene, string(body))
	}
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := ogDescRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}
	seen := make(map[string]bool)
	for _, m := range tagRe.FindAllStringSubmatch(detail, -1) {
		if m[1] == "tags" || strings.HasPrefix(m[1], "updates_") {
			continue
		}
		tag := cleanText(m[2])
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		scene.Tags = append(scene.Tags, tag)
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
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

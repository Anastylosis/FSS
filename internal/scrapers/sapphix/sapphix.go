// Package sapphix scrapes the Sapphix lesbian/glamour network, which runs a
// shared CMS across several standalone domains (sapphix.com, sapphicerotica.com,
// fistflush.com, givemepink.com). It is a table-driven package: one scraper is
// registered per site in init().
//
// The public listing is paginated newest-first at /movies/page-{N}/ and each
// card links to a detail page at /movies/{slug}/. The scene slug is the ID.
// Listing cards carry the title, MM/DD/YYYY date and thumbnail; detail pages
// add the description, featured models and tags.
package sapphix

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

// SiteConfig describes one site served by the shared Sapphix CMS.
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "sapphicerotica"
	Domain     string // bare domain, e.g. "sapphicerotica.com"
	StudioName string // display name, e.g. "Sapphic Erotica"
}

// sites lists every Sapphix-CMS domain with a working public /movies/ listing.
// Hanna's Honeypot, InFocusGirls and Only Cuties were probed but only serve a
// static tour/landing page (no paginated /movies/ listing), so they are not
// registered.
var sites = []SiteConfig{
	{SiteID: "sapphicerotica", Domain: "sapphicerotica.com", StudioName: "Sapphic Erotica"},
	{SiteID: "sapphix", Domain: "sapphix.com", StudioName: "Sapphix"},
	{SiteID: "fistflush", Domain: "fistflush.com", StudioName: "Fist Flush"},
	{SiteID: "givemepink", Domain: "givemepink.com", StudioName: "Give Me Pink"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// newFor builds the registered scraper for a given site id. Used by the
// integration tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for a single Sapphix-CMS site.
type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New constructs a Scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	escaped := regexp.QuoteMeta(cfg.Domain)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/movies/page-{n}/",
		s.cfg.Domain + "/movies/{slug}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

var (
	// cardRe isolates each listing card. Each card opens with the movie anchor
	// and the card's metadata (date + name) follows in an <h5> block before the
	// next card's anchor begins.
	cardRe = regexp.MustCompile(`(?s)<a class="bloc-link[^"]*" href="/movies/[a-z0-9-]+/".*?</h5>`)
	// cardLinkRe pulls the scene slug from a card anchor; the slug is the ID.
	cardLinkRe = regexp.MustCompile(`href="/movies/([a-z0-9-]+)/"`)
	// cardThumbRe pulls the first (active) thumbnail image from the card.
	cardThumbRe = regexp.MustCompile(`<img[^>]+src="([^"]+/cover/[^"]+)"`)
	// cardDateRe / cardNameRe read the metadata spans in the card's <h5>.
	cardDateRe = regexp.MustCompile(`<span class="nm-date">\s*(\d{2}/\d{2}/\d{4})\s*</span>`)
	cardNameRe = regexp.MustCompile(`(?s)<span class="nm-name[^"]*"[^>]*>\s*(.*?)\s*</span>`)

	// detailTitleRe / detailPosterRe / detailDescRe / detailDateRe pull the
	// scene fields from the detail page.
	detailTitleRe  = regexp.MustCompile(`(?s)<h2>\s*(.*?)\s*</h2>`)
	detailPosterRe = regexp.MustCompile(`poster="([^"]+)"`)
	detailDescRe   = regexp.MustCompile(`(?s)<p class="mg-md">\s*(.*?)\s*</p>`)
	detailDateRe   = regexp.MustCompile(`<span>\s*Added\s+([A-Za-z]+ \d{1,2}, \d{4})\s*</span>`)

	// detailModelsRe captures the "Featured model(s)" paragraph; modelLinkRe
	// then extracts each model name from it.
	detailModelsRe = regexp.MustCompile(`(?s)<h4>\s*Featured model\(s\):\s*</h4>\s*<p>(.*?)</p>`)
	modelLinkRe    = regexp.MustCompile(`<a[^>]+href=['"]/models/[^'"]+['"][^>]*>\s*(.*?)\s*</a>`)

	// detailTagsRe captures the "Tags" block; tagLinkRe extracts each tag.
	detailTagsRe = regexp.MustCompile(`(?s)<h4>\s*Tags:\s*</h4>(.*?)</div>`)
	tagLinkRe    = regexp.MustCompile(`<a[^>]+href=['"][^'"]*tag\[?\]?=[^'"]*['"][^>]*>\s*(.*?)\s*</a>`)
)

type listItem struct {
	id        string
	title     string
	thumbnail string
	date      time.Time
}

type detailData struct {
	title       string
	description string
	thumbnail   string
	date        time.Time
	performers  []string
	tags        []string
}

func parseListingPage(body []byte) []listItem {
	var items []listItem
	for _, card := range cardRe.FindAll(body, -1) {
		if it, ok := parseCard(card); ok {
			items = append(items, it)
		}
	}
	return items
}

func parseCard(card []byte) (listItem, bool) {
	m := cardLinkRe.FindSubmatch(card)
	if m == nil {
		return listItem{}, false
	}
	it := listItem{id: string(m[1])}

	if mt := cardThumbRe.FindSubmatch(card); mt != nil {
		it.thumbnail = string(mt[1])
	}
	if mn := cardNameRe.FindSubmatch(card); mn != nil {
		it.title = cleanText(string(mn[1]))
	}
	if md := cardDateRe.FindSubmatch(card); md != nil {
		if t, err := time.Parse("01/02/2006", string(md[1])); err == nil {
			it.date = t.UTC()
		}
	}
	return it, true
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/movies/page-%d/?tag=&q=&model=&sort=", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		// Pages eventually clamp/repeat rather than 404, so detect the end via
		// cross-page dedup: a page with no previously-unseen slugs means we have
		// exhausted the catalogue.
		fresh := items[:0]
		for _, it := range items {
			if seen[it.id] {
				continue
			}
			seen[it.id] = true
			fresh = append(fresh, it)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := s.fetchDetails(ctx, fresh, opts, now)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	results := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		// Known IDs become lightweight stubs so Paginate's early-stop fires
		// without spending a detail fetch.
		if opts.KnownIDs[it.id] {
			results[i] = models.Scene{ID: it.id, SiteID: s.cfg.SiteID}
			continue
		}
		wg.Add(1)
		go func(idx int, item listItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			d, err := s.fetchDetail(ctx, s.base+"/movies/"+item.id+"/")
			if err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (skipping)", s.cfg.SiteID, item.id, err)
				return
			}
			results[idx] = s.toScene(item, d, now)
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" {
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func (s *Scraper) fetchDetail(ctx context.Context, rawURL string) (detailData, error) {
	body, err := s.fetchPage(ctx, rawURL)
	if err != nil {
		return detailData{}, err
	}
	return parseDetailPage(body), nil
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = cleanText(string(m[1]))
	}
	if m := detailPosterRe.FindSubmatch(body); m != nil {
		d.thumbnail = string(m[1])
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = cleanText(string(m[1]))
	}
	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("January 2, 2006", string(m[1])); err == nil {
			d.date = t.UTC()
		}
	}
	if m := detailModelsRe.FindSubmatch(body); m != nil {
		for _, mm := range modelLinkRe.FindAllSubmatch(m[1], -1) {
			if name := cleanText(string(mm[1])); name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}
	if m := detailTagsRe.FindSubmatch(body); m != nil {
		seen := make(map[string]bool)
		for _, mm := range tagLinkRe.FindAllSubmatch(m[1], -1) {
			tag := cleanText(string(mm[1]))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				d.tags = append(d.tags, tag)
			}
		}
	}
	return d
}

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
	}
	date := it.date
	if !d.date.IsZero() {
		date = d.date
	}
	thumb := it.thumbnail
	if d.thumbnail != "" {
		thumb = d.thumbnail
	}

	return models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       title,
		URL:         s.base + "/movies/" + it.id + "/",
		Studio:      s.cfg.StudioName,
		Date:        date,
		Description: d.description,
		Thumbnail:   thumb,
		Performers:  d.performers,
		Tags:        d.tags,
		ScrapedAt:   now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func cleanText(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}

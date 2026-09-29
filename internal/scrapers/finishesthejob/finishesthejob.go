// Package finishesthejob scrapes the Finishes The Job network — Mano Job
// (manojob.com), Mr. POV (mrpov.com), The Dick Suckers (thedicksuckers.com)
// and the Finishes The Job hub (finishesthejob.com). All four run the same
// custom Bootstrap CMS: a paginated public listing at /updates/{brand}/{N}
// (newest-first) whose cards link to detail pages at /scene/{brand}/{slug}.
//
// It is a table-driven package: one scraper is registered per site in init().
// Each scraper scrapes its own domain's /updates/{brand} listing. The hub
// (finishesthejob.com) additionally surfaces the three sub-brands, but those
// scenes are already covered by the brands' own scrapers, so the hub scraper
// only walks its own /updates/finishesthejob listing.
package finishesthejob

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

// SiteConfig describes one Finishes The Job site served by this package.
//
// Brand is the path segment used in both /updates/{brand} and
// /scene/{brand}/{slug}; it is also the SiteID. Domain is the site's own host.
type SiteConfig struct {
	SiteID     string // stable lowercase id and URL brand segment, e.g. "manojob"
	Domain     string // bare domain, e.g. "manojob.com"
	StudioName string // display name, e.g. "Mano Job"
}

var sites = []SiteConfig{
	{SiteID: "manojob", Domain: "manojob.com", StudioName: "Mano Job"},
	{SiteID: "mrpov", Domain: "mrpov.com", StudioName: "Mr. POV"},
	{SiteID: "thedicksuckers", Domain: "thedicksuckers.com", StudioName: "The Dick Suckers"},
	{SiteID: "finishesthejob", Domain: "finishesthejob.com", StudioName: "Finishes The Job"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newFor(cfg.SiteID))
	}
}

// newFor builds the registered scraper for a given site id. It is also used by
// the integration tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for a single Finishes The Job site.
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
		s.cfg.Domain + "/updates/" + s.cfg.SiteID + "/{n}",
		s.cfg.Domain + "/scene/" + s.cfg.SiteID + "/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

var (
	// cardRe isolates each listing card. Cards open with `card scene`.
	cardRe = regexp.MustCompile(`(?s)<div class="card scene".*?</div>\s*</div>\s*</div>`)
	// cardLinkRe pulls the scene href + brand + slug from a card's anchor.
	cardLinkRe = regexp.MustCompile(`href="https?://[^"]*/scene/([^/"]+)/([^"]+)"`)
	// cardThumbRe pulls the featured thumbnail.
	cardThumbRe = regexp.MustCompile(`<img src="([^"]+)"`)
	// cardTitleRe pulls the card title.
	cardTitleRe = regexp.MustCompile(`(?s)<h3 class="card-title name"[^>]*>(.*?)</h3>`)
	// cardPerfRe pulls performer names from the card's performer links.
	cardPerfRe = regexp.MustCompile(`href="https?://[^"]*/performer/[^"]*">\s*([^<]+)</a>`)

	// pageNumRe finds /updates/{brand}/{N} page numbers for a total estimate.
	pageNumRe = regexp.MustCompile(`/updates/[^/"]+/(\d+)`)

	// detailTitleRe pulls the detail-page <h1 itemprop="name">.
	detailTitleRe = regexp.MustCompile(`(?s)<h1 itemprop="name">(.*?)</h1>`)
	// detailDateRe pulls the uploadDate microdata meta.
	detailDateRe = regexp.MustCompile(`itemprop="uploadDate" content="([^"]+)"`)
	// detailDescRe pulls the description paragraph.
	detailDescRe = regexp.MustCompile(`(?s)<p itemprop="description">(.*?)</p>`)
	// detailCatBlockRe / detailCatRe pull the "Categories" badge links.
	detailCatRe = regexp.MustCompile(`(?s)href="https?://[^"]*/category/[^"]*"[^>]*>(.*?)</a>`)
	// detailStarringRe isolates the "Starring:" <h3> block so performer
	// extraction is scoped to the actual cast and not the page's recommended
	// /performer/ links elsewhere.
	detailStarringRe = regexp.MustCompile(`(?s)<h3>\s*Starring:(.*?)</h3>`)
	// detailPerfRe pulls performer names from the starring block.
	detailPerfRe = regexp.MustCompile(`(?s)href="https?://[^"]*/performer/[^"]*">(.*?)</a>`)
	// detailThumbRe pulls the og:image fallback thumbnail.
	detailThumbRe = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
)

type listItem struct {
	id         string
	brand      string
	url        string
	title      string
	thumbnail  string
	performers []string
}

type detailData struct {
	title       string
	description string
	date        time.Time
	tags        []string
	performers  []string
	thumbnail   string
}

func parseListing(body []byte) []listItem {
	cards := cardRe.FindAll(body, -1)
	items := make([]listItem, 0, len(cards))
	seen := make(map[string]bool)
	for _, card := range cards {
		it, ok := parseCard(card)
		if !ok || seen[it.id] {
			continue
		}
		seen[it.id] = true
		items = append(items, it)
	}
	return items
}

func parseCard(card []byte) (listItem, bool) {
	m := cardLinkRe.FindSubmatch(card)
	if m == nil {
		return listItem{}, false
	}
	brand := string(m[1])
	slug := string(m[2])
	it := listItem{
		id:    brand + "/" + slug,
		brand: brand,
		url:   "/scene/" + brand + "/" + slug,
	}
	if mt := cardTitleRe.FindSubmatch(card); mt != nil {
		it.title = cleanText(string(mt[1]))
	}
	if mTh := cardThumbRe.FindSubmatch(card); mTh != nil {
		it.thumbnail = cleanText(string(mTh[1]))
	}
	for _, mp := range cardPerfRe.FindAllSubmatch(card, -1) {
		if name := cleanText(string(mp[1])); name != "" {
			it.performers = append(it.performers, name)
		}
	}
	return it, true
}

func parseDetail(body []byte) detailData {
	var d detailData
	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = cleanText(string(m[1]))
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = cleanText(string(m[1]))
	}
	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(string(m[1])),
			time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"); err == nil {
			d.date = t.UTC()
		}
	}
	if m := detailThumbRe.FindSubmatch(body); m != nil {
		d.thumbnail = cleanText(string(m[1]))
	}
	seenTag := make(map[string]bool)
	for _, m := range detailCatRe.FindAllSubmatch(body, -1) {
		tag := cleanText(string(m[1]))
		if tag != "" && !seenTag[tag] {
			seenTag[tag] = true
			d.tags = append(d.tags, tag)
		}
	}
	if blk := detailStarringRe.FindSubmatch(body); blk != nil {
		seenPerf := make(map[string]bool)
		for _, m := range detailPerfRe.FindAllSubmatch(blk[1], -1) {
			name := cleanText(string(m[1]))
			if name != "" && !seenPerf[name] {
				seenPerf[name] = true
				d.performers = append(d.performers, name)
			}
		}
	}
	return d
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping updates listing", s.cfg.SiteID)

	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/updates/%s/%d", s.base, s.cfg.SiteID, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if firstPage {
			firstPage = false
			total = maxPageNum(body) * len(items)
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// fetchDetails enriches each listing item from its detail page with a worker
// pool. Order is preserved so Paginate's KnownIDs early-stop fires on the
// right scene; known IDs become lightweight stubs (no detail fetch).
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

			var d detailData
			if body, err := s.fetchPage(ctx, s.base+item.url); err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (using card data)", s.cfg.SiteID, item.id, err)
			} else {
				d = parseDetail(body)
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

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
	}
	thumbnail := it.thumbnail
	if thumbnail == "" {
		thumbnail = d.thumbnail
	}
	performers := it.performers
	if len(d.performers) > 0 {
		performers = d.performers
	}
	return models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       title,
		URL:         s.base + it.url,
		Studio:      s.cfg.StudioName,
		Description: d.description,
		Thumbnail:   thumbnail,
		Date:        d.date,
		Performers:  performers,
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

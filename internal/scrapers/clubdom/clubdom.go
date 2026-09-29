// Package clubdom scrapes the ClubDomCash femdom network — Club Dom
// (clubdom.com) and Subby Hubby (subbyhubby.com) — which run the ElevatedX
// "tour" CMS. It is a table-driven package: one scraper is registered per
// site in init(). The public tour listing is paginated newest-first at
// /tour/categories/movies/{N}/latest/ and each card links to a detail page at
// /tour/trailers/{slug}.html.
package clubdom

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one ElevatedX tour site served by this package.
//
// ListPath defaults to the standard tour template path when empty, so most
// sites only need SiteID/Domain/StudioName. Detail pages always live under
// /tour/trailers/{slug}.html; the slug is the scene ID.
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "clubdom"
	Domain     string // bare domain, e.g. "clubdom.com"
	StudioName string // display name, e.g. "Club Dom"
	ListPath   string // listing path template; "%d" is the page number
}

func (c SiteConfig) listPath() string {
	if c.ListPath != "" {
		return c.ListPath
	}
	return "/tour/categories/movies/%d/latest/"
}

var sites = []SiteConfig{
	{SiteID: "clubdom", Domain: "clubdom.com", StudioName: "Club Dom"},
	{SiteID: "subbyhubby", Domain: "subbyhubby.com", StudioName: "Subby Hubby"},
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

// Scraper implements scraper.StudioScraper for a single ElevatedX tour site.
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
		s.cfg.Domain + "/tour/categories/movies/{n}/latest/",
		s.cfg.Domain + "/tour/trailers/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

var (
	// cardRe isolates each listing card. Cards open with `item grabthis`.
	cardRe = regexp.MustCompile(`(?s)<div class="item grabthis">.*?<!--//item-->`)
	// cardLinkRe pulls the trailer href + slug from a card. Hrefs are
	// protocol-relative (//www.host/tour/trailers/{slug}.html).
	cardLinkRe = regexp.MustCompile(`href="(?://[^/]+)?/tour/trailers/([^"]+)\.html"`)
	// cardTitleRe pulls the title from the card's <h2><a ...>Title</a>.
	cardTitleRe = regexp.MustCompile(`(?s)<span class="item-meta">.*?<h2><a [^>]*>([^<]+)</a>`)
	// cardThumbRe pulls the 2x content thumbnail.
	cardThumbRe = regexp.MustCompile(`src0_2x="([^"]+)"`)
	cardDurRe   = regexp.MustCompile(`<span class="duration">[^<]*<i[^>]*></i>\s*([0-9:]+)`)
	cardDateRe  = regexp.MustCompile(`<span class="date">[^<]*<i[^>]*></i>\s*(\d{4}-\d{2}-\d{2})`)

	// pageNumRe finds pagination page numbers for a total estimate.
	pageNumRe = regexp.MustCompile(`categories/movies/(\d+)/latest/`)

	// detailRuntimeRe / detailDateRe parse the trailer-info <h5> line.
	detailRuntimeRe = regexp.MustCompile(`Runtime:</strong>\s*([0-9:]+)`)
	detailDateRe    = regexp.MustCompile(`Release date:</strong>\s*(\d{2}/\d{2}/\d{4})`)
	// detailTagRe pulls "Tagged with" category links scoped to the
	// categories <span>.
	detailTagBlockRe = regexp.MustCompile(`(?s)<span class="categories">.*?</span>`)
	detailTagRe      = regexp.MustCompile(`<a [^>]*href="[^"]*/tour/categories/[^"]+">([^<]+)</a>`)
)

type listItem struct {
	id        string
	url       string
	title     string
	thumbnail string
	duration  int
	date      time.Time
}

type detailData struct {
	duration int
	date     time.Time
	tags     []string
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
	slug := string(m[1])
	it := listItem{
		id:  slug,
		url: "/tour/trailers/" + slug + ".html",
	}
	if mt := cardTitleRe.FindSubmatch(card); mt != nil {
		it.title = cleanText(string(mt[1]))
	}
	if mTh := cardThumbRe.FindSubmatch(card); mTh != nil {
		it.thumbnail = normalizeURL(string(mTh[1]))
	}
	if md := cardDurRe.FindSubmatch(card); md != nil {
		it.duration = parseutil.ParseDurationColon(string(md[1]))
	}
	if md := cardDateRe.FindSubmatch(card); md != nil {
		if t, err := time.Parse("2006-01-02", string(md[1])); err == nil {
			it.date = t.UTC()
		}
	}
	return it, true
}

func parseDetail(body []byte) detailData {
	var d detailData
	if m := detailRuntimeRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}
	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("01/02/2006", string(m[1])); err == nil {
			d.date = t.UTC()
		}
	}
	if blk := detailTagBlockRe.Find(body); blk != nil {
		seen := make(map[string]bool)
		for _, m := range detailTagRe.FindAllSubmatch(blk, -1) {
			tag := cleanText(string(m[1]))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				d.tags = append(d.tags, tag)
			}
		}
	}
	return d
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping tour listing", s.cfg.SiteID)

	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + fmt.Sprintf(s.cfg.listPath(), page)
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
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > maxPage {
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
	duration := it.duration
	if d.duration > 0 {
		duration = d.duration
	}
	date := it.date
	if !d.date.IsZero() {
		date = d.date
	}
	return models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: s.base,
		Title:     it.title,
		URL:       s.base + it.url,
		Studio:    s.cfg.StudioName,
		Thumbnail: it.thumbnail,
		Date:      date,
		Duration:  duration,
		Tags:      d.tags,
		ScrapedAt: now,
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

// normalizeURL upgrades a protocol-relative URL (//host/path) to https.
func normalizeURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

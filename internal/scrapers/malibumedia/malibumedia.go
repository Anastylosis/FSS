// Package malibumedia scrapes the Malibu Media network — X-Art (x-art.com)
// and Colette (colettevideos.com) — which run the same in-house "X-Art" CMS
// (Colette's age-gate even sets an xartsess cookie). It is a table-driven
// package: one scraper is registered per site in init().
//
// Both sites expose a public, server-rendered tour with real scene metadata.
// They share a detail-page layout (an <h1> title, an og:description meta, an
// <h2> release date, and "featuring" model links) but use two different
// listing templates:
//
//   - X-Art: a modern Bootstrap grid at /videos/, paginated newest-first at
//     /videos/all/{N}. Cards link to /videos/{slug}.
//   - Colette: an older Foundation list at /updates/ (single page). Cards link
//     to /videos/{slug}/. Content is gated behind a one-shot age check, so the
//     scraper sends a static "Cookie: _warning=true" header on every request.
//
// Each listing card carries only the slug (the scene ID) and title; the rest
// of the metadata (date, performers, description, thumbnail) is enriched from
// the scene's detail page with a worker pool.
package malibumedia

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

// template selects the listing layout / pagination model for a site.
type template int

const (
	// templateXArt is the modern Bootstrap grid with /videos/all/{N} pages.
	templateXArt template = iota
	// templateColette is the older Foundation single-page /updates/ list.
	templateColette
)

// SiteConfig describes one Malibu Media site served by this package.
type SiteConfig struct {
	SiteID     string   // stable lowercase id, e.g. "x-art"
	Domain     string   // bare domain, e.g. "x-art.com"
	StudioName string   // display name, e.g. "X-Art"
	Template   template // listing template / pagination model
	ListPath   string   // listing path; for X-Art "%d" is the page number
	Cookie     string   // static cookie header (age gate), empty if none
}

var sites = []SiteConfig{
	{
		SiteID:     "x-art",
		Domain:     "x-art.com",
		StudioName: "X-Art",
		Template:   templateXArt,
		ListPath:   "/videos/all/%d",
	},
	{
		SiteID:     "colettevideos",
		Domain:     "colettevideos.com",
		StudioName: "Colette",
		Template:   templateColette,
		ListPath:   "/updates/",
		Cookie:     "_warning=true",
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newFor(cfg.SiteID))
	}
}

// newFor builds the registered scraper for a given site id. Also used by tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for a single Malibu Media site.
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
	switch s.cfg.Template {
	case templateColette:
		return []string{
			s.cfg.Domain,
			s.cfg.Domain + "/updates/",
			s.cfg.Domain + "/videos/{slug}/",
		}
	default:
		return []string{
			s.cfg.Domain,
			s.cfg.Domain + "/videos/",
			s.cfg.Domain + "/videos/all/{n}",
			s.cfg.Domain + "/videos/{slug}",
		}
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

var (
	// xartCardRe pulls the slug (scene ID) and title from each Bootstrap
	// listing card. The /videos/ link has no trailing slash on X-Art.
	xartCardRe = regexp.MustCompile(`(?s)<a href="https?://[^/]+/videos/([a-z0-9+._-]+)">\s*<div class="item d-flex[^>]*>\s*<div class="item-img">\s*<img src="([^"]+)" alt="([^"]*)"`)
	// xartMaxPageRe finds /videos/all/{N} pagination links for a total estimate.
	xartMaxPageRe = regexp.MustCompile(`/videos/all/(\d+)`)

	// coletteCardRe pulls the slug (scene ID) from each Foundation list card.
	// The /videos/ link has a trailing slash on Colette.
	coletteCardRe = regexp.MustCompile(`<a href="https?://[^/]+/videos/([a-z0-9+._-]+)/">`)

	// Detail-page parsing (shared by both sites).
	detailTitleRe = regexp.MustCompile(`(?s)<h1>([^<]+)</h1>`)
	detailOGDesc  = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
	// detailPDescRe is the fallback description: the first <p>…</p> inside the
	// scene "info" column (Colette has no og:description meta). Inner tags are
	// stripped by stripTags.
	detailPDescRe = regexp.MustCompile(`(?s)class="[^"]*columns info"[^>]*>\s*<p>(.*?)</p>`)
	// detailDateRe matches the standalone "<h2>Mon DD, YYYY </h2>" line.
	detailDateRe = regexp.MustCompile(`<h2>\s*([A-Z][a-z]{2} \d{1,2}, \d{4})\s*</h2>`)
	// detailFeaturingRe isolates the "featuring" <h2> block holding model links.
	detailFeaturingRe = regexp.MustCompile(`(?s)<span>featuring</span>(.*?)</h2>`)
	detailModelRe     = regexp.MustCompile(`<a [^>]*href=['"][^'"]*/models/[^'"]+['"][^>]*>([^<]+)</a>`)
	// detailThumbRe pulls the large scene preview image from the detail page.
	detailThumbRe = regexp.MustCompile(`<img[^>]+src="(https?://[^"]+/videos/[^"]+\.jpg)(?:\?[^"]*)?"`)
	// tagStripRe / wsRe clean inner HTML out of the fallback description.
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

type listItem struct {
	id        string
	title     string
	thumbnail string
}

type detailData struct {
	title       string
	description string
	thumbnail   string
	date        time.Time
	performers  []string
}

// parseListing returns the listing cards for the site's template.
func (s *Scraper) parseListing(body []byte) []listItem {
	seen := make(map[string]bool)
	var items []listItem

	add := func(id, title, thumb string) {
		id = strings.TrimSuffix(id, "/")
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		items = append(items, listItem{
			id:        id,
			title:     cleanText(title),
			thumbnail: thumb,
		})
	}

	switch s.cfg.Template {
	case templateColette:
		for _, m := range coletteCardRe.FindAllSubmatch(body, -1) {
			add(string(m[1]), "", "")
		}
	default:
		for _, m := range xartCardRe.FindAllSubmatch(body, -1) {
			add(string(m[1]), string(m[3]), string(m[2]))
		}
	}
	return items
}

func parseDetail(body []byte) detailData {
	var d detailData

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = cleanText(string(m[1]))
	}
	if m := detailOGDesc.FindSubmatch(body); m != nil && strings.TrimSpace(string(m[1])) != "" {
		d.description = cleanText(string(m[1]))
	} else if m := detailPDescRe.FindSubmatch(body); m != nil {
		d.description = stripTags(string(m[1]))
	}
	if m := detailThumbRe.FindSubmatch(body); m != nil {
		d.thumbnail = string(m[1])
	}
	if m := detailDateRe.FindSubmatch(body); m != nil {
		// Both sites print "Jun 18, 2026" style dates.
		if t, err := parseutil.TryParseDate(string(m[1]), "Jan 2, 2006"); err == nil {
			d.date = t.UTC()
		}
	}
	if blk := detailFeaturingRe.FindSubmatch(body); blk != nil {
		seen := make(map[string]bool)
		for _, m := range detailModelRe.FindAllSubmatch(blk[1], -1) {
			name := cleanText(string(m[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				d.performers = append(d.performers, name)
			}
		}
	}
	return d
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	if s.cfg.Template == templateColette {
		scraper.Debugf(1, "%s: scraping single-page listing", s.cfg.SiteID)
		s.runSinglePage(ctx, opts, out, now)
		return
	}

	scraper.Debugf(1, "%s: scraping paginated listing", s.cfg.SiteID)
	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + s.cfg.ListPath
		if strings.Contains(s.cfg.ListPath, "%d") {
			pageURL = s.base + fmt.Sprintf(s.cfg.ListPath, page)
		} else if page > 1 {
			// non-templated path: only one page exists.
			return scraper.PageResult{}, nil
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
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

// runSinglePage scrapes a one-page listing (Colette /updates/), respecting
// KnownIDs early-stop and ctx cancellation.
func (s *Scraper) runSinglePage(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	body, err := s.fetchPage(ctx, s.base+s.cfg.ListPath)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := s.parseListing(body)
	if len(items) == 0 {
		return
	}

	scraper.Debugf(1, "%s: found %d scenes on listing", s.cfg.SiteID, len(items))
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	scenes := s.fetchDetails(ctx, items, opts, now)
	for _, sc := range scenes {
		if opts.KnownIDs[sc.ID] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(sc):
		case <-ctx.Done():
			return
		}
	}
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range xartMaxPageRe.FindAllSubmatch(body, -1) {
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
// pool. Order is preserved so KnownIDs early-stop fires on the right scene;
// known IDs become lightweight stubs (no detail fetch).
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
			if body, err := s.fetchPage(ctx, s.sceneURL(item.id)); err != nil {
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

// sceneURL builds the detail-page URL for a scene id. Colette uses a trailing
// slash, X-Art does not.
func (s *Scraper) sceneURL(id string) string {
	if s.cfg.Template == templateColette {
		return s.base + "/videos/" + id + "/"
	}
	return s.base + "/videos/" + id
}

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
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
		URL:         s.sceneURL(it.id),
		Studio:      s.cfg.StudioName,
		Description: d.description,
		Thumbnail:   thumb,
		Date:        d.date,
		Performers:  d.performers,
		ScrapedAt:   now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	if s.cfg.Cookie != "" {
		headers["Cookie"] = s.cfg.Cookie
	}
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: headers,
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

// stripTags removes inner HTML tags and collapses whitespace, then unescapes
// entities. Used for the fallback description block.
func stripTags(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = wsRe.ReplaceAllString(s, " ")
	return cleanText(s)
}

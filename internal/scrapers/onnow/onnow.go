// Package onnow scrapes the Brazilian "OnNow" membership CMS (onvicdn.com
// assets, `/videos/{page}` listings, `/video/{id}` teaser pages). See
// docs/scrapers.md.
package onnow

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
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

const detailWorkers = 4

// SiteConfig describes one OnNow site. The skins differ only in listing card
// markup, so a row carries the regexes that read it.
type SiteConfig struct {
	ID       string
	BaseURL  string // no trailing slash
	SiteName string
	Patterns []string
	MatchRe  *regexp.Regexp

	// CardRe marks the start of each listing card and captures the scene id
	// as group 1. A card's block runs to the next mark.
	CardRe *regexp.Regexp
	// TitleRe and DurationRe capture group 1 from within a card's block.
	TitleRe    *regexp.Regexp
	DurationRe *regexp.Regexp
	// DetailDurationRe captures the runtime on the detail page, for scenes
	// the listing never showed.
	DetailDurationRe *regexp.Regexp
	// SitemapIDRe captures scene ids from /sitemap.xml. Set it where the
	// sitemap lists scenes the public listings omit.
	SitemapIDRe *regexp.Regexp
	// NewestFirst enables the KnownIDs early stop.
	NewestFirst bool
}

// Scraper implements scraper.StudioScraper for one OnNow site.
type Scraper struct {
	Client *http.Client
	cfg    SiteConfig
}

// New builds the scraper for cfg.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(45 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }

func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	categoryRe = regexp.MustCompile(`^/videos/([^/?#]+)`)
	pageNumRe  = regexp.MustCompile(`^\d+$`)
	durationRe = regexp.MustCompile(`(?:(\d+)h)?\s*(?:(\d+)m)?\s*(?:(\d+)s)?`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
	pagerRe    = regexp.MustCompile(`href='[^']*/videos/(?:[^/']+/)?(\d+)'\s+class="page-link"`)
)

// category extracts the category slug from an operator URL, or "" for the
// whole catalogue. A bare page number (`/videos/5`) is the catalogue too.
func category(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	m := categoryRe.FindStringSubmatch(u.Path)
	if m == nil || pageNumRe.MatchString(m[1]) {
		return ""
	}
	return m[1]
}

func (s *Scraper) listingURL(cat string, page int) string {
	path := "/videos"
	if cat != "" {
		path += "/" + cat
	}
	if page > 1 {
		path += "/" + strconv.Itoa(page)
	}
	return s.cfg.BaseURL + path
}

type card struct {
	id       string
	title    string
	duration int
}

func (s *Scraper) parseListing(body string) []card {
	marks := s.cfg.CardRe.FindAllStringSubmatchIndex(body, -1)
	cards := make([]card, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[0]:end]
		c := card{id: body[m[2]:m[3]]}
		if t := s.cfg.TitleRe.FindStringSubmatch(block); t != nil {
			c.title = cleanText(t[1])
		}
		if d := s.cfg.DurationRe.FindStringSubmatch(block); d != nil {
			c.duration = parseDuration(d[1])
		}
		cards = append(cards, c)
	}
	return cards
}

// parseDuration reads the "1h 2m 3s" runtime form.
func parseDuration(s string) int {
	m := durationRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	var total int
	for i, mult := range []int{3600, 60, 1} {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err == nil {
			total += n * mult
		}
	}
	return total
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	cat := category(studioURL)
	scraper.Debugf(1, "%s: category %q", s.cfg.ID, cat)
	now := time.Now().UTC()
	seen := map[string]bool{}
	sitemap := cat == "" && s.cfg.SitemapIDRe != nil

	// Past a listing's last page the CMS serves the unfiltered catalogue
	// again, so the pager's own last page is the only reliable end.
	last := 0
	newScene := func(id, title string, duration int) models.Scene {
		return models.Scene{
			ID:        id,
			SiteID:    s.cfg.ID,
			StudioURL: studioURL,
			Studio:    s.cfg.SiteName,
			Title:     title,
			URL:       s.cfg.BaseURL + "/video/" + id,
			Duration:  duration,
			ScrapedAt: now,
		}
	}

	pagOpts := opts
	if !s.cfg.NewestFirst {
		pagOpts.KnownIDs = nil
	}

	scraper.Paginate(ctx, pagOpts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page == 1 || page <= last {
			pageURL := s.listingURL(cat, page)
			body, err := s.fetch(ctx, pageURL)
			if err != nil {
				return scraper.PageResult{}, err
			}
			cards := s.parseListing(body)
			if page == 1 {
				if len(cards) == 0 {
					return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
				}
				last = lastPage(body)
			}
			var scenes []models.Scene
			for _, c := range cards {
				if !seen[c.id] {
					seen[c.id] = true
					scenes = append(scenes, newScene(c.id, c.title, c.duration))
				}
			}
			s.enrich(ctx, scenes, pagOpts, out)
			return scraper.PageResult{Scenes: scenes, Done: page >= last && !sitemap, Continue: true}, nil
		}

		// The sitemap lists scenes the public listings leave out.
		ids, err := s.sitemapIDs(ctx)
		if err != nil {
			return scraper.PageResult{}, err
		}
		var scenes []models.Scene
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				scenes = append(scenes, newScene(id, "", 0))
			}
		}
		scraper.Debugf(1, "%s: %d scenes only in the sitemap", s.cfg.ID, len(scenes))
		s.enrich(ctx, scenes, pagOpts, out)
		return scraper.PageResult{Scenes: scenes, Done: true, Continue: true}, nil
	})
}

// lastPage reads the highest page number the pager links to; 0 means there is
// no pager, i.e. a single page.
func lastPage(body string) int {
	var last int
	for _, m := range pagerRe.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > last {
			last = n
		}
	}
	return last
}

func (s *Scraper) sitemapIDs(ctx context.Context) ([]string, error) {
	u := s.cfg.BaseURL + "/sitemap.xml"
	body, err := s.fetch(ctx, u)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range s.cfg.SitemapIDRe.FindAllStringSubmatch(body, -1) {
		ids = append(ids, m[1])
	}
	if len(ids) == 0 {
		return nil, scraper.ParseError(u, fmt.Errorf("no scene ids in the sitemap"))
	}
	return ids, nil
}

// enrich fetches the detail pages, which carry the full title, synopsis and
// thumbnail; the listing truncates long titles.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	var todo []int
	for i, sc := range scenes {
		if !opts.KnownIDs[sc.ID] {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(todo), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, i := range todo {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			s.applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
}

func (s *Scraper) applyDetail(sc *models.Scene, body string) {
	og := parseutil.OpenGraph([]byte(body))
	if t := strings.TrimSpace(html.UnescapeString(og["og:title"])); t != "" {
		sc.Title = t
	}
	sc.Thumbnail = html.UnescapeString(og["og:image"])
	sc.Description = cleanLines(html.UnescapeString(og["og:description"]))
	if sc.Duration == 0 && s.cfg.DetailDurationRe != nil {
		if m := s.cfg.DetailDurationRe.FindStringSubmatch(body); m != nil {
			sc.Duration = parseDuration(m[1])
		}
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

// cleanLines trims each line of a multi-paragraph synopsis and drops blanks.
func cleanLines(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

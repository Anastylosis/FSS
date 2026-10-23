// Package maverickmen scrapes the two OLB Media sites hosted on
// vod.maverickmen.com: Maverick Men (the VOD theater, `?page=videos`) and
// Maverick Men Directs (`/m/r/site/Maverick_Directs/`). Both are the same
// CMS behind two templates; each listing is walked newest-first and every
// card's detail page supplies the full title, release date and description.
package maverickmen

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

const detailWorkers = 3

var vodBase = "https://vod.maverickmen.com"

type variant int

const (
	variantVOD variant = iota
	variantDirects
)

type siteConfig struct {
	id       string
	studio   string
	variant  variant
	matchRe  *regexp.Regexp
	patterns []string
}

var sites = []siteConfig{
	{
		id:      "maverickmen",
		studio:  "Maverick Men",
		variant: variantVOD,
		// Only the site root: Directs lives under the same host at /m/r/site/.
		matchRe:  regexp.MustCompile(`(?i)^https?://(?:www\.|vod\.)?maverickmen\.com/?(?:\?.*)?$`),
		patterns: []string{"maverickmen.com", "vod.maverickmen.com/?page=videos"},
	},
	{
		id:      "maverickmendirects",
		studio:  "Maverick Men Directs",
		variant: variantDirects,
		matchRe: regexp.MustCompile(`(?i)^https?://(?:(?:www\.)?maverickmendirects\.com(?:[/?#]|$)|vod\.maverickmen\.com/m/r/site/maverick_directs(?:[/?#]|$))`),
		patterns: []string{
			"maverickmendirects.com",
			"vod.maverickmen.com/m/r/site/Maverick_Directs",
			"vod.maverickmen.com/m/r/site/Maverick_Directs/trailers",
		},
	},
}

// Scraper implements scraper.StudioScraper for one of the two sites.
type Scraper struct {
	cfg    siteConfig
	Client *http.Client
}

// New constructs the scraper for cfg.
func New(cfg siteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	for _, c := range sites {
		scraper.Register(New(c))
	}
}

func (s *Scraper) ID() string               { return s.cfg.id }
func (s *Scraper) Patterns() []string       { return s.cfg.patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.matchRe.MatchString(u) }
func (s *Scraper) isDirects() bool          { return s.cfg.variant == variantDirects }
func (s *Scraper) directsBase() string      { return vodBase + "/m/r/site/Maverick_Directs" }
func (s *Scraper) vodDetailURL(slug string) string {
	return vodBase + "/?page=videos&contentId=" + slug
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

type listItem struct {
	id    string
	url   string
	thumb string
	date  time.Time
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Paginate(ctx, opts, s.cfg.id, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listURL(page)
		body, err := s.get(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		var items []listItem
		var total int
		var hasNext bool
		if s.isDirects() {
			items, hasNext = parseDirectsListing(body, s.directsBase(), page)
		} else {
			items, total, hasNext = parseVODListing(body, s, page)
		}
		scraper.Debugf(1, "%s: page %d -> %d videos", s.cfg.id, page, len(items))
		if len(items) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, errors.New("no video cards found on listing"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, workers, studioURL, items, now, opts, out)
		return scraper.PageResult{Scenes: scenes, Total: total, Done: !hasNext, Continue: true}, nil
	})
}

func (s *Scraper) listURL(page int) string {
	if s.isDirects() {
		return fmt.Sprintf("%s/trailers?p=%d", s.directsBase(), page)
	}
	return fmt.Sprintf("%s/?page=videos&p=%d", vodBase, page)
}

// enrich fetches the detail page of every card not already stored. Known
// cards are returned as stubs so Paginate's early stop still sees them.
func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, opts scraper.ListOpts, out chan<- scraper.SceneResult) []models.Scene {
	scenes := make([]models.Scene, len(items))
	errs := make([]error, len(items))
	var todo []int
	for i, it := range items {
		if opts.KnownIDs[it.id] {
			scenes[i] = models.Scene{ID: it.id}
			continue
		}
		todo = append(todo, i)
	}
	if len(todo) > 0 {
		scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.id, len(todo), workers)
	}
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
			scenes[i], errs[i] = s.fetchDetail(ctx, studioURL, items[i], now)
		}(i)
	}
	wg.Wait()

	res := scenes[:0]
	for i, sc := range scenes {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(errs[i]):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		if sc.ID != "" {
			res = append(res, sc)
		}
	}
	return res
}

// ---- listing parsing ----

var (
	vodCardRe   = regexp.MustCompile(`(?s)<div class="vid-list-thumb">\s*<a href="[^"]*[?&]contentId=([^"&]+)"[^>]*>\s*<img src="([^"]+)"`)
	vodTotalRe  = regexp.MustCompile(`VIDEOS:\s*<span>\s*\d+\s*-\s*\d+\s+of\s+(\d+)`)
	dirCardRe   = regexp.MustCompile(`(?s)<div class="videobox2">.*?<a href="([^"]*/ms/video/([^"/?#]+))"[^>]*>\s*<img src="([^"]+)".*?fa-clock-o"></i>\s*([0-9/]+)`)
	dirNextTmpl = `trailers?p=%d"`
)

func parseVODListing(body []byte, s *Scraper, page int) ([]listItem, int, bool) {
	src := string(body)
	var items []listItem
	seen := map[string]bool{}
	for _, m := range vodCardRe.FindAllStringSubmatch(src, -1) {
		slug := html.UnescapeString(m[1])
		if seen[slug] {
			continue
		}
		seen[slug] = true
		items = append(items, listItem{
			id:    slug,
			url:   s.vodDetailURL(slug),
			thumb: html.UnescapeString(m[2]),
		})
	}
	total := 0
	if m := vodTotalRe.FindStringSubmatch(src); m != nil {
		total, _ = strconv.Atoi(m[1])
	}
	hasNext := strings.Contains(src, fmt.Sprintf("?page=videos&p=%d\">NEXT", page+1)) ||
		strings.Contains(src, fmt.Sprintf("?page=videos&amp;p=%d\">NEXT", page+1))
	return items, total, hasNext
}

func parseDirectsListing(body []byte, base string, page int) ([]listItem, bool) {
	src := string(body)
	var items []listItem
	seen := map[string]bool{}
	for _, m := range dirCardRe.FindAllStringSubmatch(src, -1) {
		slug := m[2]
		if seen[slug] {
			continue
		}
		seen[slug] = true
		it := listItem{
			id:    slug,
			url:   base + "/ms/video/" + slug,
			thumb: html.UnescapeString(m[3]),
		}
		if t, err := parseutil.TryParseDate(m[4], "01/02/06"); err == nil {
			it.date = t.UTC()
		}
		items = append(items, it)
	}
	hasNext := strings.Contains(src, fmt.Sprintf(dirNextTmpl, page+1))
	return items, hasNext
}

// ---- detail parsing ----

var (
	vodTitleRe   = regexp.MustCompile(`<h1 id="view_title">([^<]*)</h1>`)
	vodDateRe    = regexp.MustCompile(`Released:</strong>\s*([0-9/]+)`)
	vodDescRe    = regexp.MustCompile(`(?s)<span id="view_description">(.*?)<div class="comment_title">`)
	dirTitleRe   = regexp.MustCompile(`<h2 class="heading">([^<]*)</h2>`)
	dirDateRe    = regexp.MustCompile(`Released:\s*<small>\s*([0-9/]+)`)
	dirDescRe    = regexp.MustCompile(`(?s)<div class="description-wrapper">\s*<h5>Description</h5>(.*?)</div>`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	spaceRe      = regexp.MustCompile(`\s+`)
	teaserSuffix = regexp.MustCompile(`(?i)\s+teaser\s*$`)
)

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, it listItem, now time.Time) (models.Scene, error) {
	body, err := s.get(ctx, it.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("%s: %w", it.url, err)
	}
	scene := models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.id,
		StudioURL: studioURL,
		URL:       it.url,
		Studio:    s.cfg.studio,
		Thumbnail: it.thumb,
		Date:      it.date,
		ScrapedAt: now,
	}
	if err := s.parseDetail(body, &scene); err != nil {
		return models.Scene{}, scraper.ParseError(it.url, err)
	}
	return scene, nil
}

func (s *Scraper) parseDetail(body []byte, scene *models.Scene) error {
	src := string(body)
	titleRe, dateRe, descRe := vodTitleRe, vodDateRe, vodDescRe
	if s.isDirects() {
		titleRe, dateRe, descRe = dirTitleRe, dirDateRe, dirDescRe
	}
	m := titleRe.FindStringSubmatch(src)
	if m == nil {
		return errors.New("scene title not found")
	}
	title := cleanText(m[1])
	title = teaserSuffix.ReplaceAllString(title, "")
	if title == "" {
		return errors.New("scene title empty")
	}
	scene.Title = title
	if m := dateRe.FindStringSubmatch(src); m != nil {
		if t, err := parseutil.TryParseDate(m[1], "01/02/06"); err == nil {
			scene.Date = t.UTC()
		}
	}
	if m := descRe.FindStringSubmatch(src); m != nil {
		scene.Description = cleanText(m[1])
	}
	return nil
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

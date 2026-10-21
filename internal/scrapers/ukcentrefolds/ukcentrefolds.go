// Package ukcentrefolds registers scrapers for the UK Centrefolds network:
// single-model WordPress sites whose videos are "portfolio" posts. Scene pages
// are members-only (they redirect to a join page), so everything comes from the
// public listings. See docs/scrapers.md for the per-site layouts.
package ukcentrefolds

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type layout int

const (
	// layoutGrid is a paginated portfolio_category grid (one div per post).
	layoutGrid layout = iota
	// layoutBlog is a paginated portfolio-category blog list with post dates.
	layoutBlog
	// layoutHome lists the whole (small) catalogue on the homepage only.
	layoutHome
)

type siteConfig struct {
	id        string
	base      string
	studio    string
	performer string // the site's model; empty when the title names her
	layout    layout
	listPath  string // grid/blog listing path, with trailing slash
	patterns  []string
	matchRe   *regexp.Regexp
}

var sites = []siteConfig{
	{
		id: "staceypoole", base: "https://staceypoole.co.uk", studio: "Stacey Poole", performer: "Stacey Poole",
		layout: layoutGrid, listPath: "/portfolio_category/videos/",
		patterns: []string{"staceypoole.co.uk", "staceypoole.co.uk/portfolio_category/videos"},
		matchRe:  regexp.MustCompile(`^https?://(?:www\.)?staceypoole\.co\.uk(?:/|$)`),
	},
	{
		id: "alicebrookes", base: "https://www.alicebrookes.com", studio: "AliceBrookes.com", performer: "Alice Brookes",
		layout: layoutGrid, listPath: "/portfolio_category/videos/",
		patterns: []string{"alicebrookes.com", "alicebrookes.com/portfolio_category/videos"},
		matchRe:  regexp.MustCompile(`^https?://(?:www\.)?alicebrookes\.com(?:/|$)`),
	},
	{
		id: "melissadebling", base: "https://melissad.co.uk", studio: "Melissa Debling", performer: "Melissa Debling",
		layout: layoutGrid, listPath: "/portfolio_category/videos/",
		patterns: []string{"melissad.co.uk", "melissad.co.uk/portfolio_category/videos"},
		matchRe:  regexp.MustCompile(`^https?://(?:www\.)?melissad\.co\.uk(?:/|$)`),
	},
	{
		id: "glamourvilla", base: "https://www.glamourvilla.com", studio: "The Glamour Villa",
		layout: layoutBlog, listPath: "/portfolio-category/video/",
		patterns: []string{"glamourvilla.com", "glamourvilla.com/portfolio-category/video"},
		matchRe:  regexp.MustCompile(`^https?://(?:www\.)?glamourvilla\.com(?:/|$)`),
	},
	{
		id: "officialsammybraddy", base: "https://www.officialsammybraddy.com", studio: "Official Sammy Braddy", performer: "Sammy Braddy",
		layout:   layoutHome,
		patterns: []string{"officialsammybraddy.com"},
		matchRe:  regexp.MustCompile(`^https?://(?:www\.)?officialsammybraddy\.com(?:/|$)`),
	},
	{
		id: "officialrae", base: "https://www.officialrae.com", studio: "OfficialRae", performer: "Rae",
		layout:   layoutHome,
		patterns: []string{"officialrae.com"},
		matchRe:  regexp.MustCompile(`^https?://(?:www\.)?officialrae\.com(?:/|$)`),
	},
}

type Scraper struct {
	cfg    siteConfig
	client *http.Client
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	return &Scraper{cfg: cfg, client: httpx.NewClient(30 * time.Second)}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string               { return s.cfg.id }
func (s *Scraper) Patterns() []string       { return s.cfg.patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.matchRe.MatchString(u) }
func (s *Scraper) pageURL(page int) string  { return pageURL(s.cfg.base, s.cfg.listPath, page) }
func (s *Scraper) nextMarker(page int) string {
	return s.cfg.listPath + "page/" + strconv.Itoa(page+1) + "/"
}

func pageURL(base, listPath string, page int) string {
	if page <= 1 {
		return base + listPath
	}
	return base + listPath + "page/" + strconv.Itoa(page) + "/"
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	if s.cfg.layout == layoutHome {
		scraper.Debugf(1, "%s: scraping homepage catalogue", s.cfg.id)
		u := s.cfg.base + "/"
		body, err := s.fetch(ctx, u)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}
		items := parseHome(body)
		if len(items) == 0 {
			send(ctx, out, scraper.Error(scraper.ParseError(u, fmt.Errorf("no video posts found on homepage"))))
			return
		}
		if !send(ctx, out, scraper.Progress(len(items))) {
			return
		}
		for _, it := range items {
			if !send(ctx, out, scraper.Scene(s.toScene(studioURL, it, now))) {
				return
			}
		}
		return
	}

	scraper.Paginate(ctx, opts, s.cfg.id, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := s.pageURL(page)
		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		var items []item
		if s.cfg.layout == layoutBlog {
			items = parseBlog(body)
		} else {
			items = parseGrid(body)
		}
		if len(items) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no portfolio posts found"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		scenes := make([]models.Scene, len(items))
		for i, it := range items {
			scenes[i] = s.toScene(studioURL, it, now)
		}
		done := !strings.Contains(string(body), s.nextMarker(page))
		return scraper.PageResult{Scenes: scenes, Done: done}, nil
	})
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type item struct {
	id    string
	url   string
	title string // raw listing title, e.g. "Name – 8:53 Minutes HD Video!"
	thumb string
	date  time.Time
}

var (
	gridSplitRe = regexp.MustCompile(`<div id="portfolio-(\d+)"`)
	gridTitleRe = regexp.MustCompile(`<h4><a href="([^"]+)" rel="bookmark">([^<]+)</a>`)
	imgSrcRe    = regexp.MustCompile(`<img[^>]*?\ssrc="([^"]+)"`)

	blogSplitRe = regexp.MustCompile(`<article id="post-(\d+)"`)
	blogTitleRe = regexp.MustCompile(`<h2><a href="([^"]+)"[^>]*>([^<]+)</a>`)
	blogDateRe  = regexp.MustCompile(`(?s)class="post_date">\s*<span class="post_info_large">(\d{1,2})</span>.*?<span>([A-Za-z]+)</span>\s*<span\s*>(\d{4})</span>`)

	homeTitleRe = regexp.MustCompile(`class="post-title">\s*<a[^>]*?href="([^"]+/portfolio/([^"/]+)/?)"[^>]*>([^<]+)</a>`)
	homeImgRe   = regexp.MustCompile(`<a[^>]*?href="([^"]+/portfolio/[^"/]+/?)"[^>]*>\s*<img[^>]*?\ssrc="([^"]+)"`)

	// The network's upload plugin names images "<unix time>-<md5>.jpg".
	uploadTimeRe = regexp.MustCompile(`/uploads/\d{4}/\d{2}/(\d{10})-[0-9a-f]{32}`)
)

// chunks splits body at every match of re, returning (first group, chunk).
func chunks(body []byte, re *regexp.Regexp) [][2]string {
	s := string(body)
	locs := re.FindAllStringSubmatchIndex(s, -1)
	res := make([][2]string, 0, len(locs))
	for i, loc := range locs {
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		res = append(res, [2]string{s[loc[2]:loc[3]], s[loc[0]:end]})
	}
	return res
}

func parseGrid(body []byte) []item {
	var items []item
	for _, c := range chunks(body, gridSplitRe) {
		m := gridTitleRe.FindStringSubmatch(c[1])
		if m == nil {
			continue
		}
		it := item{id: c[0], url: m[1], title: m[2]}
		if im := imgSrcRe.FindStringSubmatch(c[1]); im != nil {
			it.thumb = im[1]
		}
		it.date = uploadDate(it.thumb)
		items = append(items, it)
	}
	return items
}

func parseBlog(body []byte) []item {
	var items []item
	for _, c := range chunks(body, blogSplitRe) {
		m := blogTitleRe.FindStringSubmatch(c[1])
		if m == nil {
			continue
		}
		it := item{id: c[0], url: m[1], title: m[2]}
		if im := imgSrcRe.FindStringSubmatch(c[1]); im != nil {
			it.thumb = im[1]
		}
		if dm := blogDateRe.FindStringSubmatch(c[1]); dm != nil {
			if t, err := time.Parse("2 January 2006", dm[1]+" "+dm[2]+" "+dm[3]); err == nil {
				it.date = t
			}
		}
		items = append(items, it)
	}
	return items
}

// parseHome reads the homepage catalogue, keeping only posts whose title
// carries a running time — the rest are photo sets.
func parseHome(body []byte) []item {
	thumbs := map[string]string{}
	for _, m := range homeImgRe.FindAllSubmatch(body, -1) {
		u := string(m[1])
		if _, ok := thumbs[u]; !ok {
			thumbs[u] = string(m[2])
		}
	}
	seen := map[string]bool{}
	var items []item
	for _, m := range homeTitleRe.FindAllSubmatch(body, -1) {
		u, slug, title := string(m[1]), string(m[2]), string(m[3])
		if seen[slug] || durationRe.FindStringSubmatch(html.UnescapeString(title)) == nil {
			continue
		}
		seen[slug] = true
		it := item{id: slug, url: u, title: title, thumb: thumbs[u]}
		it.date = uploadDate(it.thumb)
		items = append(items, it)
	}
	return items
}

func uploadDate(thumb string) time.Time {
	m := uploadTimeRe.FindStringSubmatch(thumb)
	if m == nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC().Truncate(24 * time.Hour)
}

var (
	durationRe    = regexp.MustCompile(`(?i)(\d{1,2}:\d{2}(?::\d{2})?)\s*(?:minutes|mins?|full|hd)\b`)
	picturesTail  = regexp.MustCompile(`(?i)\s*\d+\s+(?:pictures|images)\s+and\s*$`)
	trailingSepRe = regexp.MustCompile(`[\s\-–—:]+$`)
)

// splitTitle separates the scene name from the running-time suffix the
// network appends ("Feeling Shadey- 11:50 Minutes HD Video!").
func splitTitle(raw string) (string, int) {
	t := strings.TrimSpace(html.UnescapeString(raw))
	loc := durationRe.FindStringSubmatchIndex(t)
	if loc == nil {
		return t, 0
	}
	dur := parseutil.ParseDurationColon(t[loc[2]:loc[3]])
	name := picturesTail.ReplaceAllString(t[:loc[0]], "")
	name = strings.TrimSpace(trailingSepRe.ReplaceAllString(name, ""))
	if name == "" {
		name = t
	}
	return name, dur
}

func (s *Scraper) toScene(studioURL string, it item, now time.Time) models.Scene {
	title, dur := splitTitle(it.title)
	sc := models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.id,
		StudioURL: studioURL,
		Title:     title,
		URL:       it.url,
		Thumbnail: it.thumb,
		Date:      it.date,
		Duration:  dur,
		Studio:    s.cfg.studio,
		ScrapedAt: now,
	}
	switch {
	case s.cfg.performer != "":
		sc.Performers = []string{s.cfg.performer}
	case s.cfg.layout == layoutBlog:
		// The Glamour Villa titles each set with its model's name.
		sc.Performers = []string{title}
	}
	return sc
}

// Package westcoastproductions scrapes the West Coast Productions catalogue
// (westcoastproductions.com and its Black Massive Cocks mirror), an Adult
// Empire / Ravana "hybrid-core" VOD site. Both hosts serve the same catalogue.
package westcoastproductions

import (
	"context"
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

const (
	siteID        = "westcoastproductions"
	defaultStudio = "West Coast Productions"
	defaultHost   = "westcoastproductions.com"
	detailWorkers = 4
)

// siteHosts maps each served host to its browse path. Tests add their
// httptest host here.
var siteHosts = map[string]string{
	"westcoastproductions.com": "/watch-newest-west-coast-productions-clips-and-scenes.html",
	"blackmassivecocks.com":    "/watch-newest-black-massive-cocks-clips-and-scenes.html",
}

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"westcoastproductions.com/",
		"westcoastproductions.com/watch-newest-west-coast-productions-clips-and-scenes.html?studio={id}",
		"blackmassivecocks.com/",
		"blackmassivecocks.com/watch-newest-black-massive-cocks-clips-and-scenes.html?studio={id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:westcoastproductions|blackmassivecocks)\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type target struct {
	base         string
	browsePath   string
	studioFilter string
}

func resolveTarget(studioURL string) target {
	t := target{base: "https://" + defaultHost, browsePath: siteHosts[defaultHost]}
	u, err := url.Parse(studioURL)
	if err != nil || u.Host == "" {
		return t
	}
	t.studioFilter = u.Query().Get("studio")
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	if path, ok := siteHosts[host]; ok {
		t.browsePath = path
		t.base = u.Scheme + "://" + host
	}
	return t
}

func (t target) browseURL(page int) string {
	v := url.Values{}
	v.Set("page", strconv.Itoa(page))
	if t.studioFilter != "" {
		v.Set("studio", t.studioFilter)
	}
	return t.base + t.browsePath + "?" + v.Encode()
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	tg := resolveTarget(studioURL)
	scraper.Debugf(1, "%s: scraping %s (studio filter=%q)", siteID, tg.base, tg.studioFilter)

	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		items, err := s.fetchListing(ctx, tg, tg.browseURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
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
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

type listItem struct {
	id         string
	url        string
	title      string
	performers []string
	thumb      string
	duration   int
}

var (
	cardSplitRe  = regexp.MustCompile(`<div class="grid-item" id="ascene_\d+">`)
	cardTitleRe  = regexp.MustCompile(`(?s)<h6[^>]*>(.*?)</h6>`)
	cardLinkRe   = regexp.MustCompile(`<a class="scene-title"\s+href="([^"]+)"`)
	cardPerfRe   = regexp.MustCompile(`(?s)<p class="scene-performer-names">(.*?)</p>`)
	cardLenRe    = regexp.MustCompile(`(?s)<p class="scene-length">\s*(\d+)\s*min`)
	cardThumbRe  = regexp.MustCompile(`data-srcset="([^ "]+)`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
	perfSplitRe  = regexp.MustCompile(`\s*(?:&amp;|&|,)\s*`)
	anchorTextRe = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
)

func (s *Scraper) fetchListing(ctx context.Context, tg target, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	blocks := cardSplitRe.Split(string(body), -1)
	items := make([]listItem, 0, len(blocks))
	for _, b := range blocks[1:] {
		if it, ok := parseCard(tg.base, b); ok {
			items = append(items, it)
		}
	}
	return items, nil
}

func parseCard(base, b string) (listItem, bool) {
	link := cardLinkRe.FindStringSubmatch(b)
	if link == nil {
		return listItem{}, false
	}
	href := link[1]
	id, _, _ := strings.Cut(strings.TrimPrefix(href, "/"), "/")
	if id == "" {
		return listItem{}, false
	}
	it := listItem{id: id, url: absURL(base, href)}
	if m := cardTitleRe.FindStringSubmatch(b); m != nil {
		it.title = cleanText(m[1])
	}
	if m := cardThumbRe.FindStringSubmatch(b); m != nil {
		it.thumb = html.UnescapeString(m[1])
	}
	if m := cardPerfRe.FindStringSubmatch(b); m != nil {
		it.performers = parsePerformers(m[1])
	}
	if m := cardLenRe.FindStringSubmatch(b); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			it.duration = n * 60
		}
	}
	return it, true
}

func parsePerformers(block string) []string {
	var raw []string
	if links := anchorTextRe.FindAllStringSubmatch(block, -1); len(links) > 0 {
		for _, m := range links {
			raw = append(raw, m[1])
		}
	} else {
		raw = perfSplitRe.Split(block, -1)
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		name := cleanText(r)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "%s: fetching %d scene details with %d workers", siteID, len(items), workers)
	scenes := make([]models.Scene, len(items))
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
			if !scraper.Pace(ctx, delay) {
				return
			}
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

type detail struct {
	date     time.Time
	studio   string
	series   string
	director string
}

var (
	releasedRe = regexp.MustCompile(`Released:</span>\s*([A-Za-z]{3,9}\.?\s+\d{1,2},?\s+\d{4})`)
	studioRe   = regexp.MustCompile(`(?s)<div class="studio"><span[^>]*>Studio:</span>\s*<span>(.*?)</span>`)
	seriesRe   = regexp.MustCompile(`(?s)<div class="series"><span[^>]*>Series:</span>\s*<a [^>]*>(.*?)</a>`)
	directorRe = regexp.MustCompile(`(?s)<div class="director"><span[^>]*>Director:</span>(.*?)</div>`)
)

func parseDetail(body string) detail {
	var d detail
	if m := releasedRe.FindStringSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(m[1], "Jan 02, 2006", "Jan 2, 2006", "January 2, 2006"); err == nil {
			d.date = t
		}
	}
	if m := studioRe.FindStringSubmatch(body); m != nil {
		d.studio = cleanText(m[1])
	}
	if m := seriesRe.FindStringSubmatch(body); m != nil {
		d.series = cleanText(m[1])
	}
	if m := directorRe.FindStringSubmatch(body); m != nil {
		d.director = cleanText(m[1])
	}
	return d
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Duration:   it.duration,
		Studio:     defaultStudio,
		ScrapedAt:  now,
	}
	body, err := s.get(ctx, it.url)
	if err != nil {
		scraper.Debugf(1, "%s: detail %s: %v", siteID, it.url, err)
		return scene
	}
	d := parseDetail(string(body))
	scene.Date = d.date
	scene.Series = d.series
	scene.Director = d.director
	if d.studio != "" {
		scene.Studio = d.studio
	}
	return scene
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["Cookie"] = "ageConfirmed=true; AgeConfirmed=True"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: headers})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func absURL(base, href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	return base + "/" + strings.TrimPrefix(href, "/")
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

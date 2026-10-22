// Package myboobsparadise scrapes MyBoobsParadise (myboobsparadise.com), the
// Mechbunny tour StashDB lists as "My Boobs" under its old domain myboobs.eu,
// which now redirects there.
//
// Two URL modes:
//
//   - the main listing, /videos/page{N}.html (25 cards a page, newest first,
//     videos only — the /most-recent/ listing interleaves photo galleries);
//   - a model page, /pornstars/{slug}-{id}.html (or /models/…), which renders
//     every one of that model's videos and galleries on one page.
//
// Cards give the title, runtime, list date ("DD MM YYYY") and thumbnail; the
// detail page adds the cast and a full timestamp. The site publishes no tags
// or descriptions.
package myboobsparadise

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
	siteID        = "myboobsparadise"
	studioName    = "My Boobs"
	defaultBase   = "https://myboobsparadise.com"
	detailWorkers = 4
)

// Scraper implements scraper.StudioScraper for MyBoobsParadise.
type Scraper struct {
	client *http.Client
	base   string
}

// New constructs a MyBoobsParadise scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"myboobsparadise.com",
		"myboobsparadise.com/videos/",
		"myboobsparadise.com/pornstars/{slug}-{id}.html",
		"myboobsparadise.com/models/{slug}-{id}.html",
		"myboobs.eu",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.|tour\.)?(?:myboobsparadise\.com|myboobs\.eu)(?:/|$)`)
	modelRe = regexp.MustCompile(`/(?:pornstars|models)/([a-z0-9-]+-\d+\.html)`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping model page %s", siteID, m[1])
		s.runModel(ctx, s.base+"/pornstars/"+m[1], studioURL, opts, out, now)
		return
	}

	scraper.Debugf(1, "%s: scraping /videos/ listing", siteID)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/videos/"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/videos/page%d.html", s.base, page)
		}
		body, err := s.get(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(body)
		if len(cards) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no video cards on first listing page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		total := 0
		if page == 1 {
			total = maxPage(body) * len(cards)
		}
		return scraper.PageResult{
			Scenes: s.enrich(ctx, cards, studioURL, opts, out, now),
			Total:  total,
			Done:   !nextRe.Match(body),
		}, nil
	})
}

func (s *Scraper) runModel(ctx context.Context, pageURL, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	cards := parseCards(body)
	select {
	case out <- scraper.Progress(len(cards)):
	case <-ctx.Done():
		return
	}
	for _, sc := range s.enrich(ctx, cards, studioURL, opts, out, now) {
		select {
		case out <- scraper.Scene(sc):
		case <-ctx.Done():
			return
		}
	}
}

// ---- listing ----

type card struct {
	id         string
	url        string
	title      string
	thumbnail  string
	duration   int
	date       time.Time
	performers []string
}

var (
	cardRe     = regexp.MustCompile(`(?s)<!-- item -->(.*?)<!-- item END -->`)
	videoRe    = regexp.MustCompile(`href="https?://[^"]*?/video/([a-z0-9-]+)-(\d+)\.html"`)
	titleRe    = regexp.MustCompile(`(?s)<span class="title">\s*(.*?)\s*</span>`)
	timeRe     = regexp.MustCompile(`<span class="time">\s*(\d{1,2}:\d{2}(?::\d{2})?)\s*</span>`)
	cardDateRe = regexp.MustCompile(`(?s)s-e-date">.*?<span class="sub-desc">\s*(\d{2} \d{2} \d{4})\s*</span>`)
	imgRe      = regexp.MustCompile(`<img[^>]*\ssrc="([^"]+)"`)
	nextRe     = regexp.MustCompile(`class="next"`)
	pageNumRe  = regexp.MustCompile(`href="page(\d+)\.html"`)
)

// parseCards returns the video cards of a listing or model page; gallery cards
// link to /galleries/ and are skipped.
func parseCards(body []byte) []card {
	var cards []card
	seen := make(map[string]bool)
	for _, m := range cardRe.FindAllSubmatch(body, -1) {
		inner := string(m[1])
		v := videoRe.FindStringSubmatch(inner)
		if v == nil || seen[v[2]] {
			continue
		}
		seen[v[2]] = true
		c := card{id: v[2], url: defaultBase + "/video/" + v[1] + "-" + v[2] + ".html"}
		if t := titleRe.FindStringSubmatch(inner); t != nil {
			c.title = html.UnescapeString(strings.TrimSpace(t[1]))
		}
		if d := timeRe.FindStringSubmatch(inner); d != nil {
			c.duration = parseutil.ParseDurationColon(d[1])
		}
		if d := cardDateRe.FindStringSubmatch(inner); d != nil {
			if t, err := time.Parse("02 01 2006", d[1]); err == nil {
				c.date = t
			}
		}
		if i := imgRe.FindStringSubmatch(inner); i != nil {
			c.thumbnail = fixURL(i[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func maxPage(body []byte) int {
	maxN := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxN {
			maxN = n
		}
	}
	return maxN
}

// fixURL repairs the CMS's "https:////host/path" image URLs.
func fixURL(u string) string {
	u = html.UnescapeString(u)
	if rest, ok := strings.CutPrefix(u, "https:"); ok {
		return "https://" + strings.TrimLeft(rest, "/")
	}
	if strings.HasPrefix(u, "//") {
		return "https://" + strings.TrimLeft(u, "/")
	}
	return u
}

// ---- detail ----

var (
	modelsListRe = regexp.MustCompile(`(?s)<ul class="models-list">(.*?)</ul>`)
	modelNameRe  = regexp.MustCompile(`<a title="([^"]+)" href="[^"]*/(?:pornstars|models)/`)
	detailDateRe = regexp.MustCompile(`i-calendar"></span><span class="sub-label">(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})</span>`)
)

func (s *Scraper) enrich(ctx context.Context, cards []card, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) []models.Scene {
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(cards), workers)

	errs := make([]error, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range cards {
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
			body, err := s.get(ctx, s.base+strings.TrimPrefix(cards[i].url, defaultBase))
			if err != nil {
				errs[i] = err
				return
			}
			parseDetail(body, &cards[i])
		}(i)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(cards))
	for i, c := range cards {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("detail %s: %w", c.url, errs[i])):
			case <-ctx.Done():
				return scenes
			}
		}
		scenes = append(scenes, models.Scene{
			ID:         c.id,
			SiteID:     siteID,
			StudioURL:  studioURL,
			Title:      c.title,
			URL:        c.url,
			Date:       c.date,
			Thumbnail:  c.thumbnail,
			Duration:   c.duration,
			Performers: c.performers,
			Studio:     studioName,
			ScrapedAt:  now,
		})
	}
	return scenes
}

func parseDetail(body []byte, c *card) {
	if m := modelsListRe.FindSubmatch(body); m != nil {
		for _, n := range modelNameRe.FindAllSubmatch(m[1], -1) {
			if name := html.UnescapeString(strings.TrimSpace(string(n[1]))); name != "" {
				c.performers = append(c.performers, name)
			}
		}
	}
	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("2006-01-02 15:04:05", string(m[1])); err == nil {
			c.date = t.UTC()
		}
	}
	og := parseutil.OpenGraph(body)
	if c.title == "" {
		c.title = html.UnescapeString(og["og:title"])
	}
	if c.thumbnail == "" && og["og:image"] != "" {
		c.thumbnail = fixURL(og["og:image"])
	}
}

func (s *Scraper) get(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

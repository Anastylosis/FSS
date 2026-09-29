// Package hookuphotshot scrapes Hookup Hotshot (hookuphotshot.com), a NATS tour
// site. The listing lives at /categories/movies/{N}/latest/ and carries the
// per-scene title, publish date, thumbnail and content id. The performers are
// only on the per-scene trailer page (/trailers/{slug}.html), so a detail
// worker pool fetches each page for the /models/ links.
package hookuphotshot

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
	siteID        = "hookuphotshot"
	studio        = "Hookup Hotshot"
	detailWorkers = 4
)

// baseURL is a var so tests can point it at an httptest server.
var baseURL = "https://hookuphotshot.com"

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?hookuphotshot\.com(?:/|$)`)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"hookuphotshot.com",
		"hookuphotshot.com/categories/movies/{N}/latest/",
	}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="item-video`)
	trailerRe   = regexp.MustCompile(`<a\s+href="([^"]*/trailers/[^"]+\.html)"[^>]*title="([^"]*)"`)
	thumbRe     = regexp.MustCompile(`src0_1x="([^"]+)"`)
	contentIDRe = regexp.MustCompile(`/contentthumbs/\d+/\d+/(\d+)`)
	dateRe      = regexp.MustCompile(`class="date">\s*(\d{4}-\d{2}-\d{2})`)
	modelRe     = regexp.MustCompile(`/models/[A-Za-z0-9_-]+\.html"[^>]*>([^<]+)<`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/movies/%d/latest/", baseURL, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseListing(body, now)
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

func parseListing(body []byte, _ time.Time) []listItem {
	parts := cardSplitRe.Split(string(body), -1)
	if len(parts) <= 1 {
		return nil
	}
	items := make([]listItem, 0, len(parts)-1)
	for _, card := range parts[1:] {
		m := trailerRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			url:   normalizeURL(m[1]),
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = normalizeURL(th[1])
			if id := contentIDRe.FindStringSubmatch(th[1]); id != nil {
				it.id = id[1]
			}
		}
		if it.id == "" {
			it.id = slugFromURL(it.url)
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if ts, err := time.Parse("2006-01-02", d[1]); err == nil {
				it.date = ts.UTC()
			}
		}
		items = append(items, it)
	}
	return items
}

func normalizeURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	if strings.HasPrefix(u, "/") {
		return baseURL + u
	}
	return u
}

func slugFromURL(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		u = u[i+1:]
	}
	return strings.TrimSuffix(u, ".html")
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
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

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Date:      it.date,
		Thumbnail: it.thumb,
		Studio:    studio,
		ScrapedAt: now,
	}
	if body, err := s.fetchPage(ctx, it.url); err == nil {
		scene.Performers = parsePerformers(body)
	}
	return scene
}

func parsePerformers(body []byte) []string {
	var performers []string
	seen := map[string]bool{}
	for _, m := range modelRe.FindAllStringSubmatch(string(body), -1) {
		name := html.UnescapeString(strings.TrimSpace(m[1]))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		performers = append(performers, name)
	}
	return performers
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: gateHeaders(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// gateHeaders adds the NATS warning-gate acceptance cookie to standard browser
// headers so the content warning interstitial is skipped.
func gateHeaders() map[string]string {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Cookie"] = "warning=accepted"
	return h
}

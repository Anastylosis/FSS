// Package colbyknox scrapes Colby Knox (colbyknox.com), a Symfony-driven gay
// studio. The /videos listing is paginated via an XHR endpoint: requesting
// ?page=N with the X-Requested-With: XMLHttpRequest header returns a JSON
// envelope {"html": "<escaped cards>"}. Each card carries the scene slug,
// title, duration and thumbnail; the detail page adds the synopsis and
// performers. There is no publish date. Pagination walks until a page yields
// no cards.
package colbyknox

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

const (
	siteID        = "colbyknox"
	studioName    = "Colby Knox"
	detailWorkers = 4
)

var siteBase = "https://www.colbyknox.com"

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
		"colbyknox.com/videos",
		"colbyknox.com/videos/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?colbyknox\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// Listing card: the anchor carries the slug; the h3 the title; the
	// icon-clock span the duration; the img alt+src the thumbnail.
	cardRe     = regexp.MustCompile(`(?s)<a href="/videos/([^"]+)" class="card card--video[^"]*">(.*?)</a>`)
	titleRe    = regexp.MustCompile(`<h3 class="h6[^"]*">([^<]+)</h3>`)
	durationRe = regexp.MustCompile(`icon-clock[^>]*></i>\s*<span>([0-9:]+)</span>`)
	thumbRe    = regexp.MustCompile(`(?s)<img[^>]*\bsrc="([^"]+)"`)

	descRe       = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	modelBlockRe = regexp.MustCompile(`(?s)class="video-model[^"]*"[^>]*>.*?<img[^>]*\balt="([^"]+)"`)

	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		items, err := s.fetchListing(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.slug] {
				seen[it.slug] = true
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
	slug      string
	title     string
	duration  int
	thumbnail string
}

func (s *Scraper) fetchListing(ctx context.Context, page int) ([]listItem, error) {
	pageURL := fmt.Sprintf("%s/videos?page=%d", siteBase, page)
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["X-Requested-With"] = "XMLHttpRequest"
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: pageURL, Headers: headers})
	if err != nil {
		return nil, err
	}
	var env struct {
		HTML string `json:"html"`
	}
	err = func() error {
		defer func() { _ = resp.Body.Close() }()
		return httpx.DecodeJSON(resp.Body, &env)
	}()
	if err != nil {
		return nil, err
	}
	items := parseCards(env.HTML)
	scraper.Debugf(1, "colbyknox: listing page %d -> %d cards", page, len(items))
	return items, nil
}

func parseCards(htmlBody string) []listItem {
	var items []listItem
	seen := make(map[string]bool)
	for _, m := range cardRe.FindAllStringSubmatch(htmlBody, -1) {
		slug, inner := m[1], m[2]
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		it := listItem{slug: slug}
		if t := titleRe.FindStringSubmatch(inner); t != nil {
			it.title = html.UnescapeString(strings.TrimSpace(t[1]))
		}
		if d := durationRe.FindStringSubmatch(inner); d != nil {
			it.duration = parseutil.ParseDurationColon(d[1])
		}
		if th := thumbRe.FindStringSubmatch(inner); th != nil {
			it.thumbnail = html.UnescapeString(strings.TrimSpace(th[1]))
		}
		items = append(items, it)
	}
	return items
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "colbyknox: fetching %d details with %d workers", len(items), workers)
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
		ID:        it.slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       siteBase + "/videos/" + it.slug,
		Duration:  it.duration,
		Thumbnail: it.thumbnail,
		Studio:    studioName,
		ScrapedAt: now,
	}

	body, err := s.get(ctx, scene.URL)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}

	var performers []string
	seen := make(map[string]bool)
	for _, p := range modelBlockRe.FindAllStringSubmatch(detail, -1) {
		name := html.UnescapeString(strings.TrimSpace(p[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			performers = append(performers, name)
		}
	}
	scene.Performers = performers

	return scene
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

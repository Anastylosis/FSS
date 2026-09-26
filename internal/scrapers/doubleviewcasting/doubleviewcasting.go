// Package doubleviewcasting scrapes Double View Casting (doubleviewcasting.com),
// a PHP studio tour on the "Deluxe Coin" shared CDN. The /scenes/page/{N}
// listing yields each scene's numeric id, title, date and thumbnail; the
// /scene/id/{N} detail page adds the description, performers, runtime and tags.
// A detail worker pool enriches the listing items. Pagination stops when a page
// yields no scenes.
package doubleviewcasting

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
	siteID        = "doubleviewcasting"
	studioName    = "Double View Casting"
	detailWorkers = 4
)

var siteBase = "http://doubleviewcasting.com"

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
		"doubleviewcasting.com",
		"doubleviewcasting.com/scenes",
		"doubleviewcasting.com/scene/id/{id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?doubleviewcasting\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// Listing card: <a class="thumb" href="/scene/id/353" title="...">
	//   <span class="title">Title <span class="date">Added: February 28, 2014</span></span>
	//   <img src="/contents/scenes/353/thumbnails/295x241.jpg" ... />
	cardRe  = regexp.MustCompile(`(?s)<a class="thumb" href="/scene/id/(\d+)" title="([^"]*)">(.*?)</a>`)
	dateRe  = regexp.MustCompile(`class="date">\s*Added:\s*([^<]+?)\s*</span>`)
	imgRe   = regexp.MustCompile(`<img src="([^"]+)"`)
	descRe  = regexp.MustCompile(`(?s)<div class="info-description">\s*<p>(.*?)</p>`)
	durRe   = regexp.MustCompile(`Duration:</span>\s*([0-9:]+)`)
	girlsRe = regexp.MustCompile(`(?s)<li class="models"><span>[^<]*</span>(.*?)</li>`)
	tagsRe  = regexp.MustCompile(`(?s)<li class="tags"><span>[^<]*</span>(.*?)</li>`)
	linkRe  = regexp.MustCompile(`<a [^>]*>([^<]+)</a>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/scenes/page/%d", siteBase, page)
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, items, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

type listItem struct {
	id, url, title, date, thumb string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	matches := cardRe.FindAllStringSubmatch(string(body), -1)
	items := make([]listItem, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{
			id:    id,
			url:   siteBase + "/scene/id/" + id,
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		}
		if d := dateRe.FindStringSubmatch(m[3]); d != nil {
			it.date = strings.TrimSpace(d[1])
		}
		if img := imgRe.FindStringSubmatch(m[3]); img != nil {
			it.thumb = absURL(img[1])
		}
		items = append(items, it)
	}
	scraper.Debugf(1, "doubleviewcasting: listing %s -> %d cards", pageURL, len(items))
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "doubleviewcasting: fetching %d details with %d workers", len(items), workers)
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
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
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
		Thumbnail: it.thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if it.date != "" {
		if d, err := parseutil.TryParseDate(it.date, "January 2, 2006"); err == nil {
			scene.Date = d
		}
	}

	body, err := s.get(ctx, it.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := durRe.FindStringSubmatch(detail); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}
	if m := girlsRe.FindStringSubmatch(detail); m != nil {
		for _, link := range linkRe.FindAllStringSubmatch(m[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(link[1]))
			if name != "" {
				scene.Performers = append(scene.Performers, name)
			}
		}
	}
	if m := tagsRe.FindStringSubmatch(detail); m != nil {
		for _, link := range linkRe.FindAllStringSubmatch(m[1], -1) {
			tag := html.UnescapeString(strings.TrimSpace(link[1]))
			if tag != "" {
				scene.Tags = append(scene.Tags, tag)
			}
		}
	}
	return scene
}

func absURL(u string) string {
	if strings.HasPrefix(u, "http") {
		return u
	}
	return siteBase + "/" + strings.TrimPrefix(u, "/")
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

var tagStripRe = regexp.MustCompile(`<[^>]+>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

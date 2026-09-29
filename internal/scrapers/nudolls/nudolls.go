// Package nudolls scrapes NuDolls (nudolls.com), a glamour/erotica video site.
// The videos.html listing (paginated via ?page=N) enumerates the per-scene
// "-video-{id}.html" pages; each detail page yields the title, the model from
// the breadcrumb and a cover thumbnail. The tour pages expose no reliable
// publish date, so Date is left zero. Listing pages past the end return no
// video links, so pagination stops on an empty page.
package nudolls

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
	siteID        = "nudolls"
	studioName    = "NuDolls"
	detailWorkers = 4
)

var siteBase = "https://nudolls.com"

// Scraper implements scraper.StudioScraper for NuDolls.
type Scraper struct {
	Client *http.Client
}

// New constructs a NuDolls scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"nudolls.com/videos.html",
		"nudolls.com/{model}-{slug}-video-{id}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?nudolls\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

// videoLinkRe matches a scene link "/something-video-{id}.html" (videos only).
var videoLinkRe = regexp.MustCompile(`href="(/[A-Za-z0-9-]*-video-(\d+)\.html)"`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/videos.html?page=%d", siteBase, page)
		items, err := s.fetchListing(ctx, pageURL)
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
	id  string
	url string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	var items []listItem
	seen := make(map[string]bool)
	for _, m := range videoLinkRe.FindAllStringSubmatch(string(body), -1) {
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, listItem{id: id, url: siteBase + m[1]})
	}
	scraper.Debugf(1, "nudolls: listing %s -> %d videos", pageURL, len(items))
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "nudolls: fetching %d details with %d workers", len(items), workers)
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

// ---- detail parsing ----

var (
	h1Re       = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	titleTagRe = regexp.MustCompile(`<title>([^<]*)</title>`)
	modelRe    = regexp.MustCompile(`/[A-Za-z0-9-]+-model-\d+\.html"[^>]*>\s*([^<]+?)\s*</a>`)
	coverRe    = regexp.MustCompile(`class="cover"><a[^>]*><img[^>]+src="([^"]+)"`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       it.url,
		Studio:    studioName,
		ScrapedAt: now,
	}

	body, err := s.get(ctx, it.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := h1Re.FindStringSubmatch(detail); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(m[1]))
	} else if m := titleTagRe.FindStringSubmatch(detail); m != nil {
		// "ND — Video — Honeyed Dreams" -> "Honeyed Dreams"
		t := m[1]
		if i := strings.LastIndex(t, "—"); i >= 0 {
			t = t[i+len("—"):]
		}
		scene.Title = html.UnescapeString(strings.TrimSpace(t))
	}

	if m := modelRe.FindStringSubmatch(detail); m != nil {
		name := html.UnescapeString(strings.TrimSpace(m[1]))
		if name != "" {
			scene.Performers = []string{name}
		}
	}

	if m := coverRe.FindStringSubmatch(detail); m != nil {
		thumb := html.UnescapeString(strings.TrimSpace(m[1]))
		if strings.HasPrefix(thumb, "/") {
			thumb = siteBase + thumb
		}
		scene.Thumbnail = thumb
	}

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

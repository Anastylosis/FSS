// Package strokiesutil scrapes the Mack Kensington network (Strokies,
// TugCasting, Public Handjobs) — modern ElevatedX "v-thumb" tour sites.
//
// Listing: {SiteBase}/ then {SiteBase}/page{N}/. Each card links a
// /video/{slug}/ detail page and carries a tour_pics thumbnail whose path
// embeds the numeric scene id (assets/tour_pics/{id}-{model_slug}/1.jpg).
// There is no publish date on these sites, so the scraper does a full
// traversal, stopping when a page yields no cards. The detail page supplies
// the title, description, performers and tags via a worker pool.
package strokiesutil

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

const detailWorkers = 4

type SiteConfig struct {
	ID       string
	Studio   string
	SiteBase string // e.g. "https://strokies.com" — no trailing slash
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string               { return s.cfg.ID }
func (s *Scraper) Patterns() []string       { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// cardRe matches a thumbnail anchor pointing at a /video/{slug}/ page
	// followed immediately by the tour image whose path holds the id.
	// It covers both the modern "v-thumb" theme (strokies, tugcasting) and
	// the older "video-card" theme (publichandjobs). tugcasting/publichandjobs
	// still serve the asset under /tour_pics/{id}-…, but strokies.com dropped
	// the "_pics" suffix (now /tour/{id}-…) — match both.
	cardRe  = regexp.MustCompile(`(?s)<a[^>]+href="([^"]*/video/[^"]+)"[^>]*>\s*<img[^>]+src="([^"]*tour(?:_pics)?/(\d+)-[^"]*)"`)
	titleRe = regexp.MustCompile(`<h1 class="video-title">([^<]*)</h1>`)
	descRe  = regexp.MustCompile(`(?s)<div class="video-description"[^>]*>(.*?)</div>`)
	perfRe  = regexp.MustCompile(`<a href="/model/[^"]+/">([^<]+)</a>`)
	tagRe   = regexp.MustCompile(`<a href="/search/[^"]+/tag">([^<]+)</a>`)

	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.cfg.SiteBase + "/"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/page%d/", s.cfg.SiteBase, page)
		}
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		// A page beyond the last yields no cards; an empty slice stops Paginate.
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
	id, url, thumb string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	matches := cardRe.FindAllStringSubmatch(string(body), -1)
	items := make([]listItem, 0, len(matches))
	for _, m := range matches {
		items = append(items, listItem{url: s.absURL(m[1]), thumb: s.absURL(m[2]), id: m[3]})
	}
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(items), workers)
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
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		URL:       it.url,
		Thumbnail: it.thumb,
		Studio:    s.cfg.Studio,
		ScrapedAt: now,
	}
	if body, err := s.get(ctx, it.url); err == nil {
		detail := string(body)
		if m := titleRe.FindStringSubmatch(detail); m != nil {
			scene.Title = cleanText(m[1])
		}
		if m := descRe.FindStringSubmatch(detail); m != nil {
			scene.Description = cleanText(m[1])
		}
		scene.Performers = uniqueText(perfRe.FindAllStringSubmatch(detail, -1))
		scene.Tags = uniqueText(tagRe.FindAllStringSubmatch(detail, -1))
	}
	return scene
}

// uniqueText collects de-duplicated, cleaned capture-group-1 values.
func uniqueText(matches [][]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		v := cleanText(m[1])
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// absURL turns a protocol-relative (//host/path) or root-relative (/path)
// URL into an absolute https URL. Protocol-relative URLs carry their own host
// (the CDN); root-relative URLs resolve against the site base. Already-absolute
// URLs are returned as-is.
func (s *Scraper) absURL(u string) string {
	switch {
	case strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	default:
		return s.cfg.SiteBase + "/" + strings.TrimPrefix(u, "/")
	}
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

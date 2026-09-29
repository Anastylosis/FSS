// Package titanmediautil scrapes the Titan Media "gloryhole" network
// (Gloryhole Swallow, CumClinic, Cumpsters, SpyTug). All four run the same
// Elevated X tour: an updates/page_{N}.html listing of date-keyed trailer
// pages. The listing yields the scene id, date and thumbnail; the detail page
// adds runtime, description, tags and the @handle performers. There is no real
// scene title — the title IS the publish date.
//
// The old categories/Movies_{N}_d.html listing now 500s on all four sites;
// updates/page_{N}.html serves the same card markup.
package titanmediautil

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

const detailWorkers = 4

type SiteConfig struct {
	ID       string
	Studio   string
	SiteBase string // e.g. "https://gloryholeswallow.com/tour" or "https://cumpsters.com"
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
	cardRe     = regexp.MustCompile(`(?s)data-videoid="bVid(\d+)"\s+data-videoposter="([^"]+)".*?<a href="([^"]+)" title="([^"]+)"`)
	runtimeRe  = regexp.MustCompile(`Runtime:\s*<span>\s*([0-9:]+)\s*</span>`)
	descRe     = regexp.MustCompile(`(?s)<div class="content">\s*<p>(.*?)</p>`)
	tagRe      = regexp.MustCompile(`categories/([A-Za-z0-9_]+)_1_d\.html`)
	handleRe   = regexp.MustCompile(`@([A-Za-z0-9_]+)`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/updates/page_%d.html", s.cfg.SiteBase, page)
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		// Pages past the end repeat the last page — stop when nothing is new.
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
	id, poster, url, date string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	matches := cardRe.FindAllStringSubmatch(string(body), -1)
	items := make([]listItem, 0, len(matches))
	for _, m := range matches {
		items = append(items, listItem{id: m[1], poster: m[2], url: m[3], date: m[4]})
	}
	return items, nil
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
	// Drop any scenes left zero-valued by a cancelled context.
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	poster := it.poster
	if !strings.HasPrefix(poster, "http") {
		poster = s.siteRoot() + "/" + strings.TrimPrefix(poster, "/")
	}
	scene := models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		Title:     html.UnescapeString(strings.TrimSpace(it.date)),
		URL:       it.url,
		Thumbnail: poster,
		Studio:    s.cfg.Studio,
		ScrapedAt: now,
	}
	if d, err := parseutil.TryParseDate(strings.TrimSpace(it.date), "Jan. 2, 2006", "Jan 2, 2006"); err == nil {
		scene.Date = d
	}

	if body, err := s.get(ctx, it.url); err == nil {
		detail := string(body)
		if m := runtimeRe.FindStringSubmatch(detail); m != nil {
			scene.Duration = parseutil.ParseDurationColon(m[1])
		}
		if m := descRe.FindStringSubmatch(detail); m != nil {
			scene.Description = cleanText(m[1])
			seen := map[string]bool{}
			for _, h := range handleRe.FindAllStringSubmatch(m[1], -1) {
				if !seen[h[1]] {
					seen[h[1]] = true
					scene.Performers = append(scene.Performers, h[1])
				}
			}
		}
		var tags []string
		tseen := map[string]bool{}
		for _, t := range tagRe.FindAllStringSubmatch(detail, -1) {
			if !tseen[t[1]] {
				tseen[t[1]] = true
				tags = append(tags, t[1])
			}
		}
		scene.Tags = tags
	}
	return scene
}

// siteRoot strips a trailing /tour so root-absolute poster paths (which already
// include /tour) resolve correctly.
func (s *Scraper) siteRoot() string {
	return strings.TrimSuffix(s.cfg.SiteBase, "/tour")
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

// Package cockyboys scrapes CockyBoys (cockyboys.com), an Elevated X gay
// studio tour. The movies_{N}_d.html listing yields the scene URL and title;
// the detail page adds the release date, models, tags and a content thumbnail.
// Listing pages past the end repeat the last page, so pagination stops when a
// page yields no new scene slugs.
package cockyboys

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
	siteID        = "cockyboys"
	studioName    = "CockyBoys"
	detailWorkers = 4
)

var siteBase = "https://cockyboys.com"

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
		"cockyboys.com/categories/movies_{n}_d.html",
		"cockyboys.com/scenes/{slug}.html",
		"cockyboys.com/models/{slug}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?cockyboys\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// Listing card: the "abso" anchor carries both the scene URL and the title.
	cardRe = regexp.MustCompile(`<a href="(/scenes/[^"]+)" class="abso" title="([^"]+)"`)

	h1Re       = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	releasedRe = regexp.MustCompile(`Released:</strong>\s*([0-9]{2}/[0-9]{2}/[0-9]{4})`)
	ogImageRe  = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
	ogTitleRe  = regexp.MustCompile(`<meta property="og:title" content="([^"]+)"`)
	thumbIDRe  = regexp.MustCompile(`/contentthumbs/(\d+)\.`)

	catSectionRe = regexp.MustCompile(`(?s)Categorized Under:</strong>(.*?)</p>`)
	catLinkRe    = regexp.MustCompile(`/categories/[^"]*\.html">([^<]*)</a>`)

	modelGridRe = regexp.MustCompile(`(?s)movieModels__grid">(.*?)</div>`)
	modelNameRe = regexp.MustCompile(`<a class="name gothamy" href="/models/[^"]+\.html" title="([^"]+)"`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/movies_%d_d.html", siteBase, page)
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		// Pages past the end repeat the last page — stop when nothing is new.
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
	slug  string
	url   string
	title string
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
		path := m[1]
		slug := sceneSlug(path)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		items = append(items, listItem{
			slug:  slug,
			url:   siteBase + path,
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		})
	}
	scraper.Debugf(1, "cockyboys: listing %s -> %d cards", pageURL, len(items))
	return items, nil
}

// sceneSlug extracts the bare slug from "/scenes/{slug}.html?type=vids".
func sceneSlug(path string) string {
	p := path
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimPrefix(p, "/scenes/")
	p = strings.TrimSuffix(p, ".html")
	return p
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "cockyboys: fetching %d details with %d workers", len(items), workers)
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
	scene := models.Scene{
		ID:        it.slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Studio:    studioName,
		ScrapedAt: now,
	}

	body, err := s.get(ctx, it.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if scene.Title == "" {
		if m := h1Re.FindStringSubmatch(detail); m != nil {
			scene.Title = cleanText(m[1])
		} else if m := ogTitleRe.FindStringSubmatch(detail); m != nil {
			scene.Title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
	}

	if m := ogImageRe.FindStringSubmatch(detail); m != nil {
		scene.Thumbnail = html.UnescapeString(strings.TrimSpace(m[1]))
		// Prefer the numeric content id from the thumbnail as a stable ID.
		if id := thumbIDRe.FindStringSubmatch(scene.Thumbnail); id != nil {
			scene.ID = id[1]
		}
	}

	if m := releasedRe.FindStringSubmatch(detail); m != nil {
		if d, derr := parseutil.TryParseDate(m[1], "01/02/2006"); derr == nil {
			scene.Date = d.UTC()
		}
	}

	if m := catSectionRe.FindStringSubmatch(detail); m != nil {
		var tags []string
		seen := make(map[string]bool)
		for _, t := range catLinkRe.FindAllStringSubmatch(m[1], -1) {
			name := cleanText(t[1])
			if name != "" && !seen[name] {
				seen[name] = true
				tags = append(tags, name)
			}
		}
		scene.Tags = tags
	}

	if m := modelGridRe.FindStringSubmatch(detail); m != nil {
		var performers []string
		seen := make(map[string]bool)
		for _, p := range modelNameRe.FindAllStringSubmatch(m[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(p[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				performers = append(performers, name)
			}
		}
		scene.Performers = performers
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

var tagStripRe = regexp.MustCompile(`<[^>]+>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

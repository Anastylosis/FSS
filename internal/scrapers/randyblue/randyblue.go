// Package randyblue scrapes Randy Blue (randyblue.com), an Elevated X gay
// studio tour. The videos_{N}_d.html listing yields the per-scene detail URLs;
// each detail page carries schema.org microdata (name, description, uploadDate,
// thumbnailUrl) plus a models list and a tags list. Listing pages past the end
// return no scene links, which stops pagination.
package randyblue

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
	siteID        = "randyblue"
	studioName    = "Randy Blue"
	detailWorkers = 4
)

var siteBase = "https://www.randyblue.com"

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
		"randyblue.com/categories/videos_{n}_d.html",
		"randyblue.com/scenes/{slug}_vids.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?randyblue\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	sceneLinkRe = regexp.MustCompile(`href="(/scenes/[^"]+_vids\.html)"`)

	nameRe      = regexp.MustCompile(`itemprop="name"\s+content="([^"]*)"`)
	descRe      = regexp.MustCompile(`itemprop="description"\s+content="([^"]*)"`)
	uploadRe    = regexp.MustCompile(`itemprop="uploadDate"\s+content="([^"]*)"`)
	thumbRe     = regexp.MustCompile(`itemprop="thumbnailUrl"\s+content="([^"]*)"`)
	contentIDRe = regexp.MustCompile(`contentthumbs/(?:\d+/)*(\d+)[-.]`)

	modelsListRe = regexp.MustCompile(`(?s)class="scene-models-list">(.*?)</ul>`)
	modelLinkRe  = regexp.MustCompile(`href="/models/[^"]*"[^>]*>([^<]+)</a>`)
	tagsListRe   = regexp.MustCompile(`(?s)class="scene-tags">(.*?)</ul>`)
	tagLinkRe    = regexp.MustCompile(`href="/categories/[^"]*"[^>]*>([^<]+)</a>`)

	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/videos_%d_d.html", siteBase, page)
		items, err := s.fetchListing(ctx, pageURL)
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
	slug string
	url  string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	items := make([]listItem, 0)
	seen := make(map[string]bool)
	for _, m := range sceneLinkRe.FindAllStringSubmatch(string(body), -1) {
		path := m[1]
		slug := strings.TrimSuffix(strings.TrimPrefix(path, "/scenes/"), "_vids.html")
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		items = append(items, listItem{slug: slug, url: siteBase + path})
	}
	scraper.Debugf(1, "randyblue: listing %s -> %d cards", pageURL, len(items))
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "randyblue: fetching %d details with %d workers", len(items), workers)
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
		ID:        it.slug,
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

	if m := nameRe.FindStringSubmatch(detail); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(m[1]))
	}
	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := uploadRe.FindStringSubmatch(detail); m != nil {
		if d, derr := parseutil.TryParseDate(strings.TrimSpace(m[1]), "01/02/2006"); derr == nil {
			scene.Date = d.UTC()
		}
	}
	if m := thumbRe.FindStringSubmatch(detail); m != nil {
		scene.Thumbnail = html.UnescapeString(strings.TrimSpace(m[1]))
		if id := contentIDRe.FindStringSubmatch(scene.Thumbnail); id != nil {
			scene.ID = id[1]
		}
	}

	if m := modelsListRe.FindStringSubmatch(detail); m != nil {
		var performers []string
		seen := make(map[string]bool)
		for _, p := range modelLinkRe.FindAllStringSubmatch(m[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(p[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				performers = append(performers, name)
			}
		}
		scene.Performers = performers
	}

	if m := tagsListRe.FindStringSubmatch(detail); m != nil {
		var tags []string
		seen := make(map[string]bool)
		for _, t := range tagLinkRe.FindAllStringSubmatch(m[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(t[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				tags = append(tags, name)
			}
		}
		scene.Tags = tags
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

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "<br>", " ")
	s = strings.ReplaceAll(s, "<br/>", " ")
	s = strings.ReplaceAll(s, "<br />", " ")
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

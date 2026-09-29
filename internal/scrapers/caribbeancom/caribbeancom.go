// Package caribbeancom scrapes Caribbeancom (caribbeancom.com), a static
// Japanese JAV catalog. Pages are served as EUC-JP and decoded to UTF-8 before
// parsing. The /listpages/all{N}.htm listing yields /moviepages/{id}/index.html
// links (ids like "062626-001") plus a card thumbnail; each detail page carries
// schema.org microdata (itemprop="name"/"description"/"actor"/"uploadDate"/
// "duration") inside a <div class="movie-info section"> block. A worker pool
// fetches the detail pages.
package caribbeancom

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
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

var siteBase = "https://www.caribbeancom.com"

const (
	siteID        = "caribbeancom"
	detailWorkers = 4
)

type Scraper struct{ Client *http.Client }

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"caribbeancom.com",
		"caribbeancom.com/listpages/all{N}.htm",
		"caribbeancom.com/moviepages/{id}/index.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?caribbeancom\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardRe     = regexp.MustCompile(`(?s)href="/moviepages/([0-9-]+)/index\.html">\s*(?:<div[^>]*>\s*)*<img[^>]+?itemprop="thumbnail" src="([^"]+)"`)
	sectionRe  = regexp.MustCompile(`(?s)<div class="movie-info section">(.*?)<!-- /\.movie-info -->`)
	nameRe     = regexp.MustCompile(`(?s)<h1 itemprop="name">(.*?)</h1>`)
	descRe     = regexp.MustCompile(`(?s)<p itemprop="description">(.*?)</p>`)
	actorRe    = regexp.MustCompile(`(?s)itemprop="actor".*?<span itemprop="name">(.*?)</span>`)
	dateRe     = regexp.MustCompile(`itemprop="uploadDate"[^>]*>\s*([0-9/]+)`)
	durationRe = regexp.MustCompile(`itemprop="duration"[^>]*>\s*([0-9:]+)`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type listItem struct{ id, thumb string }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/listpages/all%d.htm", siteBase, page)
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

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	matches := cardRe.FindAllStringSubmatch(string(body), -1)
	items := make([]listItem, 0, len(matches))
	for _, m := range matches {
		items = append(items, listItem{id: m[1], thumb: m[2]})
	}
	scraper.Debugf(1, "%s: listing page has %d cards", siteID, len(items))
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
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
	body, err := s.get(ctx, fmt.Sprintf("%s/moviepages/%s/index.html", siteBase, it.id))
	if err != nil {
		return models.Scene{}
	}
	detail := string(body)

	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       fmt.Sprintf("%s/moviepages/%s/index.html", siteBase, it.id),
		Studio:    "Caribbeancom",
		Thumbnail: it.thumb,
		ScrapedAt: now,
	}

	section := detail
	if m := sectionRe.FindStringSubmatch(detail); m != nil {
		section = m[1]
	}

	if m := nameRe.FindStringSubmatch(section); m != nil {
		scene.Title = cleanText(m[1])
	}
	if scene.Title == "" {
		scene.Title = it.id
	}
	if m := descRe.FindStringSubmatch(section); m != nil {
		scene.Description = cleanText(m[1])
	}
	seen := map[string]bool{}
	for _, m := range actorRe.FindAllStringSubmatch(section, -1) {
		name := cleanText(m[1])
		if name != "" && !seen[name] {
			seen[name] = true
			scene.Performers = append(scene.Performers, name)
		}
	}
	if m := dateRe.FindStringSubmatch(section); m != nil {
		if d, err := time.Parse("2006/01/02", strings.TrimSpace(m[1])); err == nil {
			scene.Date = d.UTC()
		}
	}
	if m := durationRe.FindStringSubmatch(section); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}
	return scene
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	raw, err := func() ([]byte, error) {
		defer func() { _ = resp.Body.Close() }()
		return httpx.ReadBody(resp.Body)
	}()
	if err != nil {
		return nil, err
	}
	return decodeEUCJP(raw), nil
}

// decodeEUCJP converts an EUC-JP byte slice to UTF-8; on a decode error it
// returns the input unchanged so ASCII-only pages still parse.
func decodeEUCJP(b []byte) []byte {
	out, _, err := transform.Bytes(japanese.EUCJP.NewDecoder(), b)
	if err != nil {
		return b
	}
	return out
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

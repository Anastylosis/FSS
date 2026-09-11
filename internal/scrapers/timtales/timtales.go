// Package timtales scrapes Timtales (https://www.timtales.com/), a TYPO3 gay
// bareback site. The /videos/latest/ listing yields one card per scene with the
// slug, short title and splash thumbnail; the detail page (/videos/{slug}) adds
// the full title, publish date, runtime, description, performers and
// categories. Pages past the end repeat the last page, so pagination stops once
// a page yields no new scenes.
package timtales

import (
	"context"
	"errors"
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
	siteID        = "timtales"
	studioName    = "Timtales"
	detailWorkers = 4
)

var baseURL = "https://www.timtales.com"

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"timtales.com/videos/latest/",
		"timtales.com/videos/{category}/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?timtales\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listBase := strings.TrimRight(studioURL, "/")
	if listBase == "" || !matchRe.MatchString(listBase) {
		listBase = baseURL + "/videos/latest"
	}

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := listBase
		if page > 1 {
			pageURL = fmt.Sprintf("%s/page-%d", listBase, page)
		}
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(items) == 0 && page == 1 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, errors.New("listing loaded but no scene cards matched"))
		}
		// Out-of-range pages repeat the last page — stop when nothing is new.
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
		scenes := s.enrich(ctx, studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// ---- listing ----

// Scene cards live in <div class="video-item ...">; each has an <h2> title, a
// player div carrying the splash thumbnail, and a /videos/{slug} anchor (the
// trailing slash was dropped in 2026 and is accepted either way).
// Category/sidebar links are plain <a> tags outside any video-item, so a card
// regex naturally excludes them.
var cardRe = regexp.MustCompile(`(?s)<div class="video-item[^"]*">\s*<h2>(.*?)</h2>.*?background-image:\s*url\(&#0?39;([^&]+)&#0?39;\).*?<a href="(/videos/[^"/]+)/?"`)

type listItem struct {
	id, url, title, thumbnail string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	matches := cardRe.FindAllStringSubmatch(string(body), -1)
	items := make([]listItem, 0, len(matches))
	for _, m := range matches {
		slug := strings.TrimPrefix(m[3], "/videos/")
		if slug == "" {
			continue
		}
		items = append(items, listItem{
			id:        slug,
			url:       baseURL + m[3],
			title:     cleanText(m[1]),
			thumbnail: html.UnescapeString(strings.TrimSpace(m[2])),
		})
	}
	scraper.Debugf(1, "%s: listing %s yielded %d cards", siteID, pageURL, len(items))
	return items, nil
}

// ---- detail ----

func (s *Scraper) enrich(ctx context.Context, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), detailWorkers)
	scenes := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, detailWorkers)
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

var (
	h1Re   = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	dateRe = regexp.MustCompile(`(?s)<p class="date">\s*(.*?)\s*(?:&#8211;|–|-)\s*Runtime:\s*([0-9:]+)\s*</p>`)
	descRe = regexp.MustCompile(`(?s)<p class="bodytext">(.*?)</p>`)
	menRe  = regexp.MustCompile(`(?s)<p class="categories">\s*Men:(.*?)</p>`)
	catsRe = regexp.MustCompile(`(?s)<p class="categories">\s*Categories:(.*?)</p>`)
	aRe    = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Thumbnail: it.thumbnail,
		Studio:    studioName,
		ScrapedAt: now,
	}

	body, err := s.get(ctx, it.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := h1Re.FindStringSubmatch(detail); m != nil {
		if t := cleanText(m[1]); t != "" {
			scene.Title = t
		}
	}
	if m := dateRe.FindStringSubmatch(detail); m != nil {
		if d, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "January 2, 2006"); err == nil {
			scene.Date = d
		}
		scene.Duration = parseutil.ParseDurationColon(m[2])
	}
	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := menRe.FindStringSubmatch(detail); m != nil {
		scene.Performers = anchorTexts(m[1])
	}
	if m := catsRe.FindStringSubmatch(detail); m != nil {
		scene.Categories = anchorTexts(m[1])
	}
	return scene
}

func anchorTexts(block string) []string {
	var out []string
	for _, m := range aRe.FindAllStringSubmatch(block, -1) {
		if t := cleanText(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ---- helpers ----

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
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

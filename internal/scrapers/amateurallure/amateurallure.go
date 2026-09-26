// Package amateurallure scrapes Amateur Allure (amateurallure.com). The public
// site is an age-splash; the real tour lives under /tour/. The
// /tour/updates/page_{N}.html listing is server-rendered and yields, per scene
// card, the detail URL, title, publish date (MM/DD/YYYY) and thumbnail. The
// detail page (/tour/scenes/{slug}_vids.html) adds the canonical og:title,
// og:description and the linked performer(s). Pagination ends naturally when a
// page yields no cards.
package amateurallure

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
	siteID        = "amateurallure"
	studioName    = "Amateur Allure"
	detailWorkers = 4
)

const (
	siteBase       = "https://www.amateurallure.com"
	listingPathFmt = "/tour/updates/page_%d.html"
)

type Scraper struct {
	Client *http.Client
	// base is the site root. A field rather than a mutable package var so tests
	// redirect fetches without swapping global state, which every test
	// previously had to save and restore.
	base string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"amateurallure.com",
		"amateurallure.com/tour/updates/page_{N}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?amateurallure\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="update_details"`)
	sceneURLRe  = regexp.MustCompile(`href="(https?://[^"]*/tour/scenes/([^"/]+)_vids\.html)"`)
	dateRe      = regexp.MustCompile(`<strong>Added:</strong>\s*([0-9]{2}/[0-9]{2}/[0-9]{4})`)
	thumbRe     = regexp.MustCompile(`src0_1x="([^"]+)"`)
	altTitleRe  = regexp.MustCompile(`alt="([^"]*)"`)
	modelLinkRe = regexp.MustCompile(`/tour/models/([^"/]+)\.html`)

	xxxVideosSuffixRe = regexp.MustCompile(`(?i)-xxx-videos$`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		items, err := s.fetchListing(ctx, fmt.Sprintf(s.base+listingPathFmt, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(items) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, items, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

type listItem struct {
	id    string
	url   string
	title string
	date  string
	thumb string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	parts := cardSplitRe.Split(string(body), -1)
	if len(parts) <= 1 {
		return nil, nil
	}
	items := make([]listItem, 0, len(parts)-1)
	seen := make(map[string]bool)
	for _, card := range parts[1:] {
		m := sceneURLRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{id: id, url: m[1]}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			it.date = d[1]
		}
		if t := altTitleRe.FindStringSubmatch(card); t != nil {
			it.title = html.UnescapeString(strings.TrimSpace(t[1]))
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = absURL(s.base, th[1])
		}
		items = append(items, it)
	}
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
	if d, err := time.Parse("01/02/2006", it.date); err == nil {
		scene.Date = d.UTC()
	}

	if body, err := s.get(ctx, it.url); err == nil {
		og := parseutil.OpenGraph(body)
		if t := strings.TrimSpace(html.UnescapeString(og["og:title"])); t != "" {
			scene.Title = t
		}
		if d := strings.TrimSpace(html.UnescapeString(og["og:description"])); d != "" {
			scene.Description = d
		}
		if img := strings.TrimSpace(og["og:image"]); img != "" {
			scene.Thumbnail = absURL(s.base, img)
		}
		scene.Performers = parsePerformers(body)
	}
	return scene
}

// parsePerformers extracts performer names from /tour/models/{Name}.html links,
// skipping the "models" all-models index link.
func parsePerformers(body []byte) []string {
	var performers []string
	seen := make(map[string]bool)
	for _, m := range modelLinkRe.FindAllSubmatch(body, -1) {
		slug := string(m[1])
		if slug == "" || strings.EqualFold(slug, "models") {
			continue
		}
		// Some model links carry a "-xxx-videos" SEO suffix; drop it.
		slug = xxxVideosSuffixRe.ReplaceAllString(slug, "")
		name := html.UnescapeString(strings.ReplaceAll(slug, "-", " "))
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		performers = append(performers, name)
	}
	return performers
}

func absURL(base, u string) string {
	if u == "" || strings.HasPrefix(u, "http") {
		return u
	}
	return base + "/" + strings.TrimPrefix(u, "/")
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

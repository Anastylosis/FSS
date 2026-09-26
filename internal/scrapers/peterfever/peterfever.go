// Package peterfever scrapes Peter Fever (peterfever.com), a gay/Asian
// studio running an ElevatedX/NATS tour. The card markup does not match any
// existing *util's selectors, so this is a standalone scraper.
//
// The listing lives at /categories/movies.html (page 1) and
// /categories/movies_{N}_d.html (page 2+). Each card yields the detail URL,
// slug, a truncated title, the publish date ("02 Jan 06") and a thumbnail.
// The detail page (/scenes/{slug}_vids.html) carries the canonical og:title
// (full, untruncated), og:description and og:image. Pagination ends naturally
// when a page yields no cards.
//
// Recon deviation from the task brief: Peter Fever scene detail pages do NOT
// expose performers, per-scene tags or a duration — the only /models/ link on a
// detail page is the all-models index. Performers are therefore only available
// via the model-page mode (/models/{slug}.html), where the page's <h1> names
// the model and the cards are that model's scenes.
package peterfever

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
	siteID        = "peterfever"
	studioName    = "Peter Fever"
	detailWorkers = 4
	dateLayout    = "02 Jan 06"
)

// siteBase is a var (not a const) so tests can point it at an httptest server.
var siteBase = "https://www.peterfever.com"

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
		"peterfever.com",
		"peterfever.com/categories/movies.html",
		"peterfever.com/models/{slug}.html",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?peterfever\.com(?:/|$)`)
	// modelRe matches a model-profile URL, capturing the model slug. The
	// all-models index (/models/models.html) is excluded by the caller.
	modelRe = regexp.MustCompile(`/models/([A-Za-z0-9._-]+)\.html`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	if m := modelRe.FindStringSubmatch(studioURL); m != nil && !strings.EqualFold(m[1], "models") {
		scraper.Debugf(1, "%s: scraping model page %s", siteID, m[1])
		s.runModel(ctx, studioURL, opts, out, now)
		return
	}

	scraper.Debugf(1, "%s: scraping movie listing", siteID)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		items, err := s.fetchListing(ctx, listingURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(items) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, items, nil, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// runModel scrapes a single model-profile page: the page's <h1> names the
// model, and the cards are that model's scenes. Model pages are single-page (no
// pagination observed), so we parse one page and emit its scenes directly.
func (s *Scraper) runModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	body, err := s.get(ctx, studioURL)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("model page: %w", err)))
		return
	}
	model := parseModelName(body)
	items := parseCards(body)
	if len(items) == 0 {
		return
	}
	var performers []string
	if model != "" {
		performers = []string{model}
	}
	scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, items, performers, now, opts.Delay)
	for _, sc := range scenes {
		if opts.KnownIDs[sc.ID] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", siteID)
			send(ctx, out, scraper.StoppedEarly())
			return
		}
		if !send(ctx, out, scraper.Scene(sc)) {
			return
		}
	}
}

// listingURL maps a 1-based page number to its tour URL: page 1 is
// /categories/movies.html, page 2+ is /categories/movies_{N}_d.html.
func listingURL(page int) string {
	if page <= 1 {
		return siteBase + "/categories/movies.html"
	}
	return fmt.Sprintf("%s/categories/movies_%d_d.html", siteBase, page)
}

type listItem struct {
	id    string
	url   string
	title string
	date  string
	thumb string
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="pi-img-wrapper">`)
	thumbRe     = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	titleInfoRe = regexp.MustCompile(`(?s)<div class="title-info">\s*<h3 class="vid">\s*<a href="(https?://[^"]*/scenes/([^"/]+)_vids\.html)">([^<]*)</a>\s*<span class="pi-added">.*?</i>\s*([^<]+?)\s*</span>`)
	h1Re        = regexp.MustCompile(`(?s)<h1[^>]*>\s*([^<]+?)\s*</h1>`)
)

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	return parseCards(body), nil
}

// parseCards extracts scene cards from a listing or model page. Each card is a
// pi-img-wrapper block; only blocks containing a /scenes/{slug}_vids.html
// title-info link are scenes (screencaps and other wrappers are skipped).
func parseCards(body []byte) []listItem {
	chunks := cardSplitRe.Split(string(body), -1)
	if len(chunks) <= 1 {
		return nil
	}
	items := make([]listItem, 0, len(chunks)-1)
	seen := make(map[string]bool)
	for _, chunk := range chunks[1:] {
		m := titleInfoRe.FindStringSubmatch(chunk)
		if m == nil {
			continue
		}
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{
			id:    id,
			url:   m[1],
			title: html.UnescapeString(strings.TrimSpace(m[3])),
			date:  strings.TrimSpace(m[4]),
		}
		if th := thumbRe.FindStringSubmatch(chunk); th != nil {
			it.thumb = absURL(th[1])
		}
		items = append(items, it)
	}
	return items
}

func parseModelName(body []byte) string {
	if m := h1Re.FindSubmatch(body); m != nil {
		return html.UnescapeString(strings.TrimSpace(string(m[1])))
	}
	return ""
}

// enrich fetches each scene's detail page concurrently to upgrade the truncated
// listing title to the full og:title and attach the description/og:image.
// performers, if non-nil (model-page mode), is applied to every scene. A detail
// fetch failure is non-fatal: the scene keeps its (non-empty) listing data
// rather than being dropped.
func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, performers []string, now time.Time, delay time.Duration) []models.Scene {
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
			scenes[i] = s.toScene(ctx, studioURL, it, performers, now)
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

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, performers []string, now time.Time) models.Scene {
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
	if len(performers) > 0 {
		scene.Performers = performers
	}
	if d, err := parseutil.TryParseDate(it.date, dateLayout); err == nil {
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
			scene.Thumbnail = absURL(img)
		}
	}
	return scene
}

func absURL(u string) string {
	if u == "" || strings.HasPrefix(u, "http") {
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

// send delivers a result on out, respecting context cancellation. It returns
// false if the context was cancelled before the send completed.
func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

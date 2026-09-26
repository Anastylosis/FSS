// Package lifeselector scrapes Life Selector (lifeselector.com, also reachable
// via 21roles.com), an interactive-POV "game" studio. The /games?page={N}
// listing renders one `story thumbnail` card per game carrying the /game/{id}/
// {slug} link, the title, a scoped list of actor /model links and a webp cover.
// The per-game detail page adds the og:description synopsis. Listing pages are
// walked until one returns no cards; each page's descriptions are fetched by a
// small worker pool.
package lifeselector

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

// siteBase is a var (not const) so tests can point it at a local httptest server.
var siteBase = "https://lifeselector.com"

type Scraper struct {
	Client *http.Client
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "lifeselector" }

func (s *Scraper) Patterns() []string {
	return []string{"lifeselector.com", "lifeselector.com/games", "21roles.com"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:lifeselector\.com|21roles\.com)(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe = regexp.MustCompile(`class="story thumbnail`)
	gameLinkRe  = regexp.MustCompile(`href="/game/(\d+)/([^"]+)"`)
	titleRe     = regexp.MustCompile(`class="title truncate"[^>]*>([^<]+)</a>`)
	actorsRe    = regexp.MustCompile(`(?s)<div class="actors truncate">(.*?)</div>`)
	modelLinkRe = regexp.MustCompile(`href="/model/[^"]+"[^>]*>([^<]+)</a>`)
	thumbRe     = regexp.MustCompile(`data-srcset="(https://[^\s"]+\.webp)`)
	ogDescRe    = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, "lifeselector", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		cards, err := s.fetchListing(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := make([]listItem, 0, len(cards))
		for _, c := range cards {
			it, ok := parseCard(c)
			if !ok || seen[it.id] {
				continue
			}
			seen[it.id] = true
			items = append(items, it)
		}
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, items, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

type listItem struct {
	id, slug, title, thumb string
	performers             []string
}

func (s *Scraper) fetchListing(ctx context.Context, page int) ([]string, error) {
	pageURL := fmt.Sprintf("%s/games?page=%d", siteBase, page)
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	parts := cardSplitRe.Split(string(body), -1)
	if len(parts) <= 1 {
		return nil, nil
	}
	return parts[1:], nil
}

func parseCard(card string) (listItem, bool) {
	m := gameLinkRe.FindStringSubmatch(card)
	if m == nil {
		return listItem{}, false
	}
	it := listItem{id: m[1], slug: m[2]}
	if t := titleRe.FindStringSubmatch(card); t != nil {
		it.title = html.UnescapeString(strings.TrimSpace(t[1]))
	}
	if it.title == "" {
		return listItem{}, false
	}
	if a := actorsRe.FindStringSubmatch(card); a != nil {
		seen := map[string]bool{}
		for _, mm := range modelLinkRe.FindAllStringSubmatch(a[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(mm[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				it.performers = append(it.performers, name)
			}
		}
	}
	if th := thumbRe.FindStringSubmatch(card); th != nil {
		it.thumb = th[1]
	}
	return it, true
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
		ID:         it.id,
		SiteID:     "lifeselector",
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        fmt.Sprintf("%s/game/%s/%s", siteBase, it.id, it.slug),
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     "Life Selector",
		ScrapedAt:  now,
	}
	// Best-effort detail fetch for the synopsis; the listing has everything else.
	if body, err := s.get(ctx, scene.URL); err == nil {
		if m := ogDescRe.FindStringSubmatch(string(body)); m != nil {
			scene.Description = cleanDesc(m[1])
		}
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

func cleanDesc(s string) string {
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

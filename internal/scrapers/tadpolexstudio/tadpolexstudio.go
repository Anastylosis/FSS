// Package tadpolexstudio scrapes TadPoleX Studio (tadpolexstudio.com), an
// Elevated X tour on the `latestUpdateB` card skin.
//
// The listing card carries every field — scene id, title, URL, performers,
// date, duration and thumbnail — so the detail worker pool only adds the
// category tags.
//
// It stays a standalone scraper rather than joining a shared Elevated X util
// for the reason set out in the goldwinpass package doc: every site in that
// family wraps its cards in a different class, so a util would just be several
// regex sets sharing a name.
package tadpolexstudio

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
	siteID        = "tadpolexstudio"
	studioName    = "TadPoleX Studio"
	detailWorkers = 4
)

var siteBase = "https://www.tadpolexstudio.com"

// Scraper implements scraper.StudioScraper for TadPoleX Studio.
type Scraper struct {
	Client *http.Client
}

// New constructs a TadPoleX Studio scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"tadpolexstudio.com",
		"tadpolexstudio.com/categories/movies.html",
		"tadpolexstudio.com/categories/movies_{N}.html",
		"tadpolexstudio.com/scenes/{slug}_vids.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?tadpolexstudio\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, listingURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
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
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// listingURL builds the movies-category page. Page 1 has no numeric suffix.
func listingURL(page int) string {
	if page <= 1 {
		return siteBase + "/categories/movies.html"
	}
	return fmt.Sprintf("%s/categories/movies_%d.html", siteBase, page)
}

// ---- listing ----

var (
	cardRe = regexp.MustCompile(`<div class="latestUpdateB" data-setid="(\d+)">`)
	// Only the scene filename is captured so the URL is rebuilt against
	// siteBase — cards link absolutely.
	titleRe = regexp.MustCompile(`(?s)<h4 class="link_bright">\s*<a\s+href="[^"]*/scenes/([^"/]+\.html)"\s*>\s*(.*?)\s*</a>`)
	modelRe = regexp.MustCompile(`class="link_bright infolink" href="[^"]*/models/[^"]*\.html">([^<]+)</a>`)
	// "<!-- Date --> 07/21/2026" — the value trails an HTML comment.
	dateRe = regexp.MustCompile(`<!-- Date -->\s*(\d{2}/\d{2}/\d{4})`)
	// "24 min"
	durationRe = regexp.MustCompile(`(\d+)\s*min\b`)
	thumbRe    = regexp.MustCompile(`src0_4x="([^"]+)"`)
	thumbAltRe = regexp.MustCompile(`src0_1x="([^"]+)"`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
	performers            []string
}

func parseListing(body []byte) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := page[loc[0]:end]

		it := listItem{id: page[loc[2]:loc[3]]}
		m := titleRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it.url = siteBase + "/scenes/" + m[1]
		it.title = cleanText(m[2])

		for _, pm := range modelRe.FindAllStringSubmatch(card, -1) {
			if name := cleanText(pm[1]); name != "" {
				it.performers = append(it.performers, name)
			}
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			// US-format date.
			if ts, err := time.Parse("01/02/2006", d[1]); err == nil {
				it.date = ts.UTC()
			}
		}
		if du := durationRe.FindStringSubmatch(card); du != nil {
			it.duration = atoi(du[1]) * 60
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = normalizeURL(th[1])
		} else if th := thumbAltRe.FindStringSubmatch(card); th != nil {
			it.thumb = normalizeURL(th[1])
		}

		items = append(items, it)
	}
	return items
}

func normalizeURL(u string) string {
	if strings.HasPrefix(u, "/") {
		return siteBase + u
	}
	return u
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
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

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

// tagRe matches the scene's category links. The listing category ("movies") is
// the section itself, not a tag, and is filtered out.
var tagRe = regexp.MustCompile(`/categories/([a-z0-9-]+)\.html"[^>]*>([^<]+)</a>`)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Date:       it.date,
		Duration:   it.duration,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     studioName,
		ScrapedAt:  now,
	}

	// Only tags need the detail page; a failure there still leaves a complete
	// scene.
	if body, err := s.fetchPage(ctx, it.url); err == nil {
		scene.Tags = parseTags(string(body))
	}
	return scene
}

func parseTags(detail string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range tagRe.FindAllStringSubmatch(detail, -1) {
		if m[1] == "movies" {
			continue
		}
		tag := cleanText(m[2])
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// Package abbywinters scrapes Abby Winters (abbywinters.com). The site runs a
// custom tour with a server-rendered /amateurs/shoots listing whose cards carry
// the full per-scene metadata (title with embedded models, category section,
// detail URL, CDN thumbnail and a YYYYMM publish date). The CDN thumbnail path
// embeds the numeric shoot id, used as the scene ID. A detail-page fetch (via a
// small worker pool) enriches each scene with the precise release date and the
// performer list, which the listing card does not break out.
package abbywinters

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
	listPath      = "/amateurs/shoots"
	detailWorkers = 4
)

// siteBase is a var (not const) so tests can point it at an httptest server.
var siteBase = "https://www.abbywinters.com"

type Scraper struct{ client *http.Client }

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "abbywinters" }

func (s *Scraper) Patterns() []string {
	return []string{
		"abbywinters.com",
		"abbywinters.com/amateurs/shoots",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?abbywinters\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	articleSplitRe = regexp.MustCompile(`<article class="item `)
	cdnIDRe        = regexp.MustCompile(`data-src="https://cdn\.abbywinters\.com/(\d+)/`)
	thumbRe        = regexp.MustCompile(`data-src="(https://cdn\.abbywinters\.com/[^"?]+)`)
	titleRe        = regexp.MustCompile(`(?s)<h2 class="card-title">(.*?)<span class="pull-right text-cap">([^<]*)</span>`)
	hrefRe         = regexp.MustCompile(`<a href="([^"]+)" class="card-thumb`)
	pubDateRe      = regexp.MustCompile(`data-publishdate="(\d{6})"`)
	totalRe        = regexp.MustCompile(`id="browse-total-count">(\d[\d,]*)`)

	releaseDateRe = regexp.MustCompile(`(?s)<th>\s*Release date\s*</th>\s*<td>([^<]+)</td>`)
	girlsRowRe    = regexp.MustCompile(`(?s)<th>\s*Girls in this Scene\s*</th>\s*<td>(.*?)</td>`)
	anchorTextRe  = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, "abbywinters", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", siteBase, listPath, page)
		body, err := s.get(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseCards(body)
		total := 0
		if m := totalRe.FindSubmatch(body); m != nil {
			total = parseInt(string(m[1]))
		}
		// Pages past the end repeat earlier content — stop when nothing is new.
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true, Total: total}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

type cardItem struct {
	id        string
	title     string
	category  string
	url       string
	thumbnail string
	pubDate   string // YYYYMM
}

func parseCards(body []byte) []cardItem {
	parts := articleSplitRe.Split(string(body), -1)
	if len(parts) <= 1 {
		return nil
	}
	items := make([]cardItem, 0, len(parts))
	for _, p := range parts[1:] {
		m := cdnIDRe.FindStringSubmatch(p)
		if m == nil {
			continue // handlebars template stub or non-shoot block
		}
		it := cardItem{id: m[1]}
		if t := titleRe.FindStringSubmatch(p); t != nil {
			it.title = html.UnescapeString(strings.TrimSpace(t[1]))
			it.category = html.UnescapeString(strings.TrimSpace(t[2]))
		}
		if h := hrefRe.FindStringSubmatch(p); h != nil {
			it.url = h[1]
		}
		if th := thumbRe.FindStringSubmatch(p); th != nil {
			it.thumbnail = th[1]
		}
		if d := pubDateRe.FindStringSubmatch(p); d != nil {
			it.pubDate = d[1]
		}
		if it.id == "" {
			continue
		}
		items = append(items, it)
	}
	return items
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []cardItem, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "abbywinters: fetching %d details with %d workers", len(items), workers)
	scenes := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it cardItem) {
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

func (s *Scraper) toScene(ctx context.Context, studioURL string, it cardItem, now time.Time) models.Scene {
	id := it.id
	if id == "" {
		id = slugFromURL(it.url)
	}
	scene := models.Scene{
		ID:        id,
		SiteID:    "abbywinters",
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Thumbnail: it.thumbnail,
		Studio:    "Abby Winters",
		ScrapedAt: now,
	}
	if it.category != "" {
		scene.Categories = []string{it.category}
	}
	// Listing only gives a YYYYMM publish bucket; use it as a fallback date.
	if len(it.pubDate) == 6 {
		if d, err := time.Parse("200601", it.pubDate); err == nil {
			scene.Date = d.UTC()
		}
	}

	// Detail page carries the precise release date and the performer list.
	if it.url != "" {
		if body, err := s.get(ctx, it.url); err == nil {
			detail := string(body)
			if m := releaseDateRe.FindStringSubmatch(detail); m != nil {
				// strings.Fields splits on nbsp too, normalising "23 Jun 2026".
				raw := strings.Join(strings.Fields(html.UnescapeString(m[1])), " ")
				if d, err := parseutil.TryParseDate(raw, "2 Jan 2006", "02 Jan 2006"); err == nil {
					scene.Date = d
				}
			}
			if m := girlsRowRe.FindStringSubmatch(detail); m != nil {
				seen := map[string]bool{}
				for _, a := range anchorTextRe.FindAllStringSubmatch(m[1], -1) {
					name := html.UnescapeString(strings.TrimSpace(a[1]))
					if name != "" && !seen[name] {
						seen[name] = true
						scene.Performers = append(scene.Performers, name)
					}
				}
			}
		}
	}
	return scene
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func slugFromURL(u string) string {
	u = strings.TrimRight(u, "/")
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

func parseInt(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

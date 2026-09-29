// Package jeffsmodels scrapes Jeff's Models (jeffsmodels.com), a RogueBucks/NATS
// BBW site. There is no paginated HTML/JSON listing worth crawling, but
// sitemap.xml enumerates every /update/{id}/ scene page along with a <lastmod>
// date. The runner reads the sitemap, then fans out a worker pool over the detail
// pages: each page yields the title, performer(s), synopsis, cover thumbnail and a
// publish date (falling back to the sitemap <lastmod>).
package jeffsmodels

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
	siteID        = "jeffsmodels"
	studioName    = "Jeff's Models"
	detailWorkers = 6
)

var siteBase = "https://jeffsmodels.com"

// sitemapURL is a var (not const) so tests can point it at a local httptest server.
var sitemapURL = siteBase + "/sitemap.xml"

// Scraper implements scraper.StudioScraper for Jeff's Models.
type Scraper struct {
	Client *http.Client
}

// New constructs a Jeff's Models scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"jeffsmodels.com",
		"jeffsmodels.com/update/{id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?jeffsmodels\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

var (
	urlBlockRe = regexp.MustCompile(`(?s)<url>(.*?)</url>`)
	locRe      = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	lastmodRe  = regexp.MustCompile(`<lastmod>\s*([^<]+?)\s*</lastmod>`)
	updateIDRe = regexp.MustCompile(`/update/(\d+)/`)
)

type sitemapItem struct {
	id      string
	url     string
	lastmod time.Time
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	items, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "jeffsmodels: sitemap has %d scene URLs", len(items))

	// Drop already-known IDs on incremental runs so the worker pool only
	// fetches new scenes; the cmd layer merges these with existing state.
	work := items[:0]
	for _, it := range items {
		if !opts.KnownIDs[it.id] {
			work = append(work, it)
		}
	}
	if len(opts.KnownIDs) > 0 {
		scraper.Debugf(1, "jeffsmodels: %d new scenes after known-ID filter", len(work))
	}

	select {
	case out <- scraper.Progress(len(work)):
	case <-ctx.Done():
		return
	}

	workers := detailWorkers
	if opts.Workers > 0 {
		workers = opts.Workers
	}
	scraper.Debugf(1, "jeffsmodels: fetching %d details with %d workers", len(work), workers)

	jobs := make(chan sitemapItem)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				if ctx.Err() != nil {
					return
				}
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchScene(ctx, studioURL, it, now)
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("scene %s: %w", it.id, err)):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, it := range work {
		select {
		case jobs <- it:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *Scraper) fetchSitemap(ctx context.Context) ([]sitemapItem, error) {
	body, err := s.get(ctx, sitemapURL)
	if err != nil {
		return nil, err
	}
	var items []sitemapItem
	seen := make(map[string]bool)
	for _, block := range urlBlockRe.FindAllStringSubmatch(string(body), -1) {
		loc := locRe.FindStringSubmatch(block[1])
		if loc == nil {
			continue
		}
		idm := updateIDRe.FindStringSubmatch(loc[1])
		if idm == nil {
			continue
		}
		if seen[idm[1]] {
			continue
		}
		seen[idm[1]] = true
		it := sitemapItem{id: idm[1], url: strings.TrimSpace(loc[1])}
		if lm := lastmodRe.FindStringSubmatch(block[1]); lm != nil {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(lm[1])); err == nil {
				it.lastmod = t.UTC()
			}
		}
		items = append(items, it)
	}
	return items, nil
}

// ---- detail parsing ----

var (
	h1Re        = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	modelNameRe = regexp.MustCompile(`class="model-name[^"]*"[^>]*>([^<]+)</a>`)
	readMoreRe  = regexp.MustCompile(`(?s)<p class="read-more">(.*?)</p>`)
	bannerRe    = regexp.MustCompile(`<img[^>]+src="([^"]+)"[^>]*class="video-banner"`)
	updatedAtRe = regexp.MustCompile(`class="updated-at">([^<]+)</`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) fetchScene(ctx context.Context, studioURL string, it sitemapItem, now time.Time) (models.Scene, error) {
	body, err := s.get(ctx, it.url)
	if err != nil {
		return models.Scene{}, err
	}
	page := string(body)

	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       it.url,
		Studio:    studioName,
		Date:      it.lastmod,
		ScrapedAt: now,
	}

	if m := h1Re.FindStringSubmatch(page); m != nil {
		scene.Title = cleanText(m[1])
	}

	seen := map[string]bool{}
	for _, m := range modelNameRe.FindAllStringSubmatch(page, -1) {
		name := html.UnescapeString(strings.TrimSpace(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			scene.Performers = append(scene.Performers, name)
		}
	}

	if m := readMoreRe.FindStringSubmatch(page); m != nil {
		scene.Description = cleanText(m[1])
	}

	if m := bannerRe.FindStringSubmatch(page); m != nil {
		scene.Thumbnail = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	// On-page publish date overrides the sitemap lastmod when present.
	if m := updatedAtRe.FindStringSubmatch(page); m != nil {
		if d, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "Jan 2, 2006"); err == nil {
			scene.Date = d
		}
	}

	return scene, nil
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

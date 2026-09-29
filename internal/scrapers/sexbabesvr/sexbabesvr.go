// Package sexbabesvr scrapes SexBabesVR (sexbabesvr.com), a VR studio. There is
// no JSON listing, but a paginated sitemap (sitemap/?type=videos&from_links_videos=N)
// enumerates every /video/{slug}/ page. Each detail page carries a schema.org
// VideoObject in a JSON-LD block, which provides the title, description, upload
// date, ISO-8601 duration and performer list; og:title / og:image fill any
// gaps. The runner reads both sitemap pages, then fans out a worker pool over
// the detail pages.
package sexbabesvr

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
	siteID        = "sexbabesvr"
	studioName    = "SexBabesVR"
	defaultWorker = 6
)

// sitemapURLs are vars (not const) so tests can point them at a local server.
var sitemapURLs = []string{
	"https://sexbabesvr.com/sitemap/?type=videos&from_links_videos=1",
	"https://sexbabesvr.com/sitemap/?type=videos&from_links_videos=2",
}

// Scraper implements scraper.StudioScraper for SexBabesVR.
type Scraper struct {
	Client *http.Client
}

// New constructs a SexBabesVR scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"sexbabesvr.com",
		"sexbabesvr.com/video/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?sexbabesvr\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

var (
	locRe       = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	videoSlugRe = regexp.MustCompile(`/video/([^/]+)/?$`)
)

type sitemapItem struct {
	id  string
	url string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	var items []sitemapItem
	seen := map[string]bool{}
	for _, sm := range sitemapURLs {
		if ctx.Err() != nil {
			return
		}
		scraper.Debugf(1, "%s: fetching sitemap %s", siteID, sm)
		body, err := s.get(ctx, sm)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("sitemap %s: %w", sm, err)):
			case <-ctx.Done():
			}
			return
		}
		for _, it := range parseSitemap(body) {
			if seen[it.id] {
				continue
			}
			seen[it.id] = true
			items = append(items, it)
		}
	}
	scraper.Debugf(1, "%s: %d video URLs in sitemaps", siteID, len(items))

	// Drop already-known IDs on incremental runs.
	work := items[:0]
	for _, it := range items {
		if !opts.KnownIDs[it.id] {
			work = append(work, it)
		}
	}

	select {
	case out <- scraper.Progress(len(work)):
	case <-ctx.Done():
		return
	}

	workers := defaultWorker
	if opts.Workers > 0 {
		workers = opts.Workers
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(work), workers)

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

func parseSitemap(body []byte) []sitemapItem {
	var items []sitemapItem
	for _, m := range locRe.FindAllSubmatch(body, -1) {
		u := strings.TrimSpace(string(m[1]))
		slugM := videoSlugRe.FindStringSubmatch(u)
		if slugM == nil {
			continue
		}
		items = append(items, sitemapItem{id: slugM[1], url: u})
	}
	return items
}

// ---- detail parsing ----

func (s *Scraper) fetchScene(ctx context.Context, studioURL string, it sitemapItem, now time.Time) (models.Scene, error) {
	body, err := s.get(ctx, it.url)
	if err != nil {
		return models.Scene{}, err
	}

	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       it.url,
		Studio:    studioName,
		ScrapedAt: now,
	}

	og := parseutil.OpenGraph(body)

	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(vo.Name))
		scene.Description = html.UnescapeString(strings.TrimSpace(vo.Description))
		scene.Duration = parseutil.ParseDurationISO(vo.Duration)
		scene.Date = parseDate(firstNonEmpty(vo.UploadDate, vo.DatePublished))
		if t := strings.TrimSpace(vo.ThumbnailURL); t != "" {
			scene.Thumbnail = t
		}
		for _, a := range vo.Actors {
			if n := strings.TrimSpace(a); n != "" {
				scene.Performers = append(scene.Performers, n)
			}
		}
	}

	// og: fallbacks.
	if scene.Title == "" {
		if t := og["og:title"]; t != "" {
			title := html.UnescapeString(strings.TrimSpace(t))
			title = strings.TrimSuffix(title, " - VR PORN")
			scene.Title = strings.TrimSpace(title)
		}
	}
	if scene.Thumbnail == "" {
		if img := og["og:image"]; img != "" {
			scene.Thumbnail = html.UnescapeString(strings.TrimSpace(img))
		}
	}
	if scene.Description == "" {
		if d := og["og:description"]; d != "" {
			scene.Description = html.UnescapeString(strings.TrimSpace(d))
		}
	}

	return scene, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parseDate parses uploadDate values like "2026-06-24T07:53:39Z".
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := parseutil.TryParseDate(s, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

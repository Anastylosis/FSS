// Package reflectivedesire scrapes Reflective Desire (reflectivedesire.com), a
// latex bondage site. There is no paginated listing worth crawling, but
// sitemap.xml enumerates every /videos/{slug}/ page. The runner reads the
// sitemap, then fans out a worker pool over the detail pages. Each video page
// carries a schema.org VideoObject in JSON-LD with the title, description,
// thumbnail, duration (PT...) and upload date, plus the performers as actors.
// Sitemap entries that are category pages (no VideoObject) are skipped.
package reflectivedesire

import (
	"context"
	"encoding/json"
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
	siteID        = "reflectivedesire"
	studioName    = "Reflective Desire"
	detailWorkers = 6
)

var siteBase = "https://reflectivedesire.com"

// sitemapURL is a var so tests can point it at a local httptest server.
var sitemapURL = siteBase + "/sitemap.xml"

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
	return []string{"reflectivedesire.com", "reflectivedesire.com/videos/{slug}/"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?reflectivedesire\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	locRe       = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	videoSlugRe = regexp.MustCompile(`/videos/([^/]+)/?$`)
	jsonLDRe    = regexp.MustCompile(`(?s)<script[^>]+type="application/ld\+json"[^>]*>(.*?)</script>`)
)

// rdVideo is the subset of the schema.org VideoObject we consume. A local
// struct (instead of parseutil.ExtractVideoObject) is required because the
// site emits "keywords" as a JSON array, which the shared helper's
// string-typed Keywords field fails to unmarshal — dropping the whole object.
type rdVideo struct {
	name, description, thumbnail, duration, uploadDate, datePublished string
	actors                                                            []string
}

func parseDetail(body []byte) *rdVideo {
	for _, m := range jsonLDRe.FindAllSubmatch(body, -1) {
		var probe struct {
			Type string `json:"@type"`
		}
		if json.Unmarshal(m[1], &probe) != nil || probe.Type != "VideoObject" {
			continue
		}
		var raw struct {
			Name          string `json:"name"`
			Description   string `json:"description"`
			ThumbnailURL  string `json:"thumbnailUrl"`
			Duration      string `json:"duration"`
			UploadDate    string `json:"uploadDate"`
			DatePublished string `json:"datePublished"`
			Actor         []struct {
				Name string `json:"name"`
			} `json:"actor"`
		}
		if json.Unmarshal(m[1], &raw) != nil {
			continue
		}
		v := &rdVideo{
			name:          raw.Name,
			description:   raw.Description,
			thumbnail:     raw.ThumbnailURL,
			duration:      raw.Duration,
			uploadDate:    raw.UploadDate,
			datePublished: raw.DatePublished,
		}
		for _, a := range raw.Actor {
			if n := strings.TrimSpace(a.Name); n != "" {
				v.actors = append(v.actors, n)
			}
		}
		return v
	}
	return nil
}

type sitemapItem struct {
	id  string
	url string
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
	scraper.Debugf(1, "reflectivedesire: sitemap has %d video URLs", len(items))

	work := items[:0]
	for _, it := range items {
		if !opts.KnownIDs[it.id] {
			work = append(work, it)
		}
	}
	if len(opts.KnownIDs) > 0 {
		scraper.Debugf(1, "reflectivedesire: %d new scenes after known-ID filter", len(work))
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
	scraper.Debugf(1, "reflectivedesire: fetching %d details with %d workers", len(work), workers)

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
				scene, ok, err := s.fetchScene(ctx, studioURL, it, now)
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("scene %s: %w", it.id, err)):
					case <-ctx.Done():
						return
					}
					continue
				}
				if !ok {
					continue // category page without a VideoObject
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
	for _, m := range locRe.FindAllStringSubmatch(string(body), -1) {
		loc := strings.TrimSpace(m[1])
		sm := videoSlugRe.FindStringSubmatch(loc)
		if sm == nil {
			continue
		}
		slug := sm[1]
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		items = append(items, sitemapItem{id: slug, url: loc})
	}
	return items, nil
}

func (s *Scraper) fetchScene(ctx context.Context, studioURL string, it sitemapItem, now time.Time) (models.Scene, bool, error) {
	body, err := s.get(ctx, it.url)
	if err != nil {
		return models.Scene{}, false, err
	}

	vo := parseDetail(body)
	if vo == nil {
		return models.Scene{}, false, nil
	}

	scene := models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		URL:         it.url,
		Studio:      studioName,
		ScrapedAt:   now,
		Title:       cleanText(vo.name),
		Description: cleanText(vo.description),
		Thumbnail:   strings.TrimSpace(vo.thumbnail),
		Performers:  vo.actors,
		Duration:    parseutil.ParseDurationISO(vo.duration),
	}
	if scene.Title == "" || scene.Thumbnail == "" {
		og := parseutil.OpenGraph(body)
		if scene.Title == "" {
			scene.Title = cleanText(og["og:title"])
		}
		if scene.Thumbnail == "" {
			scene.Thumbnail = strings.TrimSpace(html.UnescapeString(og["og:image"]))
		}
	}
	if d := parseDate(vo.uploadDate); !d.IsZero() {
		scene.Date = d
	} else if d := parseDate(vo.datePublished); !d.IsZero() {
		scene.Date = d
	}

	return scene, true, nil
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
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

var tagStripRe = regexp.MustCompile(`<[^>]+>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

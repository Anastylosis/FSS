// Package realjamvr scrapes RealJamVR (realjamvr.com), a Django-backed VR site.
// There is no JSON listing, but two scene sitemaps (sitemap-scenes-1.xml and
// sitemap-scenes-2.xml) enumerate every /scene/{slug}/ page along with a poster
// thumbnail and <lastmod>. The runner reads both sitemaps, then fans out a
// worker pool over the detail pages: each page yields the title, description,
// duration, starring performers and an on-page publish date (falling back to
// the sitemap <lastmod>).
package realjamvr

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
	siteID        = "realjamvr"
	studioName    = "RealJamVR"
	defaultWorker = 6
)

// sitemapURLs are vars (not const) so tests can point them at a local server.
var sitemapURLs = []string{
	"https://realjamvr.com/sitemap-scenes-1.xml",
	"https://realjamvr.com/sitemap-scenes-2.xml",
}

// Scraper implements scraper.StudioScraper for RealJamVR.
type Scraper struct {
	Client *http.Client
}

// New constructs a RealJamVR scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"realjamvr.com",
		"realjamvr.com/scene/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?realjamvr\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

var (
	urlBlockRe  = regexp.MustCompile(`(?s)<url>(.*?)</url>`)
	locRe       = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	lastmodRe   = regexp.MustCompile(`<lastmod>\s*([^<]+?)\s*</lastmod>`)
	thumbLocRe  = regexp.MustCompile(`<video:thumbnail_loc>\s*([^<]+?)\s*</video:thumbnail_loc>`)
	sceneSlugRe = regexp.MustCompile(`/scene/([^/]+)/?$`)
)

type sitemapItem struct {
	id      string
	url     string
	thumb   string
	lastmod time.Time
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
	scraper.Debugf(1, "%s: %d scene URLs in sitemaps", siteID, len(items))

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
	for _, block := range urlBlockRe.FindAllSubmatch(body, -1) {
		loc := locRe.FindSubmatch(block[1])
		if loc == nil {
			continue
		}
		u := strings.TrimSpace(string(loc[1]))
		slugM := sceneSlugRe.FindStringSubmatch(u)
		if slugM == nil {
			continue
		}
		it := sitemapItem{id: slugM[1], url: u}
		if tm := thumbLocRe.FindSubmatch(block[1]); tm != nil {
			it.thumb = html.UnescapeString(strings.TrimSpace(string(tm[1])))
		}
		if lm := lastmodRe.FindSubmatch(block[1]); lm != nil {
			if t, err := parseutil.TryParseDate(strings.TrimSpace(string(lm[1])), "2006-01-02"); err == nil {
				it.lastmod = t
			}
		}
		items = append(items, it)
	}
	return items
}

// ---- detail parsing ----

var (
	titleRe    = regexp.MustCompile(`(?s)<title>\s*(.*?)\s*(?:\|\s*RealJamVR)?\s*</title>`)
	metaDescRe = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	durationRe = regexp.MustCompile(`(?s)bi-clock-history.*?<span[^>]*>\s*([0-9:]+)\s*</span>`)
	starringRe = regexp.MustCompile(`(?s)Starring:(.*?)</div>`)
	actorRe    = regexp.MustCompile(`<a href="/actor/[^"]*">\s*([^<]+?)\s*</a>`)
	dateRe     = regexp.MustCompile(`(January|February|March|April|May|June|July|August|September|October|November|December) \d{1,2}, \d{4}`)
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
		Thumbnail: it.thumb,
		Date:      it.lastmod,
		ScrapedAt: now,
	}

	if m := titleRe.FindStringSubmatch(page); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	if m := metaDescRe.FindStringSubmatch(page); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	if m := durationRe.FindStringSubmatch(page); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}

	if block := starringRe.FindStringSubmatch(page); block != nil {
		seen := map[string]bool{}
		for _, m := range actorRe.FindAllStringSubmatch(block[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(m[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				scene.Performers = append(scene.Performers, name)
			}
		}
	}

	if m := dateRe.FindString(page); m != "" {
		if d, err := parseutil.TryParseDate(m, "January 2, 2006"); err == nil {
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

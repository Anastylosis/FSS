// Package putalocura scrapes Puta Locura (putalocura.com), the Spanish (Torbe)
// POV/gonzo studio. Scenes are enumerated from the XML sitemap rather than a
// paginated listing: the sitemap index points at sitemap-scenes-es.xml, which
// lists every scene URL. The sitemap's <loc> values carry a double-"www"
// typo (https://www.www.putalocura.com/...) that 404s, so each URL is
// normalised back to a single "www." before fetching. Each detail page is a
// server-rendered HTML document (no OpenGraph/JSON-LD); a concurrent worker
// pool fetches them and parses title, performer, date, duration and the
// trailer preview.
package putalocura

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	detailWorkers = 6
)

// siteBase is a var (not const) so tests can point it at a local httptest server.
var siteBase = "https://www.putalocura.com"

type Scraper struct{ client *http.Client }

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "putalocura" }

func (s *Scraper) Patterns() []string {
	return []string{
		"putalocura.com",
		"putalocura.com/{category}/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?putalocura\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	locRe       = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	dwwwRe      = regexp.MustCompile(`://www\.www\.`)
	titleRe     = regexp.MustCompile(`(?s)<span class="model-name">(.*?)</span>\s*<span class="dash">.*?</span>\s*<span class="site-name">(.*?)</span>`)
	releasedRe  = regexp.MustCompile(`(?s)released-views[^>]*>\s*<span>([^<]+)</span>\s*-\s*<span>([^<]+)</span>`)
	trailerRe   = regexp.MustCompile(`<source src="(https://sd\.putalocura\.com/trailers/[^"]+)"`)
	descRe      = regexp.MustCompile(`(?s)<p class="desc">(.*?)</div>`)
	durHMRe     = regexp.MustCompile(`(?:(\d+)\s*h)?\s*(?:(\d+)\s*min)?`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
	wsCollapseR = regexp.MustCompile(`\s+`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	defer close(out)

	now := time.Now().UTC()

	urls, err := s.sceneURLs(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "putalocura: %d scene URLs from sitemap", len(urls))

	// Drop already-known scenes so incremental runs don't re-fetch them.
	if len(opts.KnownIDs) > 0 {
		filtered := urls[:0]
		for _, u := range urls {
			if !opts.KnownIDs[sceneID(u)] {
				filtered = append(filtered, u)
			}
		}
		urls = filtered
		scraper.Debugf(1, "putalocura: %d scene URLs after KnownIDs filter", len(urls))
	}

	select {
	case out <- scraper.Progress(len(urls)):
	case <-ctx.Done():
		return
	}

	scraper.Debugf(1, "putalocura: fetching %d details with %d workers", len(urls), workers)

	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				scene, ok := s.toScene(ctx, studioURL, u, now)
				if !ok {
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	for _, u := range urls {
		select {
		case jobs <- u:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

// sceneURLs reads the sitemap index, locates the Spanish scenes sitemap, and
// returns every normalised scene URL.
func (s *Scraper) sceneURLs(ctx context.Context) ([]string, error) {
	idx, err := s.get(ctx, siteBase+"/sitemap.xml")
	if err != nil {
		return nil, fmt.Errorf("sitemap index: %w", err)
	}
	var scenesMap string
	for _, m := range locRe.FindAllStringSubmatch(string(idx), -1) {
		loc := normalizeURL(m[1])
		if strings.Contains(loc, "sitemap-scenes-es.xml") {
			scenesMap = loc
			break
		}
	}
	if scenesMap == "" {
		return nil, fmt.Errorf("sitemap index: scenes sitemap not found")
	}

	body, err := s.get(ctx, scenesMap)
	if err != nil {
		return nil, fmt.Errorf("scenes sitemap: %w", err)
	}
	matches := locRe.FindAllStringSubmatch(string(body), -1)
	urls := make([]string, 0, len(matches))
	for _, m := range matches {
		urls = append(urls, normalizeURL(m[1]))
	}
	return urls, nil
}

// normalizeURL collapses the sitemap's double-"www" typo so the URL resolves.
func normalizeURL(u string) string {
	return dwwwRe.ReplaceAllString(strings.TrimSpace(u), "://www.")
}

// sceneID returns the slug (last path segment) used as the scene ID.
func sceneID(u string) string {
	if p, err := url.Parse(u); err == nil {
		seg := strings.Split(strings.Trim(p.Path, "/"), "/")
		return seg[len(seg)-1]
	}
	parts := strings.Split(strings.Trim(u, "/"), "/")
	return parts[len(parts)-1]
}

// sceneCategory returns the first path segment (e.g. "micro-escena").
func sceneCategory(u string) string {
	if p, err := url.Parse(u); err == nil {
		seg := strings.Split(strings.Trim(p.Path, "/"), "/")
		if len(seg) > 1 {
			return seg[0]
		}
	}
	return ""
}

func (s *Scraper) toScene(ctx context.Context, studioURL, sceneURL string, now time.Time) (models.Scene, bool) {
	body, err := s.get(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, false
	}
	page := string(body)

	scene := models.Scene{
		ID:        sceneID(sceneURL),
		SiteID:    "putalocura",
		StudioURL: studioURL,
		URL:       sceneURL,
		Studio:    "Puta Locura",
		ScrapedAt: now,
	}
	if cat := sceneCategory(sceneURL); cat != "" {
		scene.Categories = []string{cat}
	}

	if m := titleRe.FindStringSubmatch(page); m != nil {
		scene.Title = cleanText(m[1])
		if p := cleanText(m[2]); p != "" {
			scene.Performers = []string{p}
		}
	}
	if scene.Title == "" {
		scraper.Debugf(1, "putalocura: no title on %s", sceneURL)
		return models.Scene{}, false
	}

	if m := releasedRe.FindStringSubmatch(page); m != nil {
		if d, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "02/01/2006"); err == nil {
			scene.Date = d.UTC()
		}
		scene.Duration = parseDuration(m[2])
	}

	if m := trailerRe.FindStringSubmatch(page); m != nil {
		scene.Preview = html.UnescapeString(m[1])
		scene.Thumbnail = scene.Preview
	}

	if m := descRe.FindStringSubmatch(page); m != nil {
		scene.Description = cleanText(m[1])
	}

	scraper.Debugf(3, "putalocura: parsed %s (%s)", scene.ID, scene.Title)
	return scene, true
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// parseDuration converts strings like "28min" or "1h 5min" to seconds.
func parseDuration(s string) int {
	m := durHMRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	hours, _ := strconv.Atoi(m[1])
	mins, _ := strconv.Atoi(m[2])
	return hours*3600 + mins*60
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsCollapseR.ReplaceAllString(s, " "))
}

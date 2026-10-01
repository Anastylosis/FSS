// Package pjgirls scrapes PJ Girls (pjgirls.com), a standalone locale-prefixed
// PHP CMS. The full catalogue is enumerated from the site sitemap
// (https://www.pjgirls.com/sitemap.xml), which lists every scene as an
// /en/video/{id}-{slug}/ URL. Each detail page is server-rendered HTML with no
// OpenGraph/JSON-LD, so a concurrent worker pool fetches them and parses the
// title, date, performers, duration and thumbnail. The numeric {id} in the URL
// is the stable Scene.ID.
package pjgirls

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
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
	siteID        = "pjgirls"
	studio        = "PJ Girls"
	base          = "https://www.pjgirls.com"
	detailWorkers = 6
)

type Scraper struct {
	client *http.Client
	// siteBase is the sitemap/detail host; overridable in tests.
	siteBase string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), siteBase: base}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"pjgirls.com/sitemap.xml",
		"pjgirls.com",
		"pjgirls.com/en/video/{id}-{slug}/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?pjgirls\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	locRe = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	idRe  = regexp.MustCompile(`/video/(\d+)-`)
	// titleRe pulls the clean title and bracketed date out of the page <title>,
	// e.g. "Joy of pee - porn video [January 5, 2013] | PJGirls".
	titleRe  = regexp.MustCompile(`(?s)<title>\s*(.*?)\s*-\s*porn video\s*\[([^\]]+)\]`)
	infoRe   = regexp.MustCompile(`(?s)<div class="info">(.*?)</div>`)
	modelRe  = regexp.MustCompile(`<a href="/[^"]*/model/[^"]*"\s+title="([^"]*)"`)
	lengthRe = regexp.MustCompile(`LENGTH:\s*([0-9:]+)`)
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
	scraper.Debugf(1, "%s: %d scene URLs from sitemap", siteID, len(urls))

	// Drop already-known scenes so incremental runs don't re-fetch them.
	if len(opts.KnownIDs) > 0 {
		filtered := urls[:0]
		for _, u := range urls {
			if !opts.KnownIDs[sceneID(u)] {
				filtered = append(filtered, u)
			}
		}
		urls = filtered
		scraper.Debugf(1, "%s: %d scene URLs after KnownIDs filter", siteID, len(urls))
	}

	select {
	case out <- scraper.Progress(len(urls)):
	case <-ctx.Done():
		return
	}

	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(urls), workers)

	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				scene, err := s.toScene(ctx, studioURL, u, now)
				if err != nil {
					select {
					case out <- scraper.Error(err):
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
				if !scraper.Pace(ctx, opts.Delay) {
					return
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

// sceneURLs reads the sitemap and returns every /en/video/ detail URL rebased
// onto s.siteBase (the sitemap's <loc> values are http:// and may point at a
// different host than the configured base).
func (s *Scraper) sceneURLs(ctx context.Context) ([]string, error) {
	body, err := s.get(ctx, s.siteBase+"/sitemap.xml")
	if err != nil {
		return nil, fmt.Errorf("sitemap: %w", err)
	}
	matches := locRe.FindAllStringSubmatch(string(body), -1)
	urls := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		p, err := url.Parse(strings.TrimSpace(m[1]))
		if err != nil {
			continue
		}
		if !strings.Contains(p.Path, "/en/video/") {
			continue
		}
		full := s.siteBase + p.Path
		if seen[full] {
			continue
		}
		seen[full] = true
		urls = append(urls, full)
	}
	return urls, nil
}

// sceneID returns the numeric project id embedded in a /video/{id}-{slug}/ URL.
func sceneID(u string) string {
	if m := idRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

func (s *Scraper) toScene(ctx context.Context, studioURL, sceneURL string, now time.Time) (models.Scene, error) {
	body, err := s.get(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("fetch %s: %w", sceneURL, err)
	}
	page := string(body)

	id := sceneID(sceneURL)
	if id == "" {
		return models.Scene{}, fmt.Errorf("no scene id in %s", sceneURL)
	}

	scene := models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       sceneURL,
		Studio:    studio,
		Thumbnail: fmt.Sprintf("%s/photo.php?type=intro2&id_project=%s", s.siteBase, id),
		ScrapedAt: now,
	}

	m := titleRe.FindStringSubmatch(page)
	if m == nil {
		return models.Scene{}, fmt.Errorf("no title on %s", sceneURL)
	}
	scene.Title = cleanText(m[1])
	if scene.Title == "" {
		return models.Scene{}, fmt.Errorf("empty title on %s", sceneURL)
	}
	if d, err := parseutil.TryParseDate(strings.TrimSpace(m[2]), "January 2, 2006"); err == nil {
		scene.Date = d.UTC()
	}

	// Performers and duration live in the scene's own <div class="info"> block;
	// scoping to it avoids picking up the "SIMILAR VIDEOS" cards further down.
	if info := infoRe.FindStringSubmatch(page); info != nil {
		block := info[1]
		seen := make(map[string]bool)
		for _, pm := range modelRe.FindAllStringSubmatch(block, -1) {
			name := cleanText(pm[1])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			scene.Performers = append(scene.Performers, name)
		}
		if lm := lengthRe.FindStringSubmatch(block); lm != nil {
			scene.Duration = parseutil.ParseDurationColon(lm[1])
		}
	}

	scraper.Debugf(3, "%s: parsed %s (%s)", siteID, scene.ID, scene.Title)
	return scene, nil
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

var (
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
	wsCollapseR = regexp.MustCompile(`\s+`)
)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsCollapseR.ReplaceAllString(s, " "))
}

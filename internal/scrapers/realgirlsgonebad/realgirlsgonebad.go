// Package realgirlsgonebad scrapes Real Girls Gone Bad (realgirlsgonebad.com),
// a NATS tour site. The video listing lives at
// /tour/categories/videos_{N}_d.html and links to per-scene trailer pages at
// /tour/trailers/{slug}.html. The listing carries only the trailer links, so a
// detail worker pool fetches each trailer page for the title, content id,
// runtime, publish date, description and thumbnail.
package realgirlsgonebad

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
	siteID        = "realgirlsgonebad"
	studio        = "Real Girls Gone Bad"
	detailWorkers = 4
)

// baseURL is a var so tests can point it at an httptest server.
var baseURL = "https://www.realgirlsgonebad.com"

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?realgirlsgonebad\.com(?:/|$)`)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper {
	c := httpx.NewClient(30 * time.Second)
	// The NATS warning gate answers the listing request with a 302 whose body
	// already contains the full listing; following the redirect lands on the
	// empty warning page. Keep the 302 response instead of following it.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Scraper{Client: c}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"realgirlsgonebad.com",
		"realgirlsgonebad.com/tour/categories/videos_{N}_d.html",
	}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	trailerRe   = regexp.MustCompile(`href="([^"]*trailers/[^"]+\.html)"`)
	runtimeRe   = regexp.MustCompile(`Runtime:</strong>\s*([0-9:]+)`)
	addedRe     = regexp.MustCompile(`Added:</strong>\s*([0-9]{1,2}\s+[A-Za-z]+,\s*[0-9]{4})`)
	descRe      = regexp.MustCompile(`(?s)<p>(.*?)</p>\s*<div class="eDtls"`)
	contentIDRe = regexp.MustCompile(`/contentthumbs/(\d+)`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := listingURL(page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		urls := parseListing(body, seen)
		if len(urls) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, urls, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func listingURL(page int) string {
	return fmt.Sprintf("%s/tour/categories/videos_%d_d.html", baseURL, page)
}

// parseListing returns the de-duplicated trailer URLs found on a listing page.
func parseListing(body []byte, seen map[string]bool) []string {
	var urls []string
	for _, m := range trailerRe.FindAllStringSubmatch(string(body), -1) {
		u := normalizeURL(m[1])
		if seen[u] {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	return urls
}

func normalizeURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	if strings.HasPrefix(u, "/") {
		return baseURL + u
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	// Bare-relative (e.g. "trailers/{slug}.html") resolves against the /tour/
	// base href the listing page declares.
	return baseURL + "/tour/" + u
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, urls []string, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(urls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
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
			scenes[i] = s.toScene(ctx, studioURL, u, now)
		}(i, u)
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

func (s *Scraper) toScene(ctx context.Context, studioURL, u string, now time.Time) models.Scene {
	body, err := s.fetchPage(ctx, u)
	if err != nil {
		return models.Scene{}
	}
	return parseDetail(body, studioURL, u, now)
}

func parseDetail(body []byte, studioURL, u string, now time.Time) models.Scene {
	og := parseutil.OpenGraph(body)
	scene := models.Scene{
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       u,
		Studio:    studio,
		ScrapedAt: now,
	}

	title := html.UnescapeString(strings.TrimSpace(og["og:title"]))
	title = strings.TrimSuffix(title, " | "+studio)
	scene.Title = strings.TrimSpace(title)

	if img := og["og:image"]; img != "" {
		thumb := normalizeURL(strings.TrimSpace(img))
		scene.Thumbnail = thumb
		if m := contentIDRe.FindStringSubmatch(thumb); m != nil {
			scene.ID = m[1]
		}
	}
	// Fall back to slug-based id only if no content id was found, so the scene
	// still has a stable identity.
	if scene.ID == "" {
		scene.ID = slugFromURL(u)
	}

	page := string(body)
	if m := runtimeRe.FindStringSubmatch(page); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}
	if m := addedRe.FindStringSubmatch(page); m != nil {
		if d, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "2 January, 2006"); err == nil {
			scene.Date = d
		}
	}
	if m := descRe.FindStringSubmatch(page); m != nil {
		scene.Description = cleanText(m[1])
	}
	return scene
}

func slugFromURL(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		u = u[i+1:]
	}
	return strings.TrimSuffix(u, ".html")
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: gateHeaders(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// gateHeaders adds the NATS warning-gate acceptance cookie to standard browser
// headers so the age/content warning interstitial is skipped.
func gateHeaders() map[string]string {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Cookie"] = "warning=accepted"
	return h
}

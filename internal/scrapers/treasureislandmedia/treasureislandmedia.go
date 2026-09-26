// Package treasureislandmedia scrapes Treasure Island Media
// (treasureislandmedia.com), a gay studio running a single Drupal catalog
// whose sub-brands live on subdomains (timfuck, timsuck, timjack, bruthaload,
// ghr, classics, latinloads). The /scenes listing yields scene detail links;
// each detail page exposes anonymous OpenGraph metadata. The og:url host
// identifies the sub-brand, the og:image cover filename supplies the numeric
// scene ID, and og:updated_time gives the publish date.
package treasureislandmedia

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
	siteID        = "treasureislandmedia"
	studioName    = "Treasure Island Media"
	detailWorkers = 4
)

// baseURL is a var (not const) so the unit test can point it at httptest.
var baseURL = "https://treasureislandmedia.com"

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
	return []string{
		"treasureislandmedia.com/scenes?channel=All&page={n}",
		"treasureislandmedia.com/scenes/{slug}",
		"{subdomain}.treasureislandmedia.com/scenes/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:[a-z]+\.)?treasureislandmedia\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// Listing card: anchors to a scene detail page. On the live site these are
	// absolute URLs on a brand subdomain (e.g.
	// https://timfuck.treasureislandmedia.com/scenes/{slug}); relative
	// /scenes/{slug} links are also accepted and resolved against baseURL. The
	// bare /scenes listing/pagination links carry a query string or no trailing
	// slug, so they do not match.
	sceneLinkRe = regexp.MustCompile(`href="((?:https?://[^"]+)?/scenes/[^"?#]+)"`)

	coverIDRe = regexp.MustCompile(`/covers/(\d+)\.`)

	// Cast: the "Starring" tab lists models as subtitle anchors to /men/{id}.
	castLinkRe = regexp.MustCompile(`class="thumbnail-subtitle-a"\s+href="[^"]*/men/[^"]*"[^>]*>([^<]+)<`)
	// Director: a /directors/{slug} taxonomy link.
	directorRe = regexp.MustCompile(`href="/directors/[^"]+"[^>]*>([^<]+)<`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/scenes?channel=All&page=%d", baseURL, page)
		urls, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		// Stop when a page yields no scene links.
		fresh := urls[:0]
		for _, u := range urls {
			if !seen[u] {
				seen[u] = true
				fresh = append(fresh, u)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]string, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	matches := sceneLinkRe.FindAllStringSubmatch(string(body), -1)
	urls := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		u := html.UnescapeString(m[1])
		if !strings.HasPrefix(u, "http") {
			u = baseURL + u
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	scraper.Debugf(1, "treasureislandmedia: listing %s -> %d scene links", pageURL, len(urls))
	return urls, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, urls []string, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(urls))
	scraper.Debugf(1, "treasureislandmedia: fetching %d details with %d workers", len(urls), workers)
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
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scenes[i] = s.toScene(ctx, studioURL, u, now)
		}(i, u)
	}
	wg.Wait()
	// Drop any scenes left zero-valued by a cancelled context or parse failure.
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Scraper) toScene(ctx context.Context, studioURL, sceneURL string, now time.Time) models.Scene {
	body, err := s.get(ctx, sceneURL)
	if err != nil {
		return models.Scene{}
	}
	detail := string(body)
	og := parseutil.OpenGraph(body)

	scene := models.Scene{
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       sceneURL,
		Studio:    studioName,
		ScrapedAt: now,
	}

	if v := og["og:title"]; v != "" {
		scene.Title = html.UnescapeString(strings.TrimSpace(v))
	}
	if v := og["og:description"]; v != "" {
		scene.Description = strings.TrimSpace(html.UnescapeString(v))
	}
	if v := og["og:image"]; v != "" {
		scene.Thumbnail = html.UnescapeString(strings.TrimSpace(v))
		if m := coverIDRe.FindStringSubmatch(scene.Thumbnail); m != nil {
			scene.ID = m[1]
		}
	}
	if v := og["og:updated_time"]; v != "" {
		if d, derr := parseutil.TryParseDate(strings.TrimSpace(v), time.RFC3339); derr == nil {
			scene.Date = d.UTC()
		}
	}
	if v := og["og:url"]; v != "" {
		ogURL := html.UnescapeString(strings.TrimSpace(v))
		scene.URL = ogURL
		id, name := brandFromURL(ogURL)
		if id != "" {
			scene.SiteID = id
			scene.Studio = name
		}
	}

	if m := directorRe.FindStringSubmatch(detail); m != nil {
		scene.Director = cleanText(m[1])
	}

	var performers []string
	seen := make(map[string]bool)
	for _, m := range castLinkRe.FindAllStringSubmatch(detail, -1) {
		name := cleanText(m[1])
		if name != "" && !seen[name] {
			seen[name] = true
			performers = append(performers, name)
		}
	}
	scene.Performers = performers

	return scene
}

// brandSubdomains maps a sub-brand subdomain to its display name. The SiteID
// returned for a known or unknown subdomain is the subdomain itself; the main
// host (no subdomain / www) falls back to the parent studio.
var brandSubdomains = map[string]string{
	"timsuck":    "TIM Suck",
	"timfuck":    "TIM Fuck",
	"timjack":    "TIM Jack",
	"bruthaload": "Bruthaload",
	"ghr":        "Grindhouse Raw",
	"classics":   "TIM Classics",
	"latinloads": "Latin Loads",
}

// brandFromURL derives (SiteID, Studio) from a scene og:url host. The host's
// leading subdomain identifies the sub-brand; the bare/www host maps to the
// parent studio.
func brandFromURL(rawURL string) (string, string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	const base = "treasureislandmedia.com"
	if host == base {
		return siteID, studioName
	}
	if !strings.HasSuffix(host, "."+base) {
		return "", ""
	}
	sub := strings.TrimSuffix(host, "."+base)
	if sub == "" || sub == "www" {
		return siteID, studioName
	}
	if name, ok := brandSubdomains[sub]; ok {
		return sub, name
	}
	return sub, sub
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

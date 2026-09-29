// Package frolicme scrapes Frolic Me (frolicme.com), a WordPress block-theme
// erotic-film site sitting behind Cloudflare. Films (the `cpt_films` post type)
// are the videos. Scenes are enumerated from the XML sitemap index, whose
// cpt_films child sitemap lists every film URL with a <lastmod>.
//
// Quirk: Cloudflare serves film detail pages with HTTP 403 but returns the FULL
// real page body (not a JS challenge), so detail fetches go through
// httpx.DoWithStatus (the documented escape hatch) and accept the 403 body. The
// sitemaps themselves return a clean 200 via httpx.Do.
//
// Each film page carries a Yoast JSON-LD WebPage node (name, datePublished,
// description, thumbnailUrl). Performers are body links to /models/{slug}/ with
// rel="tag"; content tags are /porn-films/{slug}/ links with rel="tag".
// Duration is not present in structured data, so it is left unset.
package frolicme

import (
	"context"
	"encoding/json"
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
	siteID        = "frolicme"
	studio        = "Frolic Me"
	detailWorkers = 6
)

// siteBase is a var (not const) so tests can point it at a local httptest server.
var siteBase = "https://www.frolicme.com"

type Scraper struct {
	client *http.Client
	base   string // overridable in tests
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"frolicme.com",
		"frolicme.com/films/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?frolicme\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	locRe       = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	performerRe = regexp.MustCompile(`href="https?://(?:www\.)?[^"/]*/models/[^"/]+/"\s+rel="tag">([^<]+)</a>`)
	tagRe       = regexp.MustCompile(`href="https?://(?:www\.)?[^"/]*/porn-films/[^"/]+/"\s+rel="tag">([^<]+)</a>`)
	ldRe        = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*?)</script>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	defer close(out)

	now := time.Now().UTC()

	urls, err := s.filmURLs(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "frolicme: %d film URLs from sitemap", len(urls))

	// Drop already-known scenes so incremental runs don't re-fetch them.
	if len(opts.KnownIDs) > 0 {
		filtered := urls[:0]
		for _, u := range urls {
			if !opts.KnownIDs[filmID(u)] {
				filtered = append(filtered, u)
			}
		}
		urls = filtered
		scraper.Debugf(1, "frolicme: %d film URLs after KnownIDs filter", len(urls))
	}

	select {
	case out <- scraper.Progress(len(urls)):
	case <-ctx.Done():
		return
	}

	scraper.Debugf(1, "frolicme: fetching %d details with %d workers", len(urls), workers)

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
					case out <- scraper.Error(fmt.Errorf("frolicme: %s: %w", u, err)):
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

// filmURLs reads the sitemap index, locates the cpt_films child sitemap, and
// returns every film URL it lists.
func (s *Scraper) filmURLs(ctx context.Context) ([]string, error) {
	idx, err := s.get(ctx, s.base+"/sitemap.xml")
	if err != nil {
		return nil, fmt.Errorf("sitemap index: %w", err)
	}
	var filmsMap string
	for _, m := range locRe.FindAllStringSubmatch(string(idx), -1) {
		if strings.Contains(m[1], "cpt_films-sitemap") {
			filmsMap = strings.TrimSpace(m[1])
			break
		}
	}
	if filmsMap == "" {
		return nil, fmt.Errorf("sitemap index: cpt_films sitemap not found")
	}
	// Point the child sitemap at the overridden base (tests serve everything
	// from one host).
	filmsMap = s.rebase(filmsMap)

	body, err := s.get(ctx, filmsMap)
	if err != nil {
		return nil, fmt.Errorf("films sitemap: %w", err)
	}
	matches := locRe.FindAllStringSubmatch(string(body), -1)
	urls := make([]string, 0, len(matches))
	for _, m := range matches {
		loc := strings.TrimSpace(m[1])
		if !strings.Contains(loc, "/films/") {
			continue
		}
		urls = append(urls, s.rebase(loc))
	}
	return urls, nil
}

// rebase rewrites the scheme+host of a sitemap URL to s.base so offline tests
// (which serve every document from one httptest host) resolve correctly.
func (s *Scraper) rebase(raw string) string {
	if s.base == siteBase {
		return raw
	}
	p, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return s.base + p.Path
}

// filmID returns the slug (last non-empty path segment) used as the scene ID.
func filmID(u string) string {
	if p, err := url.Parse(u); err == nil {
		seg := strings.Split(strings.Trim(p.Path, "/"), "/")
		return seg[len(seg)-1]
	}
	parts := strings.Split(strings.Trim(u, "/"), "/")
	return parts[len(parts)-1]
}

// ldNode is the subset of a Yoast JSON-LD @graph node we consume.
type ldNode struct {
	Type          string `json:"@type"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DatePublished string `json:"datePublished"`
	ThumbnailURL  string `json:"thumbnailUrl"`
}

type ldGraph struct {
	Graph []ldNode `json:"@graph"`
}

// parseWebPage extracts the WebPage node (title, date, description, thumbnail)
// from the JSON-LD blocks in a film page.
func parseWebPage(body []byte) (ldNode, bool) {
	for _, m := range ldRe.FindAllSubmatch(body, -1) {
		var g ldGraph
		if err := json.Unmarshal(m[1], &g); err != nil {
			continue
		}
		for _, n := range g.Graph {
			if n.Type == "WebPage" {
				return n, true
			}
		}
	}
	return ldNode{}, false
}

func (s *Scraper) toScene(ctx context.Context, studioURL, filmURL string, now time.Time) (models.Scene, error) {
	body, err := s.getDetail(ctx, filmURL)
	if err != nil {
		return models.Scene{}, err
	}

	node, ok := parseWebPage(body)
	if !ok {
		return models.Scene{}, fmt.Errorf("no WebPage JSON-LD")
	}

	scene := models.Scene{
		ID:          filmID(filmURL),
		SiteID:      siteID,
		StudioURL:   studioURL,
		URL:         filmURL,
		Title:       cleanText(node.Name),
		Description: cleanText(node.Description),
		Thumbnail:   strings.TrimSpace(node.ThumbnailURL),
		Studio:      studio,
		ScrapedAt:   now,
	}
	if scene.Title == "" {
		// Fall back to OpenGraph title if JSON-LD name was empty.
		if og := parseutil.OpenGraph(body); og["og:title"] != "" {
			scene.Title = cleanText(og["og:title"])
		}
	}
	if scene.Title == "" {
		return models.Scene{}, fmt.Errorf("no title")
	}

	if node.DatePublished != "" {
		if d, err := parseutil.TryParseDate(node.DatePublished, time.RFC3339); err == nil {
			scene.Date = d.UTC()
		}
	}

	scene.Performers = uniqueText(performerRe.FindAllSubmatch(body, -1))
	scene.Tags = uniqueText(tagRe.FindAllSubmatch(body, -1))

	scraper.Debugf(3, "frolicme: parsed %s (%s) performers=%d tags=%d", scene.ID, scene.Title, len(scene.Performers), len(scene.Tags))
	return scene, nil
}

// uniqueText cleans the first capture group of each regex match, de-duplicating
// while preserving order.
func uniqueText(matches [][][]byte) []string {
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var out []string
	for _, m := range matches {
		v := cleanText(string(m[1]))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// get fetches a clean-200 resource (the sitemaps) via httpx.Do.
func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// getDetail fetches a film page. Cloudflare serves these with HTTP 403 but the
// body is the real page, so it uses DoWithStatus and accepts whatever status.
func (s *Scraper) getDetail(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.DoWithStatus(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

var wsCollapseRe = regexp.MustCompile(`\s+`)

func cleanText(s string) string {
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsCollapseRe.ReplaceAllString(s, " "))
}

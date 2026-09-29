// Package joybear scrapes JoyBear Pictures (joybear.com), a custom PHP site
// (assets on media.joybear.com, members split to members.joybear.com).
//
// Enumeration runs off `/sitemap.xml`, which lists all 361 `/movies/{slug}`
// pages. The listing at `/chapters/page-{N}/` works too, but only the
// `page-{N}` form does — `?page=2` and `/chapters/2` both silently return page
// one — and the sitemap costs a single request. Its `<lastmod>` is useless
// (733 of 744 entries carry the same timestamp) and is ignored.
//
// What the site does not publish, anywhere:
//
//   - **Date.** No release date exists on the listing, the detail page or the
//     sitemap, so scenes carry none.
//   - **Duration.** Likewise absent.
//
// Two things worth knowing about the detail page:
//
//   - The `<h1>` is prefixed with "Scene - ", which is stripped.
//   - The categories block is **rendered inside an HTML comment**. The values
//     are real per-scene metadata the template simply does not display, so they
//     are read out of the comment rather than discarded.
//
// The `<title>` is "Joybear.com | {Series} | {Title}", which is the only place
// the collection a scene belongs to is exposed.
package joybear

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "joybear"
	studioName    = "JoyBear Pictures"
	detailWorkers = 4
)

var siteBase = "https://www.joybear.com"

// Scraper implements scraper.StudioScraper for JoyBear.
type Scraper struct {
	Client *http.Client
}

// New constructs a JoyBear scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"joybear.com",
		"joybear.com/chapters/page-{N}/",
		"joybear.com/movies/{slug}",
		"joybear.com/models/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?joybear\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- sitemap ----

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// movieURLRe matches scene pages; the sitemap also lists models and DVDs.
var movieURLRe = regexp.MustCompile(`/movies/([^/?#]+)$`)

func (s *Scraper) fetchSitemap(ctx context.Context) ([]string, error) {
	body, err := s.fetchPage(ctx, siteBase+"/sitemap.xml")
	if err != nil {
		return nil, err
	}

	var us urlset
	if err := xml.Unmarshal(body, &us); err != nil {
		return nil, fmt.Errorf("parsing sitemap: %w", err)
	}

	slugs := make([]string, 0, len(us.URLs))
	seen := make(map[string]bool)
	for _, u := range us.URLs {
		m := movieURLRe.FindStringSubmatch(u.Loc)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		slugs = append(slugs, m[1])
	}
	return slugs, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	defer close(out)

	slugs, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes in sitemap", siteID, len(slugs))

	select {
	case out <- scraper.Progress(len(slugs)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	work := make(chan string)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching details with %d workers", siteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for slug := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ok := s.toScene(ctx, studioURL, slug, now)
				if !ok {
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

	for _, slug := range slugs {
		select {
		case work <- slug:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return
		}
	}
	close(work)
	wg.Wait()
}

// ---- detail ----

var (
	h1Re = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	// The heading is prefixed with "Scene - ".
	h1PrefixRe = regexp.MustCompile(`^Scene\s*-\s*`)
	// "Joybear.com | {Series} | {Title}" — the only place the collection a
	// scene belongs to is exposed.
	titleRe  = regexp.MustCompile(`<title>([^<]*)</title>`)
	castRe   = regexp.MustCompile(`(?s)<div class="castBlock">(.*?)</div>`)
	modelRe  = regexp.MustCompile(`<a href="/models/[^"]*"><span>([^<]+)</span>`)
	descRe   = regexp.MustCompile(`(?s)<div class="descriptions">.*?<p>(.*?)</p>`)
	posterRe = regexp.MustCompile(`<video[^>]*poster="([^"]+)"`)
	// The categories block is rendered inside an HTML comment; the values are
	// real per-scene metadata the template simply does not display.
	commentedCatsRe = regexp.MustCompile(`(?s)<!--\s*<div class="categories">(.*?)</div>\s*-->`)
	catRe           = regexp.MustCompile(`<li><a[^>]*>([^<]+)</a></li>`)
	tagStripRe      = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL, slug string, now time.Time) (models.Scene, bool) {
	sceneURL := siteBase + "/movies/" + slug

	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, false
	}
	detail := string(body)

	m := h1Re.FindStringSubmatch(detail)
	if m == nil {
		return models.Scene{}, false
	}
	title := cleanText(h1PrefixRe.ReplaceAllString(cleanText(m[1]), ""))
	if title == "" {
		return models.Scene{}, false
	}

	scene := models.Scene{
		// The site exposes no numeric id; the slug is the stable key.
		ID:        slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     title,
		URL:       sceneURL,
		Studio:    studioName,
		Series:    seriesFromTitle(detail),
		ScrapedAt: now,
	}

	if d := descRe.FindStringSubmatch(detail); d != nil {
		scene.Description = cleanText(tagStripRe.ReplaceAllString(d[1], " "))
	}
	if p := posterRe.FindStringSubmatch(detail); p != nil {
		scene.Thumbnail = p[1]
	}
	if cb := castRe.FindStringSubmatch(detail); cb != nil {
		seen := make(map[string]bool)
		for _, pm := range modelRe.FindAllStringSubmatch(cb[1], -1) {
			name := cleanText(pm[1])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			scene.Performers = append(scene.Performers, name)
		}
	}
	scene.Categories = commentedCategories(detail)

	return scene, true
}

// seriesFromTitle reads the collection out of the "Joybear.com | {Series} |
// {Title}" page title.
func seriesFromTitle(detail string) string {
	m := titleRe.FindStringSubmatch(detail)
	if m == nil {
		return ""
	}
	parts := strings.Split(m[1], "|")
	if len(parts) < 3 {
		return ""
	}
	return cleanText(parts[1])
}

// commentedCategories reads the categories the template renders inside an HTML
// comment.
func commentedCategories(detail string) []string {
	cb := commentedCatsRe.FindStringSubmatch(detail)
	if cb == nil {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, m := range catRe.FindAllStringSubmatch(cb[1], -1) {
		cat := cleanText(m[1])
		if cat == "" || seen[cat] {
			continue
		}
		seen[cat] = true
		out = append(out, cat)
	}
	return out
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

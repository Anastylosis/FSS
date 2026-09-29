// Package premiumbukkake scrapes public tour listings from premiumbukkake.com.
//
// The site runs the NATS "tour2" template. The site's homepage 301-redirects to
// /tour2/?nats=..., so all paths live under /tour2/. The latest/all movies
// listing paginates at /tour2/updates/page_{N}.html (page 1 == /tour2/). Each
// listing card's main image links to a NATS signup, but every card also carries
// a js-copy `data-url` pointing at the public detail page
// /tour2/updates/{slug}.html — that slug is the stable scene ID. Detail pages
// are publicly accessible and carry the richest per-scene metadata (og:title /
// og:description, the player thumbnail, trailer, Categories list and the posted
// date), so the scraper extracts detail URLs from the listing and worker-pool
// fetches each one.
package premiumbukkake

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

func init() { scraper.Register(New()) }

const (
	siteID     = "premiumbukkake"
	studioName = "Premium Bukkake"
)

// Scraper implements scraper.StudioScraper for premiumbukkake.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New returns a ready-to-use Premium Bukkake scraper.
func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://premiumbukkake.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"premiumbukkake.com",
		"premiumbukkake.com/tour2/",
		"premiumbukkake.com/tour2/updates/{slug}.html",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?premiumbukkake\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// dataURLRe captures the canonical detail URL each listing card exposes via
	// its js-copy "data-url" attribute. The slug (group 1) is the scene ID. The
	// host is intentionally not anchored so the matcher works against any base
	// (live site or a test server).
	dataURLRe = regexp.MustCompile(`data-url="[^"]*/tour2/updates/([^"/]+)\.html"`)

	// nextPageRe matches the "more"/next-page link (page_{N}.html) that is only
	// present while more pages exist; the last page omits it.
	nextPageRe = regexp.MustCompile(`href="[^"]*/tour2/updates/page_\d+\.html"`)

	// detail-page field extractors. The first slide on a detail page is the
	// scene itself; related videos follow, so the "first match" extractors below
	// must be anchored to that first block.
	slideTitleRe = regexp.MustCompile(`<h2 class="slide_title">([^<]+)</h2>`)
	largeThumbRe = regexp.MustCompile(`data-src="([^"]+)"[^>]*class="large_update_thumb`)
	trailerRe    = regexp.MustCompile(`tload\('([^']+)'\)`)
	categoryRe   = regexp.MustCompile(`<a href="[^"]*/tour2/categories/[^"]+">([^<]+)</a>`)
	postedRe     = regexp.MustCompile(`Posted\s+([A-Z][a-z]+ \d{1,2}, \d{4})`)

	// performerRe pulls the model name from a title like "Cintia Lara #1 - ...".
	performerRe = regexp.MustCompile(`^(.+?)\s+#\d+`)
)

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "premiumbukkake: scraping latest movies listing")

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/tour2/updates/page_%d.html", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		slugs := listingSlugs(body)
		if len(slugs) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := s.fetchDetails(ctx, slugs, opts, now)
		return scraper.PageResult{
			Scenes: scenes,
			Done:   !nextPageRe.Match(body),
		}, nil
	})
}

// listingSlugs returns the per-card detail slugs in document order, deduped.
func listingSlugs(body []byte) []string {
	seen := make(map[string]bool)
	var slugs []string
	for _, m := range dataURLRe.FindAllSubmatch(body, -1) {
		slug := string(m[1])
		if slug != "" && !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

func (s *Scraper) fetchDetails(ctx context.Context, slugs []string, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	scraper.Debugf(1, "premiumbukkake: fetching %d details with %d workers", len(slugs), workers)

	results := make([]models.Scene, len(slugs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, slug := range slugs {
		if ctx.Err() != nil {
			break
		}
		// Known IDs become lightweight stubs so Paginate's early-stop fires
		// without spending a detail fetch.
		if opts.KnownIDs[slug] {
			results[i] = models.Scene{ID: slug, SiteID: siteID}
			continue
		}
		wg.Add(1)
		go func(idx int, slug string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			detailURL := fmt.Sprintf("%s/tour2/updates/%s.html", s.base, slug)
			body, err := s.fetchPage(ctx, detailURL)
			if err != nil {
				scraper.Debugf(1, "premiumbukkake: detail %s failed: %v (skipping)", slug, err)
				return
			}
			results[idx] = s.parseDetail(body, slug, detailURL, now)
		}(i, slug)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" { // failed fetch
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func (s *Scraper) parseDetail(body []byte, slug, detailURL string, now time.Time) models.Scene {
	og := parseutil.OpenGraph(body)

	scene := models.Scene{
		ID:        slug,
		SiteID:    siteID,
		StudioURL: s.base,
		URL:       detailURL,
		Studio:    studioName,
		ScrapedAt: now,
	}

	// Title: prefer the first slide_title (clean, no site suffix), fall back to
	// og:title (which carries a " - PREMIUM BUKKAKE" suffix to strip).
	if m := slideTitleRe.FindSubmatch(body); m != nil {
		scene.Title = cleanText(string(m[1]))
	}
	if scene.Title == "" {
		scene.Title = cleanText(stripSiteSuffix(og["og:title"]))
	}
	if scene.Title == "" {
		// Last resort: derive from the slug so Title is never empty.
		scene.Title = cleanText(strings.ReplaceAll(slug, "-", " "))
	}

	if d := cleanText(og["og:description"]); d != "" {
		scene.Description = d
	}

	if m := largeThumbRe.FindSubmatch(body); m != nil {
		scene.Thumbnail = absURL(s.base, string(m[1]))
	}

	if m := trailerRe.FindSubmatch(body); m != nil {
		scene.Preview = string(m[1])
	}

	if p := performerFromTitle(scene.Title); p != "" {
		scene.Performers = []string{p}
	}

	scene.Tags = detailCategories(body)

	if m := postedRe.FindSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(string(m[1]), "January 2, 2006"); err == nil {
			scene.Date = t.UTC()
		}
	}

	return scene
}

// detailCategories returns the scene's Categories, scoped to the first
// slide_info block so related-video categories below are not mixed in.
func detailCategories(body []byte) []string {
	region := firstSlideInfo(body)
	seen := make(map[string]bool)
	var tags []string
	for _, m := range categoryRe.FindAllSubmatch(region, -1) {
		t := cleanText(string(m[1]))
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	return tags
}

// firstSlideInfo returns the bytes of the first `<div class="slide_info">`
// block, which belongs to the main scene. If the marker is absent, the whole
// body is returned (the category regex is specific enough to be safe).
func firstSlideInfo(body []byte) []byte {
	const marker = `class="slide_info"`
	start := strings.Index(string(body), marker)
	if start < 0 {
		return body
	}
	rest := body[start:]
	// End at the next slide block so we don't bleed into related videos.
	if end := strings.Index(string(rest), `class="swiper-slide`); end > 0 {
		return rest[:end]
	}
	return rest
}

func performerFromTitle(title string) string {
	if m := performerRe.FindStringSubmatch(title); m != nil {
		return cleanText(m[1])
	}
	return ""
}

func stripSiteSuffix(s string) string {
	if i := strings.Index(strings.ToUpper(s), "- PREMIUM BUKKAKE"); i >= 0 {
		return s[:i]
	}
	return s
}

func absURL(base, u string) string {
	switch {
	case u == "":
		return ""
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return base + u
	default:
		return base + "/" + u
	}
}

func cleanText(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

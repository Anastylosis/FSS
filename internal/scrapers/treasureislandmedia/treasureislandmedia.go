// Package treasureislandmedia scrapes Treasure Island Media
// (treasureislandmedia.com), a gay studio whose sub-brands (TIMFUCK, TIMSUCK,
// TIMJACK, Bruthaload, TIM Classics, Latin Loads) once lived on subdomains and
// are now categories of one Next.js catalog. The /scenes listing yields scene
// detail links; each detail page carries the CMS scene id, OpenGraph tags, a
// schema.org VideoObject with the release date, and the sub-brand in its
// header. See docs/scrapers.md.
package treasureislandmedia

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
	// /scenes/{slug} links are also accepted and resolved against baseURL.
	//
	// A trailing query is matched but not captured: the grid's cards now carry
	// `?from=grid&n=1&sort=released`, and requiring the href to end at the slug
	// made every card invisible. The bare `/scenes` listing and pagination
	// links still do not match — they have no slug after the segment.
	sceneLinkRe = regexp.MustCompile(`href="((?:https?://[^"]+)?/scenes/[^"?#]+)(?:[?#][^"]*)?"`)

	// The scene id is the CMS content id the player is initialised with. It
	// used to be the numeric filename of the scene image, which broke twice as
	// the image moved directories (`/covers/` → `/splashes/` → `/sliders/`),
	// each time silently dropping every scene. See docs/scrapers.md.
	sceneIDRe = regexp.MustCompile(`\\?"sceneId\\?":(\d+)`)

	// The sub-brand is the header brand block on a scene page (TIMFUCK,
	// TIM CLASSICS, …); the sub-brand subdomains now redirect to categories.
	brandLabelRe = regexp.MustCompile(`class="to-brandblock__label">([^<]+)<`)

	// The full description; og:description is truncated with an ellipsis.
	richtextRe   = regexp.MustCompile(`(?s)<div class="payload-richtext">(.*?)</div>`)
	blockBreakRe = regexp.MustCompile(`(?i)</p>|<br\s*/?>`)
	metaTagsRe   = regexp.MustCompile(`(?s)to-meta__label">Tags:</span>\s*<span class="to-meta__chips">(.*?)</span>`)

	// Cast: the older template listed models as subtitle anchors to /men/{id};
	// the 2026 rebuild renders a "Starring" strip of named cards instead.
	// Both are read — the sub-brand hosts have not all been rebuilt.
	castLinkRe     = regexp.MustCompile(`class="thumbnail-subtitle-a"\s+href="[^"]*/men/[^"]*"[^>]*>([^<]+)<`)
	castStripNames = regexp.MustCompile(`class="to-caststrip__name">([^<]+)<`)

	// The rebuild moved the director into a meta row of chips and added a
	// runtime chip; neither existed in the older template.
	metaDirectorRe = regexp.MustCompile(`(?s)to-meta__label">Directors?:</span>\s*<span class="to-meta__chips">(.*?)</span>`)
	metaRuntimeRe  = regexp.MustCompile(`to-chip--fact">(\d+)<!-- --> min`)
	chipTextRe     = regexp.MustCompile(`(?s)<a[^>]*class="to-chip"[^>]*>(.*?)</a>`)
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
		if page == 1 && len(urls) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene links on the first listing page"))
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
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay, out)
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

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, urls []string, now time.Time, delay time.Duration, out chan<- scraper.SceneResult) []models.Scene {
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
			if !scraper.Pace(ctx, delay) {
				return
			}
			sc, err := s.toScene(ctx, studioURL, u, now)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			scenes[i] = sc
		}(i, u)
	}
	wg.Wait()
	// Drop any scenes left zero-valued by a cancelled context or a failure,
	// which has already been reported.
	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

func (s *Scraper) toScene(ctx context.Context, studioURL, sceneURL string, now time.Time) (models.Scene, error) {
	body, err := s.get(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, err
	}
	return parseDetail(body, studioURL, sceneURL, now)
}

func parseDetail(body []byte, studioURL, sceneURL string, now time.Time) (models.Scene, error) {
	detail := string(body)
	og := parseutil.OpenGraph(body)

	scene := models.Scene{
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       sceneURL,
		Studio:    studioName,
		ScrapedAt: now,
	}

	m := sceneIDRe.FindStringSubmatch(detail)
	if m == nil {
		return models.Scene{}, scraper.ParseError(sceneURL, fmt.Errorf("no scene id on the page"))
	}
	scene.ID = m[1]

	if v := og["og:title"]; v != "" {
		scene.Title = html.UnescapeString(strings.TrimSpace(v))
	}
	if m := richtextRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(blockBreakRe.ReplaceAllString(m[1], " "))
	}
	if scene.Description == "" {
		if v := og["og:description"]; v != "" {
			scene.Description = strings.TrimSpace(html.UnescapeString(v))
		}
	}
	if v := og["og:image"]; v != "" {
		scene.Thumbnail = html.UnescapeString(strings.TrimSpace(v))
	}

	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		if scene.Title == "" {
			scene.Title = strings.TrimSpace(vo.Name)
		}
		if d, derr := parseutil.TryParseDate(strings.TrimSpace(vo.UploadDate), time.RFC3339); derr == nil {
			scene.Date = d.UTC()
		}
	}
	if scene.Date.IsZero() {
		if v := og["og:updated_time"]; v != "" {
			if d, derr := parseutil.TryParseDate(strings.TrimSpace(v), time.RFC3339); derr == nil {
				scene.Date = d.UTC()
			}
		}
	}
	if scene.Title == "" {
		return models.Scene{}, scraper.ParseError(sceneURL, fmt.Errorf("no title on the page"))
	}

	if v := og["og:url"]; v != "" {
		ogURL := html.UnescapeString(strings.TrimSpace(v))
		scene.URL = ogURL
		if id, name := brandFromURL(ogURL); id != "" {
			scene.SiteID = id
			scene.Studio = name
		}
	}
	if scene.SiteID == siteID {
		if m := brandLabelRe.FindStringSubmatch(detail); m != nil {
			if id, name := brandFromLabel(m[1]); id != "" {
				scene.SiteID = id
				scene.Studio = name
			}
		}
	}

	if m := directorRe.FindStringSubmatch(detail); m != nil {
		scene.Director = cleanText(m[1])
	} else if m := metaDirectorRe.FindStringSubmatch(detail); m != nil {
		if c := chipTextRe.FindStringSubmatch(m[1]); c != nil {
			scene.Director = cleanText(c[1])
		}
	}

	if m := metaRuntimeRe.FindStringSubmatch(detail); m != nil {
		if mins, cerr := strconv.Atoi(m[1]); cerr == nil {
			scene.Duration = mins * 60
		}
	}

	if m := metaTagsRe.FindStringSubmatch(detail); m != nil {
		scene.Tags = chipTexts(m[1])
	}

	var performers []string
	seen := make(map[string]bool)
	for _, re := range []*regexp.Regexp{castLinkRe, castStripNames} {
		for _, m := range re.FindAllStringSubmatch(detail, -1) {
			name := cleanText(m[1])
			if name != "" && !seen[name] {
				seen[name] = true
				performers = append(performers, name)
			}
		}
	}
	scene.Performers = performers

	return scene, nil
}

func chipTexts(cell string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, c := range chipTextRe.FindAllStringSubmatch(cell, -1) {
		v := cleanText(c[1])
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// brandLabels maps a scene page's brand-block label, keyed lowercase with
// non-alphanumerics removed, to its sub-brand subdomain. The parent studio's
// own pages carry "Paul Morris", which is not listed and so stays the parent.
var brandLabels = map[string]string{
	"timfuck":       "timfuck",
	"timsuck":       "timsuck",
	"timjack":       "timjack",
	"bruthaload":    "bruthaload",
	"grindhouseraw": "ghr",
	"timclassics":   "classics",
	"latinloads":    "latinloads",
}

func brandFromLabel(label string) (string, string) {
	var b strings.Builder
	for _, r := range strings.ToLower(html.UnescapeString(label)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	sub, ok := brandLabels[b.String()]
	if !ok {
		return "", ""
	}
	return sub, brandSubdomains[sub]
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

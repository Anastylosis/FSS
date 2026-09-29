// Package seehim scrapes the See HIM Network (seehimfuck.com, seehimsolo.com,
// ravebunnys.com), three sites on the classic Elevated X tour skin.
//
// The listing gives id, title, URL, date, duration and thumbnail; performers,
// description and tags exist only on the detail page, which a worker pool
// fetches. Listings are newest-first, so the KnownIDs early-stop applies.
//
// Two per-site details are configurable:
//
//   - the listing path segment, which is "movies" everywhere except
//     seehimsolo.com, where it is "movies-2"; and
//   - nothing else — the three sites are otherwise byte-identical in structure.
//
// The card's title attribute is unreliable (some cards carry a neighbouring
// scene's title), so the detail page's <h1> wins when it is available.
//
// seehimclips.com is listed on the network portal but no longer resolves.
package seehim

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

const detailWorkers = 4

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// ListingPath is the /categories/{path}/{N}/latest/ segment.
	ListingPath string
}

var sites = []siteConfig{
	{"seehimfuck", "seehimfuck.com", "See HIM Fuck", "movies"},
	// seehimsolo serves its listing from "movies-2", not "movies".
	{"seehimsolo", "seehimsolo.com", "See HIM Solo", "movies-2"},
	{"ravebunnys", "ravebunnys.com", "Rave Bunnys", "movies"},
}

// Scraper implements scraper.StudioScraper for one See HIM Network site.
type Scraper struct {
	cfg     siteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		fmt.Sprintf("%s/categories/%s/{N}/latest/", s.cfg.Domain, s.cfg.ListingPath),
		s.cfg.Domain + "/trailers/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/%s/%d/latest/", s.base, s.cfg.ListingPath, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// ---- listing ----

var (
	cardSplitRe = regexp.MustCompile(`<div class="item-video`)
	// Cards link with absolute URLs; only the trailer filename is captured so
	// the detail URL is rebuilt against the configured base. That normalises
	// www/non-www and keeps every request on one host.
	trailerRe    = regexp.MustCompile(`<a\s+href="[^"]*/trailers/([^"/]+\.html)"[^>]*title="([^"]*)"`)
	setIDRe      = regexp.MustCompile(`id="set-target-(\d+)"`)
	thumbRe      = regexp.MustCompile(`src0_3x="([^"]+)"`)
	thumbSmallRe = regexp.MustCompile(`src0_1x="([^"]+)"`)
	listDateRe   = regexp.MustCompile(`class="date">\s*(\d{4}-\d{2}-\d{2})`)
	// The "time" div is not always just a runtime: on photo-bearing scenes it
	// reads "280&nbsp;Photos, 54:16". Capture the div, then pick the clock
	// value out of it — matching digits directly would take the photo count.
	listTimeRe  = regexp.MustCompile(`class="time">([^<]*)</div>`)
	clockTimeRe = regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?)`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
}

func (s *Scraper) parseListing(body []byte) []listItem {
	parts := cardSplitRe.Split(string(body), -1)
	if len(parts) <= 1 {
		return nil
	}
	items := make([]listItem, 0, len(parts)-1)
	for _, card := range parts[1:] {
		m := trailerRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			url:   s.base + "/trailers/" + m[1],
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		}
		if id := setIDRe.FindStringSubmatch(card); id != nil {
			it.id = id[1]
		} else {
			it.id = slugFromURL(it.url)
		}
		// Prefer the largest thumbnail the card offers.
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = s.normalizeURL(th[1])
		} else if th := thumbSmallRe.FindStringSubmatch(card); th != nil {
			it.thumb = s.normalizeURL(th[1])
		}
		if d := listDateRe.FindStringSubmatch(card); d != nil {
			if ts, err := time.Parse("2006-01-02", d[1]); err == nil {
				it.date = ts.UTC()
			}
		}
		if t := listTimeRe.FindStringSubmatch(card); t != nil {
			if c := clockTimeRe.FindStringSubmatch(t[1]); c != nil {
				it.duration = parseutil.ParseDurationColon(c[1])
			}
		}
		items = append(items, it)
	}
	return items
}

// normalizeURL resolves the CMS's protocol-relative and root-relative URLs.
// Thumbnail paths contain a doubled slash ("/content//contentthumbs/…") which
// the CDN serves as-is, so it is left alone.
func (s *Scraper) normalizeURL(u string) string {
	switch {
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return s.base + u
	default:
		return u
	}
}

func slugFromURL(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		u = u[i+1:]
	}
	return strings.TrimSuffix(u, ".html")
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
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
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

var (
	h1Re         = regexp.MustCompile(`(?s)<div class="videoDetails clear">\s*<h1>([^<]*)</h1>`)
	descRe       = regexp.MustCompile(`(?s)<div class="videoDetails clear">.*?<p>(.*?)</p>`)
	detailDateRe = regexp.MustCompile(`<span>Date Added:</span>\s*(\d{4}-\d{2}-\d{2})`)
	detailMinsRe = regexp.MustCompile(`(\d+)&nbsp;Min&nbsp;Of Video`)
	performerRe  = regexp.MustCompile(`<li class="update_models">\s*<a[^>]*/models/[^"]*"[^>]*>([^<]+)</a>`)
	tagRe        = regexp.MustCompile(`href="[^"]*/categories/([^/"]+)/\d+/latest/"[^>]*>([^<]+)</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Date:      it.date,
		Duration:  it.duration,
		Thumbnail: it.thumb,
		Studio:    s.cfg.StudioName,
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		return scene
	}
	s.applyDetail(&scene, string(body))
	return scene
}

// applyDetail overlays the detail page's fields onto a scene built from the
// listing card.
func (s *Scraper) applyDetail(scene *models.Scene, detail string) {
	// The card's title attribute is unreliable — some cards carry a
	// neighbouring scene's title — so the detail <h1> is authoritative.
	if m := h1Re.FindStringSubmatch(detail); m != nil {
		if t := html.UnescapeString(strings.TrimSpace(m[1])); t != "" {
			scene.Title = t
		}
	}
	if m := descRe.FindStringSubmatch(detail); m != nil {
		text := html.UnescapeString(tagStripRe.ReplaceAllString(m[1], ""))
		scene.Description = strings.Join(strings.Fields(text), " ")
	}
	if scene.Date.IsZero() {
		if m := detailDateRe.FindStringSubmatch(detail); m != nil {
			if ts, err := time.Parse("2006-01-02", m[1]); err == nil {
				scene.Date = ts.UTC()
			}
		}
	}
	if scene.Duration == 0 {
		if m := detailMinsRe.FindStringSubmatch(detail); m != nil {
			scene.Duration = atoi(m[1]) * 60
		}
	}

	scene.Performers = uniqueMatches(performerRe, detail, 1)

	// Tag links share the /categories/ namespace with the listing path, so the
	// site's own "movies" listing shows up among them and must be dropped.
	for _, m := range tagRe.FindAllStringSubmatch(detail, -1) {
		if m[1] == s.cfg.ListingPath {
			continue
		}
		tag := html.UnescapeString(strings.TrimSpace(m[2]))
		if tag != "" && !contains(scene.Tags, tag) {
			scene.Tags = append(scene.Tags, tag)
		}
	}
}

func uniqueMatches(re *regexp.Regexp, s string, group int) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		v := html.UnescapeString(strings.TrimSpace(m[group]))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
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

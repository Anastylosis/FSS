// Package itspov scrapes It's POV (itspov.com), a custom server-rendered PHP
// CMS run by the Dorcel group (CDN hosts `public{N}-content.cdn-medias.com`,
// asset paths under `/images/cache/{id}/project/front_skeleton/`).
//
// The network's nine brands — Academy POV, Backdoor POV, Feetish POV, I Love
// POV, Intimate POV, More POV, Office POV, Petite POV and Step POV — are
// channels on this one host, not separate sites. Their apex domains
// (intimatepov.com, backdoorpov.com, …) serve a marketing splash with no scene
// links at all, so a `/channels/{slug}` URL is scraped through the main
// listing's collection filter instead.
//
// Two things the listing does not give:
//
//   - Sort order. Bare `/videos` is sorted by views, and an unrecognised
//     `sorting` value silently falls back to oldest-first, so the listing URL
//     always carries `sorting=new` explicitly. Without it the KnownIDs
//     early-stop would be meaningless.
//   - Date, duration and description. Those are on the detail page, so every
//     scene costs one extra fetch.
//
// Pagination is 36/page, and a page past the end does not come back empty —
// the CMS clamps to the last page and re-serves it. Termination therefore comes
// from the `<span class="total">N videos</span>` count, not from an empty page.
//
// There is no tag taxonomy on scene pages: categories exist only as a search
// facet.
package itspov

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "itspov"
	studioName    = "It's POV"
	detailWorkers = 4
	perPage       = 36
	dateLayout    = "January 2, 2006"
)

var siteBase = "https://itspov.com"

// channels are the network's brands. They are collection facets on the main
// listing, reachable at /channels/{slug} but only ever showing 12 scenes there.
var channels = map[string]string{
	"academypov":  "Academy POV",
	"backdoorpov": "Backdoor POV",
	"feetishpov":  "Feetish POV",
	"ilovepov":    "I Love POV",
	"intimatepov": "Intimate POV",
	"morepov":     "More POV",
	"officepov":   "Office POV",
	"petitepov":   "Petite POV",
	"steppov":     "Step POV",
}

// Scraper implements scraper.StudioScraper for It's POV.
type Scraper struct {
	Client *http.Client
}

// New constructs an It's POV scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"itspov.com",
		"itspov.com/videos",
		"itspov.com/channels/{slug}",
		"itspov.com/pornstars/{slug}",
		"itspov.com/categories/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?itspov\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- URL modes ----

var (
	channelURLRe  = regexp.MustCompile(`/channels/([a-z0-9-]+)`)
	pornstarURLRe = regexp.MustCompile(`/pornstars/([a-z0-9-]+)`)
	categoryURLRe = regexp.MustCompile(`/categories/([a-z0-9-]+)`)
)

// listingURL builds the paginated listing for whatever the studio URL selects.
// Facet slugs are prefixed by type (`collection_`, `actor_`, `category_`); the
// bare form is silently ignored and returns the whole catalogue.
func listingURL(studioURL string, page int) (string, string) {
	filter, studio := "", studioName
	switch {
	case channelURLRe.MatchString(studioURL):
		slug := channelURLRe.FindStringSubmatch(studioURL)[1]
		filter = "collection_" + slug
		if name, ok := channels[slug]; ok {
			studio = name
		}
	case pornstarURLRe.MatchString(studioURL):
		filter = "actor_" + pornstarURLRe.FindStringSubmatch(studioURL)[1]
	case categoryURLRe.MatchString(studioURL):
		filter = "category_" + categoryURLRe.FindStringSubmatch(studioURL)[1]
	}

	u := fmt.Sprintf("%s/videos?sorting=new&page=%d", siteBase, page)
	if filter != "" {
		u += "&filters=" + filter
	}
	return u, studio
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if _, studio := listingURL(studioURL, 1); studio != studioName {
		scraper.Debugf(1, "%s: scraping channel %q", siteID, studio)
	}

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u, studio := listingURL(studioURL, page)
		body, err := s.fetchPage(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		total := parseTotal(body)
		items := parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}

		// A page past the end is clamped to the last one and re-served, so the
		// total is the only reliable stop signal.
		done := total > 0 && page*perPage >= total
		return scraper.PageResult{
			Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, studio, fresh, now, opts.Delay),
			Total:  total,
			Done:   done,
		}, nil
	})
}

// ---- listing ----

var (
	totalRe = regexp.MustCompile(`<span class="total">\s*(\d+)\s*videos?\s*</span>`)
	cardRe  = regexp.MustCompile(`<div class="scene thumbnail`)
	// Only the id and slug are captured so the URL is rebuilt against siteBase.
	linkRe  = regexp.MustCompile(`<a href="/videos/(\d+)/([^"]*)" class="thumb">`)
	thumbRe = regexp.MustCompile(`<img class="lazyload thumb" data-src="([^"]+)"`)
)

type listItem struct {
	id, slug, thumb string
}

func parseTotal(body []byte) int {
	m := totalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0
	}
	return n
}

func parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{id: m[1], slug: m[2]}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}
		items = append(items, it)
	}
	return items
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL, studio string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
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
			scenes[i] = s.toScene(ctx, studioURL, studio, it, now)
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
	titleRe = regexp.MustCompile(`(?s)<h1 class="title">(.*?)</h1>`)
	// The cast block is the scene's own; /pornstars/ links also appear in the
	// site-wide nav, so the search is scoped to this container.
	actressBlockRe = regexp.MustCompile(`(?s)<div class="actress">(.*?)</div>`)
	performerRe    = regexp.MustCompile(`<a href="/pornstars/[^"]*">([^<]+)</a>`)
	durationRe     = regexp.MustCompile(`<span class="duration">\s*([^<]+?)\s*</span>`)
	dateRe         = regexp.MustCompile(`<span class="publish_date">\s*([^<]+?)\s*</span>`)
	// The "full" span holds the untruncated text; "small" is an ellipsised copy.
	descRe     = regexp.MustCompile(`(?s)<span class="full">(.*?)</span>`)
	tagStripRe = regexp.MustCompile(`(?s)<style[^>]*>.*?</style>|<[^>]+>`)
	// Runtimes render as "24m55" or "1h04m12".
	hmsRe = regexp.MustCompile(`^(?:(\d+)h)?(\d+)m(\d+)?$`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL, studio string, it listItem, now time.Time) models.Scene {
	sceneURL := fmt.Sprintf("%s/videos/%s/%s", siteBase, it.id, it.slug)

	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}
	}
	detail := string(body)

	title := ""
	if m := titleRe.FindStringSubmatch(detail); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}
	}

	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     title,
		URL:       sceneURL,
		Thumbnail: it.thumb,
		Studio:    studio,
		ScrapedAt: now,
	}

	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(tagStripRe.ReplaceAllString(m[1], " "))
	}
	if m := dateRe.FindStringSubmatch(detail); m != nil {
		if t, err := time.Parse(dateLayout, m[1]); err == nil {
			scene.Date = t.UTC()
		}
	}
	if m := durationRe.FindStringSubmatch(detail); m != nil {
		scene.Duration = parseDuration(m[1])
	}
	if mb := actressBlockRe.FindStringSubmatch(detail); mb != nil {
		seen := make(map[string]bool)
		for _, pm := range performerRe.FindAllStringSubmatch(mb[1], -1) {
			name := cleanText(pm[1])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			scene.Performers = append(scene.Performers, name)
		}
	}

	return scene
}

// parseDuration reads the CMS's own runtime format, "24m55" or "1h04m12", and
// returns seconds. It is not a colon-separated value, so parseutil's helpers do
// not apply.
func parseDuration(s string) int {
	m := hmsRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	atoi := func(v string) int {
		n, _ := strconv.Atoi(v)
		return n
	}
	return atoi(m[1])*3600 + atoi(m[2])*60 + atoi(m[3])
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

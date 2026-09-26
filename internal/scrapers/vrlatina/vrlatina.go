// Package vrlatina scrapes VRLatina (vrlatina.com), a Latina VR studio running
// the Mechbunny PHP CMS.
//
// Three URL modes are supported:
//
//   - the main listing, walked via /most-recent/page{N}.html (15/page, ~392
//     scenes). Note the site also exposes /videos/, but that endpoint returns
//     a randomised order — only /most-recent/ is date-sorted, so only it
//     supports the KnownIDs early-stop;
//   - a model page, /models/{slug}-{id}.html or the equivalent
//     /pornstars/{slug}-{id}.html, which lists that model's scenes; and
//   - a tag page, /search/{slug}/.
//
// Model and tag pages render every result at once — they have no pagination —
// so both are handled as a single fetch.
//
// Listing cards carry the id, title, URL, duration, performers and thumbnail;
// the release date, tags and description live only on the detail page, which a
// worker pool fetches. Detail pages embed ~15 "related videos" in the same card
// markup, so detail parsing never reuses the card parser.
package vrlatina

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
	siteID        = "vrlatina"
	studioName    = "VRLatina"
	detailWorkers = 4
	dateLayout    = "Jan 2, 2006"
)

var siteBase = "https://vrlatina.com"

// Scraper implements scraper.StudioScraper for VRLatina.
type Scraper struct {
	Client *http.Client
}

// New constructs a VRLatina scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"vrlatina.com/most-recent/",
		"vrlatina.com/models/{slug}-{id}.html",
		"vrlatina.com/pornstars/{slug}-{id}.html",
		"vrlatina.com/search/{tag}/",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?vrlatina\.com(?:/|$)`)
	// singlePageRe matches the modes that render all results at once.
	singlePageRe = regexp.MustCompile(`/(?:models|pornstars)/[a-z0-9-]+-\d+\.html|/search/[^/]+/?`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	if singlePageRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: scraping single-page listing %s", siteID, studioURL)
		s.runSinglePage(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, out, now, opts.Delay)
		return
	}
	scraper.Debugf(1, "%s: scraping /most-recent/ listing", siteID)
	s.runListing(ctx, studioURL, opts, out, now)
}

// runListing walks /most-recent/, the only date-sorted listing on the site.
func (s *Scraper) runListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.get(ctx, listingPageURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(body)
		if len(cards) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, cards, now, opts.Delay)}, nil
	})
}

// listingPageURL builds the /most-recent/ page URL. Page 1 is the bare path —
// there is no page1.html.
func listingPageURL(page int) string {
	if page <= 1 {
		return siteBase + "/most-recent/"
	}
	return fmt.Sprintf("%s/most-recent/page%d.html", siteBase, page)
}

// runSinglePage handles model and tag pages, which have no pagination.
func (s *Scraper) runSinglePage(ctx context.Context, workers int, studioURL string, out chan<- scraper.SceneResult, now time.Time, delay time.Duration) {
	body, err := s.get(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	cards := parseCards(body)
	scraper.Debugf(1, "%s: single page has %d videos", siteID, len(cards))

	select {
	case out <- scraper.Progress(len(cards)):
	case <-ctx.Done():
		return
	}

	for _, scene := range s.enrich(ctx, workers, studioURL, cards, now, delay) {
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

// ---- listing parsing ----

var (
	// The CMS brackets each grid card in HTML comments, which makes for a
	// far more reliable delimiter than matching nested <div>s.
	cardRe = regexp.MustCompile(`(?s)<!-- item -->(.*?)<!-- item END -->`)
	// Cards link with absolute URLs. Only the slug and id are captured so the
	// detail URL can be rebuilt against siteBase — that normalises www/non-www
	// and keeps the scraper pointed at a single host.
	videoHrefRe = regexp.MustCompile(`href="https?://[^"]*?/video/([a-z0-9-]+)-(\d+)\.html"`)
	itemNameRe  = regexp.MustCompile(`<span class="item-name">([^<]*)</span>`)
	itemTimeRe  = regexp.MustCompile(`(?s)<span class="item-time">.*?(\d{1,2}:\d{2}(?::\d{2})?)`)
	subLabelRe  = regexp.MustCompile(`<span class="sub-label">([^<]*)</span>`)
	thumbRe     = regexp.MustCompile(`<img src="(https?://[^"]+)"`)
	previewRe   = regexp.MustCompile(`data-video="(https?://[^"]+)"`)
)

type card struct {
	id         string
	url        string
	title      string
	duration   int
	performers []string
	thumbnail  string
	preview    string
}

// parseCards extracts grid cards from a listing, model or tag page.
func parseCards(body []byte) []card {
	var cards []card
	seen := make(map[string]bool)
	for _, m := range cardRe.FindAllSubmatch(body, -1) {
		c, ok := parseCard(string(m[1]))
		if !ok || seen[c.id] {
			continue
		}
		seen[c.id] = true
		cards = append(cards, c)
	}
	return cards
}

func parseCard(inner string) (card, bool) {
	m := videoHrefRe.FindStringSubmatch(inner)
	if m == nil {
		return card{}, false
	}
	slug, id := m[1], m[2]
	c := card{
		id:  id,
		url: fmt.Sprintf("%s/video/%s-%s.html", siteBase, slug, id),
	}

	if t := itemNameRe.FindStringSubmatch(inner); t != nil {
		c.title = html.UnescapeString(strings.TrimSpace(t[1]))
	}
	if c.title == "" {
		// Fall back to the slug, e.g. "sensationanal-562" -> "sensationanal".
		c.title = strings.ReplaceAll(slug, "-", " ")
	}
	if d := itemTimeRe.FindStringSubmatch(inner); d != nil {
		c.duration = parseutil.ParseDurationColon(d[1])
	}
	// Every performer is rendered as a "sub-label" span inside the card's
	// "Starring:" row; the duration lives in its own span, so the only
	// sub-labels present are names.
	for _, p := range subLabelRe.FindAllStringSubmatch(inner, -1) {
		if name := html.UnescapeString(strings.TrimSpace(p[1])); name != "" {
			c.performers = append(c.performers, name)
		}
	}
	if th := thumbRe.FindStringSubmatch(inner); th != nil {
		c.thumbnail = th[1]
	}
	if pv := previewRe.FindStringSubmatch(inner); pv != nil {
		c.preview = pv[1]
	}
	return c, true
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, cards []card, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(cards))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(cards), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		wg.Add(1)
		go func(i int, c card) {
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
			scenes[i] = s.toScene(ctx, studioURL, c, now)
		}(i, c)
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

// Detail-page fields are matched document-wide rather than by first isolating
// their container <div>. The containers open with a nested `<div class="label">`
// caption, so a lazy match to `</div>` would capture only that caption and stop
// short of the data. Both anchor shapes below are unique to their block — the
// related-video cards further down the page carry neither — so scoping buys
// nothing and costs correctness.
var (
	releaseDateRe = regexp.MustCompile(`(?s)Release date:\s*</div>\s*<span class="sub-label">\s*([^<]+?)\s*</span>`)
	tagRe         = regexp.MustCompile(`class="tag">([^<]+)</a>`)
	modelRe       = regexp.MustCompile(`<a title="([^"]+)" href="[^"]*/(?:pornstars|models)/[a-z0-9-]+-\d+\.html"`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, c card, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         c.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      c.title,
		URL:        c.url,
		Thumbnail:  c.thumbnail,
		Preview:    c.preview,
		Duration:   c.duration,
		Performers: c.performers,
		Studio:     studioName,
		ScrapedAt:  now,
	}

	body, err := s.get(ctx, c.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := releaseDateRe.FindStringSubmatch(detail); m != nil {
		if d, err := time.Parse(dateLayout, strings.TrimSpace(m[1])); err == nil {
			scene.Date = d.UTC()
		}
	}

	// The detail page credits performers with profile links, which is more
	// reliable than the card's bare names.
	var names []string
	for _, p := range modelRe.FindAllStringSubmatch(detail, -1) {
		if name := html.UnescapeString(strings.TrimSpace(p[1])); name != "" {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		scene.Performers = names
	}

	// The tag list repeats each performer's name; drop those so tags stay
	// descriptive.
	isPerformer := make(map[string]bool, len(scene.Performers))
	for _, p := range scene.Performers {
		isPerformer[strings.ToLower(p)] = true
	}
	for _, t := range tagRe.FindAllStringSubmatch(detail, -1) {
		tag := html.UnescapeString(strings.TrimSpace(t[1]))
		if tag != "" && !isPerformer[strings.ToLower(tag)] {
			scene.Tags = append(scene.Tags, tag)
		}
	}

	og := parseutil.OpenGraph(body)
	if v := og["og:description"]; v != "" {
		scene.Description = html.UnescapeString(v)
	}
	if v := og["og:image"]; v != "" {
		scene.Thumbnail = html.UnescapeString(v)
	}
	if scene.Title == "" {
		if v := og["og:title"]; v != "" {
			scene.Title = html.UnescapeString(v)
		}
	}

	return scene
}

// ---- HTTP ----

func (s *Scraper) get(ctx context.Context, rawURL string) ([]byte, error) {
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

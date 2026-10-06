// Package ghostproclassic scrapes the four Ghost Pro Productions sister
// sites still running the older Elevated-X-Classic HTML template:
//
//   - analjesse.com
//   - creampiethais.com
//   - mongerinasia.com
//   - tailynn.com
//
// The newer Next.js sister sites (asiansuckdolls, thaigirlswild, etc.) live
// in the sibling [ghostpro] package.
//
// Card markup:
//
//	<div class="latestUpdateB" data-setid="2008">
//	  <div class="videoPic">
//	    <a href="https://join.{site}.com/signup/signup.php">
//	      <img id="set-target-2008" class="update_thumb thumbs stdimage"
//	           src0_1x="/content//contentthumbs/41/62/54162-1x.jpg" ... />
//	    </a>
//	  </div>
//	  <div class="latestUpdateBinfo">
//	    <h4 class="link_bright"><a>Sara</a></h4>          ← model name (no scene title)
//	    <p class="description-right">Amazing half Arab …</p>
//	    <p class="link_light">
//	      <a class="link_bright infolink" href="/models/sara.html">Sara</a>
//	    </p>
//	    <ul class="videoInfo">
//	      <li class="text_med"><i class="fas fa-video"></i>24 min</li>
//	    </ul>
//	  </div>
//	</div>
//
// Notable quirks:
//
//   - There is no separate scene title; the `<h4>` anchor is the **model
//     name**. We use the first sentence of the description as a synthesised
//     title and store the model name in Performers.
//   - No publication date is shown on the card — `Scene.Date` stays zero, and
//     the listing is not in date order: creampiethais' first page runs set ids
//     2008, 1816, 1700, 1644, 10061, 10079. There is nothing to order on and
//     nothing to sort by, so the `KnownIDs` early-stop is **not used** — it
//     would abort at whatever position the first stored scene occupied and
//     permanently miss everything after it. Every run re-walks, which is cheap
//     here: the pages are listing-only, with no detail fetch behind them.
//   - Every detail link goes to `join.{site}.com/signup/signup.php`. There
//     are no public detail pages, so all metadata comes from the card.
//
// Pagination: `/categories/updates_{N}_p.html` (note `_p`, not `_d` like
// the College-Uniform variant of the same template family). Past-end pages
// return a page with zero `data-setid` matches (clean stop signal).
package ghostproclassic

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const studioName = "Ghost Pro Productions"

// SiteConfig describes one Elevated-X-Classic Ghost Pro sister site.
type SiteConfig struct {
	ID       string
	SiteBase string
	SiteName string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// Card parsing. cardStartRe anchors each card on its data-setid div.
var (
	cardStartRe = regexp.MustCompile(`<div class="latestUpdateB"\s+data-setid="(\d+)"`)
	// Thumbnail — src0_1x is the listing's lazy-load attribute.
	thumbRe = regexp.MustCompile(`src0_1x="([^"]+)"`)
	// description-right paragraph holds the scene description and doubles as
	// our synthesised title (first sentence).
	descRe = regexp.MustCompile(`(?s)<p class="description-right">(.*?)</p>`)
	// Performer link — inside `<a class="link_bright infolink" href="…/models/…">`.
	performerAnchorRe = regexp.MustCompile(
		`<a[^>]+class="[^"]*infolink[^"]*"[^>]+href="[^"]*/models/[^"]+"[^>]*>([^<]+)</a>`,
	)
	// Duration "24 min" inside <li class="text_med"><i class="fas fa-video"></i>NN min</li>.
	durationRe = regexp.MustCompile(`fa-video[^>]*></i>\s*(\d+)\s*min`)
	// Pagination — max page number from any updates_N_p.html link on the page.
	maxPageRe = regexp.MustCompile(`updates_(\d+)_p\.html`)
)

type sceneItem struct {
	id          string
	title       string // synthesised (first sentence of description)
	description string
	thumb       string
	duration    int // seconds
	performers  []string
}

func parseListing(body []byte, siteBase string) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		id := page[loc[2]:loc[3]]
		if seen[id] {
			continue
		}
		seen[id] = true

		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		item := sceneItem{id: id}

		if m := descRe.FindStringSubmatch(block); m != nil {
			item.description = cleanHTML(m[1])
		}
		// Synthesise a title from the first sentence of the description so
		// scenes have *something* useful to display. If the description is
		// empty, fall back to "Scene {id}" so the field is never literally
		// blank.
		item.title = synthesizeTitle(item.description, id)

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			thumb := strings.TrimSpace(m[1])
			if strings.HasPrefix(thumb, "/") {
				thumb = strings.TrimRight(siteBase, "/") + thumb
			}
			item.thumb = thumb
		}

		for _, pm := range performerAnchorRe.FindAllStringSubmatch(block, -1) {
			name := html.UnescapeString(strings.TrimSpace(pm[1]))
			if name != "" {
				item.performers = append(item.performers, name)
			}
		}
		// De-dup performers (a card sometimes lists the same model twice via
		// both the title-anchor and the infolink).
		item.performers = dedupStrings(item.performers)

		if m := durationRe.FindStringSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(m[1])
			item.duration = mins * 60
		}

		items = append(items, item)
	}
	return items
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func (s *Scraper) listingURL(page int) string {
	return fmt.Sprintf("%s/categories/updates_%d_p.html", s.cfg.SiteBase, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "%s: scraping full catalog", s.cfg.ID)

	// See the package doc: this listing has no date and no dependable order, so
	// there is no position at which stopping early is safe.
	opts.KnownIDs = nil

	now := time.Now().UTC()
	var firstPageSize int
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, s.listingURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body, s.cfg.SiteBase)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			firstPageSize = len(items)
			total = estimateTotal(body, firstPageSize)
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = s.toScene(item, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (s *Scraper) toScene(item sceneItem, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.ID,
		StudioURL:   studioURL,
		Title:       item.title,
		Description: item.description,
		// No public detail page — synthesise a stable per-scene URL anchor.
		URL:        fmt.Sprintf("%s/#scene-%s", s.cfg.SiteBase, item.id),
		Studio:     studioName,
		Series:     s.cfg.SiteName,
		Thumbnail:  item.thumb,
		Duration:   item.duration,
		Performers: item.performers,
		ScrapedAt:  now,
	}
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

var (
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
	wsRe      = regexp.MustCompile(`\s+`)
)

// cleanHTML strips HTML tags, decodes entities, and collapses whitespace.
// `&nbsp;` decodes to U+00A0 (non-breaking space) which `\s+` does not match
// in Go's regexp, so we normalise it to a regular space first.
func cleanHTML(s string) string {
	if s == "" {
		return ""
	}
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// synthesizeTitle pulls the first sentence (or first 80 chars) out of a
// description to act as a Scene title. Falls back to "Scene {id}" when the
// description is empty.
func synthesizeTitle(desc, id string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return "Scene " + id
	}
	// Prefer cutting at the first sentence-ending punctuation followed by
	// whitespace — gives us a natural-feeling title.
	for _, sep := range []string{". ", "! ", "? "} {
		if i := strings.Index(desc, sep); i > 0 {
			return strings.TrimSpace(desc[:i+1])
		}
	}
	// No sentence break: clamp to 80 chars on a word boundary.
	const maxLen = 80
	if len(desc) <= maxLen {
		return desc
	}
	trim := desc[:maxLen]
	if cut := strings.LastIndex(trim, " "); cut > 40 {
		trim = trim[:cut]
	}
	return strings.TrimSpace(trim) + "…"
}

func dedupStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

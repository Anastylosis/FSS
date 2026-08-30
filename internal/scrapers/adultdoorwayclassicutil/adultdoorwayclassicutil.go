// Package adultdoorwayclassicutil scrapes Adult Doorway sister sites running
// the older Elevated X "Classic" template (flexslider banner + `item-thumb`
// cards + `item-info` blocks). Black Payback is the only known instance in
// the Adult Doorway tree today, but the util is intentionally table-driven so
// further Classic-theme sites can be added as one-line config rows.
//
// Differences from the Modern variant (adultdoorwayutil):
//
//  1. Pagination: `/tour/categories/movies/{page}/latest/` (slash-segmented),
//     not `movies_{page}_d.html`.
//  2. Card markup uses `<div class="item-thumb">` wrapping a single anchor.
//     There's no `item-update` wrapper and no `<!--//item-update-->` comment.
//  3. Detail page has no publication date — only description + tags + a
//     duration-in-minutes line in `<div class="videoInfo">`.
//
// Listing markup:
//
//	<div class="item-thumb">
//	  <a href="https://blackpayback.com/tour/trailers/extra-mayo.html" title="Extra Mayo">
//	    <img id="set-target-141" class="mainThumb thumbs stdimage"
//	         src0_1x="https://cdn77.blackpayback.com/tour/content/contentthumbs/15/53/1553-1x.jpg" />
//	  </a>
//	</div>
//	<div class="item-info clear">
//	  <h4><a href=".../trailers/extra-mayo.html" title="Extra Mayo">Extra Mayo</a></h4>
//	  <ul class="stars">…</ul>
//	</div>
//
// Detail markup:
//
//	<h1>Extra Mayo</h1>
//	<p>Description text…</p>
//	<div class="videoInfo clear">
//	  <p>868 Photos, 57 min of video</p>
//	</div>
//	<div class="featuring clear">
//	  <ul>
//	    <li class="label">Tags:</li>
//	    <li><a href=".../categories/black-owned-business/1/latest/">Black Owned Business</a></li>
//	    …
//	  </ul>
//	</div>
package adultdoorwayclassicutil

import (
	"context"
	"errors"
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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one Classic-theme Adult Doorway site.
type SiteConfig struct {
	ID       string
	SiteBase string
	Studio   string
	// TourPrefix is "/tour" for sites whose listings are rooted at /tour/
	// (e.g. blackpayback), or "" for sites that serve listings at the bare
	// `/categories/movies/{N}/latest/` path (babearchives). The empty default
	// keeps the original blackpayback behaviour intact via the SiteConfig
	// rows that explicitly set TourPrefix to "/tour".
	TourPrefix string
	Patterns   []string
	MatchRe    *regexp.Regexp
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

// Listing regexes.
//
// cardRe captures (slug, title) from `<div class="item-thumb"><a href=".../trailers/<slug>.html" title="<title>">`.
// We anchor on `item-thumb` so the flexslider banner at the top of every page
// — which uses `<li><a href=".../trailers/...">` without an `item-thumb` —
// is excluded.
//
// thumbRe pulls the lazy-loaded high-res thumb attribute `src0_1x`. The base
// CDN path is `cdn77.{site}/tour/content/contentthumbs/{XX}/{YY}/{N}-1x.jpg`.
var (
	// Trailer URL accepts any tour prefix — `/tour/trailers/{slug}.html`
	// (blackpayback), `/trailers/{slug}.html` (babearchives), `/v3/trailers/…`
	// and `/creeper/trailers/…` (Dreamnet). The slug charset is deliberately
	// case-insensitive: Dreamnet's slugs are mixed case
	// ("OMG-14-Loads-on-the-Face"), and a lowercase-only pattern matched no
	// card at all there. The href may carry trailing whitespace inside the
	// attribute (Brand New Amateurs writes `…​.html "`), which is why the
	// closing quote is reached through `\s*`.
	cardRe       = regexp.MustCompile(`(?s)<div class="item-thumb"[^>]*>\s*<a\s+href="([^"]*/trailers/([A-Za-z0-9][A-Za-z0-9_-]*)\.html)\s*"[^>]*title="([^"]*)"`)
	thumbRe      = regexp.MustCompile(`src0_1x="([^"]+)"`)
	pageLinkRe   = regexp.MustCompile(`(?:/tour)?/categories/movies/(\d+)/latest/?`)
	categorySlug = regexp.MustCompile(`(?:/tour)?/categories/([^/]+)/\d+/latest/?`)
)

// Detail regexes.
var (
	detailH1Re   = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	detailDescRe = regexp.MustCompile(`(?s)<h1[^>]*>[^<]+</h1>\s*(?:<br\s*/?>\s*)?<p>(.+?)</p>`)
	videoInfoRe  = regexp.MustCompile(`(?s)<div class="videoInfo[^"]*"[^>]*>(.*?)</div>`)
	// The separators between the number and the words are any mix of spaces
	// and &nbsp; entities — Adult Doorway prints "57&nbsp;min&nbsp;of&nbsp;video",
	// Dreamnet prints "48&nbsp;min&nbsp;of video". Writing the entity as
	// `&nbsp;?` made the entity itself mandatory (only its semicolon optional),
	// so a plain space anywhere in the phrase lost the duration.
	durationMinsRe  = regexp.MustCompile(`(\d+)(?:\s|&nbsp;)*min(?:\s|&nbsp;)*of(?:\s|&nbsp;)*video`)
	durationColonRe = regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?)`)
	featuringRe     = regexp.MustCompile(`(?s)<div class="featuring[^"]*"[^>]*>(.*?)</div>`)
	// Dreamnet's build of the template puts the title and description in a
	// `videoDetails` block under an <h3> instead of the bare <h1>/<p> the
	// Adult Doorway sites use.
	detailH3Re     = regexp.MustCompile(`(?s)<div class="videoDetails[^"]*"[^>]*>\s*<h3[^>]*>([^<]+)</h3>`)
	detailH3DescRe = regexp.MustCompile(`(?s)<div class="videoDetails[^"]*"[^>]*>\s*<h3[^>]*>[^<]*</h3>\s*<p>(.+?)</p>`)
	// The same build prints the publication date in the videoInfo strip. The
	// Adult Doorway sites print none at all.
	detailDateRe = regexp.MustCompile(`(?i)Date Added:\s*</span>\s*([A-Z][a-z]+ \d{1,2}, \d{4})`)
	tagItemRe    = regexp.MustCompile(`<a[^>]+href="[^"]*(?:/tour)?/categories/[^"]+"[^>]*>([^<]+)</a>`)
)

type sceneItem struct {
	id          string // slug
	title       string
	url         string
	thumb       string
	duration    int // seconds (from detail "N min of video")
	date        time.Time
	description string
	tags        []string
}

// parseListing finds every distinct `item-thumb` card on the page. Banner
// flexslider links are not wrapped in `item-thumb`, so they're skipped.
func parseListing(body []byte) []sceneItem {
	page := string(body)
	matches := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]sceneItem, 0, len(matches))
	seen := make(map[string]bool, len(matches))

	for _, loc := range matches {
		url := page[loc[2]:loc[3]]
		slug := page[loc[4]:loc[5]]
		title := html.UnescapeString(strings.TrimSpace(page[loc[6]:loc[7]]))
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true

		// Thumbnail lives inside the same anchor; scan a short window after
		// the match end for `src0_1x="..."`.
		end := loc[1]
		windowEnd := end + 2000
		if windowEnd > len(page) {
			windowEnd = len(page)
		}
		var thumb string
		if tm := thumbRe.FindStringSubmatch(page[end:windowEnd]); tm != nil {
			thumb = tm[1]
		}

		items = append(items, sceneItem{
			id:    slug,
			title: title,
			url:   url,
			thumb: thumb,
		})
	}
	return items
}

// estimateTotal reads pagination links on the page to compute max-page * items.
func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range pageLinkRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func enrichFromDetail(body []byte, item *sceneItem) {
	s := string(body)

	if m := detailH1Re.FindStringSubmatch(s); m != nil {
		if t := html.UnescapeString(strings.TrimSpace(m[1])); t != "" {
			item.title = t
		}
	} else if m := detailH3Re.FindStringSubmatch(s); m != nil {
		if t := html.UnescapeString(strings.TrimSpace(m[1])); t != "" {
			item.title = t
		}
	}
	if m := detailDescRe.FindStringSubmatch(s); m != nil {
		item.description = strings.TrimSpace(html.UnescapeString(stripTags(m[1])))
	} else if m := detailH3DescRe.FindStringSubmatch(s); m != nil {
		item.description = strings.TrimSpace(html.UnescapeString(stripTags(m[1])))
	}
	if m := detailDateRe.FindStringSubmatch(s); m != nil {
		if d, err := parseutil.TryParseDate(m[1], "January 2, 2006"); err == nil {
			item.date = d
		}
	}
	if m := videoInfoRe.FindStringSubmatch(s); m != nil {
		section := m[1]
		// Detail page typically shows "N min of video" (decimal minutes).
		// A few sites may use HH:MM:SS — try both, prefer the colon form
		// because it's more precise when present.
		if d := durationColonRe.FindStringSubmatch(section); d != nil {
			item.duration = parseColonDuration(d[1])
		} else if d := durationMinsRe.FindStringSubmatch(section); d != nil {
			mins, _ := strconv.Atoi(d[1])
			item.duration = mins * 60
		}
	}
	if m := featuringRe.FindStringSubmatch(s); m != nil {
		for _, tm := range tagItemRe.FindAllStringSubmatch(m[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(tm[1]))
			if name == "" {
				continue
			}
			item.tags = append(item.tags, name)
		}
	}
}

// absURL resolves a root-relative thumbnail against the site base. Most sites
// in this template print an absolute CDN URL; Dreamnet's prints "/v3/content/…".
func absURL(siteBase, u string) string {
	if strings.HasPrefix(u, "/") {
		return siteBase + u
	}
	return u
}

// stripTags removes the minimum HTML noise (br, anchors) likely to sneak into
// a <p> body. Not a general HTML sanitizer.
func stripTags(s string) string {
	s = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func parseColonDuration(s string) int {
	parts := strings.Split(s, ":")
	total := 0
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		total = total*60 + n
	}
	return total
}

// URL mode handling — full catalog vs category page.

type listMode int

const (
	modeFullCatalog listMode = iota
	modeCategory
)

type listConfig struct {
	mode listMode
	slug string
}

func parseStudioURL(studioURL string) listConfig {
	if m := categorySlug.FindStringSubmatch(studioURL); m != nil {
		slug := m[1]
		if slug != "movies" {
			return listConfig{mode: modeCategory, slug: slug}
		}
	}
	return listConfig{mode: modeFullCatalog}
}

func (lc listConfig) pageURL(base, tourPrefix string, page int) string {
	switch lc.mode {
	case modeCategory:
		return fmt.Sprintf("%s%s/categories/%s/%d/latest/", base, tourPrefix, lc.slug, page)
	default:
		return fmt.Sprintf("%s%s/categories/movies/%d/latest/", base, tourPrefix, page)
	}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	lc := parseStudioURL(studioURL)
	if lc.mode == modeCategory {
		scraper.Debugf(1, "%s: scraping category %q", s.cfg.ID, lc.slug)
	} else {
		scraper.Debugf(1, "%s: scraping full catalog", s.cfg.ID)
	}

	items, sentTotal := s.collectListing(ctx, lc, opts, out)
	if items == nil || ctx.Err() != nil {
		return
	}
	if !sentTotal && len(items) > 0 {
		select {
		case out <- scraper.Progress(len(items)):
		case <-ctx.Done():
			return
		}
	}

	s.fetchDetails(ctx, items, opts, out)
}

func (s *Scraper) collectListing(ctx context.Context, lc listConfig, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []sceneItem, sentTotal bool) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return items, sentTotal
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return items, sentTotal
			}
		}

		pageURL := lc.pageURL(s.cfg.SiteBase, s.cfg.TourPrefix, page)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", s.cfg.ID, page, pageURL)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return nil, sentTotal
		}

		scenes := parseListing(body)
		if len(scenes) == 0 {
			return items, sentTotal
		}

		if !sentTotal {
			total := estimateTotal(body, len(scenes))
			scraper.Debugf(1, "%s: %d total scenes (estimated)", s.cfg.ID, total)
			if total > 0 {
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return items, sentTotal
				}
			}
			sentTotal = true
		}

		for _, item := range scenes {
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, item.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, sentTotal
			}
			items = append(items, item)
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, items []sceneItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(items), workers)

	work := make(chan sceneItem, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(work)

	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				body, err := s.fetchPage(ctx, item.url)
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, err)):
					case <-ctx.Done():
						return
					}
					continue
				}
				enrichFromDetail(body, &item)
				select {
				case out <- scraper.Scene(item.toScene(s.cfg.ID, s.cfg.SiteBase, s.cfg.Studio, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, item := range items {
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
}

func (item sceneItem) toScene(siteID, siteBase, studio string, now time.Time) models.Scene {
	url := item.url
	if strings.HasPrefix(url, "/") {
		url = siteBase + url
	}
	return models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   siteBase,
		Title:       item.title,
		URL:         url,
		Thumbnail:   absURL(siteBase, item.thumb),
		Date:        item.date,
		Duration:    item.duration,
		Description: item.description,
		Tags:        item.tags,
		Studio:      studio,
		ScrapedAt:   now,
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

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := checkCMSError(body); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	return body, nil
}

// dbErrorRe matches the error page this CMS serves when its database is
// unreachable. It comes back as HTTP 200 with a short body, so without this
// check a site-side outage reads as a page with no scenes on it — every
// listing parses to zero results and the run looks like an empty catalogue
// rather than a failure.
var dbErrorRe = regexp.MustCompile(`(?i)Error</B></span> connecting to database server|PDO Error Message`)

func checkCMSError(body []byte) error {
	if dbErrorRe.Match(body) {
		return errors.New("site database is unreachable (CMS returned its DB-error page with HTTP 200)")
	}
	return nil
}

// Package shopmaker scrapes sites running the Shopmaker clip-store tour
// (files5.shopmaker.com assets, Bootstrap card grid).
//
// Listing card at `/collections/page/{N}?media=video`:
//
//	<div class="card" id="collection_1062034128">
//	  <a href="/collections/pegged">
//	    <div class="init-video" data-attributes="{…&quot;poster&quot;:&quot;https://images5…/md-0.jpg&quot;…}"
//	                            data-sources="[{&quot;src&quot;:&quot;https://files5…_background.mp4&quot;…}]"></div></a>
//	  <div class="card-body">
//	    <div class="meta-description">
//	      <span title="Published at">…<span class="fa5-text">2026-08-16</span></span>
//	      <span title="Models">…<a href="/models/x">MISTRESS DAMAZONIA</a></span>
//	      <span title="Media">…<span class="fa5-text">16:23 minutes</span></span>
//	      <span title="Category">…<a href="/collections?category=Anal+Sex">Anal Sex</a></span>
//	    </div>
//	    <h2 class="card-title"><a href="/collections/pegged">Pegged</a></h2>
//	  </div>
//	</div>
//
// Everything but the description is on the card; the detail page carries that
// as `og:description`.
//
// Two things the tour gets wrong for a scraper. **The apex host drops the path
// on redirect** — `kinkymistresses.com/collections/page/2` lands on
// `www.kinkymistresses.com/collections` — so the config's SiteBase must name
// the www host or every page is page 1. And **the pager is a sliding window**
// (page N advertises N+1 as its highest link), so the walk ends on an empty
// page, not on a page count. Prices are shown in the visitor's own currency,
// which would store a number that depends on where the scrape ran, so none is
// recorded.
package shopmaker

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

// SiteConfig describes one Shopmaker site.
type SiteConfig struct {
	SiteID     string
	Domain     string // bare domain, e.g. "kinkymistresses.com"
	StudioName string
	// Locale pins the content language of a site that localises by
	// Accept-Language; empty leaves the request untouched.
	Locale string
	// RecommendedDelay is the pace the origin tolerates; Andy-Star answers 429
	// to everything for minutes after roughly 250 requests in a few minutes.
	RecommendedDelay time.Duration
}

var sites = []SiteConfig{
	{SiteID: "kinkymistresses", Domain: "kinkymistresses.com", StudioName: "Kinky Mistresses"},
	{SiteID: "andystar", Domain: "andy-star-videos.com", StudioName: "Andy-Star", Locale: "en", RecommendedDelay: 2500 * time.Millisecond},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
		// The apex 301s to www and drops the path doing it, so requests go to
		// the www host.
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	d := s.cfg.Domain
	return []string{d, d + "/videos", d + "/collections", d + "/collections?category={name}", d + "/models/{slug}"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.WarnDelayBelow(s.cfg.SiteID, opts.Delay, s.cfg.RecommendedDelay)
	l := parseListingURL(studioURL)
	scraper.Debugf(1, "%s: scraping %s", s.cfg.SiteID, l.kind)
	items, ok := s.collectListing(ctx, l, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

func (s *Scraper) collectListing(ctx context.Context, l listingMode, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, ok bool) {
	seen := make(map[string]bool)

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return items, false
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return items, false
		}

		pageURL := s.base + l.pagePath(page)
		if s.cfg.Locale != "" {
			pageURL = withQuery(pageURL, "locale="+s.cfg.Locale)
		}
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", s.cfg.SiteID, page, pageURL)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return items, false
		}

		parsed := parseListing(body)
		if l.videoOnly {
			parsed = withDuration(parsed)
		}
		// The pager is a sliding window — page N advertises N+1 as its highest
		// link — so an empty page is the only end-of-listing signal.
		if len(parsed) == 0 {
			return items, true
		}

		fresh := 0
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, item.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, true
			}
			fresh++
			items = append(items, item)
		}
		if fresh == 0 {
			return items, true
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, items []listItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(items) == 0 {
		return
	}
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	workers := scraper.WorkerCount(opts, 4)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	work := make(chan listItem, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(work)

	now := time.Now().UTC()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				detailURL := s.base + item.path
				if s.cfg.Locale != "" {
					detailURL = withQuery(detailURL, "locale="+s.cfg.Locale)
				}
				body, err := s.fetchPage(ctx, detailURL)
				if err != nil {
					// The card carries everything but the description.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.path, err)):
					case <-ctx.Done():
						return
					}
				} else {
					item.description = parseDescription(body)
					if item.date == "" {
						applyMeta(detailMeta(body), &item)
					}
				}
				select {
				case out <- scraper.Scene(s.toScene(item, studioURL, now)):
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

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- parsing ----

type listItem struct {
	id          string
	path        string
	title       string
	thumbnail   string
	preview     string
	performers  []string
	categories  []string
	date        string
	duration    int
	description string
}

var (
	cardStartRe      = regexp.MustCompile(`<div class="card(?: collection)?" id="collection_(\d+)">`)
	titleCardStartRe = regexp.MustCompile(`<div class="card">`)
	collectionIDRe   = regexp.MustCompile(`data-collection="(\d+)"`)
	titleCardRe      = regexp.MustCompile(`(?s)<div class="title-card-outer"><a href="(/collections/[^"]+)"[^>]*>(.*?)</a>`)
	viewCardRe       = regexp.MustCompile(`<div class="view-card"><span>([\d:]+)</span>\s*[Mm]inutes?`)
	cardTitleRe      = regexp.MustCompile(`(?s)<h2 class="card-title[^"]*"><a href="(/collections/[^"]+)"[^>]*>(.*?)</a>`)
	// The player's config is an HTML-escaped JSON blob in a data attribute.
	posterRe   = regexp.MustCompile(`&quot;poster&quot;:&quot;([^&]+)&quot;`)
	sourceRe   = regexp.MustCompile(`&quot;src&quot;:&quot;([^&]+\.mp4[^&]*)&quot;`)
	metaSpanRe = regexp.MustCompile(`(?s)<span title="([^"]+)"[^>]*>(.*?)</span>\s*(?:&minus;|</div>)`)
	fa5TextRe  = regexp.MustCompile(`(?s)<span class="fa5-text">(.*?)</span>`)
	// Attribute-aware: the Models anchors carry a tooltip whose value is an
	// escaped <img …/> tag, so it contains a real ">" inside quotes. A plain
	// `<a[^>]*>` stops at that and leaves markup in the performer name.
	anchorRe   = regexp.MustCompile(`(?s)<a(?:"[^"]*"|'[^']*'|[^>"'])*>(.*?)</a>`)
	tagStripRe = regexp.MustCompile(`<[^>]*>`)
	minutesRe  = regexp.MustCompile(`([\d:]+)\s*minutes?`)
	ogDescRe   = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
)

// listingMode is the page sequence one operator URL selects.
type listingMode struct {
	kind string
	// videoOnly drops cards with no runtime, for listings that cannot be
	// filtered to video server-side.
	videoOnly bool
	pagePath  func(page int) string
}

var (
	modelPathRe = regexp.MustCompile(`^https?://[^/]+/models/([^/?#]+)`)
	categoryRe  = regexp.MustCompile(`^https?://[^/]+/collections/?\?(?:[^#]*&)?category=([^&#]+)`)
)

func parseListingURL(studioURL string) listingMode {
	if m := modelPathRe.FindStringSubmatch(studioURL); m != nil {
		slug := m[1]
		return listingMode{kind: "model " + slug, videoOnly: true, pagePath: func(page int) string {
			if page <= 1 {
				return "/models/" + slug
			}
			return fmt.Sprintf("/models/%s/page/%d", slug, page)
		}}
	}
	if m := categoryRe.FindStringSubmatch(studioURL); m != nil {
		cat := m[1]
		return listingMode{kind: "category " + cat, pagePath: func(page int) string {
			return fmt.Sprintf("/collections/page/%d?category=%s&media=video", page, cat)
		}}
	}
	return listingMode{kind: "full catalogue", pagePath: func(page int) string {
		// media=video keeps photo sets out of the catalogue.
		return fmt.Sprintf("/collections/page/%d?media=video", page)
	}}
}

func withQuery(u, kv string) string {
	if strings.Contains(u, "?") {
		return u + "&" + kv
	}
	return u + "?" + kv
}

func withDuration(items []listItem) []listItem {
	out := items[:0]
	for _, it := range items {
		if it.duration > 0 {
			out = append(out, it)
		}
	}
	return out
}

var detailMetaRe = regexp.MustCompile(`(?s)<div class="posted-info meta-description[^"]*">(.*?)</div>`)

func detailMeta(body []byte) []byte {
	if m := detailMetaRe.FindSubmatch(body); m != nil {
		// applyMeta expects each span to be followed by a separator or the strip's end.
		return append(append([]byte(nil), m[1]...), "</div>"...)
	}
	return nil
}

func parseListing(body []byte) []listItem {
	if cardStartRe.Match(body) {
		return parseCollectionCards(body)
	}
	return parseTitleCards(body)
}

// parseTitleCards reads the plain `<div class="card">` theme, whose card has
// no id and carries only title, runtime, poster and preview; the rest comes
// from the detail page.
func parseTitleCards(body []byte) []listItem {
	locs := titleCardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		idm := collectionIDRe.FindSubmatch(block)
		tm := titleCardRe.FindSubmatch(block)
		if idm == nil || tm == nil {
			continue
		}
		item := listItem{
			id:    string(idm[1]),
			path:  html.UnescapeString(string(tm[1])),
			title: cleanText(string(tm[2])),
		}
		if item.title == "" {
			continue
		}
		if p := posterRe.FindSubmatch(block); p != nil {
			item.thumbnail = html.UnescapeString(string(p[1]))
		}
		if p := sourceRe.FindSubmatch(block); p != nil {
			item.preview = html.UnescapeString(string(p[1]))
		}
		if d := viewCardRe.FindSubmatch(block); d != nil {
			item.duration = parseutil.ParseDurationColon(string(d[1]))
		}
		items = append(items, item)
	}
	return items
}

func parseCollectionCards(body []byte) []listItem {
	locs := cardStartRe.FindAllSubmatchIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		item := listItem{id: string(body[loc[2]:loc[3]])}
		m := cardTitleRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item.path = html.UnescapeString(string(m[1]))
		item.title = cleanText(string(m[2]))
		if item.title == "" {
			continue
		}
		if p := posterRe.FindSubmatch(block); p != nil {
			item.thumbnail = html.UnescapeString(string(p[1]))
		}
		if p := sourceRe.FindSubmatch(block); p != nil {
			item.preview = html.UnescapeString(string(p[1]))
		}
		applyMeta(block, &item)
		items = append(items, item)
	}
	return items
}

// applyMeta reads the card's meta strip, which labels each field with a `title`
// attribute rather than a class.
func applyMeta(block []byte, item *listItem) {
	for _, m := range metaSpanRe.FindAllSubmatch(block, -1) {
		label := string(m[1])
		inner := m[2]
		switch label {
		case "Published at":
			if t := fa5TextRe.FindSubmatch(inner); t != nil {
				item.date = cleanText(string(t[1]))
			}
		case "Models":
			item.performers = anchorNames(inner)
		case "Media":
			if t := fa5TextRe.FindSubmatch(inner); t != nil {
				if d := minutesRe.FindStringSubmatch(cleanText(string(t[1]))); d != nil {
					item.duration = parseutil.ParseDurationColon(d[1])
				}
			}
		// The label is singular on some cards and plural on others.
		case "Category", "Categories":
			item.categories = anchorNames(inner)
		}
	}
}

func parseDescription(body []byte) string {
	if m := ogDescRe.FindSubmatch(body); m != nil {
		return cleanText(string(m[1]))
	}
	return ""
}

func anchorNames(block []byte) []string {
	var names []string
	seen := make(map[string]bool)
	for _, m := range anchorRe.FindAllSubmatch(block, -1) {
		n := cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		names = append(names, n)
	}
	return names
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         s.base + item.path,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Preview:     item.preview,
		Performers:  item.performers,
		Categories:  item.categories,
		Studio:      s.cfg.StudioName,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "2006-01-02"); err == nil {
		sc.Date = d
	}
	return sc
}

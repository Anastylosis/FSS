// Package ukxxxpass scrapes the ukXXXpass network — splatbukkake.xxx,
// ukpornparty.xxx and sexyukpornstars.xxx — which run one Laravel/Livewire app
// per host.
//
// Each host lists the catalogue at `/movies?page={N}`; everything is also
// filed under `/movies/studio/{id}/{slug}`, and such a URL scrapes just that
// studio. Listing card:
//
//	<div class="movieItem" wire:key="32520">
//	  <a href="https://splatbukkake.xxx/movie/32520/july-2026-…"><img src="/movie/c/2/…/thumbs/thumb.jpg"></a>
//	  <div class="title"><a href="…">July 2026 Bukkake Party in Bristol …</a></div>
//	  <div class="actors"><a href="…/model/10818/penny-charms">Penny Charms</a>, …</div>
//	  <div class="text-xs …">26.08.2026</div>
//	</div>
//
// The card carries the title, cast, date and thumbnail; `/movie/{id}/{slug}`
// adds the description and names the studio the scene was released by, which
// matters because every host serves several.
//
// Page 1 of `/movies` interleaves a "Check out these DVDs as well" strip of
// DVD cards, which are not scenes and are cut out before parsing. The pager is
// Livewire's (`wire:click="nextPage('page')"`); a page without a next button
// is the last one.
package ukxxxpass

import (
	"bytes"
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

// SiteConfig describes one ukXXXpass host.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []SiteConfig{
	{SiteID: "splatbukkake", Domain: "splatbukkake.xxx", StudioName: "Splat Bukkake"},
	{SiteID: "ukpornparty", Domain: "ukpornparty.xxx", StudioName: "UK Porn Party"},
	{SiteID: "sexyukpornstars", Domain: "sexyukpornstars.xxx", StudioName: "Sexy UK Pornstars"},
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
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/movies",
		s.cfg.Domain + "/movies/studio/{id}/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

// studioPathRe recognises the per-studio listing the network files its brands
// under.
var studioPathRe = regexp.MustCompile(`(/movies/studio/\d+/[^/?#]+)`)

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listPath := "/movies"
	if m := studioPathRe.FindStringSubmatch(studioURL); m != nil {
		listPath = m[1]
		scraper.Debugf(1, "%s: scraping studio listing %s", s.cfg.SiteID, listPath)
	} else {
		scraper.Debugf(1, "%s: scraping full catalogue", s.cfg.SiteID)
	}

	seen := make(map[string]bool)
	now := time.Now().UTC()

	// Details are fetched a page at a time rather than after the whole walk,
	// so scenes stream out instead of arriving in one burst at the end.
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", s.base, listPath, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		parsed := parseListing(body)
		info := parsePager(body)
		if len(parsed) == 0 {
			// A page the site says has scenes on it, but that yielded no
			// cards, is a markup change rather than the end of the listing.
			if info.hasNext || (page == 1 && info.sceneCount != 0) {
				return scraper.PageResult{}, scraper.ParseError(pageURL, errNoCards)
			}
			return scraper.PageResult{}, nil
		}

		fresh := parsed[:0]
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			fresh = append(fresh, item)
		}
		// A page whose cards were all seen already means the listing is
		// repeating; Paginate's own guards then end the walk.
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := s.enrichPage(ctx, fresh, studioURL, opts, out, now)
		// Without a pager at all (a one-page studio, or a pager redesign) the
		// walk falls back to ending on the first page with no cards.
		return scraper.PageResult{
			Scenes: scenes,
			Total:  max(info.sceneCount, 0),
			Done:   info.hasPager && !info.hasNext,
		}, nil
	})
}

// enrichPage fetches one page's detail pages concurrently and returns the
// scenes in listing order.
func (s *Scraper) enrichPage(ctx context.Context, items []listItem, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	// Paginate stops at the first known ID without emitting it or anything
	// after it, so their detail pages would be fetched for nothing.
	fetchN := len(items)
	for i, item := range items {
		if opts.KnownIDs[item.id] {
			fetchN = i
			break
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	errs := make([]error, len(items))

	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, fetchN, workers)
	for i := range items[:fetchN] {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			body, err := s.fetchPage(ctx, items[idx].url)
			if err != nil {
				errs[idx] = err
				return
			}
			enrichFromDetail(body, &items[idx])
		}(i)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(items))
	for i, item := range items {
		if errs[i] != nil {
			// The card already names the scene, its cast and its date; a
			// detail page that will not load costs the description and the
			// releasing studio, so the scene is still emitted.
			select {
			case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, errs[i])):
			case <-ctx.Done():
				return scenes
			}
		}
		scenes = append(scenes, s.toScene(item, studioURL, now))
	}
	return scenes
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
	url         string
	title       string
	thumbnail   string
	performers  []string
	date        string
	description string
	studio      string
}

// descWindow bounds the search for the description to the block that follows
// the release line.
const descWindow = 2000

var errNoCards = errors.New("listing page has no scene cards")

var (
	// Cards carry a wire:key="{id}" attribute since the 2026-09 markup.
	cardStartRe = regexp.MustCompile(`<div class="movieItem"[^>]*>`)
	// The DVD strip is a full-width grid cell holding a "View all DVDs"
	// button that switches the listing type.
	colSpanRe    = regexp.MustCompile(`<div class="col-span-[^"]*"`)
	divTagRe     = regexp.MustCompile(`<div\b|</div>`)
	dvdSwitch    = []byte(`setType('movie')`)
	pagerNav     = []byte(`aria-label="Pagination Navigation"`)
	pagerNext    = []byte(`wire:click="nextPage(`)
	sceneCountRe = regexp.MustCompile(`sceneCount(?:&quot;|")\s*:\s*(\d+)`)
	cardURLRe    = regexp.MustCompile(`href="([^"]*/movie/(\d+)/[^"]*)"`)
	cardThumbRe  = regexp.MustCompile(`<img src="([^"]+)"`)
	cardTitleRe  = regexp.MustCompile(`(?s)<div class="title">.*?<a[^>]*>(.*?)</a>`)
	cardActorRe  = regexp.MustCompile(`(?s)<div class="actors">(.*?)</div>`)
	cardDateRe   = regexp.MustCompile(`(\d{2}\.\d{2}\.\d{4})`)

	detailTitleRe = regexp.MustCompile(`(?s)<div class="movieTitle">(.*?)</div>`)
	// "Released on: 26-08-2026 by: <a …>SplatBukkake</a>"
	detailReleaseRe = regexp.MustCompile(`(?s)Released on:\s*(\d{2}-\d{2}-\d{4})\s*by:\s*<a[^>]*>(.*?)</a>`)
	// The description is bare text after the release line's enclosing divs
	// close, ending at the <br> the cast list follows.
	// The description is one bare text node between the release block's
	// closing </div> and the <br> the cast list follows. It is matched inside
	// a window after the release line, because a scene with no description at
	// all would otherwise let the search run on into the related-movies grid
	// and store a neighbour's card text.
	detailDescRe   = regexp.MustCompile(`(?s)</div>\s*([^<]{10,})\s*<br`)
	anchorRe       = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	blockCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagStripRe     = regexp.MustCompile(`<[^>]*>`)
)

type pagerInfo struct {
	hasPager bool
	hasNext  bool
	// sceneCount is the listing's own scene total, or -1 when absent.
	sceneCount int
}

func parsePager(body []byte) pagerInfo {
	info := pagerInfo{
		hasPager:   bytes.Contains(body, pagerNav),
		hasNext:    bytes.Contains(body, pagerNext),
		sceneCount: -1,
	}
	if m := sceneCountRe.FindSubmatch(body); m != nil {
		if n, err := strconv.Atoi(string(m[1])); err == nil {
			info.sceneCount = n
		}
	}
	return info
}

// stripDVDStrip cuts the DVD recommendation cell out of the listing grid.
func stripDVDStrip(body []byte) []byte {
	for _, loc := range colSpanRe.FindAllIndex(body, -1) {
		end := divEnd(body, loc[0])
		if end < 0 || !bytes.Contains(body[loc[0]:end], dvdSwitch) {
			continue
		}
		cut := make([]byte, 0, len(body)-(end-loc[0]))
		cut = append(cut, body[:loc[0]]...)
		cut = append(cut, body[end:]...)
		return stripDVDStrip(cut)
	}
	return body
}

// divEnd returns the offset just past the </div> closing the <div> at start,
// or -1 when it is never closed.
func divEnd(body []byte, start int) int {
	depth := 0
	for _, loc := range divTagRe.FindAllIndex(body[start:], -1) {
		if body[start+loc[0]+1] == '/' {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return start + loc[1]
		}
	}
	return -1
}

func parseListing(body []byte) []listItem {
	body = stripDVDStrip(body)
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		m := cardURLRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item := listItem{url: html.UnescapeString(string(m[1])), id: string(m[2])}
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if t := cardThumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := cardActorRe.FindSubmatch(block); t != nil {
			item.performers = anchorNames(t[1])
		}
		if t := cardDateRe.FindSubmatch(block); t != nil {
			item.date = string(t[1])
		}
		if item.title == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

func enrichFromDetail(body []byte, item *listItem) {
	if m := detailTitleRe.FindSubmatch(body); m != nil {
		if t := cleanText(string(m[1])); t != "" {
			item.title = t
		}
	}
	if m := detailReleaseRe.FindSubmatch(body); m != nil {
		// The detail spells the date DD-MM-YYYY where the card writes
		// DD.MM.YYYY; both are day-first.
		item.date = strings.ReplaceAll(string(m[1]), "-", ".")
		item.studio = cleanText(string(m[2]))
	}
	if loc := detailReleaseRe.FindIndex(body); loc != nil {
		window := body[loc[1]:min(loc[1]+descWindow, len(body))]
		if m := detailDescRe.FindSubmatch(window); m != nil {
			item.description = cleanText(string(m[1]))
		}
	}
}

func anchorNames(block []byte) []string {
	var names []string
	seen := make(map[string]bool)
	for _, m := range anchorRe.FindAllSubmatch(block, -1) {
		n := cleanText(string(m[1]))
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		names = append(names, n)
	}
	return names
}

// cleanText drops the Livewire block comments the template sprinkles
// everywhere ("<!--[if BLOCK]><![endif]-->") along with the markup.
func cleanText(s string) string {
	s = blockCommentRe.ReplaceAllString(s, " ")
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	studio := s.cfg.StudioName
	if item.studio != "" {
		// splatbukkake.xxx hosts several brands, and the detail page names the
		// one that released the scene.
		studio = item.studio
	}
	sc := models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Description: item.description,
		Thumbnail:   absURL(s.base, item.thumbnail),
		Performers:  item.performers,
		Studio:      studio,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "02.01.2006"); err == nil {
		sc.Date = d
	}
	return sc
}

func absURL(base, u string) string {
	if u == "" || strings.HasPrefix(u, "http") {
		return u
	}
	return base + "/" + strings.TrimPrefix(u, "/")
}

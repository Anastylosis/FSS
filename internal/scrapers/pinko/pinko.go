// Package pinko scrapes the Pinko network sites — Pinko TGirls
// (pinkotgirls.com) and Pinko Club (pinkoclub.com). The two sites no longer
// share a template:
//
//   - pinkotgirls.com still runs the original custom PHP CMS: ID-descending
//     `/new-video.php?next={N}` listing pages carry the scene ID, English
//     title and CDN thumbnail on each `link-photo-home` card; cast is scoped
//     to the `titolo-video` block's `/trans-star/` links. The site exposes
//     neither a publish date nor a duration anywhere, so Scene.Date and
//     Scene.Duration are intentionally left zero for this site. The static
//     JSON-LD VideoObject on detail pages is a copy-paste artifact (it
//     references unrelated scenes) and is deliberately ignored.
//   - pinkoclub.com was rebuilt on a modern template: listing pages moved to
//     `/new-video.php?page={N}` (still ID-descending) with `article.card`
//     entries whose href carries the scene ID after a sub-brand "line"
//     segment (`/video-porno-italiani/`, `/frameleaks/`, `/xtime/`,
//     `/pinkocomics/`, …) — the numeric ID is what matters, not which line
//     segment precedes it. Detail pages expose a real duration and publish
//     date in the `.video-meta` block, a full per-scene description in
//     `span.video-desc__full` (og:description on this template is
//     boilerplate marketing copy and is intentionally ignored), and cast
//     linked via `/pornostar/{slug}.php` next to "View profile →".
//
// SiteConfig.Modern selects which parser (legacy vs. modern) a given site
// uses; a worker pool fetches each detail page found on the listing to
// enrich the scene with description/cast/date/duration.
package pinko

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one Pinko network site.
type SiteConfig struct {
	SiteID       string // stable scraper identifier
	Domain       string // bare hostname for URL matching
	Base         string // scheme + host, no trailing slash
	StudioName   string // human-readable studio name
	DetailPrefix string // legacy-template scene-path prefix, e.g. "/videotrans/". Unused when Modern is true.
	Modern       bool   // true for sites running the redesigned template (pinkoclub.com)
}

var sites = []SiteConfig{
	{SiteID: "pinkotgirls", Domain: "pinkotgirls.com", Base: "https://www.pinkotgirls.com", StudioName: "Pinko TGirls", DetailPrefix: "/videotrans/"},
	{SiteID: "pinkoclub", Domain: "pinkoclub.com", Base: "https://www.pinkoclub.com", StudioName: "Pinko Club", Modern: true},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// Scraper implements scraper.StudioScraper for one Pinko site.
type Scraper struct {
	cfg      SiteConfig
	Client   *http.Client
	matchRe  *regexp.Regexp
	cardIDRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New builds a Scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	dom := regexp.QuoteMeta(cfg.Domain)
	s := &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + dom + `(?:/|$)`),
	}
	if cfg.Modern {
		// The modern template splits scenes across several sub-brand "line"
		// path segments (/video-porno-italiani/, /frameleaks/, /xtime/,
		// /pinkocomics/, …); only the numeric ID after the segment matters.
		s.cardIDRe = modernCardIDRe
	} else {
		prefix := regexp.QuoteMeta(strings.Trim(cfg.DetailPrefix, "/"))
		s.cardIDRe = regexp.MustCompile(`/` + prefix + `/(\d+)-`)
	}
	return s
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/new-video.php",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one entry parsed from a listing page.
type card struct {
	id    string
	url   string
	title string
	thumb string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan card)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching detail pages with %d workers", s.cfg.SiteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range work {
				scene, err := s.fetchDetail(ctx, c, studioURL, opts.Delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		s.enqueueListing(ctx, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueueListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- card) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		pageURL := s.listURL(page)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", s.cfg.SiteID, page, pageURL)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}
		cards := s.parseListing(body)
		if len(cards) == 0 {
			scraper.Debugf(1, "%s: page %d empty, stopping", s.cfg.SiteID, page)
			return
		}
		for _, c := range cards {
			if opts.KnownIDs[c.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, c.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- c:
			case <-ctx.Done():
				return
			}
		}
	}
}

// listURL builds the 1-indexed listing page URL. The legacy template's page
// parameter is `next=`; the modern template (pinkoclub.com) uses `page=`.
func (s *Scraper) listURL(page int) string {
	if s.cfg.Modern {
		return fmt.Sprintf("%s/new-video.php?page=%d", s.cfg.Base, page)
	}
	return fmt.Sprintf("%s/new-video.php?next=%d", s.cfg.Base, page)
}

// listingCardRe captures href, title and thumbnail src from a legacy-template
// card anchor.
var listingCardRe = regexp.MustCompile(`<a class="link-photo-home" href="([^"]+)" title="([^"]*)">\s*<img[^>]*?\bsrc="([^"]+)"`)

// modernCardIDRe extracts the numeric scene ID from a modern-template href,
// regardless of which sub-brand "line" path segment precedes it
// (/video-porno-italiani/, /frameleaks/, /xtime/, /pinkocomics/, …).
var modernCardIDRe = regexp.MustCompile(`^/[a-z0-9-]+/(\d+)-`)

// modernArticleRe isolates one listing card block.
var modernArticleRe = regexp.MustCompile(`(?s)<article class="card">(.*?)</article>`)

// modernHrefRe, modernThumbRe and modernTitleRe pull the fields out of one
// modern-template card block.
var (
	modernHrefRe  = regexp.MustCompile(`<a href="([^"]+)"`)
	modernThumbRe = regexp.MustCompile(`<img src="(https://img\.pinkocdn\.com/[^"]+)"`)
	modernTitleRe = regexp.MustCompile(`<h3 class="card__title">([^<]*)</h3>`)
)

// parseListing extracts the ID, detail URL, title and thumbnail from each
// card on a listing page, de-duplicating by ID.
func (s *Scraper) parseListing(body []byte) []card {
	if s.cfg.Modern {
		return s.parseListingModern(body)
	}
	return s.parseListingLegacy(body)
}

func (s *Scraper) parseListingLegacy(body []byte) []card {
	page := string(body)
	ms := listingCardRe.FindAllStringSubmatch(page, -1)
	cards := make([]card, 0, len(ms))
	seen := map[string]bool{}
	for _, m := range ms {
		href := m[1]
		idm := s.cardIDRe.FindStringSubmatch(href)
		if idm == nil {
			continue
		}
		id := idm[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		cards = append(cards, card{
			id:    id,
			url:   s.absURL(href),
			title: strings.TrimSpace(html.UnescapeString(m[2])),
			thumb: m[3],
		})
	}
	return cards
}

func (s *Scraper) parseListingModern(body []byte) []card {
	blocks := modernArticleRe.FindAllString(string(body), -1)
	cards := make([]card, 0, len(blocks))
	seen := map[string]bool{}
	for _, block := range blocks {
		hm := modernHrefRe.FindStringSubmatch(block)
		if hm == nil {
			continue
		}
		href := hm[1]
		idm := s.cardIDRe.FindStringSubmatch(href)
		if idm == nil {
			continue
		}
		id := idm[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		c := card{id: id, url: s.absURL(href)}
		if tm := modernTitleRe.FindStringSubmatch(block); tm != nil {
			c.title = strings.TrimSpace(html.UnescapeString(tm[1]))
		}
		if thm := modernThumbRe.FindStringSubmatch(block); thm != nil {
			c.thumb = thm[1]
		}
		cards = append(cards, c)
	}
	return cards
}

type detailData struct {
	title       string
	description string
	image       string
	performers  []string
	date        time.Time
	duration    int
}

var (
	// titoloH4Re isolates the cast <h4> inside the scene's title block so
	// performer links are scoped to the scene cast (not page chrome). Legacy
	// template only (Pinko TGirls).
	titoloH4Re = regexp.MustCompile(`(?s)<div class="titolo-video">.*?<h4>(.*?)</h4>`)
	// performerRe matches legacy-template cast anchors: /trans-star/.
	performerRe = regexp.MustCompile(`<a href="/trans-star/[^"]+"[^>]*>([^<]+)</a>`)
)

// parseDetail dispatches to the legacy or modern detail-page parser.
func (s *Scraper) parseDetail(body []byte) detailData {
	if s.cfg.Modern {
		return parseDetailModern(body)
	}
	return parseDetailLegacy(body)
}

// parseDetailLegacy pulls the title/description/image from OpenGraph tags
// and the cast from the title block. The static JSON-LD VideoObject is
// ignored. Neither date nor duration is available on this template.
func parseDetailLegacy(body []byte) detailData {
	og := parseutil.OpenGraph(body)
	d := detailData{
		title:       strings.TrimSpace(html.UnescapeString(og["og:title"])),
		description: strings.TrimSpace(html.UnescapeString(og["og:description"])),
		image:       strings.TrimSpace(html.UnescapeString(og["og:image"])),
	}
	if m := titoloH4Re.FindSubmatch(body); m != nil {
		for _, pm := range performerRe.FindAllStringSubmatch(string(m[1]), -1) {
			name := strings.TrimSpace(html.UnescapeString(pm[1]))
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}
	return d
}

var (
	// modernTitleH1Re is the authoritative scene title on the modern
	// template. og:title on this template appends " | Pinko Club" and is
	// used only as a fallback.
	modernTitleH1Re = regexp.MustCompile(`<h1 class="video-title">([^<]*)</h1>`)
	// modernDescFullRe holds the full per-scene description; og:description
	// on this template is generic boilerplate marketing copy and is
	// intentionally ignored.
	modernDescFullRe = regexp.MustCompile(`(?s)<span class="video-desc__full">(.*?)</span>`)
	// modernMetaBlockRe isolates the .video-meta block (views/date/duration/
	// likes) so date and duration extraction can't false-match elsewhere on
	// the page.
	modernMetaBlockRe = regexp.MustCompile(`(?s)<div class="video-meta">(.*?)</div>`)
	// modernDateRe pulls the publish date, formatted DD/MM/YYYY.
	modernDateRe = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	// modernDurationRe pulls the whole-minutes duration, e.g. "42 min".
	modernDurationRe = regexp.MustCompile(`(\d+)\s*min\b`)
	// modernPerformerRe matches modern-template cast entries: the performer
	// name immediately followed by its /pornostar/{slug}.php "View profile"
	// link.
	modernPerformerRe = regexp.MustCompile(`<strong>([^<]+)</strong>\s*<a href="/pornostar/[^"]+\.php">View profile`)
)

// parseDetailModern pulls title/description from the modern template's own
// markup (not OpenGraph, which is boilerplate on this template except for
// og:image), the cast from the "View profile" performer cards, and date/
// duration from the .video-meta block.
func parseDetailModern(body []byte) detailData {
	og := parseutil.OpenGraph(body)
	d := detailData{
		image: strings.TrimSpace(html.UnescapeString(og["og:image"])),
	}
	if m := modernTitleH1Re.FindSubmatch(body); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
	} else if t := strings.TrimSpace(html.UnescapeString(og["og:title"])); t != "" {
		if i := strings.LastIndex(t, " | "); i > 0 {
			t = t[:i]
		}
		d.title = t
	}
	if m := modernDescFullRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	for _, pm := range modernPerformerRe.FindAllSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(string(pm[1])))
		if name != "" {
			d.performers = append(d.performers, name)
		}
	}
	if mm := modernMetaBlockRe.FindSubmatch(body); mm != nil {
		block := mm[1]
		if dm := modernDateRe.FindSubmatch(block); dm != nil {
			if t, err := parseutil.TryParseDate(string(dm[1]), "02/01/2006"); err == nil {
				d.date = t
			}
		}
		if durm := modernDurationRe.FindSubmatch(block); durm != nil {
			if mins, err := strconv.Atoi(string(durm[1])); err == nil {
				d.duration = mins * 60
			}
		}
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, c card, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	scene := models.Scene{
		ID:        c.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       c.url,
		Thumbnail: c.thumb,
		Studio:    s.cfg.StudioName,
		ScrapedAt: time.Now().UTC(),
	}

	body, err := s.fetchPage(ctx, c.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", c.id, err)
	}
	d := s.parseDetail(body)
	if d.title != "" {
		scene.Title = d.title
	}
	scene.Description = d.description
	if scene.Thumbnail == "" && d.image != "" {
		scene.Thumbnail = d.image
	}
	scene.Performers = d.performers
	if !d.date.IsZero() {
		scene.Date = d.date
	}
	if d.duration > 0 {
		scene.Duration = d.duration
	}
	return scene, nil
}

func (s *Scraper) absURL(u string) string {
	if strings.HasPrefix(u, "http") {
		return u
	}
	if strings.HasPrefix(u, "/") {
		return s.cfg.Base + u
	}
	return s.cfg.Base + "/" + u
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// Package kickass scrapes the Kick Ass Pictures network. It reads only the
// public guest/tour pages; everything behind the join wall is left alone.
//
// The network runs two templates, so one table-driven config registers two
// kinds of scraper:
//
//   - Guest (17 sites, e.g. ultracuckolds.com, kickassteens.com) — a 12-card
//     `/guest/videos/?page=N` listing that names only the media id, title and
//     poster. The `/guest/video/?id=N` preview page carries the publication
//     date, the full description, the cast and an unsigned preview mp4, so
//     every card is followed by a detail fetch. The listing is not
//     date-ordered, so the KnownIDs early-stop is deliberately disabled.
//
//   - Tour (cumeatingcuckolds.com) — a 24-card `/tour/updates?page=N` listing
//     covering ~120 pages. Cards carry the cast and a truncated blurb; the
//     `/tour/updates/{id}` page carries the full one. Updates are a mix of
//     scenes and photo galleries and are badged as such, so photo-only updates
//     are skipped. This listing IS newest-first, so the early-stop applies.
//
// Neither template publishes a duration or tags, and the tour template
// publishes no date either — the fields simply are not on the public pages.
package kickass

import (
	"bytes"
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

// tmplKind selects the listing/detail parsing strategy for a site.
type tmplKind int

const (
	tmplGuest tmplKind = iota // /guest/videos/?page=N
	tmplTour                  // /tour/updates?page=N
)

// SiteConfig describes one Kick Ass Pictures site served by this package.
type SiteConfig struct {
	SiteID     string // stable lowercase id, e.g. "ultracuckolds"
	Domain     string // bare domain, e.g. "ultracuckolds.com"
	StudioName string // display name, e.g. "Ultra Cuckolds"
	Template   tmplKind
}

var sites = []SiteConfig{
	{SiteID: "cumeatingcuckolds", Domain: "cumeatingcuckolds.com", StudioName: "Cum Eating Cuckolds", Template: tmplTour},
	{SiteID: "ultracuckolds", Domain: "ultracuckolds.com", StudioName: "Ultra Cuckolds"},
	{SiteID: "kickasspictures", Domain: "kickass.com", StudioName: "Kick Ass Pictures"},
	{SiteID: "aloadineveryhole", Domain: "aloadineveryhole.com", StudioName: "A Load in Every Hole"},
	{SiteID: "barefootconfidential", Domain: "barefootconfidential.com", StudioName: "Barefoot Confidential"},
	{SiteID: "blackjelly", Domain: "black-jelly.com", StudioName: "Black Jelly"},
	{SiteID: "chicaboom", Domain: "chica-boom.com", StudioName: "Chica Boom"},
	{SiteID: "epichandjobs", Domain: "epichandjobs.com", StudioName: "Epic Handjobs"},
	{SiteID: "kickasspussypump", Domain: "kickasspussypump.com", StudioName: "Honey, We Blew Up Your Pussy"},
	{SiteID: "2blackmen4her", Domain: "2blackmen4her.com", StudioName: "Inseminated by 2 Black Men"},
	{SiteID: "milfdoesabonergood", Domain: "milfdoesabonergood.com", StudioName: "MILF Does a Boner Good"},
	{SiteID: "nakedgirlssmoking", Domain: "nakedgirlssmoking.com", StudioName: "Naked Girls Smoking"},
	{SiteID: "revengeisabitch", Domain: "revengeisabitch.com", StudioName: "Revenge is a Bitch!"},
	{SiteID: "babeswithglasses", Domain: "babeswithglasses.com", StudioName: "Specs Appeal"},
	{SiteID: "stoporillsquirt", Domain: "stoporillsquirt.com", StudioName: "Stop or I'll Squirt"},
	{SiteID: "kickassteens", Domain: "kickassteens.com", StudioName: "Teen Power"},
	{SiteID: "10mancumslam", Domain: "10mancumslam.com", StudioName: "10 Man Cum Slam"},
	{SiteID: "organicshemales", Domain: "organicshemales.com", StudioName: "100% Organic She-Males"},
	{SiteID: "5guycreampie", Domain: "5-guy-cream-pie.com", StudioName: "5-Guy Cream Pie"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// newFor builds the registered scraper for a given site id. Used by tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for a single Kick Ass site.
type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New constructs a Scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	if s.cfg.Template == tmplTour {
		return []string{
			s.cfg.Domain,
			s.cfg.Domain + "/tour/updates",
		}
	}
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/guest/videos/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if s.cfg.Template == tmplGuest {
		// The guest listing is ordered by neither date nor id, so a known id on
		// page 1 says nothing about page 2. Stopping there would leave scenes
		// uncollected and --full's authoritative Save would delete them.
		opts.KnownIDs = nil
	}

	items, stopped := s.collectListing(ctx, opts, out)
	if ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
	if stopped {
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

// ---- listing ----

type listItem struct {
	id         string
	url        string
	title      string
	thumbnail  string
	preview    string
	blurb      string
	performers []string
	date       time.Time
}

func (s *Scraper) listingURL(page int) string {
	if s.cfg.Template == tmplTour {
		return fmt.Sprintf("%s/tour/updates?page=%d", s.base, page)
	}
	return fmt.Sprintf("%s/guest/videos/?page=%d", s.base, page)
}

func (s *Scraper) collectListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, stoppedEarly bool) {
	seen := make(map[string]bool)
	sentTotal := false

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return items, false
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return items, false
			}
		}

		pageURL := s.listingURL(page)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", s.cfg.SiteID, page, pageURL)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return items, false
		}

		var parsed []listItem
		var pageNo, lastPage int
		if s.cfg.Template == tmplTour {
			parsed = parseTourListing(body, s.base)
			pageNo, lastPage = page, maxTourPage(body)
		} else {
			parsed = parseGuestListing(body, s.base)
			pageNo, lastPage = guestPageInfo(body)
		}
		if len(parsed) == 0 {
			return items, false
		}

		if !sentTotal && lastPage > 0 {
			total := lastPage * len(parsed)
			scraper.Debugf(1, "%s: ~%d scenes over %d pages", s.cfg.SiteID, total, lastPage)
			select {
			case out <- scraper.Progress(total):
			case <-ctx.Done():
				return items, false
			}
			sentTotal = true
		}

		fresh := 0
		for _, it := range parsed {
			if seen[it.id] {
				continue
			}
			seen[it.id] = true
			if opts.KnownIDs[it.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, it.id)
				return items, true
			}
			fresh++
			items = append(items, it)
		}

		// Past the last page the guest listing clamps to it rather than 404ing,
		// so the page number it prints is the end-of-list signal. A page that
		// added nothing new is the same signal for a template that prints none.
		if fresh == 0 || (lastPage > 0 && pageNo >= lastPage) {
			return items, false
		}
	}
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, items []listItem, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(items) == 0 {
		return
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
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
			for it := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				body, err := s.fetch(ctx, it.url)
				if err != nil {
					// The card already names the scene; a detail page that will
					// not load costs the description, cast and date, not the
					// scene itself.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", it.url, err)):
					case <-ctx.Done():
						return
					}
				} else if s.cfg.Template == tmplTour {
					enrichTourDetail(body, &it)
				} else {
					enrichGuestDetail(body, &it)
				}
				select {
				case out <- s.toScene(it, studioURL, now):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, it := range items {
		select {
		case work <- it:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) toScene(it listItem, studioURL string, now time.Time) scraper.SceneResult {
	return scraper.Scene(models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       it.title,
		URL:         it.url,
		Date:        it.date,
		Description: it.blurb,
		Thumbnail:   it.thumbnail,
		Preview:     it.preview,
		Performers:  it.performers,
		Studio:      s.cfg.StudioName,
		ScrapedAt:   now,
	})
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- guest template ----

var (
	guestCardRe     = regexp.MustCompile(`(?s)<div class="update-card" data-media-id="(\d+)".*?</div>\s*</a>\s*</div>`)
	guestCardIDRe   = regexp.MustCompile(`data-media-id="(\d+)"`)
	guestTitleRe    = regexp.MustCompile(`(?s)<h3 class="update-card-title">\s*(.*?)\s*</h3>`)
	guestThumbRe    = regexp.MustCompile(`<img\s+src="([^"]+)"`)
	guestPageInfoRe = regexp.MustCompile(`Page\s+(\d+)\s+of\s+(\d+)`)

	guestDetailTitleRe = regexp.MustCompile(`(?s)<h1 class="video-page-title">\s*(.*?)\s*</h1>`)
	guestDateRe        = regexp.MustCompile(`(?s)<p class="video-publish-date">\s*(.*?)\s*</p>`)
	guestDescRe        = regexp.MustCompile(`(?s)<p class="video-description">\s*(.*?)\s*</p>`)
	guestStarringRe    = regexp.MustCompile(`(?s)<span class="meta-label">Starring:</span>(.*?)</div>`)
	guestPosterRe      = regexp.MustCompile(`poster="([^"]+)"`)
	guestPreviewRe     = regexp.MustCompile(`<source\s+src="([^"]+\.mp4)"`)
	anchorTextRe       = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe         = regexp.MustCompile(`<[^>]*>`)
)

func parseGuestListing(body []byte, base string) []listItem {
	var items []listItem
	for _, block := range guestCardRe.FindAll(body, -1) {
		id := string(guestCardIDRe.FindSubmatch(block)[1])
		it := listItem{id: id, url: base + "/guest/video/?id=" + id}
		if m := guestTitleRe.FindSubmatch(block); m != nil {
			it.title = cleanText(string(m[1]))
		}
		if m := guestThumbRe.FindSubmatch(block); m != nil {
			it.thumbnail = html.UnescapeString(string(m[1]))
		}
		if it.title == "" {
			continue
		}
		items = append(items, it)
	}
	return items
}

// guestPageInfo reads the "Page N of M" banner the guest listing prints above
// the grid.
func guestPageInfo(body []byte) (page, last int) {
	m := guestPageInfoRe.FindSubmatch(body)
	if m == nil {
		return 0, 0
	}
	page, _ = strconv.Atoi(string(m[1]))
	last, _ = strconv.Atoi(string(m[2]))
	return page, last
}

func enrichGuestDetail(body []byte, it *listItem) {
	if m := guestDetailTitleRe.FindSubmatch(body); m != nil {
		if t := cleanText(string(m[1])); t != "" {
			it.title = t
		}
	}
	if m := guestDescRe.FindSubmatch(body); m != nil {
		it.blurb = cleanText(string(m[1]))
	}
	if m := guestDateRe.FindSubmatch(body); m != nil {
		if d, err := parseutil.TryParseDate(cleanText(string(m[1])), "January 2, 2006"); err == nil {
			it.date = d
		}
	}
	if m := guestStarringRe.FindSubmatch(body); m != nil {
		it.performers = anchorNames(m[1])
	}
	if m := guestPosterRe.FindSubmatch(body); m != nil {
		it.thumbnail = html.UnescapeString(string(m[1]))
	}
	if m := guestPreviewRe.FindSubmatch(body); m != nil {
		it.preview = html.UnescapeString(string(m[1]))
	}
}

// ---- tour template ----

var (
	tourCardSplitRe = regexp.MustCompile(`<div class="tile update-card">`)
	tourLinkRe      = regexp.MustCompile(`<a href="([^"]*/tour/updates/(\d+))"`)
	tourTitleRe     = regexp.MustCompile(`(?s)<h3 class="title"><a[^>]*>(.*?)</a>`)
	tourThumbRe     = regexp.MustCompile(`<img\s+src="([^"]+)"`)
	tourTrailerRe   = regexp.MustCompile(`<a href="([^"]+\.mp4[^"]*)"\s+data-trailer`)
	tourStarsRe     = regexp.MustCompile(`(?s)<div class="meta meta-stars">(.*?)</div>`)
	tourBlurbRe     = regexp.MustCompile(`(?s)<p class="update-blurb">\s*(.*?)\s*</p>`)
	tourVideoBadge  = []byte("badge-media--video")
	tourPageRe      = regexp.MustCompile(`/tour/updates\?page=(\d+)`)

	tourDetailBlurbRe = regexp.MustCompile(`(?s)<p class="blurb">\s*(.*?)\s*</p>`)
	tourDetailMetaRe  = regexp.MustCompile(`(?s)<div class="meta">\s*Starring:(.*?)</div>`)
	// The tour publishes no date field. A minority of remastered updates name
	// their original air date at the end of the description, which is the only
	// date the public pages carry at all.
	tourPremiereRe = regexp.MustCompile(`(?i)original premiere?\s*:?\s*(\d{1,2}/\d{1,2}/\d{4})`)
)

func parseTourListing(body []byte, base string) []listItem {
	locs := tourCardSplitRe.FindAllIndex(body, -1)
	var items []listItem
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		// Updates are a mix of scenes and photo galleries. Only the ones badged
		// as carrying a video are scenes.
		if !bytes.Contains(block, tourVideoBadge) {
			continue
		}
		m := tourLinkRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		// The card's own href is absolute against the live host; rebuilding it
		// from the configured base keeps a redirected or test host consistent.
		it := listItem{id: string(m[2]), url: base + "/tour/updates/" + string(m[2])}
		if t := tourTitleRe.FindSubmatch(block); t != nil {
			it.title = cleanText(string(t[1]))
		}
		if t := tourThumbRe.FindSubmatch(block); t != nil {
			it.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := tourTrailerRe.FindSubmatch(block); t != nil {
			it.preview = html.UnescapeString(string(t[1]))
		}
		if t := tourStarsRe.FindSubmatch(block); t != nil {
			it.performers = anchorNames(t[1])
		}
		if t := tourBlurbRe.FindSubmatch(block); t != nil {
			it.blurb = cleanText(string(t[1]))
		}
		if it.title == "" {
			continue
		}
		items = append(items, it)
	}
	return items
}

func maxTourPage(body []byte) int {
	last := 0
	for _, m := range tourPageRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > last {
			last = n
		}
	}
	return last
}

func enrichTourDetail(body []byte, it *listItem) {
	if m := tourDetailBlurbRe.FindSubmatch(body); m != nil {
		if b := cleanText(string(m[1])); b != "" {
			it.blurb = b
		}
	}
	if m := tourDetailMetaRe.FindSubmatch(body); m != nil {
		if names := anchorNames(m[1]); len(names) > 0 {
			it.performers = names
		}
	}
	if m := tourPremiereRe.FindStringSubmatch(it.blurb); m != nil {
		if d, err := parseutil.TryParseDate(m[1], "1/2/2006"); err == nil {
			it.date = d
		}
	}
}

// ---- helpers ----

func anchorNames(block []byte) []string {
	var names []string
	seen := make(map[string]bool)
	for _, m := range anchorTextRe.FindAllSubmatch(block, -1) {
		n := cleanText(string(m[1]))
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		names = append(names, n)
	}
	return names
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

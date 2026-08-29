// Package pervypass scrapes the PervyPass network — pawged.com, onlybbc.com
// and pawgnextdoor.com — which run an Elevated X tour with the "updateItem"
// theme.
//
// Listing card:
//
//	<div class="updateItem">
//	  <a href="https://www.pawged.com/tour/updates/PAWG-Poses-for-Prick.html">
//	    <img class="stdimage " src="content/256pawg/1.jpg" src0_4x="content/256pawg/1-4x.jpg" />
//	  </a>
//	  <div class="updateDetails">
//	    <h4><a href="…">PAWG Poses for Prick</a></h4>
//	    <p><span class="tour_update_models"><a href="…">Scarlett Shadows</a></span>
//	       <span>08/28/2026</span></p>
//	  </div>
//	</div>
//
// The detail page adds the description, the tag list and a trailer mp4.
//
// **Past its last page the listing clamps and then repeats forever** — page 12
// of pawged's 11-page catalogue returns eight cards and pages 13, 14, … return
// the same eight. The pager is no help either: page 1 advertises 11 pages and
// page 12 advertises 13, a sliding window. So the walk ends when a page
// repeats the previous one, not on a page count.
package pervypass

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

// SiteConfig describes one PervyPass site.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// Aliases are extra domains that redirect to Domain. Requests still go to
	// Domain; these only widen URL matching.
	Aliases []string
}

var sites = []SiteConfig{
	// justpov.com redirects to pawged.com and serves its tour, so it is an
	// alias rather than a second catalogue.
	{SiteID: "pawged", Domain: "pawged.com", StudioName: "PAWGED", Aliases: []string{"justpov.com"}},
	{SiteID: "onlybbc", Domain: "onlybbc.com", StudioName: "Only BBC"},
	{SiteID: "pawgnextdoor", Domain: "pawgnextdoor.com", StudioName: "PAWG NEXT DOOR"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// newFor builds the scraper for a site id. Used by tests.
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
	domains := append([]string{cfg.Domain}, cfg.Aliases...)
	for i, d := range domains {
		domains[i] = regexp.QuoteMeta(d)
	}
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(fmt.Sprintf(`^https?://(?:www\.)?(?:%s)(?:/|$)`, strings.Join(domains, "|"))),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	p := []string{
		s.cfg.Domain,
		s.cfg.Domain + "/tour/categories/{category}.html",
	}
	return append(p, s.cfg.Aliases...)
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

// categoryRe strips the `_{page}_{sort}` suffix the tour appends to a listing
// slug; the slug itself is what identifies the view.
var categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)

// category returns the category a studio URL selects, or "" for the whole
// catalogue. "movies" is the tour's own name for the full listing.
func category(u string) string {
	m := categoryRe.FindStringSubmatch(u)
	if m == nil || strings.EqualFold(m[1], "movies") {
		return ""
	}
	return m[1]
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	cat := category(studioURL)
	if cat == "" {
		cat = "movies"
		scraper.Debugf(1, "%s: scraping full catalogue", s.cfg.SiteID)
	} else {
		scraper.Debugf(1, "%s: scraping category %q", s.cfg.SiteID, cat)
	}

	items, ok := s.collectListing(ctx, cat, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

func (s *Scraper) listingURL(cat string, page int) string {
	return fmt.Sprintf("%s/tour/categories/%s_%d_d.html", s.base, cat, page)
}

func (s *Scraper) collectListing(ctx context.Context, cat string, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, ok bool) {
	seen := make(map[string]bool)
	prevKey := ""

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

		pageURL := s.listingURL(cat, page)
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
		if len(parsed) == 0 {
			return items, true
		}

		// Past the last page the tour clamps and then repeats that page, and
		// its pager is a sliding window, so the repeat is the only reliable
		// end signal.
		key := pageKey(parsed)
		if key == prevKey {
			scraper.Debugf(1, "%s: page %d repeats the previous page, stopping", s.cfg.SiteID, page)
			return items, true
		}
		prevKey = key

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
			items = append(items, item)
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
			for item := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				body, err := s.fetchPage(ctx, absURL(s.base, item.url))
				if err != nil {
					// The card names the scene already; a detail page that
					// will not load costs the description, tags and trailer.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, err)):
					case <-ctx.Done():
						return
					}
				} else {
					enrichFromDetail(body, &item)
				}
				select {
				case out <- scraper.Scene(item.toScene(s.cfg, s.base, studioURL, now)):
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
	title       string
	url         string
	thumbnail   string
	preview     string
	performers  []string
	date        string
	description string
	tags        []string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="updateItem">`)
	cardURLRe   = regexp.MustCompile(`<a\s+href="([^"]*/tour/updates/[^"]+\.html)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<h4>\s*<a[^>]*>(.*?)</a>`)
	modelsRe    = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	dateRe      = regexp.MustCompile(`<span>\s*(\d{2}/\d{2}/\d{4})\s*</span>`)
	// The card offers the same still at four widths; 4x first, then down.
	thumbRes = []*regexp.Regexp{
		regexp.MustCompile(`src0_4x="([^"]+)"`),
		regexp.MustCompile(`src0_3x="([^"]+)"`),
		regexp.MustCompile(`src0_2x="([^"]+)"`),
		regexp.MustCompile(`src0_1x="([^"]+)"`),
		regexp.MustCompile(`<img[^>]+src="([^"]+)"`),
	}
	// setDirRe pulls the content directory out of a thumbnail path. It is the
	// site's own set identifier and the only stable id on the card — the
	// update slug is a title and changes with one.
	setDirRe = regexp.MustCompile(`content/([^/"]+)/`)

	availDateRe = regexp.MustCompile(`(?s)<span class="availdate">\s*(\d{2}/\d{2}/\d{4})`)
	descRe      = regexp.MustCompile(`(?s)<span class="latest_update_description">(.*?)</span>`)
	tagBlockRe  = regexp.MustCompile(`(?s)<span class="update_tags">(.*?)</span>`)
	trailerRe   = regexp.MustCompile(`tload\('([^']+\.mp4)'\)`)
	anchorRe    = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		var item listItem
		m := cardURLRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item.url = html.UnescapeString(string(m[1]))
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		for _, re := range thumbRes {
			if tm := re.FindSubmatch(block); tm != nil {
				item.thumbnail = html.UnescapeString(string(tm[1]))
				break
			}
		}
		item.id = setID(item.thumbnail, item.url)
		if item.id == "" || item.title == "" {
			continue
		}
		if mm := modelsRe.FindSubmatch(block); mm != nil {
			item.performers = anchorNames(mm[1])
		}
		if dm := dateRe.FindSubmatch(block); dm != nil {
			item.date = string(dm[1])
		}
		items = append(items, item)
	}
	return items
}

// setID prefers the content directory ("256pawg"); a card whose thumbnail is
// missing falls back to the update slug so the scene is still collected.
func setID(thumb, updateURL string) string {
	if m := setDirRe.FindStringSubmatch(thumb); m != nil {
		return m[1]
	}
	slug := strings.TrimSuffix(updateURL, ".html")
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		slug = slug[i+1:]
	}
	return slug
}

func enrichFromDetail(body []byte, item *listItem) {
	if m := descRe.FindSubmatch(body); m != nil {
		item.description = cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
	}
	if m := tagBlockRe.FindSubmatch(body); m != nil {
		item.tags = anchorNames(m[1])
	}
	if m := trailerRe.FindSubmatch(body); m != nil {
		item.preview = html.UnescapeString(string(m[1]))
	}
	if item.date == "" {
		if m := availDateRe.FindSubmatch(body); m != nil {
			item.date = string(m[1])
		}
	}
}

func pageKey(items []listItem) string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	return strings.Join(ids, "\x00")
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

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (item listItem) toScene(cfg SiteConfig, base, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         absURL(base, item.url),
		Description: item.description,
		Thumbnail:   absURL(base, item.thumbnail),
		Preview:     absURL(base, item.preview),
		Performers:  item.performers,
		Tags:        item.tags,
		Studio:      cfg.StudioName,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "01/02/2006"); err == nil {
		sc.Date = d
	}
	return sc
}

// absURL resolves a tour-relative path. The tour sets <base href=".../tour/">,
// so a bare "content/…" path is relative to /tour/, not to the site root.
func absURL(base, u string) string {
	switch {
	case u == "", strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "/"):
		return base + u
	default:
		return base + "/tour/" + u
	}
}

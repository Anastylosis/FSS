// Package cutlersden scrapes cutlersden.com, Cutler X's site. It runs an
// Elevated X tour, but neither of the two Elevated X utils in this repo fits:
// pagination is `/categories/movies_{N}_d.html` (cherrypimpsutil's shape, not
// adultdoorwayclassicutil's `/categories/movies/{N}/latest/`) while the cards
// are `item-thumb` + `item-info` (adultdoorwayclassicutil's shape, not
// cherrypimpsutil's `item-update item-video`), and the info block is this
// site's own: an `item-info` h3, a `models` div of linked names and a
// `dateCount` line packing the date, runtime and photo count together.
//
// The card carries everything but the description and categories, which the
// `/trailers/{SLUG}.html` detail page holds.
package cutlersden

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
	siteID     = "cutlersden"
	studioName = "Cutler's Den"
	defaultURL = "https://cutlersden.com"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultURL,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"cutlersden.com",
		"cutlersden.com/categories/{slug}.html",
		"cutlersden.com/models/{Name}.html",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?cutlersden\.com(?:/|$)`)
	// The site suffixes a listing slug with `_{page}_{sort}`; the slug itself
	// is what identifies the view.
	categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)
	modelRe    = regexp.MustCompile(`/models/([^/?#]+?)(?:_\d+_[a-z])?\.html`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

type urlKind int

const (
	kindAll urlKind = iota
	kindCategory
	kindModel
)

// classifyURL decides which listing a studio URL selects. "movies" is the
// site's own name for the whole catalogue and "categories" is the index page,
// so neither is a filter.
func classifyURL(u string) (urlKind, string) {
	if m := modelRe.FindStringSubmatch(u); m != nil && !strings.EqualFold(m[1], "models") {
		return kindModel, m[1]
	}
	if m := categoryRe.FindStringSubmatch(u); m != nil {
		switch strings.ToLower(m[1]) {
		case "movies", "categories", "photos":
		default:
			return kindCategory, m[1]
		}
	}
	return kindAll, ""
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	kind, slug := classifyURL(studioURL)
	switch kind {
	case kindModel:
		scraper.Debugf(1, "%s: scraping model %q", siteID, slug)
	case kindCategory:
		scraper.Debugf(1, "%s: scraping category %q", siteID, slug)
	default:
		scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	}

	items, ok := s.collectListing(ctx, kind, slug, opts, out)
	if !ok || ctx.Err() != nil {
		return
	}
	s.fetchDetails(ctx, studioURL, items, opts, out)
}

// listingURL builds the page URL for a mode. Every listing on the site — the
// catalogue, a category and a model — paginates the same way.
func (s *Scraper) listingURL(kind urlKind, slug string, page int) string {
	switch kind {
	case kindModel:
		return fmt.Sprintf("%s/models/%s_%d_d.html", s.base, slug, page)
	case kindCategory:
		return fmt.Sprintf("%s/categories/%s_%d_d.html", s.base, slug, page)
	default:
		return fmt.Sprintf("%s/categories/movies_%d_d.html", s.base, page)
	}
}

func (s *Scraper) collectListing(ctx context.Context, kind urlKind, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) (items []listItem, ok bool) {
	seen := make(map[string]bool)

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

		pageURL := s.listingURL(kind, slug, page)
		scraper.Debugf(1, "%s: fetching listing page %d (%s)", siteID, page, pageURL)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return items, false
		}

		parsed := parseListing(body)
		// The pager prints no page count — only a "next" arrow — so a page
		// with no cards is the end of the listing.
		if len(parsed) == 0 {
			return items, true
		}

		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return items, true
			}
			items = append(items, item)
		}

		if !hasNextPage(body) {
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

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)

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
					// The card already names the scene; a detail page that
					// will not load costs the description and categories.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, err)):
					case <-ctx.Done():
						return
					}
				} else {
					item.description, item.categories = parseDetail(body)
				}
				select {
				case out <- scraper.Scene(item.toScene(s.base, studioURL, now)):
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
	duration    int
	description string
	categories  []string
}

var (
	itemStartRe = regexp.MustCompile(`<div class="item">`)
	titleRe     = regexp.MustCompile(`(?s)<div class="item-info[^"]*">\s*<h3><a href="([^"]+)"[^>]*>(.*?)</a>`)
	modelsRe    = regexp.MustCompile(`(?s)<div class="models">(.*?)</div>`)
	dateCountRe = regexp.MustCompile(`(?s)<div class="dateCount">(.*?)</div>`)
	thumbRe     = regexp.MustCompile(`<img[^>]+src="((?:https?://|/|content/)[^"]+\.jpg)"`)
	previewRe   = regexp.MustCompile(`<video src="([^"]+\.mp4)"`)
	nextPageRe  = regexp.MustCompile(`<a href="[^"]+" class="next"`)
	anchorRe    = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
	// The dateCount line packs three values behind icons:
	// "August 27, 2026 | <i class="fa fa-play"></i> 22:25 | <i …camera…> 66".
	durationRe = regexp.MustCompile(`(\d+:\d+(?::\d+)?)`)
	// idRe turns the trailer path into a stable scene id. The slug is the only
	// identifier the tour exposes — there is no numeric set id anywhere.
	idRe = regexp.MustCompile(`/trailers/(.+?)\.html`)
)

func parseListing(body []byte) []listItem {
	locs := itemStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		var item listItem
		m := titleRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item.url = html.UnescapeString(string(m[1]))
		item.title = cleanText(string(m[2]))
		item.id = sceneID(item.url)
		if item.id == "" || item.title == "" {
			continue
		}
		if mm := modelsRe.FindSubmatch(block); mm != nil {
			item.performers = anchorNames(mm[1])
		}
		if dm := dateCountRe.FindSubmatch(block); dm != nil {
			item.date, item.duration = parseDateCount(string(dm[1]))
		}
		if tm := thumbRe.FindSubmatch(block); tm != nil {
			item.thumbnail = html.UnescapeString(string(tm[1]))
		}
		if pm := previewRe.FindSubmatch(block); pm != nil {
			item.preview = html.UnescapeString(string(pm[1]))
		}
		items = append(items, item)
	}
	return items
}

// sceneID derives the scene id from its trailer URL. Slugs carry the cast in
// upper case and sometimes a leading hyphen, which is kept verbatim: it is the
// site's own identifier and changing it would orphan stored scenes.
func sceneID(u string) string {
	if m := idRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// parseDateCount splits the card's combined date/runtime/photo-count line. The
// photo count is deliberately dropped — it counts stills, not video.
func parseDateCount(raw string) (date string, duration int) {
	text := cleanText(tagStripRe.ReplaceAllString(raw, " "))
	parts := strings.Split(text, "|")
	if len(parts) > 0 {
		date = strings.TrimSpace(parts[0])
	}
	if m := durationRe.FindString(text); m != "" {
		duration = parseutil.ParseDurationColon(m)
	}
	return date, duration
}

var (
	descRe       = regexp.MustCompile(`(?s)</ul>\s*<p>(.*?)</p>`)
	categoriesRe = regexp.MustCompile(`(?s)<div class="vCategories">\s*Categories:(.*?)</div>`)
)

func parseDetail(body []byte) (description string, categories []string) {
	if m := descRe.FindSubmatch(body); m != nil {
		description = cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
	}
	if m := categoriesRe.FindSubmatch(body); m != nil {
		categories = anchorNames(m[1])
	}
	return description, categories
}

func hasNextPage(body []byte) bool { return nextPageRe.Match(body) }

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
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (item listItem) toScene(base, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         absURL(base, item.url),
		Description: item.description,
		Thumbnail:   absURL(base, item.thumbnail),
		Preview:     absURL(base, item.preview),
		Performers:  item.performers,
		Categories:  item.categories,
		Studio:      studioName,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "January 2, 2006"); err == nil {
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

// Package boundhoneys scrapes boundhoneys.com.
//
// The whole catalogue is one request: `/bondage-videos.php?perpage=9999` — the
// site's own "Videos per Page" control offers 9999, and there is no pager at
// all, so the walk is a single fetch of 126 cards.
//
//	<div class='update'>
//	  <a href="bondage-video/alessandra-jane-strapon-bondage-fuck.php">
//	    <img class='trailerOverlay' src="images/smallpreview/…s.jpg" …></a>
//	  <div class='updateTitle'><a href="…">Anything For The Boss!</a></div>
//	  <div class='updateModels'><a href="alessandra-jane.php">Alessandra Jane</a>, …</div>
//	  <div class='updateCategories'><a href="bondage-category/ballgag.php">Ball / Ring Gag</a>, …</div>
//	</div>
//
// The detail page adds the runtime ("16 Minute Video") and the description.
// **The site publishes no date anywhere** — not on the card, not on the detail
// page — so Scene.Date is left zero rather than guessed.
package boundhoneys

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
	siteID     = "boundhoneys"
	studioName = "BoundHoneys"
	defaultURL = "https://boundhoneys.com"
	// listingPath asks for every video on one page; the site's own control
	// offers 9999 and the response carries no pager.
	listingPath = "/bondage-videos.php?perpage=9999"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"boundhoneys.com",
		"boundhoneys.com/bondage-videos.php",
		"boundhoneys.com/{model}.php",
		"boundhoneys.com/bondage-category/{category}.php",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?boundhoneys\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

// filterPath returns the site-relative listing a studio URL selects, or "" for
// the whole catalogue. Model pages (`/{name}.php`) and category pages
// (`/bondage-category/{slug}.php`) take the same `perpage` control.
var (
	categoryPathRe = regexp.MustCompile(`/bondage-category/([^/?#]+)\.php`)
	modelPathRe    = regexp.MustCompile(`^/([a-z0-9-]+)\.php$`)
	// The site's own pages, which are not models.
	reservedPages = map[string]bool{
		"bondage-videos": true, "bondage-girls": true, "bondage-categories": true,
		"join": true, "search": true, "2257": true, "index": true,
	}
)

func filterPath(studioURL string) string {
	if m := categoryPathRe.FindStringSubmatch(studioURL); m != nil {
		return "/bondage-category/" + m[1] + ".php?perpage=9999"
	}
	// A model page is a bare `{name}.php` at the site root.
	if i := strings.Index(studioURL, "boundhoneys.com"); i >= 0 {
		path := studioURL[i+len("boundhoneys.com"):]
		if q := strings.IndexAny(path, "?#"); q >= 0 {
			path = path[:q]
		}
		if m := modelPathRe.FindStringSubmatch(path); m != nil && !reservedPages[m[1]] {
			return path + "?perpage=9999"
		}
	}
	return ""
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	path := filterPath(studioURL)
	if path == "" {
		path = listingPath
		scraper.Debugf(1, "%s: fetching the whole catalogue in one request", siteID)
	} else {
		scraper.Debugf(1, "%s: scraping %s", siteID, path)
	}

	body, err := s.fetchPage(ctx, s.base+path)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("listing: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	items := parseListing(body)
	if len(items) == 0 {
		select {
		case out <- scraper.Error(scraper.ParseError(s.base+path, fmt.Errorf("no update cards on the listing"))):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes", siteID, len(items))

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	var fresh []listItem
	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		fresh = append(fresh, item)
	}

	s.fetchDetails(ctx, fresh, studioURL, opts, out)
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
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
				body, err := s.fetchPage(ctx, s.base+"/"+item.path)
				if err != nil {
					// The card already names the scene, its cast and its
					// categories; a detail page that will not load costs the
					// runtime and the description.
					select {
					case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.path, err)):
					case <-ctx.Done():
						return
					}
				} else {
					enrichFromDetail(body, &item)
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
	performers  []string
	categories  []string
	duration    int
	description string
}

var (
	cardStartRe = regexp.MustCompile(`<div class='update'>`)
	cardLinkRe  = regexp.MustCompile(`href="(bondage-video/([a-z0-9-]+)\.php)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<div class='updateTitle'>\s*<a[^>]*>(.*?)</a>`)
	cardThumbRe = regexp.MustCompile(`<img[^>]+src="(images/[^"]+)"`)
	cardModelRe = regexp.MustCompile(`(?s)<div class='updateModels'>(.*?)</div>`)
	cardCatRe   = regexp.MustCompile(`(?s)<div class='updateCategories'>(.*?)</div>`)

	detailDurRe  = regexp.MustCompile(`(?s)<div class='updateVideoDuration'>\s*(\d+)\s*Minute`)
	detailDescRe = regexp.MustCompile(`(?s)<div class='updateDescription'>.*?</div>\s*<b>(.*?)</b>`)
	anchorRe     = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	seen := make(map[string]bool)
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		m := cardLinkRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item := listItem{path: html.UnescapeString(string(m[1])), id: string(m[2])}
		// The same video appears in more than one block on some pages.
		if seen[item.id] {
			continue
		}
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if item.title == "" {
			continue
		}
		seen[item.id] = true
		if t := cardThumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := cardModelRe.FindSubmatch(block); t != nil {
			item.performers = anchorNames(t[1])
		}
		if t := cardCatRe.FindSubmatch(block); t != nil {
			item.categories = anchorNames(t[1])
		}
		items = append(items, item)
	}
	return items
}

func enrichFromDetail(body []byte, item *listItem) {
	if m := detailDurRe.FindSubmatch(body); m != nil {
		if mins, err := strconv.Atoi(string(m[1])); err == nil {
			item.duration = mins * 60
		}
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		item.description = cleanText(string(m[1]))
	}
}

func anchorNames(block []byte) []string {
	var names []string
	seen := make(map[string]bool)
	for _, m := range anchorRe.FindAllSubmatch(block, -1) {
		n := cleanText(string(m[1]))
		if n == "" || n == "..." || seen[strings.ToLower(n)] {
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
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         s.base + "/" + item.path,
		Description: item.description,
		Thumbnail:   absURL(s.base, item.thumbnail),
		Performers:  item.performers,
		Categories:  item.categories,
		Studio:      studioName,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
}

func absURL(base, u string) string {
	if u == "" || strings.HasPrefix(u, "http") {
		return u
	}
	return base + "/" + strings.TrimPrefix(u, "/")
}

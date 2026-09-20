// Package mindundermaster scrapes mindundermaster.com. Listing cards carry the
// catalogue, detail pages add tags and price, the RSS feed supplies release
// dates and the model index is the only place performers are named.
// See docs/scrapers.md.
package mindundermaster

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "mindundermaster"
	studioName  = "Mind Under Master"
	defaultBase = "https://www.mindundermaster.com"
	// detailWorkers caps the detail and model-page pools; the catalogue is
	// ~100 scenes and ~100 models, so this is one small site's whole load.
	detailWorkers = 4
)

var (
	cardRe     = regexp.MustCompile(`(?s)<a href="([^"]+/video/[^"]+\.html)" title="([^"]*)" class="videos__element">(.*?)</a>`)
	videoIDRe  = regexp.MustCompile(`/video/.*?-(\d+)\.html$`)
	cardImgRe  = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	cardTextRe = regexp.MustCompile(`(?s)<p class="videos__text">(.*?)</p>`)

	detailTagsRe = regexp.MustCompile(`(?s)<div class="video__tags">(.*?)</div>`)
	tagLinkRe    = regexp.MustCompile(`(?s)<a[^>]*class="tag"[^>]*>(.*?)</a>`)
	detailDescRe = regexp.MustCompile(`(?s)<p class="video__desc">(.*?)</p>`)
	priceRe      = regexp.MustCompile(`(?s)<div class="video__tokens">.*?<span>\s*\$\s*([\d.]+)\s*</span>`)

	modelLinkRe = regexp.MustCompile(`href="([^"]*/models/([^"/]+)\.html)"`)
	pageFileRe  = regexp.MustCompile(`^page\d+$`)
	titleRe     = regexp.MustCompile(`(?s)<h1 class="videos__topTitle">(.*?)</h1>`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)

	matchRe = regexp.MustCompile(`^https?://(?:www\.)?mindundermaster\.com(?:/|$)`)
)

// Scraper implements scraper.StudioScraper for mindundermaster.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Mind Under Master scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: defaultBase}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"mindundermaster.com",
		"mindundermaster.com/channels/{id}/{slug}/",
		"mindundermaster.com/search/{tag}/",
		"mindundermaster.com/models/{slug}-{id}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry, before the detail page is fetched.
type card struct {
	id    string
	path  string
	title string
	thumb string
	text  string
}

// listingMode describes what an operator URL selects: the full catalogue, a
// channel, a search tag (all paginated) or one model page (never paginated).
type listingMode struct {
	prefix string
	single bool
}

// resolveMode maps an operator URL onto the listing it selects. Anything that
// is not a channel, search or model page walks the main catalogue.
func resolveMode(studioURL string) listingMode {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listingMode{prefix: "/videos/"}
	}
	p := u.Path
	switch {
	case strings.HasPrefix(p, "/models/") && strings.HasSuffix(p, ".html"):
		return listingMode{prefix: p, single: true}
	case strings.HasPrefix(p, "/channels/") || strings.HasPrefix(p, "/search/"):
		return listingMode{prefix: dirOf(p)}
	default:
		return listingMode{prefix: "/videos/"}
	}
}

// dirOf trims a trailing pageN.html and guarantees a trailing slash.
func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && strings.HasSuffix(p, ".html") {
		p = p[:i+1]
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	mode := resolveMode(studioURL)
	scraper.Debugf(1, "%s: listing %s (single page: %v)", siteID, mode.prefix, mode.single)

	now := time.Now().UTC()
	seen := map[string]bool{}
	var (
		datesOnce sync.Once
		dates     map[string]time.Time
		castOnce  sync.Once
		cast      map[string][]string
	)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if mode.single && page > 1 {
			return scraper.PageResult{Done: true}, nil
		}
		pageURL := s.base + mode.prefix
		if !mode.single {
			pageURL = fmt.Sprintf("%s%spage%d.html", s.base, mode.prefix, page)
		}
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		cards := parseListing(body)
		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			fresh = append(fresh, c)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		// The listing is date-descending, so the first known ID ends the walk.
		// Enrich only the cards ahead of it, then hand the whole page back so
		// Paginate can trip on it and report the early stop.
		enrichTo := len(fresh)
		for i, c := range fresh {
			if opts.KnownIDs[c.id] {
				enrichTo = i
				break
			}
		}
		if enrichTo > 0 {
			datesOnce.Do(func() { dates = s.fetchDates(ctx) })
			castOnce.Do(func() { cast = s.fetchCast(ctx, opts) })
		}

		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now, dates, cast)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes, Done: mode.single}, nil
	})
}

// parseListing reads the scene cards on a listing page.
func parseListing(body string) []card {
	var cards []card
	for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
		href, err := url.Parse(html.UnescapeString(m[1]))
		if err != nil {
			continue
		}
		id := videoIDRe.FindStringSubmatch(href.Path)
		if id == nil {
			continue
		}
		c := card{id: id[1], path: href.Path, title: cleanText(m[2])}
		if im := cardImgRe.FindStringSubmatch(m[3]); im != nil {
			c.thumb = html.UnescapeString(im[1])
		}
		if tx := cardTextRe.FindStringSubmatch(m[3]); tx != nil {
			c.text = cleanText(tx[1])
		}
		cards = append(cards, c)
	}
	return cards
}

// baseScene builds a scene from the listing card plus the feed and cast
// indexes, before the detail page is consulted.
func (s *Scraper) baseScene(c card, studioURL string, now time.Time, dates map[string]time.Time, cast map[string][]string) models.Scene {
	sc := models.Scene{
		ID:          c.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       c.title,
		URL:         s.base + c.path,
		Thumbnail:   c.thumb,
		Description: c.text,
		ScrapedAt:   now,
	}
	if d, ok := dates[c.id]; ok {
		sc.Date = d
	}
	if names := cast[c.id]; len(names) > 0 {
		sc.Performers = append([]string(nil), names...)
	}
	return sc
}

// enrich fetches each scene's detail page for tags, the untruncated
// description and the price. A failure leaves the listing-only record.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), detailWorkers)
	s.pool(ctx, len(scenes), opts.Delay, func(i int) {
		body, err := s.fetch(ctx, scenes[i].URL)
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
			return
		}
		applyDetail(&scenes[i], body)
	})
}

// applyDetail folds the detail page into a scene built from its listing card.
func applyDetail(sc *models.Scene, body string) {
	if tb := detailTagsRe.FindStringSubmatch(body); tb != nil {
		for _, t := range tagLinkRe.FindAllStringSubmatch(tb[1], -1) {
			if name := cleanText(t[1]); name != "" {
				sc.Tags = append(sc.Tags, name)
			}
		}
	}
	if d := detailDescRe.FindStringSubmatch(body); d != nil {
		if text := cleanText(d[1]); text != "" {
			sc.Description = text
		}
	}
	if p := priceRe.FindStringSubmatch(body); p != nil {
		if amount, err := strconv.ParseFloat(p[1], 64); err == nil && amount > 0 {
			sc.AddPrice(models.PriceSnapshot{Date: sc.ScrapedAt, Regular: amount})
		}
	}
}

// ---- release dates ----

type rssFeed struct {
	Items []struct {
		Link    string `xml:"link"`
		PubDate string `xml:"pubDate"`
	} `xml:"channel>item"`
}

var rssLayouts = []string{
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05",
}

// fetchDates indexes the RSS feed by scene ID. The feed carries the most recent
// 100 entries and is the only place the site publishes a release date, so older
// scenes simply have none.
func (s *Scraper) fetchDates(ctx context.Context) map[string]time.Time {
	dates := map[string]time.Time{}
	body, err := s.fetchBytes(ctx, s.base+"/rss")
	if err != nil {
		scraper.Debugf(1, "%s: rss unavailable: %v", siteID, err)
		return dates
	}
	var feed rssFeed
	if err := parseutil.DecodeXML(body, &feed); err != nil {
		scraper.Debugf(1, "%s: rss parse failed: %v", siteID, err)
		return dates
	}
	for _, it := range feed.Items {
		id := videoIDRe.FindStringSubmatch(strings.TrimSpace(it.Link))
		if id == nil {
			continue
		}
		t, err := parseutil.TryParseDate(strings.TrimSpace(it.PubDate), rssLayouts...)
		if err != nil {
			continue
		}
		dates[id[1]] = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	scraper.Debugf(1, "%s: %d release dates from rss", siteID, len(dates))
	return dates
}

// ---- cast index ----

// fetchCast builds a scene ID to performer names index by walking the model
// directory. Scene pages never name their cast; each model page lists the
// scenes that performer appears in, so the index has to be inverted.
func (s *Scraper) fetchCast(ctx context.Context, opts scraper.ListOpts) map[string][]string {
	paths := s.modelPaths(ctx, opts)
	if len(paths) == 0 {
		return nil
	}
	scraper.Debugf(1, "%s: fetching %d model pages with %d workers", siteID, len(paths), detailWorkers)

	var mu sync.Mutex
	cast := map[string][]string{}
	s.pool(ctx, len(paths), opts.Delay, func(i int) {
		body, err := s.fetch(ctx, s.base+paths[i])
		if err != nil {
			scraper.Debugf(1, "%s: model page %s: %v", siteID, paths[i], err)
			return
		}
		name := ""
		if t := titleRe.FindStringSubmatch(body); t != nil {
			name = cleanText(t[1])
		}
		if name == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, c := range parseListing(body) {
			cast[c.id] = append(cast[c.id], name)
		}
	})
	for id := range cast {
		sort.Strings(cast[id])
	}
	return cast
}

// modelPaths walks the paginated model directory and returns each model page's
// path, in directory order.
func (s *Scraper) modelPaths(ctx context.Context, opts scraper.ListOpts) []string {
	var paths []string
	seen := map[string]bool{}
	for page := 1; page <= 200; page++ {
		if ctx.Err() != nil {
			return paths
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return paths
			}
		}
		body, err := s.fetch(ctx, fmt.Sprintf("%s/models/page%d.html", s.base, page))
		if err != nil {
			scraper.Debugf(1, "%s: model index page %d: %v", siteID, page, err)
			return paths
		}
		added := 0
		for _, m := range modelLinkRe.FindAllStringSubmatch(body, -1) {
			// The pager's own links live in the same directory.
			if pageFileRe.MatchString(m[2]) {
				continue
			}
			u, err := url.Parse(html.UnescapeString(m[1]))
			if err != nil || seen[u.Path] {
				continue
			}
			seen[u.Path] = true
			paths = append(paths, u.Path)
			added++
		}
		if added == 0 {
			return paths
		}
	}
	return paths
}

// ---- plumbing ----

// pool runs fn for each index 0..n-1 over a bounded worker pool, applying the
// configured delay before each request.
func (s *Scraper) pool(ctx context.Context, n int, delay time.Duration, fn func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, detailWorkers)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
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
			fn(i)
		}(i)
	}
	wg.Wait()
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	body, err := s.fetchBytes(ctx, pageURL)
	return string(body), err
}

func (s *Scraper) fetchBytes(ctx context.Context, pageURL string) ([]byte, error) {
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

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

// Package pinupfiles scrapes pinupfiles.com, a big-bust glamour site running
// a NATS/MJEdge ElevatedX-style tour CMS. It reads the public, paginated tour
// listings (videos only) and enriches each scene from its detail page.
package pinupfiles

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func init() { scraper.Register(New()) }

const (
	siteID  = "pinupfiles"
	studio  = "Pinup Files"
	baseURL = "https://www.pinupfiles.com"
)

// Scraper implements scraper.StudioScraper for pinupfiles.com.
type Scraper struct {
	Client *http.Client
	base   string
}

// New constructs a Pinup Files scraper.
func New() *Scraper {
	return &Scraper{
		Client: httpx.NewClient(30 * time.Second),
		base:   baseURL,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"pinupfiles.com",
		"pinupfiles.com/categories/movies/{N}/latest/",
		"pinupfiles.com/models/{slug}.html",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?pinupfiles\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var modelPageRe = regexp.MustCompile(`(?i)/models/[^/]+\.html`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if modelPageRe.MatchString(studioURL) {
		scraper.Debugf(1, "pinupfiles: detected model page")
		s.scrapeSinglePage(ctx, studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "pinupfiles: scraping movies listing")
	s.scrapePaginated(ctx, opts, out)
}

// scrapePaginated walks the path-numbered movies listing (newest first).
func (s *Scraper) scrapePaginated(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/movies/%d/latest/", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = maxPageNum(body) * len(items)
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   !hasNextPage(body),
		}, nil
	})
}

// scrapeSinglePage handles a model page: one page of mixed cards, videos only.
func (s *Scraper) scrapeSinglePage(ctx context.Context, pageURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := parseListingPage(body)
	if len(items) == 0 {
		return
	}

	scraper.Debugf(1, "pinupfiles: found %d videos on model page", len(items))
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	scenes := s.fetchDetails(ctx, items, opts, now)
	for _, sc := range scenes {
		if opts.KnownIDs[sc.ID] {
			scraper.Debugf(1, "pinupfiles: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(sc):
		case <-ctx.Done():
			return
		}
	}
}

// ---- listing parsing ----

var (
	// cardRe captures a single video card. Photo cards use item-photo and are
	// excluded by requiring item-video in the class list.
	cardRe = regexp.MustCompile(`(?s)<div class="item-update[^"]*\bitem-video\b[^"]*">.*?</div><!--//item-->`)

	// cardLinkRe pulls the /trailers/{slug}.html detail link and its title.
	cardLinkRe = regexp.MustCompile(`<a href="https?://[^"]*?/trailers/([^"]+)\.html" title="([^"]*)" class="item-thumb-link"`)

	// cardThumbRe pulls the real (non-placeholder) thumbnail from src0_1x.
	cardThumbRe = regexp.MustCompile(`class="update_thumb thumbs stdimage"[^>]*?src0_1x="([^"]+)"`)

	// cardDateDurRe pulls "MM:SS | <i ...></i> Mon D, YYYY" from item-date.
	cardDateDurRe = regexp.MustCompile(`(?s)<div class="item-date">\s*([0-9:]+)\s*\|[^>]*></i>\s*([A-Za-z]{3,} \d{1,2}, \d{4})`)

	// cardStarsRe matches the item-stars block; filled stars are counted below.
	cardStarsRe = regexp.MustCompile(`(?s)<div class="item-stars">(.*?)</div>`)

	// cardModelRe pulls each performer from /models/{slug}.html links in the card.
	cardModelRe = regexp.MustCompile(`<a href="https?://[^"]*?/models/[^"]+\.html">([^<]+)</a>`)

	starRe = regexp.MustCompile(`fa-star\b`)

	// pageNumRe finds /categories/movies/{N}/latest/ links for total estimation.
	pageNumRe = regexp.MustCompile(`/categories/movies/(\d+)/latest/`)

	// nextPageRe detects the "next page" control in the pager.
	nextPageRe = regexp.MustCompile(`class="next"`)
)

type listItem struct {
	id         string
	url        string
	title      string
	thumbnail  string
	performers []string
	date       time.Time
	duration   int
	likes      int
}

func parseListingPage(body []byte) []listItem {
	cards := cardRe.FindAll(body, -1)
	items := make([]listItem, 0, len(cards))
	for _, card := range cards {
		if it, ok := parseCard(card); ok {
			items = append(items, it)
		}
	}
	return items
}

func parseCard(card []byte) (listItem, bool) {
	m := cardLinkRe.FindSubmatch(card)
	if m == nil {
		return listItem{}, false
	}

	it := listItem{
		id:    string(m[1]),
		url:   "/trailers/" + string(m[1]) + ".html",
		title: cleanText(string(m[2])),
	}

	if mThumb := cardThumbRe.FindSubmatch(card); mThumb != nil {
		it.thumbnail = html.UnescapeString(string(mThumb[1]))
	}

	if mDD := cardDateDurRe.FindSubmatch(card); mDD != nil {
		it.duration = parseutil.ParseDurationColon(string(mDD[1]))
		if t, err := parseutil.TryParseDate(strings.TrimSpace(string(mDD[2])), "Jan 2, 2006", "January 2, 2006"); err == nil {
			it.date = t.UTC()
		}
	}

	if mStars := cardStarsRe.FindSubmatch(card); mStars != nil {
		it.likes = len(starRe.FindAll(mStars[1], -1))
	}

	for _, mp := range cardModelRe.FindAllSubmatch(card, -1) {
		name := cleanText(string(mp[1]))
		if name != "" {
			it.performers = append(it.performers, name)
		}
	}

	return it, true
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func hasNextPage(body []byte) bool { return nextPageRe.Match(body) }

// ---- detail parsing ----

var (
	detailTitleRe = regexp.MustCompile(`(?s)<h1>([^<]+)</h1>`)
	detailDescRe  = regexp.MustCompile(`(?s)<h3>Description:</h3>\s*(.*?)\s*</div>`)
	detailRuntime = regexp.MustCompile(`(?s)<strong>Runtime:</strong>\s*([0-9:]+)`)
	detailAddedRe = regexp.MustCompile(`(?s)<strong>Added:</strong>\s*([A-Za-z]+ \d{1,2}, \d{4})`)
	detailTagsRe  = regexp.MustCompile(`(?s)<h3>Tags:</h3>\s*<ul class="tags">(.*?)</ul>`)
	tagLinkRe     = regexp.MustCompile(`<a href="[^"]*">([^<]+)</a>`)
	tagStripRe    = regexp.MustCompile(`<[^>]+>`)
)

type detailData struct {
	title       string
	description string
	duration    int
	date        time.Time
	hasDate     bool
	tags        []string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = cleanText(string(m[1]))
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		text := tagStripRe.ReplaceAll(m[1], []byte(" "))
		d.description = cleanText(string(text))
	}

	if m := detailRuntime.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}

	if m := detailAddedRe.FindSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(string(m[1])), "January 2, 2006", "Jan 2, 2006"); err == nil {
			d.date = t.UTC()
			d.hasDate = true
		}
	}

	if m := detailTagsRe.FindSubmatch(body); m != nil {
		seen := make(map[string]bool)
		for _, tm := range tagLinkRe.FindAllSubmatch(m[1], -1) {
			tag := cleanText(string(tm[1]))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				d.tags = append(d.tags, tag)
			}
		}
	}

	return d
}

// ---- detail fetch (worker pool) ----

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "pinupfiles: fetching %d details with %d workers", len(items), workers)

	type enriched struct {
		item   listItem
		detail detailData
		err    error
	}

	results := make([]enriched, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int, item listItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				return
			}

			body, err := s.fetchPage(ctx, s.base+item.url)
			if err != nil {
				results[idx] = enriched{item: item, err: err}
				return
			}
			results[idx] = enriched{item: item, detail: parseDetailPage(body)}
		}(i, it)
	}
	wg.Wait()

	var scenes []models.Scene
	for _, r := range results {
		if ctx.Err() != nil {
			break
		}
		if r.item.id == "" {
			continue
		}
		// A detail-fetch failure still yields a usable scene from the card.
		scenes = append(scenes, toScene(s.base, r.item, r.detail, now))
	}
	return scenes
}

func toScene(base string, it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if title == "" {
		title = d.title
	}

	date := it.date
	if date.IsZero() && d.hasDate {
		date = d.date
	}

	duration := it.duration
	if duration == 0 {
		duration = d.duration
	}

	return models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   base,
		Title:       title,
		URL:         base + it.url,
		Date:        date,
		Description: d.description,
		Thumbnail:   it.thumbnail,
		Performers:  it.performers,
		Tags:        d.tags,
		Duration:    duration,
		Likes:       it.likes,
		Studio:      studio,
		ScrapedAt:   now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, raw string) ([]byte, error) {
	if _, err := url.Parse(raw); err != nil {
		return nil, err
	}
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     raw,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func cleanText(s string) string {
	return strings.TrimSpace(html.UnescapeString(strings.Join(strings.Fields(s), " ")))
}

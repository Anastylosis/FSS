package realspankingsutil

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteType int

const (
	TypeRSI          SiteType = iota // realspankingsinstitute.com — page-based (0-indexed, 12/page)
	TypeSpankedCoeds                 // spankedcoeds.com — search API (1-indexed, 50/page)
	TypeSTB                          // spankingteenbrandi.com — year-based navigation
	TypeSTJ                          // spankingteenjessica.com — page-based (1-indexed, 15/page)
	TypeBailey                       // spankingbailey.com — single page
)

// maxPages bounds a paged walk at 12-50 scenes per page — past every site on
// the network, and a backstop against a pager that never reports an end.
const maxPages = 500

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	Type       SiteType
}

type Scraper struct {
	client *http.Client
	base   string
	cfg    SiteConfig
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://" + cfg.Domain,
		cfg:    cfg,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain}
}

func (s *Scraper) MatchesURL(u string) bool {
	return scraper.HostMatches(u, s.cfg.Domain)
}

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

type listingItem struct {
	id          string
	title       string
	date        time.Time
	thumb       string
	description string
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: starting scrape (type=%d)", s.cfg.SiteID, s.cfg.Type)
	switch s.cfg.Type {
	case TypeRSI:
		s.runPaged(ctx, opts, out, 0, s.buildRSIURL, parseRSI)
	case TypeSpankedCoeds:
		s.runPaged(ctx, opts, out, 1, s.buildSCURL, parseSpankedCoeds)
	case TypeSTJ:
		s.runPaged(ctx, opts, out, 1, s.buildSTJURL, parseSTJ)
	case TypeSTB:
		s.runYears(ctx, opts, out)
	case TypeBailey:
		s.runSingle(ctx, opts, out)
	}
}

func (s *Scraper) runPaged(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult,
	startPage int, buildURL func(int) string, parse func([]byte, string) []listingItem) {

	// The walk ends on an empty page, but a CMS that clamps an out-of-range
	// page back to a real one would repeat it forever. A page yielding no id
	// the walk has not already emitted is that clamp; maxPages is the backstop.
	seen := map[string]bool{}
	for page := startPage; page < startPage+maxPages; page++ {
		if ctx.Err() != nil {
			return
		}

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)
		pageURL := buildURL(page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		items := parse(body, s.base)
		if len(items) == 0 {
			return
		}

		fresh := items[:0]
		for _, item := range items {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			fresh = append(fresh, item)
		}
		if len(fresh) == 0 {
			scraper.Debugf(1, "%s: page %d repeated scenes already seen, stopping", s.cfg.SiteID, page)
			return
		}

		if !s.sendItems(ctx, opts, out, fresh, pageURL) {
			return
		}

		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)
	}
}

func (s *Scraper) runYears(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	currentYear := time.Now().Year()
	for year := currentYear; year >= 2001; year-- {
		if ctx.Err() != nil {
			return
		}
		scraper.Debugf(1, "%s: fetching year %d", s.cfg.SiteID, year)

		url := fmt.Sprintf("%s/updates.php?year=%d", s.base, year)
		body, err := s.fetchPage(ctx, url)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("year %d: %w", year, err)):
			case <-ctx.Done():
			}
			return
		}

		items := parseSTB(body, s.base)
		if len(items) == 0 {
			continue
		}

		if !s.sendItems(ctx, opts, out, items, url) {
			return
		}

		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
	}
}

func (s *Scraper) runSingle(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchPage(ctx, s.base+"/updates.php")
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := parseBailey(body, s.base)
	s.sendItems(ctx, opts, out, items, s.base+"/updates.php")
}

// sendItems emits the scenes from one listing page. pageURL is where they were
// found: these sites publish no per-scene page, so it becomes the scene URL
// with the scene's own id as the anchor — the same shape the other
// listing-only scrapers here use. See docs/scrapers.md.
func (s *Scraper) sendItems(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, items []listingItem, pageURL string) bool {
	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return false
		}
		scene := s.buildScene(item, pageURL)
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return false
		}
	}
	return true
}

func (s *Scraper) buildScene(item listingItem, pageURL string) models.Scene {
	return models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       item.title,
		URL:         sceneURL(pageURL, s.base, item.id),
		Thumbnail:   item.thumb,
		Studio:      s.cfg.StudioName,
		Date:        item.date,
		Description: item.description,
		ScrapedAt:   time.Now().UTC(),
	}
}

// sceneURL anchors the listing page on the scene's id. Without the anchor every
// scene on a site shared one URL, which is no use to a reader and collapses in
// any tool that keys on it.
func sceneURL(pageURL, base, id string) string {
	if pageURL == "" {
		pageURL = base
	}
	if id == "" {
		return pageURL
	}
	return pageURL + "#" + id
}

func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

var thumbIDRe = regexp.MustCompile(`updates/(\d+)_\d+\.jpg`)

func extractThumbID(thumbPath string) string {
	m := thumbIDRe.FindStringSubmatch(thumbPath)
	if m != nil {
		return m[1]
	}
	return ""
}

// RSI: realspankingsinstitute.com

func (s *Scraper) buildRSIURL(page int) string {
	return fmt.Sprintf("%s/updates.php?v=%s", s.base, encodeBase64(fmt.Sprintf("page=%d", page)))
}

var rsiRe = regexp.MustCompile(`(?s)<a href="update_images\.php\?v=[^"]*"><img src="(updates/\d+_\d+\.jpg)"[^>]*></a>` +
	`.*?<a href="update_images\.php\?v=[^"]*">(.*?)\s*<br>\s*(.*?)</a>`)

func parseRSI(body []byte, base string) []listingItem {
	matches := rsiRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listingItem

	for _, m := range matches {
		thumb := string(m[1])
		id := extractThumbID(thumb)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		title := strings.TrimSpace(html.UnescapeString(string(m[2])))
		dateStr := strings.TrimSpace(string(m[3]))

		var date time.Time
		if t, err := time.Parse("Mon. Jan 2, 2006", dateStr); err == nil {
			date = t.UTC()
		}

		items = append(items, listingItem{
			id:    id,
			title: title,
			date:  date,
			thumb: base + "/" + thumb,
		})
	}
	return items
}

// SpankedCoeds: spankedcoeds.com

func (s *Scraper) buildSCURL(page int) string {
	params := fmt.Sprintf("section=&implement=&position=&spanker=&model=&order_by=&per_page=50&page=%d", page)
	return fmt.Sprintf("%s/search.php?v=%s", s.base, encodeBase64(params))
}

var (
	scBlockRe = regexp.MustCompile(`(?s)class="searchResults(?:First|Middle)">(.*?)<div style="clear: both;">`)
	scThumbRe = regexp.MustCompile(`src="(updates/\d+_\d+\.jpg)"`)
	scTitleRe = regexp.MustCompile(`(?s)class="episodeTitle"[^>]*>(.*?)</div>`)
	scDescRe  = regexp.MustCompile(`(?s)class="episodeDescription"[^>]*>(.*?)</div>`)
	scDateRe  = regexp.MustCompile(`(?s)class="episodeUpdate">Updated:\s*(.*?)</div>`)
)

func parseSpankedCoeds(body []byte, base string) []listingItem {
	blocks := scBlockRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listingItem

	for _, b := range blocks {
		block := b[1]

		tm := scThumbRe.FindSubmatch(block)
		if tm == nil {
			continue
		}
		thumb := string(tm[1])
		id := extractThumbID(thumb)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		item := listingItem{
			id:    id,
			thumb: base + "/" + thumb,
		}

		if m := scTitleRe.FindSubmatch(block); m != nil {
			item.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
		}
		if m := scDescRe.FindSubmatch(block); m != nil {
			item.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
		}
		if m := scDateRe.FindSubmatch(block); m != nil {
			dateStr := strings.TrimSpace(string(m[1]))
			if t, err := time.Parse("Mon. Jan. 2, 2006", dateStr); err == nil {
				item.date = t.UTC()
			}
		}

		items = append(items, item)
	}
	return items
}

// STB: spankingteenbrandi.com

var stbRe = regexp.MustCompile(`(?s)<a href="viewImage\.php\?v=[^"]*"><img src="(updates/\d+_\d+\.jpg)"[^>]*></a></div>\s*` +
	`<div[^>]*>\s*<div[^>]*>(.*?)</div>\s*<div[^>]*font-style:\s*italic[^>]*>(.*?)</div>`)

func parseSTB(body []byte, base string) []listingItem {
	matches := stbRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listingItem

	for _, m := range matches {
		thumb := string(m[1])
		id := extractThumbID(thumb)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		title := strings.TrimSpace(html.UnescapeString(string(m[2])))
		dateStr := strings.TrimSpace(string(m[3]))

		var date time.Time
		if t, err := time.Parse("Jan. 2, 2006", dateStr); err == nil {
			date = t.UTC()
		}

		items = append(items, listingItem{
			id:    id,
			title: title,
			date:  date,
			thumb: base + "/" + thumb,
		})
	}
	return items
}

// STJ: spankingteenjessica.com

func (s *Scraper) buildSTJURL(page int) string {
	return fmt.Sprintf("%s/updates.php?v=%s", s.base, encodeBase64(fmt.Sprintf("id=&page=%d", page)))
}

var stjRe = regexp.MustCompile(`(?s)<a href="image_view\.php\?v=[^"]*"><img src="(updates/\d+_\d+\.jpg)"[^>]*></a>` +
	`.*?<b><span[^>]*>(.*?)</span></b>.*?Updated on (\d{2}/\d{2}/\d{2})`)

func parseSTJ(body []byte, base string) []listingItem {
	matches := stjRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listingItem

	for _, m := range matches {
		thumb := string(m[1])
		id := extractThumbID(thumb)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		title := strings.TrimSpace(html.UnescapeString(string(m[2])))
		dateStr := strings.TrimSpace(string(m[3]))

		var date time.Time
		if t, err := time.Parse("01/02/06", dateStr); err == nil {
			date = t.UTC()
		}

		items = append(items, listingItem{
			id:    id,
			title: title,
			date:  date,
			thumb: base + "/" + thumb,
		})
	}
	return items
}

// Bailey: spankingbailey.com

var baileyRe = regexp.MustCompile(`(?s)<a href="freeImage\.php\?v=[^"]*"[^>]*><img src="(updates/\d+_[^"]+\.jpg)"[^>]*alt="([^"]*)"[^>]*/?>` +
	`</a>\s*<div class="mainText10b">[^<]*</div>\s*<div class="mainText12Bb">(.*?)</div>`)

func parseBailey(body []byte, base string) []listingItem {
	matches := baileyRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listingItem

	for _, m := range matches {
		thumb := string(m[1])
		id := extractThumbID(thumb)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		title := strings.TrimSpace(html.UnescapeString(string(m[2])))
		dateStr := strings.TrimSpace(string(m[3]))

		var date time.Time
		if t, err := time.Parse("Mon. Jan. 2, 2006", dateStr); err == nil {
			date = t.UTC()
		}

		items = append(items, listingItem{
			id:    id,
			title: title,
			date:  date,
			thumb: base + "/" + thumb,
		})
	}
	return items
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
	return httpx.ReadBody(resp.Body)
}

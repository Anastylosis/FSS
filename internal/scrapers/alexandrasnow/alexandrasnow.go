// Package alexandrasnow scrapes the Alexandra Snow femdom VOD clip store at
// goddesssnow.com (a self-hosted MightyCMS-style VOD CMS). The companion
// domain alexandrasnow.com is only a WordPress blog that links out, so any
// alexandrasnow.com URL is rewritten to the goddesssnow.com catalogue.
package alexandrasnow

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

func init() { scraper.Register(New()) }

const (
	siteID     = "alexandrasnow"
	studioName = "Alexandra Snow"
	baseURL    = "https://www.goddesssnow.com"
)

// Scraper implements scraper.StudioScraper for goddesssnow.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New returns a ready-to-register Scraper.
func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   baseURL,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"goddesssnow.com/",
		"goddesssnow.com/vod/updates",
		"goddesssnow.com/vod/categories/{cat}.html",
		"alexandrasnow.com (rewritten to goddesssnow.com)",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?(goddesssnow|alexandrasnow)\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// cardOpenRe marks the start of each grid card. The carousel block at the
	// top of the listing uses a different wrapper, so anchoring on
	// update_details + data-setid isolates only the grid cards. Cards are split
	// on these offsets rather than matched whole (RE2 has no lookahead).
	cardOpenRe = regexp.MustCompile(`<div class="update_details" data-setid="\d+">`)

	cardSetIDRe   = regexp.MustCompile(`data-setid="(\d+)"`)
	cardTitleRe   = regexp.MustCompile(`(?s)<a\s+class="update-title" href="[^"]*"><h4>(.*?)</h4></a>`)
	cardURLRe     = regexp.MustCompile(`<a\s+class="update-title" href="([^"]+)"`)
	cardThumbRe   = regexp.MustCompile(`class="update_thumb[^"]*"[^>]*src0_1x="([^"]+)"`)
	cardDateRe    = regexp.MustCompile(`<span class="date">([^<]+)</span>`)
	cardDurRe     = regexp.MustCompile(`(?s)<span class="duration">\s*(.*?)</span>`)
	cardPriceRe   = regexp.MustCompile(`(?s)class="buy_button">\s*<!--Buy-->\s*\$([0-9.]+)`)
	cardCatPathRe = regexp.MustCompile(`/vod/categories/`)

	// detail page category tags (skip the empty movies/photos nav links).
	detailCatRe  = regexp.MustCompile(`<a href="https?://[^"]*/vod/categories/[^"]+\.html"\s*>([^<]+)</a>`)
	detailDescRe = regexp.MustCompile(`(?s)<span class="update_description">(.*?)</span>`)
	durMinRe     = regexp.MustCompile(`(\d+)\s*(?:\x{00a0}|&nbsp;|\s)*min`)
)

type listItem struct {
	id        string
	url       string
	title     string
	thumbnail string
	date      time.Time
	duration  int
	price     float64
	hasPrice  bool
}

type detailData struct {
	description string
	thumbnail   string
	categories  []string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// alexandrasnow.com is just a blog; always scrape the goddesssnow catalogue.
	listBase := s.base + "/vod/updates"
	if cardCatPathRe.MatchString(studioURL) {
		// A category listing: e.g. /vod/categories/SissyTraining.html paginates
		// via page_N too, so reuse the same loop with its own base path.
		listBase = strings.TrimSuffix(s.rewriteHost(studioURL), ".html")
		scraper.Debugf(1, "alexandrasnow: scraping category listing %s", listBase)
	}

	now := time.Now().UTC()
	firstPage := true

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/page_%d.html?s=d", listBase, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if firstPage {
			firstPage = false
			scraper.Debugf(1, "alexandrasnow: %d cards on first page", len(items))
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{Scenes: scenes, Total: total, Done: false}, nil
	})
}

// rewriteHost forces any goddesssnow/alexandrasnow URL onto the scraper's base
// host (www.goddesssnow.com in production, the httptest server in tests),
// preserving the /vod/... path.
func (s *Scraper) rewriteHost(rawURL string) string {
	if i := strings.Index(rawURL, "/vod/"); i >= 0 {
		return s.base + rawURL[i:]
	}
	return s.base + "/vod/updates"
}

// absURL resolves a possibly-relative listing asset path against s.base.
func (s *Scraper) absURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	return s.base + u
}

func parseListingPage(body []byte) []listItem {
	locs := cardOpenRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if it, ok := parseCard(body[loc[0]:end]); ok {
			items = append(items, it)
		}
	}
	return items
}

func parseCard(card []byte) (listItem, bool) {
	m := cardSetIDRe.FindSubmatch(card)
	if m == nil {
		return listItem{}, false
	}
	it := listItem{id: string(m[1])}

	if mURL := cardURLRe.FindSubmatch(card); mURL != nil {
		it.url = string(mURL[1])
	}
	if it.url == "" {
		return listItem{}, false
	}

	if mTitle := cardTitleRe.FindSubmatch(card); mTitle != nil {
		it.title = cleanText(string(mTitle[1]))
	}

	if mThumb := cardThumbRe.FindSubmatch(card); mThumb != nil {
		it.thumbnail = strings.TrimSpace(string(mThumb[1]))
	}

	if mDate := cardDateRe.FindSubmatch(card); mDate != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(string(mDate[1])), "01/02/2006"); err == nil {
			it.date = t.UTC()
		}
	}

	if mDur := cardDurRe.FindSubmatch(card); mDur != nil {
		it.duration = parseDuration(string(mDur[1]))
	}

	if mPrice := cardPriceRe.FindSubmatch(card); mPrice != nil {
		if v, err := strconv.ParseFloat(string(mPrice[1]), 64); err == nil {
			it.price = v
			it.hasPrice = true
		}
	}

	return it, true
}

// parseDuration converts the listing's "4&nbsp;min&nbsp;of video" text into
// seconds. Falls back to MM:SS parsing if a colon form ever appears.
func parseDuration(s string) int {
	s = html.UnescapeString(s)
	if m := durMinRe.FindStringSubmatch(s); m != nil {
		if mins, err := strconv.Atoi(m[1]); err == nil {
			return mins * 60
		}
	}
	if strings.Contains(s, ":") {
		return parseutil.ParseDurationColon(strings.TrimSpace(s))
	}
	return 0
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "alexandrasnow: fetching %d details with %d workers", len(items), workers)

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
		// A known ID stops Paginate early — skip the detail fetch and emit a
		// lightweight scene from the listing card alone.
		if opts.KnownIDs[it.id] {
			results[i] = enriched{item: it}
			continue
		}
		wg.Add(1)
		go func(idx int, item listItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			detail, err := s.fetchDetail(ctx, item.url)
			results[idx] = enriched{item: item, detail: detail, err: err}
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, r := range results {
		if ctx.Err() != nil {
			break
		}
		if r.item.id == "" {
			continue
		}
		scenes = append(scenes, s.toScene(r.item, r.detail, now))
	}
	return scenes
}

func (s *Scraper) fetchDetail(ctx context.Context, rawURL string) (detailData, error) {
	body, err := s.fetchPage(ctx, s.rewriteHost(rawURL))
	if err != nil {
		return detailData{}, err
	}
	return parseDetailPage(body), nil
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	og := parseutil.OpenGraph(body)
	if desc := strings.TrimSpace(og["description"]); desc != "" {
		d.description = cleanText(desc)
	}
	if img := strings.TrimSpace(og["image"]); img != "" {
		d.thumbnail = html.UnescapeString(img)
	}

	// The full (untruncated) description lives in the update_description span.
	if m := detailDescRe.FindSubmatch(body); m != nil {
		if full := cleanText(string(m[1])); full != "" {
			d.description = full
		}
	}

	seen := make(map[string]bool)
	for _, m := range detailCatRe.FindAllSubmatch(body, -1) {
		cat := cleanText(string(m[1]))
		if cat != "" && !seen[cat] {
			seen[cat] = true
			d.categories = append(d.categories, cat)
		}
	}

	return d
}

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	thumb := s.absURL(it.thumbnail)
	if d.thumbnail != "" {
		thumb = d.thumbnail
	}

	scene := models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   s.base,
		Title:       it.title,
		URL:         s.rewriteHost(it.url),
		Date:        it.date,
		Description: d.description,
		Thumbnail:   thumb,
		Duration:    it.duration,
		Categories:  d.categories,
		Studio:      studioName,
		ScrapedAt:   now,
	}

	if it.hasPrice {
		when := now
		if !it.date.IsZero() {
			when = it.date
		}
		scene.AddPrice(models.PriceSnapshot{
			Date:    when,
			Regular: it.price,
		})
	}

	return scene
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	scraper.Debugf(2, "alexandrasnow: GET %s", url)
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

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " "))
}

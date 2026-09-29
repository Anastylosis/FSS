// Package japanhdv scrapes scene metadata from japanhdv.com, an uncensored
// JAV studio running WordPress (custom post type vms_videos).
//
// Listing pages at /japan-porn/page/N/ are newest-first and carry most of a
// scene's metadata directly in each card (title, duration, performer,
// thumbnail). A worker pool then fetches each scene's detail page for the
// og:description and the full Categories tag list. The scene slug is the ID.
package japanhdv

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

const studioName = "Japan HDV"

// Scraper implements scraper.StudioScraper for japanhdv.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New returns a Scraper configured for the live site.
func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://japanhdv.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return "japanhdv" }

func (s *Scraper) Patterns() []string {
	return []string{
		"japanhdv.com",
		"japanhdv.com/japan-porn",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?japanhdv\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// cardRe isolates one scene card from the listing grid. Each card opens with
	// the thumbnail anchor and runs up to (but not into) the next one.
	cardRe = regexp.MustCompile(`(?s)<a title="[^"]*" href="https?://[^"]*?/[a-z0-9-]+/"\s+class="video-thumb-prev">.*?<h3 class="title_desc">.*?</h3><div class="act_list">.*?</div>`)
	// cardLinkRe pulls the scene URL (slug) and title from the thumbnail anchor.
	cardLinkRe  = regexp.MustCompile(`<a title="([^"]*)" href="(https?://[^"]*?/([a-z0-9-]+)/)"\s+class="video-thumb-prev">`)
	cardThumbRe = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	cardDurRe   = regexp.MustCompile(`<span class="th_video_duration">(\d{1,2}:\d{2}(?::\d{2})?)</span>`)
	cardPerfRe  = regexp.MustCompile(`<div class="act_list">(.*?)</div>`)
	cardModelRe = regexp.MustCompile(`<a href="https?://[^"]*?/model/[^"]*"[^>]*>([^<]+)</a>`)

	// pageNumRe finds the highest /japan-porn/page/N/ number in the pagination
	// strip, used for a one-time total estimate.
	pageNumRe = regexp.MustCompile(`/japan-porn/page/(\d+)/`)
	// nextRe detects the "Next »" pagination link.
	nextRe = regexp.MustCompile(`class="next page-numbers"`)
)

type listItem struct {
	id         string
	url        string
	title      string
	thumbnail  string
	duration   int
	performers []string
}

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	firstPage := true

	scraper.Paginate(ctx, opts, "japanhdv", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/japan-porn/page/%d/", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}
		scraper.Debugf(1, "japanhdv: page %d parsed %d cards", page, len(items))

		total := 0
		if firstPage {
			firstPage = false
			total = maxPageNum(body) * len(items)
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   !nextRe.Match(body),
		}, nil
	})
}

func parseListing(body []byte) []listItem {
	cards := cardRe.FindAll(body, -1)
	items := make([]listItem, 0, len(cards))
	seen := make(map[string]bool)
	for _, card := range cards {
		if it, ok := parseCard(card); ok && !seen[it.id] {
			seen[it.id] = true
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
		title: cleanText(string(m[1])),
		url:   string(m[2]),
		id:    string(m[3]),
	}
	if mt := cardThumbRe.FindSubmatch(card); mt != nil {
		it.thumbnail = absURL(string(mt[1]))
	}
	if md := cardDurRe.FindSubmatch(card); md != nil {
		it.duration = parseutil.ParseDurationColon(string(md[1]))
	}
	if mp := cardPerfRe.FindSubmatch(card); mp != nil {
		for _, pm := range cardModelRe.FindAllSubmatch(mp[1], -1) {
			if name := cleanText(string(pm[1])); name != "" {
				it.performers = append(it.performers, name)
			}
		}
	}
	return it, true
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

type detailData struct {
	description string
	performers  []string
	categories  []string
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "japanhdv: fetching %d details with %d workers", len(items), workers)

	results := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		// Known IDs become lightweight stubs so Paginate's early-stop fires
		// without spending a detail fetch on already-stored scenes.
		if opts.KnownIDs[it.id] {
			results[i] = models.Scene{ID: it.id, SiteID: "japanhdv"}
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

			detail := detailData{}
			if body, err := s.fetchPage(ctx, item.url); err != nil {
				scraper.Debugf(1, "japanhdv: detail %s failed: %v (using card data)", item.id, err)
			} else {
				detail = parseDetail(body)
			}
			results[idx] = toScene(item, detail, now)
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" {
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

var (
	// metaBlockRe scopes parsing to the scene's own metadata block (the first
	// occurrence), keeping the related-videos sidebar's links out.
	actressBlockRe = regexp.MustCompile(`(?s)<strong>Actress:\s*</strong>(.*?)</(?:p|li)>`)
	catsBlockRe    = regexp.MustCompile(`(?s)<strong>Categories:\s*</strong>(.*?)</(?:p|li)>`)
	tagLinkRe      = regexp.MustCompile(`<a href="https?://[^"]*"[^>]*>([^<]+)</a>`)
)

func parseDetail(body []byte) detailData {
	var d detailData

	og := parseutil.OpenGraph(body)
	d.description = cleanText(og["og:description"])

	if m := actressBlockRe.FindSubmatch(body); m != nil {
		for _, pm := range cardModelRe.FindAllSubmatch(m[1], -1) {
			if name := cleanText(string(pm[1])); name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}
	if m := catsBlockRe.FindSubmatch(body); m != nil {
		seen := make(map[string]bool)
		for _, tm := range tagLinkRe.FindAllSubmatch(m[1], -1) {
			tag := cleanText(string(tm[1]))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				d.categories = append(d.categories, tag)
			}
		}
	}
	return d
}

func toScene(it listItem, d detailData, now time.Time) models.Scene {
	performers := it.performers
	if len(d.performers) > len(performers) {
		performers = d.performers
	}
	return models.Scene{
		ID:          it.id,
		SiteID:      "japanhdv",
		StudioURL:   "https://japanhdv.com",
		Studio:      studioName,
		Title:       it.title,
		URL:         it.url,
		Description: d.description,
		Thumbnail:   it.thumbnail,
		Duration:    it.duration,
		Performers:  performers,
		Categories:  d.categories,
		Tags:        d.categories,
		ScrapedAt:   now,
	}
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

func cleanText(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}

// absURL upgrades protocol-relative (//host/...) thumbnail URLs to https.
func absURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

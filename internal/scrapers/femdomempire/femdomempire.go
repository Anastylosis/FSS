// Package femdomempire scrapes scene metadata from femdomempire.com, a femdom
// studio running a NATS/MGA "tour" ElevatedX-style CMS. Only the public tour
// listing pages are read.
package femdomempire

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

func init() { scraper.Register(New()) }

const (
	studioName = "Femdom Empire"
	siteID     = "femdomempire"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://femdomempire.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"femdomempire.com",
		"femdomempire.com/tour/categories/movies/{n}/latest/",
		"femdomempire.com/tour/categories/{name}/{n}/latest/",
		"femdomempire.com/tour/trailers/{slug}.html",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?femdomempire\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// cardRe isolates a single listing card.
	cardRe = regexp.MustCompile(`(?s)<div class="item hover">.*?<!--//item-->`)
	// cardIDRe extracts the numeric scene id from the thumbnail target.
	cardIDRe = regexp.MustCompile(`id="set-target-(\d+)"`)
	// cardLinkRe extracts the relative trailer path and slug from the card.
	cardLinkRe = regexp.MustCompile(`href="https?://[^"/]+(/tour/trailers/([^"./]+)\.html)"`)
	// cardTitleRe pulls the card title from the trailer link's title attribute.
	cardTitleRe = regexp.MustCompile(`href="https?://[^"]*/tour/trailers/[^"]+\.html" title="([^"]*)"`)
	// cardThumbRe pulls the first thumbnail (src0_1x).
	cardThumbRe = regexp.MustCompile(`src0_1x="([^"]+)"`)
	// cardModelRe pulls each featured performer name from the card.
	cardModelRe = regexp.MustCompile(`href="https?://[^"]*/tour/models/[^"]+\.html"[^>]*>([^<]+)</a>`)
	// cardDateRe pulls the "June 19, 2026" date text.
	cardDateRe = regexp.MustCompile(`(?s)<span class="date">.*?</i>\s*([^<]+?)\s*</span>`)
	// cardDurRe pulls the "11:26" duration from the time span.
	cardDurRe = regexp.MustCompile(`(?s)<span class="time">.*?,\s*([\d:]+)\s*</span>`)

	// detailTitleRe / detailDescRe parse the videoDetails block.
	detailTitleRe = regexp.MustCompile(`(?s)<div class="videoDetails clear">\s*<h3>(.*?)</h3>`)
	detailDescRe  = regexp.MustCompile(`(?s)<div class="videoDetails clear">\s*<h3>.*?</h3>\s*<p>(.*?)</p>`)
	// detailCatBlockRe isolates the Categories featuring block; detailCatRe
	// extracts each category link text within it.
	detailCatBlockRe = regexp.MustCompile(`(?s)<li class="label">Categories:</li>(.*?)</ul>`)
	detailCatRe      = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
)

const dateLayout = "January 2, 2006"

type listItem struct {
	id         string
	path       string // relative, e.g. /tour/trailers/Foo.html
	title      string
	thumbnail  string
	performers []string
	date       time.Time
	duration   int
}

type detailData struct {
	title       string
	description string
	tags        []string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	baseURL := s.listingBase(studioURL)
	scraper.Debugf(1, "femdomempire: paginating %s", baseURL)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := pageURLFor(baseURL, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		// No reliable total on the listing; report per-page progress so the
		// loop keeps walking until an empty page (Done) ends it.
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// listingBase returns the path-based listing root to paginate. If the studio
// URL already points at a /tour/categories/.../latest/ listing, that category
// is preserved; otherwise the default movies/latest catalogue is used.
func (s *Scraper) listingBase(studioURL string) string {
	if m := regexp.MustCompile(`(?i)(https?://[^/]+)?(/tour/categories/[^/]+)/\d+/(latest|popular|name)/?`).FindStringSubmatch(studioURL); m != nil {
		return s.base + m[2] + "/%d/" + m[3] + "/"
	}
	return s.base + "/tour/categories/movies/%d/latest/"
}

// pageURLFor renders a page URL from a base template containing a single %d.
func pageURLFor(baseTemplate string, page int) string {
	return fmt.Sprintf(baseTemplate, page)
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
	link := cardLinkRe.FindSubmatch(card)
	if link == nil {
		return listItem{}, false
	}
	it := listItem{
		path: string(link[1]),
		id:   string(link[2]), // slug is the stable scene id
	}
	if m := cardIDRe.FindSubmatch(card); m != nil {
		it.id = string(m[1])
	}

	if m := cardTitleRe.FindSubmatch(card); m != nil {
		it.title = cleanText(string(m[1]))
	}
	if it.title == "" {
		// fall back to the slug as a readable title
		it.title = strings.ReplaceAll(string(link[2]), "-", " ")
	}

	if m := cardThumbRe.FindSubmatch(card); m != nil {
		it.thumbnail = string(m[1])
	}

	seen := make(map[string]bool)
	for _, m := range cardModelRe.FindAllSubmatch(card, -1) {
		name := cleanText(string(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			it.performers = append(it.performers, name)
		}
	}

	if m := cardDateRe.FindSubmatch(card); m != nil {
		if t, err := parseutil.TryParseDate(cleanText(string(m[1])), dateLayout); err == nil {
			it.date = t.UTC()
		}
	}

	if m := cardDurRe.FindSubmatch(card); m != nil {
		it.duration = parseutil.ParseDurationColon(strings.TrimSpace(string(m[1])))
	}

	return it, true
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "femdomempire: fetching %d details with %d workers", len(items), workers)

	type enriched struct {
		item   listItem
		detail detailData
	}

	results := make([]enriched, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		// Known IDs become lightweight stubs (no detail fetch) so Paginate's
		// KnownIDs early-stop still fires while preserving newest-first order.
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

			body, err := s.fetchPage(ctx, s.base+item.path)
			if err != nil {
				scraper.Debugf(1, "femdomempire: detail %s failed: %v (using listing data)", item.id, err)
				results[idx] = enriched{item: item}
				return
			}
			results[idx] = enriched{item: item, detail: parseDetailPage(body)}
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, r := range results {
		if r.item.id == "" {
			continue
		}
		scenes = append(scenes, toScene(s.base, r.item, r.detail, now))
	}
	return scenes
}

func parseDetailPage(body []byte) detailData {
	var d detailData
	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = cleanText(string(m[1]))
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = cleanText(string(m[1]))
	}
	if block := detailCatBlockRe.FindSubmatch(body); block != nil {
		seen := make(map[string]bool)
		for _, m := range detailCatRe.FindAllSubmatch(block[1], -1) {
			tag := cleanText(string(m[1]))
			if tag != "" && !seen[tag] {
				seen[tag] = true
				d.tags = append(d.tags, tag)
			}
		}
	}
	return d
}

func toScene(base string, it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
	}
	thumb := it.thumbnail
	if thumb != "" && strings.HasPrefix(thumb, "/") {
		thumb = base + thumb
	}
	return models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   base,
		Studio:      studioName,
		Title:       title,
		URL:         base + it.path,
		Thumbnail:   thumb,
		Date:        it.date,
		Duration:    it.duration,
		Performers:  it.performers,
		Description: d.description,
		Tags:        d.tags,
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
	return strings.TrimSpace(html.UnescapeString(strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")))
}

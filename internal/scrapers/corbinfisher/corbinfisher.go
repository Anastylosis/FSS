// Package corbinfisher scrapes the Corbin Fisher tour (corbinfisher.com), a gay
// studio running the ElevatedX "tour" CMS. It walks the public tour listings
// (paginated category pages) and enriches each scene from its /tour/trailers/
// detail page. Authorized open-source metadata scraper (public tour listings).
package corbinfisher

import (
	"bytes"
	"context"
	"errors"
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

const studioName = "Corbin Fisher"

// Scraper implements scraper.StudioScraper for corbinfisher.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New returns a Corbin Fisher scraper.
func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www.corbinfisher.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return "corbinfisher" }

func (s *Scraper) Patterns() []string {
	return []string{
		"corbinfisher.com",
		"corbinfisher.com/tour/categories/guys/{N}/latest/",
		"corbinfisher.com/tour/models/{Name}.html",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?corbinfisher\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// cardStart marks each listing card on a tour listing/model page. Cards are
	// split on it rather than matched to a closing tag: the hover-preview
	// thumbnail nests enough empty divs to end any non-greedy match early.
	cardStart = []byte(`<div class="item item-update">`)
	// cardHrefRe captures the trailer URL inside a card.
	cardHrefRe = regexp.MustCompile(`href="[^"]*?/tour/trailers/([^"]+)\.html"[^>]*?title="([^"]*)"`)
	// cardThumbRe captures the card thumbnail (token-signed CDN URL).
	cardThumbRe = regexp.MustCompile(`class="update_thumb thumbs stdimage"[^>]*?src0_1x="([^"]+)"`)
	// cardPlaceholderRe captures the poster of a hover-preview card.
	cardPlaceholderRe = regexp.MustCompile(`<img src="([^"]+)"[^>]*?class="video_placeholder"`)
	// nextPageRe captures the target of each pagination "next" link.
	nextPageRe = regexp.MustCompile(`href="[^"]*?/tour/categories/guys/(\d+)/latest/" class="next-page"`)
	// cardTimeRe captures "MM:SS Minutes" from the item-meta block.
	cardTimeRe = regexp.MustCompile(`<div class="time">\s*(\d+:\d{2})`)

	// detailTitleRe captures the scene title from the memberInfo header.
	detailTitleRe = regexp.MustCompile(`(?s)<div class="blogSpace memberInfo">\s*<h2>([^<]+)</h2>`)
	// detailDateRe captures the "Added:" date.
	detailDateRe = regexp.MustCompile(`<span>Added:</span>\s*([^|<]+?)\s*\|`)
	// detailDurRe captures "Video Length: MM:SS".
	detailDurRe = regexp.MustCompile(`<span>Video Length:</span>\s*(\d+:\d{2})`)
	// detailFeatBlockRe isolates the "Featuring:" model list.
	detailFeatBlockRe = regexp.MustCompile(`(?s)<div class="modelFeaturing">(.*?)</div>`)
	// detailModelRe captures each model name from the featuring block.
	detailModelRe = regexp.MustCompile(`<a href="[^"]*?/tour/models/[^"]+\.html">([^<]+)</a>`)
	// detailDescRe captures the description paragraph.
	detailDescRe = regexp.MustCompile(`(?s)<div class="description">\s*<h4>[^<]*</h4>\s*<p>(.*?)</p>`)

	// modelPageRe detects a model scenes page (/tour/models/{Name}.html).
	modelPageRe = regexp.MustCompile(`(?i)/tour/models/[^/]+\.html$`)
)

type listItem struct {
	id        string // trailer slug (also the scene ID)
	slug      string
	title     string
	thumbnail string
	duration  int
}

type detailData struct {
	title       string
	date        time.Time
	duration    int
	performers  []string
	description string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if modelPageRe.MatchString(studioURL) {
		scraper.Debugf(1, "corbinfisher: detected model page")
		s.scrapeSinglePage(ctx, studioURL, opts, out)
		return
	}

	s.scrapePaginated(ctx, opts, out)
}

// scrapePaginated walks the main category listing newest-first.
func (s *Scraper) scrapePaginated(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "corbinfisher", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/tour/categories/guys/%d/latest/", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		more := hasNextPage(body, page)
		if len(items) == 0 {
			if page == 1 || more {
				return scraper.PageResult{}, scraper.ParseError(pageURL, errNoCards)
			}
			return scraper.PageResult{}, nil
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{
			Scenes: scenes,
			Done:   !more,
		}, nil
	})
}

var errNoCards = errors.New("no scene cards found on listing page")

// hasNextPage reports whether the pagination links on page point at page+1.
func hasNextPage(body []byte, page int) bool {
	for _, m := range nextPageRe.FindAllSubmatch(body, -1) {
		if string(m[1]) == strconv.Itoa(page+1) {
			return true
		}
	}
	return false
}

// scrapeSinglePage handles a model page (single page of that model's scenes).
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
		select {
		case out <- scraper.Error(scraper.ParseError(pageURL, errNoCards)):
		case <-ctx.Done():
		}
		return
	}

	scraper.Debugf(1, "corbinfisher: found %d scenes on model page", len(items))
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	scenes := s.fetchDetails(ctx, items, opts, now)
	for _, sc := range scenes {
		if opts.KnownIDs[sc.ID] {
			scraper.Debugf(1, "corbinfisher: hit known ID, stopping early")
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

func parseListingPage(body []byte) []listItem {
	parts := bytes.Split(body, cardStart)
	items := make([]listItem, 0, len(parts))
	seen := make(map[string]bool)
	for _, card := range parts[min(1, len(parts)):] {
		it, ok := parseCard(card)
		if !ok || seen[it.id] {
			continue
		}
		seen[it.id] = true
		items = append(items, it)
	}
	return items
}

func parseCard(card []byte) (listItem, bool) {
	m := cardHrefRe.FindSubmatch(card)
	if m == nil {
		return listItem{}, false
	}
	it := listItem{
		id:    string(m[1]),
		slug:  string(m[1]),
		title: deslugify(string(m[1])),
	}
	if t := strings.TrimSpace(html.UnescapeString(string(m[2]))); t != "" {
		it.title = t
	}
	if mt := cardThumbRe.FindSubmatch(card); mt != nil {
		it.thumbnail = html.UnescapeString(string(mt[1]))
	} else if mt := cardPlaceholderRe.FindSubmatch(card); mt != nil {
		it.thumbnail = html.UnescapeString(string(mt[1]))
	}
	if md := cardTimeRe.FindSubmatch(card); md != nil {
		it.duration = parseutil.ParseDurationColon(string(md[1]))
	}
	return it, true
}

// deslugify turns a trailer slug ("Fucking-Charlie") into a fallback title.
func deslugify(slug string) string {
	return strings.TrimSpace(strings.ReplaceAll(slug, "-", " "))
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := scraper.WorkerCount(opts, 4)

	scraper.Debugf(1, "corbinfisher: fetching %d details with %d workers", len(items), workers)

	type enriched struct {
		item   listItem
		detail detailData
		ok     bool
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

			detail, err := s.fetchDetail(ctx, s.trailerURL(item.slug))
			results[idx] = enriched{item: item, detail: detail, ok: err == nil}
			if err != nil {
				scraper.Debugf(1, "corbinfisher: detail %s failed: %v (using listing data)", item.slug, err)
			}
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

func (s *Scraper) trailerURL(slug string) string {
	return s.base + "/tour/trailers/" + slug + ".html"
}

func (s *Scraper) fetchDetail(ctx context.Context, rawURL string) (detailData, error) {
	body, err := s.fetchPage(ctx, rawURL)
	if err != nil {
		return detailData{}, err
	}
	return parseDetailPage(body), nil
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}
	if d.title == "" {
		// Fallback to og:title ("Corbin Fisher - {Title}").
		if og := parseutil.OpenGraph(body); og["title"] != "" {
			t := html.UnescapeString(strings.TrimSpace(og["title"]))
			t = strings.TrimPrefix(t, studioName+" - ")
			d.title = t
		}
	}

	if m := detailDateRe.FindSubmatch(body); m != nil {
		raw := parseutil.StripOrdinalSuffix(strings.TrimSpace(string(m[1])))
		if t, err := parseutil.TryParseDate(raw, "January 2, 2006", "Jan 2, 2006"); err == nil {
			d.date = t.UTC()
		}
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}

	if block := detailFeatBlockRe.FindSubmatch(body); block != nil {
		seen := make(map[string]bool)
		for _, m := range detailModelRe.FindAllSubmatch(block[1], -1) {
			name := strings.TrimSpace(html.UnescapeString(string(m[1])))
			if name != "" && !seen[name] {
				seen[name] = true
				d.performers = append(d.performers, name)
			}
		}
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = cleanDescription(string(m[1]))
	}

	return d
}

// cleanDescription strips <br> tags and collapses whitespace.
var brRe = regexp.MustCompile(`(?i)<br\s*/?>`)

func cleanDescription(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	var out []string
	for _, ln := range lines {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n\n")
}

func (s *Scraper) toScene(it listItem, d detailData, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
	}
	duration := d.duration
	if duration == 0 {
		duration = it.duration
	}

	return models.Scene{
		ID:          it.id,
		SiteID:      "corbinfisher",
		StudioURL:   s.base,
		Studio:      studioName,
		Title:       title,
		URL:         s.trailerURL(it.slug),
		Date:        d.date,
		Description: d.description,
		Thumbnail:   it.thumbnail,
		Performers:  d.performers,
		Duration:    duration,
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

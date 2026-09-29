package spizooutil

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

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
	base   string
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www." + cfg.Domain,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain + "/categories/movies.html",
		s.cfg.Domain + "/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	return scraper.HostMatches(u, s.cfg.Domain)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	thumbBlockRe = regexp.MustCompile(`(?s)<div class="thumb-pic">.*?</div>\s*</div>\s*</div>`)
	sceneURLRe   = regexp.MustCompile(`href="(https?://[^"]+/updates/[^"]+\.html)"`)
	thumbImgRe   = regexp.MustCompile(`<img[^>]+src="([^"]+)"[^>]+class="[^"]*thumbs[^"]*"`)
	listTitleRe  = regexp.MustCompile(`(?s)<div class="title-label">\s*<a[^>]*>\s*([^<]+?)\s*</a>`)
	listPerfRe   = regexp.MustCompile(`(?s)<span class="tour_update_models">\s*<a[^>]+title="([^"]+)"`)
	maxPageRe    = regexp.MustCompile(`movies_(\d+)_d\.html`)
	pageOfRe     = regexp.MustCompile(`Page\s+\d+\s+of\s+(\d+)`)

	detailDateRe = regexp.MustCompile(`class="date">(\d{4}-\d{2}-\d{2})`)
	detailDurRe  = regexp.MustCompile(`(?s)Length:\s*</h4>\s*<p>\s*(\d+:\d{2})`)
	detailPerfRe = regexp.MustCompile(`(?s)Pornstars:\s*</h3>(.*?)</div>`)
	perfLinkRe   = regexp.MustCompile(`title="([^"]+)"`)
	detailDescRe = regexp.MustCompile(`name="description"\s+content="([^"]+)"`)
	detailTagRe  = regexp.MustCompile(`title="([^"]+)"\s+class="\s*category-tag"`)

	modelSlugRe = regexp.MustCompile(`/models/[^/]+\.html`)
)

type listItem struct {
	slug       string
	url        string
	title      string
	thumbnail  string
	performers []string
}

type detailData struct {
	date        time.Time
	duration    int
	performers  []string
	description string
	tags        []string
}

func parseListingPage(body []byte) []listItem {
	blocks := thumbBlockRe.FindAll(body, -1)
	items := make([]listItem, 0, len(blocks))
	for _, block := range blocks {
		if it, ok := parseListItem(block); ok {
			items = append(items, it)
		}
	}
	return items
}

func parseListItem(block []byte) (listItem, bool) {
	m := sceneURLRe.FindSubmatch(block)
	if m == nil {
		return listItem{}, false
	}
	rawURL := string(m[1])

	slug := extractSlug(rawURL)
	if slug == "" {
		return listItem{}, false
	}

	it := listItem{
		slug: slug,
		url:  rawURL,
	}

	if mTitle := listTitleRe.FindSubmatch(block); mTitle != nil {
		it.title = html.UnescapeString(strings.TrimSpace(string(mTitle[1])))
	}

	if mThumb := thumbImgRe.FindSubmatch(block); mThumb != nil {
		it.thumbnail = string(mThumb[1])
	}

	for _, mPerf := range listPerfRe.FindAllSubmatch(block, -1) {
		name := strings.TrimSpace(html.UnescapeString(string(mPerf[1])))
		if name != "" {
			it.performers = append(it.performers, name)
		}
	}

	return it, true
}

var slugRe = regexp.MustCompile(`/updates/([^/.]+)\.html`)

func extractSlug(rawURL string) string {
	m := slugRe.FindStringSubmatch(rawURL)
	if m == nil {
		return ""
	}
	return m[1]
}

func estimateTotal(body []byte, perPage int) int {
	if m := pageOfRe.FindSubmatch(body); m != nil {
		pages, _ := strconv.Atoi(string(m[1]))
		return pages * perPage
	}
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if modelSlugRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: detected model page", s.cfg.SiteID)
		s.scrapeModelPage(ctx, studioURL, opts, out)
		return
	}

	s.scrapeListingPages(ctx, opts, out)
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetchPage(ctx, studioURL)
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
	scraper.Debugf(1, "%s: found %d scenes on model page", s.cfg.SiteID, len(items))

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > 1 {
			return scraper.PageResult{}, nil
		}
		scenes, err := s.fetchDetailScenes(ctx, items, opts, now)
		if err != nil {
			return scraper.PageResult{}, err
		}
		return scraper.PageResult{Scenes: scenes, Total: len(items), Done: true}, nil
	})
}

func (s *Scraper) scrapeListingPages(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/movies_%d_d.html", s.base, page)

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
			total = estimateTotal(body, len(items))
		}

		scenes, err := s.fetchDetailScenes(ctx, items, opts, now)
		if err != nil {
			return scraper.PageResult{}, err
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (s *Scraper) fetchDetailScenes(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) ([]models.Scene, error) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

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

			detail, err := s.fetchDetail(ctx, item.url)
			results[idx] = enriched{item: item, detail: detail, err: err}
		}(i, it)
	}
	wg.Wait()

	var scenes []models.Scene
	for _, r := range results {
		if ctx.Err() != nil {
			break
		}
		if r.err != nil {
			scraper.Debugf(2, "%s: detail %s: %v", s.cfg.SiteID, r.item.slug, r.err)
			continue
		}
		scenes = append(scenes, toScene(s.cfg.SiteID, s.base, r.item, r.detail, now))
	}
	return scenes, nil
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

	if m := detailDateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("2006-01-02", string(m[1])); err == nil {
			d.date = t
		}
	}

	if m := detailDurRe.FindSubmatch(body); m != nil {
		d.duration = parseutil.ParseDurationColon(string(m[1]))
	}

	if m := detailPerfRe.FindSubmatch(body); m != nil {
		for _, pm := range perfLinkRe.FindAllSubmatch(m[1], -1) {
			name := strings.TrimSpace(html.UnescapeString(string(pm[1])))
			if name != "" && name != "Pornstars" {
				d.performers = append(d.performers, name)
			}
		}
	}

	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	seen := make(map[string]bool)
	for _, m := range detailTagRe.FindAllSubmatch(body, -1) {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			d.tags = append(d.tags, tag)
		}
	}

	return d
}

func toScene(siteID, base string, it listItem, d detailData, now time.Time) models.Scene {
	performers := d.performers
	if len(performers) == 0 {
		performers = it.performers
	}

	return models.Scene{
		ID:          it.slug,
		SiteID:      siteID,
		StudioURL:   base,
		Title:       it.title,
		URL:         it.url,
		Thumbnail:   it.thumbnail,
		Date:        d.date,
		Duration:    d.duration,
		Performers:  performers,
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

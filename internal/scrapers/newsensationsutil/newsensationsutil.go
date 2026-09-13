package newsensationsutil

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

// RecommendedDelay is the minimum delay between page fetches at which
// New Sensations' NATS tour does not rate-limit. It is **not** silently
// enforced — the operator's `opts.Delay` is always honoured — but
// `WarnDelayBelow` will surface a one-shot stderr warning when the
// configured delay is lower.
const RecommendedDelay = 500 * time.Millisecond

type SiteConfig struct {
	SiteID     string
	Domain     string
	SiteBase   string
	TourPrefix string
	StudioName string
	AltDomains []string
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	matchRe *regexp.Regexp
	modelRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	pattern := regexp.QuoteMeta(cfg.Domain)
	for _, alt := range cfg.AltDomains {
		pattern += "|" + regexp.QuoteMeta(alt)
	}
	re := regexp.MustCompile(`^https?://(?:www\.)?(?:` + pattern + `)(?:/|$)`)

	// Model pages appear both prefixed (sister sites: /tour_x/models/) and
	// prefix-less (site-canonical, e.g. newsensations.com/models/jane.html,
	// live-verified). Match either so the canonical form isn't mistaken for the
	// full catalogue.
	modelRe := regexp.MustCompile(`/(?:` + regexp.QuoteMeta(cfg.TourPrefix) + `/)?models/`)

	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		matchRe: re,
		modelRe: modelRe,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	tp := s.cfg.TourPrefix
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/" + tp + "/categories/movies_{page}_d.html",
		s.cfg.Domain + "/" + tp + "/models/{name}.html",
		s.cfg.Domain + "/" + tp + "/categories/{category}_{page}_d.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type workItem struct {
	url        string
	id         string
	title      string
	performers []string
	thumb      string
	preview    string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.WarnDelayBelow(s.cfg.SiteID, opts.Delay, RecommendedDelay)
	delay := opts.Delay

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan workItem)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching detail pages with %d workers", s.cfg.SiteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				scene, err := s.fetchDetail(ctx, item, studioURL, delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	s.produceListing(ctx, studioURL, opts, out, work)
	close(work)
	wg.Wait()
}

func (s *Scraper) produceListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- workItem) {
	// `run` already emitted the WarnDelayBelow once for this scraper.
	delay := opts.Delay

	if s.modelRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: detected model page", s.cfg.SiteID)
		s.scrapeListingPage(ctx, studioURL, opts, out, work)
		return
	}

	if m := catRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: detected category filter: %s", s.cfg.SiteID, m[1])
		s.paginateCategory(ctx, m[1], m[3], opts, out, work, delay)
		return
	}

	scraper.WarnURLFallthrough(s.cfg.SiteID, studioURL)
	s.paginateCategory(ctx, "movies", "d", opts, out, work, delay)
}

// dateSort is the ElevatedX listing suffix for newest-first. The template also
// offers `_n` (alphabetical) and `_p` (popularity), and an operator who pastes
// one of those URLs gets it honoured — but a KnownIDs early-stop only means
// anything under a date sort, so it is dropped for the others rather than
// truncating the catalogue at whichever stored scene happens to sort first.
const dateSort = "d"

func (s *Scraper) paginateCategory(ctx context.Context, category, sort string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- workItem, delay time.Duration) {
	if sort != dateSort {
		scraper.Debugf(1, "%s: %q sort is not date-ordered, walking the whole listing", s.cfg.SiteID, sort)
		opts.KnownIDs = nil
	}

	totalSent := false
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}

		if page > 1 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)
		pageURL := fmt.Sprintf("%s/%s/categories/%s_%d_%s.html", s.cfg.SiteBase, s.cfg.TourPrefix, category, page, sort)
		items, err := s.parseListingPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		if len(items) == 0 {
			return
		}

		if !totalSent {
			scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, 0)
			select {
			case out <- scraper.Progress(0):
			case <-ctx.Done():
				return
			}
			totalSent = true
		}

		for _, item := range items {
			if opts.KnownIDs[item.id] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- item:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (s *Scraper) scrapeListingPage(ctx context.Context, pageURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- workItem) {
	items, err := s.parseListingPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	if len(items) > 0 {
		select {
		case out <- scraper.Progress(len(items)):
		case <-ctx.Done():
			return
		}
	}

	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case work <- item:
		case <-ctx.Done():
			return
		}
	}
}

var (
	videoThumbRe = regexp.MustCompile(`videothumb_(\d+)`)
	titleRe      = regexp.MustCompile(`<h4><a\s+href="([^"]+)">([^<]+)</a></h4>`)
	performersRe = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	performerRe  = regexp.MustCompile(`>([^<]+)</a>`)
	thumbRe      = regexp.MustCompile(`data-src="([^"]+)"`)
	previewRe    = regexp.MustCompile(`src="([^"]*\.mp4[^"]*)"`)

	catRe = regexp.MustCompile(`/categories/([A-Za-z0-9_-]+)_(\d+)_([dnopuz])\.html`)
)

func (s *Scraper) parseListingPage(ctx context.Context, pageURL string) ([]workItem, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	html := string(body)

	blocks := SplitVideoBlocks(html)
	items := make([]workItem, 0, len(blocks))

	for _, block := range blocks {
		item := s.parseVideoBlock(block)
		if item.id == "" || item.url == "" {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}

func SplitVideoBlocks(page string) []string {
	ids := videoThumbRe.FindAllStringSubmatchIndex(page, -1)
	if len(ids) == 0 {
		return nil
	}

	var blocks []string
	for i, loc := range ids {
		start := loc[0]
		var end int
		if i+1 < len(ids) {
			end = ids[i+1][0]
		} else {
			end = len(page)
		}
		blocks = append(blocks, page[start:end])
	}
	return blocks
}

func (s *Scraper) parseVideoBlock(block string) workItem {
	var item workItem

	if m := videoThumbRe.FindStringSubmatch(block); m != nil {
		item.id = m[1]
	}

	if m := titleRe.FindStringSubmatch(block); m != nil {
		item.url = s.absoluteURL(m[1])
		item.title = html.UnescapeString(m[2])
	}

	if m := performersRe.FindStringSubmatch(block); m != nil {
		matches := performerRe.FindAllStringSubmatch(m[1], -1)
		for _, pm := range matches {
			item.performers = append(item.performers, strings.TrimSpace(pm[1]))
		}
	}

	if m := thumbRe.FindStringSubmatch(block); m != nil {
		item.thumb = m[1]
	}

	if m := previewRe.FindStringSubmatch(block); m != nil {
		item.preview = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	return item
}

var (
	detailDateRe     = regexp.MustCompile(`(?si)class="sceneDateP"><span>(\d{2}/\d{2}/\d{4}),</span>.*?(\d+)(?:\s|&nbsp;)*min`)
	detailDescRe     = regexp.MustCompile(`(?s)Description:</span>\s*(?:<h2[^>]*>)?\s*(.*?)\s*(?:</h2>|</p>)`)
	detailKeywordsRe = regexp.MustCompile(`<meta\s+name="keywords"\s+content="([^"]*)"`)
	detailIDRe       = regexp.MustCompile(`data-id="(\d+)"`)
)

func (s *Scraper) fetchDetail(ctx context.Context, item workItem, studioURL string, delay time.Duration) (models.Scene, error) {
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return models.Scene{}, ctx.Err()
	}

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     item.url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return models.Scene{}, fmt.Errorf("reading detail %s: %w", item.url, err)
	}
	page := string(body)

	var date time.Time
	var duration int
	if m := detailDateRe.FindStringSubmatch(page); m != nil {
		date, _ = time.Parse("01/02/2006", m[1])
		duration, _ = strconv.Atoi(m[2])
		duration *= 60
	}

	var description string
	if m := detailDescRe.FindStringSubmatch(page); m != nil && m[1] != "" {
		description = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	tags, series := s.extractTagsAndSeries(page)

	if item.id == "" {
		if m := detailIDRe.FindStringSubmatch(page); m != nil {
			item.id = m[1]
		}
	}

	now := time.Now().UTC()
	return models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Date:        date.UTC(),
		Description: description,
		Thumbnail:   item.thumb,
		Preview:     item.preview,
		Performers:  item.performers,
		Studio:      s.cfg.StudioName,
		Tags:        tags,
		Series:      series,
		Duration:    duration,
		ScrapedAt:   now,
	}, nil
}

func (s *Scraper) extractTagsAndSeries(page string) ([]string, string) {
	m := detailKeywordsRe.FindStringSubmatch(page)
	if m == nil {
		return nil, ""
	}

	skip := map[string]bool{
		"4K": true, "Updates": true, "Movies": true, "Photos": true,
		"Interactive Toys": true,
	}
	skip[s.cfg.StudioName] = true
	skip[s.cfg.Domain] = true
	domainUpper := strings.ToUpper(s.cfg.Domain[:1]) + s.cfg.Domain[1:]
	skip[domainUpper] = true
	if s.cfg.Domain != "newsensations.com" {
		skip["New Sensations"] = true
		skip["NewSensations.com"] = true
	}

	var tags []string
	var series string
	parts := strings.Split(m[1], ",")
	for _, p := range parts {
		tag := strings.TrimSpace(p)
		if tag == "" {
			continue
		}
		if strings.HasSuffix(tag, ".com") {
			continue
		}
		if skip[tag] {
			continue
		}
		tags = append(tags, tag)
	}

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.Contains(p, "#") {
			series = strings.TrimSpace(strings.Split(p, "#")[0])
			break
		}
	}

	return tags, series
}

func (s *Scraper) absoluteURL(href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	return s.cfg.SiteBase + "/" + strings.TrimPrefix(href, "/")
}

package cherrypimpsutil

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	ID       string
	SiteBase string
	Studio   string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardStartRe = regexp.MustCompile(`<div class="item-update[^"]*item-video[^"]*">`)
	sceneIDRe   = regexp.MustCompile(`/trailers/(\d+)-`)
	titleRe     = regexp.MustCompile(`(?s)<div class="item-title">\s*<a[^>]+title="([^"]+)"`)
	sceneURLRe  = regexp.MustCompile(`(?s)<div class="item-title">\s*<a\s+href="([^"]+)"`)
	thumbRe     = regexp.MustCompile(`<img[^>]+src="([^"]+)"[^>]+class="video_placeholder"`)
	dateRe      = regexp.MustCompile(`([A-Z][a-z]+ \d{1,2}, \d{4})`)
	durationRe  = regexp.MustCompile(`(\d+:\d{2})`)
	performerRe = regexp.MustCompile(`<a[^>]+href="[^"]*models/[^"]*">([^<]+)</a>`)
	maxPageRe   = regexp.MustCompile(`_(\d+)_d\.html`)

	dvdCardStartRe = regexp.MustCompile(`<div class="item-update[^"]*item-model[^"]*col">`)
	dvdLinkRe      = regexp.MustCompile(`<a\s+href="([^"]+/dvds/[^"]+\.html)"`)
	dvdPageRe      = regexp.MustCompile(`dvds_page_(\d+)\.html`)

	seriesSlugRe   = regexp.MustCompile(`/series/([^_/.]+)`)
	categorySlugRe = regexp.MustCompile(`/categories/([^_/.]+)`)
	modelSlugRe    = regexp.MustCompile(`/models/([^_/.]+?)(?:\.html)?$`)
	dvdSlugRe      = regexp.MustCompile(`/dvds/([^_/.]+?)(?:\.html)?$`)
)

const cardEnd = "<!--//item-update-->"

type sceneItem struct {
	id         string
	title      string
	url        string
	thumb      string
	date       time.Time
	duration   int
	performers []string
}

func parseListingPage(body []byte) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))

	for _, loc := range starts {
		rest := page[loc[0]:]
		endIdx := strings.Index(rest, cardEnd)
		if endIdx < 0 {
			continue
		}
		block := rest[:endIdx]

		var item sceneItem

		if sm := sceneIDRe.FindStringSubmatch(block); sm != nil {
			item.id = sm[1]
		}
		if item.id == "" {
			continue
		}

		if sm := titleRe.FindStringSubmatch(block); sm != nil {
			item.title = html.UnescapeString(strings.TrimSpace(sm[1]))
		}

		if sm := sceneURLRe.FindStringSubmatch(block); sm != nil {
			item.url = sm[1]
		}

		if sm := thumbRe.FindStringSubmatch(block); sm != nil {
			item.thumb = sm[1]
		}

		if idx := strings.Index(block, `class="item-date"`); idx >= 0 {
			dateSection := block[idx:]
			if end := strings.Index(dateSection, "</div>"); end > 0 {
				dateSection = dateSection[:end]
				if sm := durationRe.FindStringSubmatch(dateSection); sm != nil {
					item.duration = parseutil.ParseDurationColon(sm[1])
				}
				if sm := dateRe.FindStringSubmatch(dateSection); sm != nil {
					if t, err := time.Parse("January 2, 2006", sm[1]); err == nil {
						item.date = t.UTC()
					}
				}
			}
		}

		if idx := strings.Index(block, `class="item-models"`); idx >= 0 {
			perfSection := block[idx:]
			if end := strings.Index(perfSection, "</div>"); end > 0 {
				perfSection = perfSection[:end]
				for _, pm := range performerRe.FindAllStringSubmatch(perfSection, -1) {
					name := strings.TrimSpace(pm[1])
					if name != "" {
						item.performers = append(item.performers, name)
					}
				}
			}
		}

		items = append(items, item)
	}
	return items
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

type listingMode int

const (
	modeFullCatalog listingMode = iota
	modeSeries
	modeCategory
	modeModel
	modeDVD
	modeDVDListing
)

type listingConfig struct {
	mode listingMode
	slug string
}

func parseStudioURL(studioURL string) listingConfig {
	if m := modelSlugRe.FindStringSubmatch(studioURL); m != nil {
		return listingConfig{mode: modeModel, slug: m[1]}
	}
	if m := dvdSlugRe.FindStringSubmatch(studioURL); m != nil {
		if m[1] == "dvds" {
			return listingConfig{mode: modeDVDListing}
		}
		return listingConfig{mode: modeDVD, slug: m[1]}
	}
	if m := seriesSlugRe.FindStringSubmatch(studioURL); m != nil {
		return listingConfig{mode: modeSeries, slug: m[1]}
	}
	if m := categorySlugRe.FindStringSubmatch(studioURL); m != nil {
		slug := m[1]
		if slug != "movies" {
			return listingConfig{mode: modeCategory, slug: slug}
		}
	}
	return listingConfig{mode: modeFullCatalog}
}

func (lc listingConfig) pageURL(base string, page int) string {
	switch lc.mode {
	case modeSeries:
		return fmt.Sprintf("%s/series/%s_%d_d.html", base, lc.slug, page)
	case modeCategory:
		return fmt.Sprintf("%s/categories/%s_%d_d.html", base, lc.slug, page)
	default:
		return fmt.Sprintf("%s/categories/movies_%d_d.html", base, page)
	}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	lc := parseStudioURL(studioURL)
	now := time.Now().UTC()

	switch lc.mode {
	case modeModel:
		scraper.Debugf(1, "%s: detected model page", s.cfg.ID)
		s.scrapeSinglePage(ctx, studioURL, opts, out, now)
		return
	case modeDVD:
		scraper.Debugf(1, "%s: detected DVD page", s.cfg.ID)
		s.scrapeSinglePage(ctx, studioURL, opts, out, now)
		return
	case modeDVDListing:
		scraper.Debugf(1, "%s: detected DVD listing mode", s.cfg.ID)
		s.scrapeDVDListing(ctx, opts, out, now)
		return
	}

	s.scrapeListingPages(ctx, lc, opts, out, now)
}

func (s *Scraper) scrapeSinglePage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	pageURL := studioURL
	if !strings.HasPrefix(pageURL, "http") {
		pageURL = s.cfg.SiteBase + pageURL
	}

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	scenes := parseListingPage(body)
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: found %d scenes on single page", s.cfg.ID, len(scenes))

	select {
	case out <- scraper.Progress(len(scenes)):
	case <-ctx.Done():
		return
	}

	for _, item := range scenes {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(item.toScene(s.cfg.ID, s.cfg.SiteBase, s.cfg.Studio, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) scrapeListingPages(ctx context.Context, lc listingConfig, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := lc.pageURL(s.cfg.SiteBase, page)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)

		total := 0
		if page == 1 {
			total = estimateTotal(body, len(items))
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(s.cfg.ID, s.cfg.SiteBase, s.cfg.Studio, now)
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
		}, nil
	})
}

func parseDVDCards(body []byte) []string {
	page := string(body)
	starts := dvdCardStartRe.FindAllStringIndex(page, -1)
	seen := make(map[string]bool, len(starts))
	var urls []string
	for _, loc := range starts {
		rest := page[loc[0]:]
		endIdx := strings.Index(rest, cardEnd)
		if endIdx < 0 {
			continue
		}
		block := rest[:endIdx]
		if m := dvdLinkRe.FindStringSubmatch(block); m != nil {
			u := m[1]
			if !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		}
	}
	return urls
}

func estimateDVDTotal(body []byte) int {
	maxPage := 1
	for _, m := range dvdPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func (s *Scraper) collectDVDURLs(ctx context.Context, opts scraper.ListOpts) ([]string, error) {
	var allDVDs []string
	totalPages := 1

	for page := 1; page <= totalPages; page++ {
		scraper.Debugf(1, "%s: fetching DVD listing page %d", s.cfg.ID, page)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		var pageURL string
		if page == 1 {
			pageURL = s.cfg.SiteBase + "/dvds/dvds.html"
		} else {
			pageURL = fmt.Sprintf("%s/dvds/dvds_page_%d.html", s.cfg.SiteBase, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return nil, fmt.Errorf("dvd listing page %d: %w", page, err)
		}

		dvds := parseDVDCards(body)
		if len(dvds) == 0 {
			break
		}
		allDVDs = append(allDVDs, dvds...)

		if page == 1 {
			totalPages = estimateDVDTotal(body)
		}
	}
	return allDVDs, nil
}

func (s *Scraper) scrapeDVDListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	allDVDs, err := s.collectDVDURLs(ctx, opts)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	if len(allDVDs) == 0 {
		return
	}

	scraper.Debugf(1, "%s: fetching %d DVD detail pages", s.cfg.ID, len(allDVDs))

	// The DVD list is ordered by release, but its *scenes* are not: a
	// compilation reissues scenes that shipped on earlier discs, so the first
	// stored scene can turn up on the very first DVD. Paginate's early stop
	// would end the walk there and every later DVD would stay undiscovered, so
	// the hint is dropped and known scenes are skipped instead. The cost is
	// that an incremental DVD run always walks the whole list.
	walkOpts := opts
	walkOpts.KnownIDs = nil
	seen := make(map[string]bool)
	skipped := 0

	scraper.Paginate(ctx, walkOpts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		idx := page - 1
		if idx >= len(allDVDs) {
			return scraper.PageResult{}, nil
		}

		body, err := s.fetchPage(ctx, allDVDs[idx])
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("dvd %s: %w", allDVDs[idx], err)
		}

		items := parseListingPage(body)

		total := 0
		if page == 1 {
			total = len(items) * len(allDVDs)
		}

		scenes := make([]models.Scene, 0, len(items))
		for _, item := range items {
			if seen[item.id] {
				continue
			}
			if opts.KnownIDs[item.id] {
				seen[item.id] = true
				skipped++
				continue
			}
			seen[item.id] = true
			scenes = append(scenes, item.toScene(s.cfg.ID, s.cfg.SiteBase, s.cfg.Studio, now))
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			// Fixed walk over the DVD list: an empty, photo-only or
			// wholly-known DVD must not abort the walk — only exhausting the
			// list (Done) ends it.
			Continue: true,
			Done:     idx >= len(allDVDs)-1,
		}, nil
	})

	if skipped > 0 {
		scraper.Debugf(1, "%s: skipped %d already-stored DVD scene(s)", s.cfg.ID, skipped)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

func (item sceneItem) toScene(siteID, siteBase, studio string, now time.Time) models.Scene {
	url := item.url
	if strings.HasPrefix(url, "/") {
		url = siteBase + url
	}
	return models.Scene{
		ID:         item.id,
		SiteID:     siteID,
		StudioURL:  siteBase,
		Title:      item.title,
		URL:        url,
		Thumbnail:  item.thumb,
		Date:       item.date,
		Duration:   item.duration,
		Performers: item.performers,
		Studio:     studio,
		ScrapedAt:  now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

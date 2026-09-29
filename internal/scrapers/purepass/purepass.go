package purepass

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
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	ID       string
	SiteBase string
	SiteName string
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

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	if strings.Contains(studioURL, "/models/") {
		s.scrapeModelPage(ctx, studioURL, opts, out, now)
	} else {
		s.scrapeListingPages(ctx, studioURL, opts, out, now)
	}
}

var catSlugRe = regexp.MustCompile(`/categories/([^_/]+)_\d+_d\.html`)

func extractSlug(studioURL string) string {
	if m := catSlugRe.FindStringSubmatch(studioURL); m != nil {
		return m[1]
	}
	return "movies"
}

func (s *Scraper) scrapeListingPages(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	slug := extractSlug(studioURL)
	reported := 0

	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/%s_%d_d.html", s.cfg.SiteBase, slug, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseListingPage(body, s.cfg.SiteBase)
		// The pager is windowed, so page 1 links only as far as its window
		// reaches and its highest page number is a floor, not the last page.
		// Re-reading it on every page raises the estimate as the walk goes, and
		// a later KindTotal replaces the earlier one at the consumer, so
		// progress corrects itself instead of staying wrong.
		var total int
		if seen := estimateTotal(body, len(items)); seen > reported {
			reported = seen
			total = seen
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(s.cfg, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	scenes := parseModelPage(body, s.cfg.SiteBase)
	if len(scenes) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(scenes)):
	case <-ctx.Done():
		return
	}

	for _, item := range scenes {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.ID)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(item.toScene(s.cfg, studioURL, now)):
		case <-ctx.Done():
			return
		}
	}
}

type sceneItem struct {
	id          string
	title       string
	performers  []string
	description string
	tags        []string
	thumb       string
	date        time.Time
	duration    int
}

func (item sceneItem) toScene(cfg SiteConfig, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:          item.id,
		SiteID:      cfg.ID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         cfg.SiteBase + "/#" + item.id,
		Date:        item.date,
		Performers:  item.performers,
		Description: item.description,
		Tags:        item.tags,
		Thumbnail:   item.thumb,
		Duration:    item.duration,
		Studio:      cfg.SiteName,
		ScrapedAt:   now,
	}
}

var (
	blockRe     = regexp.MustCompile(`data-setid="(\d+)"`)
	titleRe     = regexp.MustCompile(`(?s)<a[^>]*(?:href="/join/"|onclick="tload\([^)]*\)")[^>]*>\s*([^<]+?)\s*</a>`)
	performerRe = regexp.MustCompile(`href="[^"]*models/[^"]*">([^<]+)</a>`)
	dateValRe   = regexp.MustCompile(`([A-Z][a-z]+ \d{1,2}, \d{4})`)
	durationRe  = regexp.MustCompile(`(\d+)\s*(?:&nbsp;)*\s*minute`)
	thumbRe     = regexp.MustCompile(`class="update_thumb[^"]*"[^>]*src="([^"]+)"`)
	maxPageRe   = regexp.MustCompile(`_(\d+)_d\.html`)
)

func parseListingPage(body []byte, base string) []sceneItem {
	page := string(body)
	locs := blockRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]sceneItem, 0, len(locs))

	for i, loc := range locs {
		id := page[loc[2]:loc[3]]
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		item := sceneItem{id: id}

		if m := titleRe.FindStringSubmatch(block); m != nil {
			item.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		for _, m := range performerRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name != "" {
				item.performers = append(item.performers, name)
			}
		}

		if m := dateValRe.FindStringSubmatch(block); m != nil {
			if t, err := time.Parse("January 2, 2006", m[1]); err == nil {
				item.date = t.UTC()
			}
		}

		if m := durationRe.FindStringSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(m[1])
			item.duration = mins * 60
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			thumb := m[1]
			if strings.HasPrefix(thumb, "/") {
				thumb = base + thumb
			}
			item.thumb = thumb
		}

		if item.title == "" {
			continue
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

var (
	modelBlockRe = regexp.MustCompile(`class="update_block"`)
	modelIDRe    = regexp.MustCompile(`id="set-target-(\d+)-`)
	modelTitleRe = regexp.MustCompile(`(?s)class="update_title"[^>]*>(.*?)</span>`)
	modelDateRe  = regexp.MustCompile(`(?s)class="update_date"[^>]*>\s*(.*?)\s*</span>`)
	modelDescRe  = regexp.MustCompile(`(?s)class="latest_update_description"[^>]*>(.*?)</span>`)
	modelTagsRe  = regexp.MustCompile(`(?s)class="tour_update_tags"[^>]*>(.*?)</span>`)
	tagLinkRe    = regexp.MustCompile(`>([^<]+)</a>`)
	modelThumbRe = regexp.MustCompile(`class="large_update_thumb[^"]*"[^>]*src="([^"]+)"`)
)

func parseModelPage(body []byte, base string) []sceneItem {
	page := string(body)
	locs := modelBlockRe.FindAllStringIndex(page, -1)
	var items []sceneItem

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		var item sceneItem

		if m := modelIDRe.FindStringSubmatch(block); m != nil {
			item.id = m[1]
		}
		if item.id == "" {
			continue
		}

		if m := modelTitleRe.FindStringSubmatch(block); m != nil {
			item.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		for _, m := range performerRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name != "" {
				item.performers = append(item.performers, name)
			}
		}

		if m := modelDateRe.FindStringSubmatch(block); m != nil {
			raw := strings.TrimSpace(m[1])
			if t, err := time.Parse("January 2, 2006", raw); err == nil {
				item.date = t.UTC()
			}
		}

		if m := modelDescRe.FindStringSubmatch(block); m != nil {
			item.description = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := modelTagsRe.FindStringSubmatch(block); m != nil {
			for _, tm := range tagLinkRe.FindAllStringSubmatch(m[1], -1) {
				tag := strings.TrimSpace(html.UnescapeString(tm[1]))
				if tag != "" {
					item.tags = append(item.tags, tag)
				}
			}
		}

		if m := modelThumbRe.FindStringSubmatch(block); m != nil {
			thumb := m[1]
			if strings.HasPrefix(thumb, "/") {
				thumb = base + thumb
			}
			item.thumb = thumb
		}

		if m := durationRe.FindStringSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(m[1])
			item.duration = mins * 60
		}

		items = append(items, item)
	}
	return items
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

package venusav

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "venusav" }

func (s *Scraper) Patterns() []string {
	return []string{
		"venus-av.com",
		"venus-av.com/all",
		"venus-av.com/new-release",
	}
}

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?venus-av\.com(?:/?$|/all(?:/|$)|/new-release(?:/|$))`,
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

type listingItem struct {
	code      string
	path      string
	title     string
	performer string
	date      time.Time
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingItem)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				detailURL := buildDetailURL(studioURL, item.path)
				scene, err := s.fetchDetail(ctx, studioURL, item, detailURL, opts.Delay)
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

	go func() {
		defer close(work)

		listURL := resolveListingURL(studioURL)
		seen := map[string]bool{}

		for page := 1; ; page++ {
			if page > 1 && !scraper.Pace(ctx, opts.Delay) {
				return
			}
			scraper.Debugf(1, "venusav: fetching page %d", page)

			pageURL := buildPageURL(listURL, page)
			body, err := s.fetchPage(ctx, pageURL)
			if err != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
				case <-ctx.Done():
				}
				return
			}

			items := parseListingItems(body)
			if len(items) == 0 {
				return
			}

			if page == 1 {
				total := extractTotal(body)
				if total <= 0 {
					total = len(items)
				}
				scraper.Debugf(1, "venusav: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}

			newItems := 0
			for _, item := range items {
				if opts.KnownIDs[item.code] {
					scraper.Debugf(1, "venusav: hit known ID, stopping early")
					select {
					case out <- scraper.StoppedEarly():
					case <-ctx.Done():
					}
					return
				}
				if seen[item.code] {
					continue
				}
				seen[item.code] = true
				newItems++
				select {
				case work <- item:
				case <-ctx.Done():
					return
				}
			}
			if newItems == 0 || !hasNextPage(body) {
				return
			}
		}
	}()

	wg.Wait()
}

// ---- URL helpers ----

func resolveListingURL(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return studioURL
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "/new-release" {
		u.Path = "/new-release/"
		u.RawQuery = ""
		return u.String()
	}
	u.Path = "/all/"
	u.RawQuery = ""
	return u.String()
}

func buildPageURL(baseURL string, page int) string {
	if page <= 1 {
		return baseURL
	}
	base := strings.TrimRight(baseURL, "/")
	return base + "/page/" + strconv.Itoa(page) + "/"
}

func buildDetailURL(studioURL, path string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return path
	}
	return u.Scheme + "://" + u.Host + path
}

// ---- listing page parsing ----

var (
	liBlockRe       = regexp.MustCompile(`(?s)<li>(.*?)</li>`)
	codeFromImgRe   = regexp.MustCompile(`/uploads/old/([^"/.]+)\.jpg`)
	detailPathRe    = regexp.MustCompile(`href="[^"]*?(/products/[^"]+)"`)
	titleListRe     = regexp.MustCompile(`(?s)topNewreleaseListTtl">\s*(.*?)\s*</p>`)
	performerListRe = regexp.MustCompile(`(?s)topNewreleaseListName">\s*<div>\s*(.*?)\s*</div>`)
	dateListStrRe   = regexp.MustCompile(`(?s)発売日[：:]\s*(.*?)\s*</p>`)
)

func parseListingItems(body []byte) []listingItem {
	blocks := liBlockRe.FindAll(body, -1)
	seen := map[string]bool{}
	var items []listingItem
	for _, block := range blocks {
		m := codeFromImgRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		code := strings.ToUpper(string(m[1]))
		if seen[code] {
			continue
		}
		seen[code] = true

		item := listingItem{code: code}

		if pm := detailPathRe.FindSubmatch(block); pm != nil {
			item.path = string(pm[1])
		}
		if tm := titleListRe.FindSubmatch(block); tm != nil {
			item.title = html.UnescapeString(strings.TrimSpace(string(tm[1])))
		}
		if pm := performerListRe.FindSubmatch(block); pm != nil {
			item.performer = html.UnescapeString(strings.TrimSpace(string(pm[1])))
		}
		if dm := dateListStrRe.FindSubmatch(block); dm != nil {
			item.date = parseDate(strings.TrimSpace(string(dm[1])))
		}

		items = append(items, item)
	}
	return items
}

var dateParseRe = regexp.MustCompile(`(\d{4})[年月]\s*(\d{1,2})月\s*(\d{1,2})日`)

func parseDate(s string) time.Time {
	m := dateParseRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
}

var (
	lastPageRe = regexp.MustCompile(`class="last"[^>]*href="[^"]*?/page/(\d+)/?"`)
	nextPageRe = regexp.MustCompile(`class="nextpostslink"`)
)

func extractTotal(body []byte) int {
	m := lastPageRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	lastPage, _ := strconv.Atoi(string(m[1]))
	return lastPage * 20
}

func hasNextPage(body []byte) bool {
	return nextPageRe.Match(body)
}

// ---- detail page parsing ----

var (
	titleH1Re = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	descDLRe  = regexp.MustCompile(`(?s)<dt>作品紹介</dt>\s*<dd>(.*?)</dd>`)
	perfDLRe  = regexp.MustCompile(`(?s)<dt>出演女優</dt>\s*<dd>(.*?)</dd>`)
	labelDLRe = regexp.MustCompile(`(?s)<dt>レーベル</dt>\s*<dd>(.*?)</dd>`)
	dateDLRe  = regexp.MustCompile(`(?s)<dt>発売日</dt>\s*<dd>(.*?)</dd>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, item listingItem, detailURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	body, err := s.fetchPage(ctx, detailURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.code, err)
	}

	return parseDetail(body, studioURL, item, detailURL), nil
}

func parseDetail(body []byte, studioURL string, item listingItem, detailURL string) models.Scene {
	scene := models.Scene{
		ID:        item.code,
		SiteID:    "venusav",
		StudioURL: studioURL,
		URL:       detailURL,
		Studio:    "VENUS",
		Title:     item.title,
		Date:      item.date,
		ScrapedAt: time.Now().UTC(),
	}

	if m := titleH1Re.FindSubmatch(body); m != nil {
		t := html.UnescapeString(strings.TrimSpace(string(m[1])))
		if t != "" {
			scene.Title = t
		}
	}

	if u, err := url.Parse(studioURL); err == nil {
		scene.Thumbnail = u.Scheme + "://" + u.Host + "/wp-content/uploads/old/" + item.code + ".jpg"
	}

	if m := descDLRe.FindSubmatch(body); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := perfDLRe.FindSubmatch(body); m != nil {
		name := html.UnescapeString(strings.TrimSpace(string(m[1])))
		if name != "" {
			scene.Performers = []string{name}
		}
	}
	if len(scene.Performers) == 0 && item.performer != "" {
		scene.Performers = []string{item.performer}
	}

	if m := dateDLRe.FindSubmatch(body); m != nil {
		d := parseDate(strings.TrimSpace(string(m[1])))
		if !d.IsZero() {
			scene.Date = d
		}
	}

	if m := labelDLRe.FindSubmatch(body); m != nil {
		label := html.UnescapeString(strings.TrimSpace(string(m[1])))
		if label != "" {
			scene.Series = label
		}
	}

	return scene
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

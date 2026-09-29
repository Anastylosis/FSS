package takaratv

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

func (s *Scraper) ID() string { return "takaratv" }

func (s *Scraper) Patterns() []string {
	return []string{
		"takara-tv.jp",
		"takara-tv.jp/search.php?ac={id}",
		"takara-tv.jp/search.php?lb={id}",
	}
}

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?takara-tv\.jp` +
		`(?:/?$` +
		`|/top_index\.php` +
		`|/catalog\.php` +
		`|/search\.php` +
		`|/product_search\.php` +
		`)`,
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

type listingItem struct {
	code  string
	title string
	date  time.Time
	thumb string
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
				detailURL := buildDetailURL(studioURL, item.code)
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
			scraper.Debugf(1, "takaratv: fetching page %d", page)

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
				scraper.Debugf(1, "takaratv: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}

			newItems := 0
			for _, item := range items {
				if opts.KnownIDs[item.code] {
					scraper.Debugf(1, "takaratv: hit known ID, stopping early")
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

	if strings.HasSuffix(u.Path, "/search.php") {
		q := u.Query()
		q.Del("p")
		u.RawQuery = q.Encode()
		return u.String()
	}

	u.Path = "/search.php"
	u.RawQuery = "search_flag=top"
	return u.String()
}

func buildPageURL(baseURL string, page int) string {
	if page <= 1 {
		return baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	q := u.Query()
	q.Set("p", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String()
}

func buildDetailURL(studioURL, code string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "https://www.takara-tv.jp/dvd_detail.php?code=" + code
	}
	return u.Scheme + "://" + u.Host + "/dvd_detail.php?code=" + code
}

// ---- listing page parsing ----

var listItemRe = regexp.MustCompile(
	`<!--発売日:(\d{4}-\d{2}-\d{2})-->\s*<li><a href="[^"]*\?code=([^"&]+)"><img src="([^"]*)"\s+alt="([^"]*)"`,
)

func parseListingItems(body []byte) []listingItem {
	matches := listItemRe.FindAllSubmatchIndex(body, -1)
	seen := map[string]bool{}
	var items []listingItem
	for _, loc := range matches {
		code := string(body[loc[4]:loc[5]])
		if seen[code] {
			continue
		}
		seen[code] = true

		dateStr := string(body[loc[2]:loc[3]])
		date, _ := time.Parse("2006-01-02", dateStr)

		thumb := string(body[loc[6]:loc[7]])
		if strings.Contains(thumb, "no_image") {
			thumb = ""
		}

		title := html.UnescapeString(string(body[loc[8]:loc[9]]))

		items = append(items, listingItem{
			code:  code,
			title: title,
			date:  date,
			thumb: thumb,
		})
	}
	return items
}

var totalRe = regexp.MustCompile(`(\d+)件中`)

func extractTotal(body []byte) int {
	m := totalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

var nextPageRe = regexp.MustCompile(`title="next page"`)

func hasNextPage(body []byte) bool {
	return nextPageRe.Match(body)
}

// ---- detail page parsing ----

var (
	titleFieldRe    = regexp.MustCompile(`>タイトル</th>\s*<td[^>]*>(.*?)</td>`)
	modelFieldRe    = regexp.MustCompile(`>モデル名</th>\s*<td[^>]*>(.*?)</td>`)
	directorFieldRe = regexp.MustCompile(`>監督名</th>\s*<td[^>]*>(.*?)</td>`)
	dateFieldRe     = regexp.MustCompile(`>発売日</th>\s*<td[^>]*>\s*(\d{4})年(\d{1,2})月(\d{1,2})日`)
	durationFieldRe = regexp.MustCompile(`>収録時間</th>\s*<td[^>]*>\s*(\d+)分`)
	descFieldRe     = regexp.MustCompile(`(?s)作品紹介</th></tr>\s*<tr><th[^>]*>(.*?)</th></tr>`)
)

const fullWidthSpace = "　"

func extractPerformer(title string) string {
	idx := strings.LastIndex(title, fullWidthSpace)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(title[idx+len(fullWidthSpace):])
}

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
		SiteID:    "takaratv",
		StudioURL: studioURL,
		URL:       detailURL,
		Studio:    "Takara",
		Title:     item.title,
		Date:      item.date,
		ScrapedAt: time.Now().UTC(),
	}

	if m := titleFieldRe.FindSubmatch(body); m != nil {
		title := html.UnescapeString(strings.TrimSpace(string(m[1])))
		if title != "" {
			scene.Title = title
		}
	}

	if u, err := url.Parse(studioURL); err == nil {
		scene.Thumbnail = u.Scheme + "://" + u.Host + "/product/l/" + strings.ToLower(item.code) + ".jpg"
	}

	if m := dateFieldRe.FindSubmatch(body); m != nil {
		y, _ := strconv.Atoi(string(m[1]))
		mo, _ := strconv.Atoi(string(m[2]))
		d, _ := strconv.Atoi(string(m[3]))
		scene.Date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	}

	if m := durationFieldRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		scene.Duration = mins * 60
	}

	if m := descFieldRe.FindSubmatch(body); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := modelFieldRe.FindSubmatch(body); m != nil {
		name := html.UnescapeString(strings.TrimSpace(string(m[1])))
		if name != "" {
			scene.Performers = []string{name}
		}
	}
	if len(scene.Performers) == 0 {
		if p := extractPerformer(scene.Title); p != "" {
			scene.Performers = []string{p}
		}
	}

	if m := directorFieldRe.FindSubmatch(body); m != nil {
		scene.Director = html.UnescapeString(strings.TrimSpace(string(m[1])))
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

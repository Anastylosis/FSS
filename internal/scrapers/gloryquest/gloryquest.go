package gloryquest

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

func (s *Scraper) ID() string { return "gloryquest" }

func (s *Scraper) Patterns() []string {
	return []string{
		"gloryquest.tv",
		"gloryquest.tv/search.php?KeyWord={keyword}",
	}
}

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?gloryquest\.tv(?:/?$|/search\.php(?:\?|$))`,
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

type listingItem struct {
	code string
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)

		listURL := resolveListingURL(studioURL)
		body, err := s.fetchPage(ctx, listURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("listing: %w", err)):
			case <-ctx.Done():
			}
			return
		}

		items := parseListingItems(body)
		if len(items) == 0 {
			return
		}

		total := extractTotal(body)
		if total <= 0 {
			total = len(items)
		}
		scraper.Debugf(1, "gloryquest: %d total scenes", total)
		select {
		case out <- scraper.Progress(total):
		case <-ctx.Done():
			return
		}

		seen := map[string]bool{}
		for _, item := range items {
			if opts.KnownIDs[item.code] {
				scraper.Debugf(1, "gloryquest: hit known ID, stopping early")
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
			select {
			case work <- item:
			case <-ctx.Done():
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
	if path == "" || path == "/" {
		u.Path = "/search.php"
		u.RawQuery = "KeyWord="
		return u.String()
	}
	return studioURL
}

func buildDetailURL(studioURL, code string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "https://www.gloryquest.tv/item.php?id=" + code
	}
	return u.Scheme + "://" + u.Host + "/item.php?id=" + code
}

// ---- listing page parsing ----

var itemCodeRe = regexp.MustCompile(`href="item\.php\?id=([A-Z]+-\d+)"`)

func parseListingItems(body []byte) []listingItem {
	matches := itemCodeRe.FindAllSubmatch(body, -1)
	seen := map[string]bool{}
	var items []listingItem
	for _, m := range matches {
		code := string(m[1])
		if seen[code] {
			continue
		}
		seen[code] = true
		items = append(items, listingItem{code: code})
	}
	return items
}

var totalRe = regexp.MustCompile(`(\d+)件該当`)

func extractTotal(body []byte) int {
	m := totalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// ---- detail page parsing ----

var (
	titleH1Re  = regexp.MustCompile(`(?s)<h1[^>]*itemprop="name"[^>]*>(.*?)</h1>`)
	descRe     = regexp.MustCompile(`(?s)<p class="long_comment"[^>]*>(.*?)</p>`)
	coverImgRe = regexp.MustCompile(`<img src="(/package/800/[^"]+)"`)
	dateRe     = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	durationRe = regexp.MustCompile(`収録時間</span></dt>\s*<dd[^>]*>\s*(\d+)分`)
	priceRe    = regexp.MustCompile(`定価</span></dt>\s*<dd[^>]*>\s*([\d,]+)円`)
	castPRe    = regexp.MustCompile(`(?s)出演</span>\s*(.*?)</p>`)
	seriesDDRe = regexp.MustCompile(`(?s)シリーズ</span></dt>\s*<dd[^>]*>(.*?)</dd>`)
	genreDDRe  = regexp.MustCompile(`(?s)ジャンル</span></dt>\s*<dd[^>]*>(.*?)</dd>`)
	dirDDRe    = regexp.MustCompile(`(?s)監督</span></dt>\s*<dd[^>]*>(.*?)</dd>`)
	linkTextRe = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
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
		SiteID:    "gloryquest",
		StudioURL: studioURL,
		URL:       detailURL,
		Studio:    "Glory Quest",
		ScrapedAt: time.Now().UTC(),
	}

	if m := titleH1Re.FindSubmatch(body); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := coverImgRe.FindSubmatch(body); m != nil {
		cover := string(m[1])
		if u, err := url.Parse(detailURL); err == nil {
			cover = u.Scheme + "://" + u.Host + cover
		}
		scene.Thumbnail = cover
	}

	if m := descRe.FindSubmatch(body); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := dateRe.FindSubmatch(body); m != nil {
		y, _ := strconv.Atoi(string(m[1]))
		mo, _ := strconv.Atoi(string(m[2]))
		d, _ := strconv.Atoi(string(m[3]))
		scene.Date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	}

	if m := durationRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		scene.Duration = mins * 60
	}

	scene.Performers = extractLinkTexts(castPRe, body)
	if dirs := extractLinkTexts(dirDDRe, body); len(dirs) > 0 {
		scene.Director = dirs[0]
	}
	if series := extractLinkTexts(seriesDDRe, body); len(series) > 0 {
		scene.Series = series[0]
	}
	scene.Tags = extractLinkTexts(genreDDRe, body)

	if m := priceRe.FindSubmatch(body); m != nil {
		priceStr := strings.ReplaceAll(string(m[1]), ",", "")
		price, _ := strconv.Atoi(priceStr)
		if price > 0 {
			scene.AddPrice(models.PriceSnapshot{
				Date:    time.Now().UTC(),
				Regular: float64(price),
			})
		}
	}

	return scene
}

func extractLinkTexts(fieldRe *regexp.Regexp, body []byte) []string {
	m := fieldRe.FindSubmatch(body)
	if m == nil {
		return nil
	}
	var names []string
	for _, lm := range linkTextRe.FindAllSubmatch(m[1], -1) {
		name := strings.TrimSpace(html.UnescapeString(string(lm[1])))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
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

package kmproduce

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

func (s *Scraper) ID() string { return "kmproduce" }

func (s *Scraper) Patterns() []string {
	return []string{
		"km-produce.com",
		"km-produce.com/works-vr",
		"km-produce.com/works-sell",
		"km-produce.com/works/tag/{slug}",
		"km-produce.com/works/category/{name}",
		"km-produce.com/{actress_slug}",
	}
}

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?km-produce\.com` +
		`(?:/?$` +
		`|/works(?:-(?:vr|sell))?(?:/page/\d+)?/?$` +
		`|/works/(?:tag|category)/` +
		`|/label(?:\?|$)` +
		`|/[a-z_][a-z0-9_]*/?$` +
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)

		listURLs, paginated := resolveListingURLs(studioURL)
		seen := map[string]bool{}
		progressSent := false

		for _, listURL := range listURLs {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if paginated {
				s.paginateList(ctx, listURL, opts, out, work, seen, &progressSent)
			} else {
				s.singlePageList(ctx, listURL, opts, out, work, seen, &progressSent)
			}
		}
	}()

	wg.Wait()
}

func (s *Scraper) paginateList(ctx context.Context, listURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingItem, seen map[string]bool, progressSent *bool) {
	for page := 1; ; page++ {
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "kmproduce: fetching page %d", page)

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

		if !*progressSent {
			total := extractTotal(body)
			if total <= 0 {
				total = len(items)
			}
			scraper.Debugf(1, "kmproduce: %d total scenes", total)
			select {
			case out <- scraper.Progress(total):
			case <-ctx.Done():
				return
			}
			*progressSent = true
		}

		newItems := 0
		for _, item := range items {
			if opts.KnownIDs[item.code] {
				scraper.Debugf(1, "kmproduce: hit known ID, stopping early")
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
}

func (s *Scraper) singlePageList(ctx context.Context, pageURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingItem, seen map[string]bool, progressSent *bool) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := parseListingItems(body)
	if len(items) == 0 {
		return
	}

	if !*progressSent {
		select {
		case out <- scraper.Progress(len(items)):
		case <-ctx.Done():
			return
		}
		*progressSent = true
	}

	for _, item := range items {
		if seen[item.code] {
			continue
		}
		seen[item.code] = true
		if opts.KnownIDs[item.code] {
			scraper.Debugf(1, "kmproduce: hit known ID, stopping early")
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

// ---- URL helpers ----

var pagePathRe = regexp.MustCompile(`/page/\d+/?$`)

func normalizeListURL(rawURL string) string {
	base, query := splitQuery(rawURL)
	base = pagePathRe.ReplaceAllString(base, "/")
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + query
}

func buildPageURL(baseURL string, page int) string {
	if page == 1 {
		return baseURL
	}
	base, query := splitQuery(baseURL)
	base = strings.TrimRight(base, "/")
	return base + "/page/" + strconv.Itoa(page) + "/" + query
}

func splitQuery(rawURL string) (base, query string) {
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		return rawURL[:idx], rawURL[idx:]
	}
	return rawURL, ""
}

func buildDetailURL(studioURL, code string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "https://www.km-produce.com/works/" + code
	}
	return u.Scheme + "://" + u.Host + "/works/" + code
}

func resolveListingURLs(studioURL string) (urls []string, paginated bool) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return []string{studioURL}, true
	}
	path := strings.TrimRight(u.Path, "/")
	host := u.Scheme + "://" + u.Host

	switch {
	case path == "" || path == "/works":
		return []string{host + "/works-vr/", host + "/works-sell/"}, true
	case path == "/works-vr" || path == "/works-sell":
		return []string{normalizeListURL(studioURL)}, true
	case strings.HasPrefix(path, "/works/tag/") ||
		strings.HasPrefix(path, "/works/category/"):
		return []string{normalizeListURL(studioURL)}, true
	case path == "/label":
		return []string{normalizeListURL(studioURL)}, true
	default:
		return []string{studioURL}, false
	}
}

// ---- listing page parsing ----

var cardImgRe = regexp.MustCompile(`src="/img/title0/([^"/.]+)\.jpg"`)

func parseListingItems(body []byte) []listingItem {
	matches := cardImgRe.FindAllSubmatchIndex(body, -1)
	seen := map[string]bool{}
	var items []listingItem
	for _, loc := range matches {
		code := string(body[loc[2]:loc[3]])
		if seen[code] {
			continue
		}
		seen[code] = true
		items = append(items, listingItem{
			code:  code,
			thumb: "/img/title0/" + code + ".jpg",
		})
	}
	return items
}

var totalRe = regexp.MustCompile(`全\s+(\d+)\s+タイトル`)

func extractTotal(body []byte) int {
	m := totalRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

var nextPageRe = regexp.MustCompile(`class="next"`)

func hasNextPage(body []byte) bool {
	return nextPageRe.Match(body)
}

// ---- detail page parsing ----

var (
	titleH1Re    = regexp.MustCompile(`<h1>([^<]+)</h1>`)
	coverRe      = regexp.MustCompile(`id="fulljk"[^>]*>\s*<a[^>]*href="([^"]+)"`)
	descRe       = regexp.MustCompile(`(?s)class="intro">(.*?)</p>`)
	dateRe       = regexp.MustCompile(`発売日</dt>\s*<dd>\s*(\d{4})/(\d{1,2})/(\d{1,2})`)
	durationRe   = regexp.MustCompile(`収録時間</dt>\s*<dd>\s*(\d+)分`)
	priceRe      = regexp.MustCompile(`定価</dt>\s*<dd>\s*(\d+)円`)
	actressDDRe  = regexp.MustCompile(`(?s)出演女優</dt>\s*<dd[^>]*>(.*?)</dd>`)
	directorDDRe = regexp.MustCompile(`(?s)監督</dt>\s*<dd[^>]*>(.*?)</dd>`)
	genreDDRe    = regexp.MustCompile(`(?s)ジャンル</dt>\s*<dd[^>]*>(.*?)</dd>`)
	linkTextRe   = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
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
		SiteID:    "kmproduce",
		StudioURL: studioURL,
		URL:       detailURL,
		Studio:    "KM Produce",
		ScrapedAt: time.Now().UTC(),
	}

	if m := titleH1Re.FindSubmatch(body); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := coverRe.FindSubmatch(body); m != nil {
		cover := string(m[1])
		if !strings.HasPrefix(cover, "http") {
			if u, err := url.Parse(detailURL); err == nil {
				cover = u.Scheme + "://" + u.Host + cover
			}
		}
		scene.Thumbnail = cover
	} else if item.thumb != "" {
		if u, err := url.Parse(detailURL); err == nil {
			scene.Thumbnail = u.Scheme + "://" + u.Host + item.thumb
		}
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

	scene.Performers = extractLinkTexts(actressDDRe, body)
	if dirs := extractLinkTexts(directorDDRe, body); len(dirs) > 0 {
		scene.Director = dirs[0]
	}
	scene.Tags = extractLinkTexts(genreDDRe, body)

	if m := priceRe.FindSubmatch(body); m != nil {
		price, _ := strconv.Atoi(string(m[1]))
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

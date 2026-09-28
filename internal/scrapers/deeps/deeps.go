package deeps

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

func (s *Scraper) ID() string { return "deeps" }

func (s *Scraper) Patterns() []string {
	return []string{
		"deeps.net/item",
		"deeps.net/item/index.php?w_{actress}",
		"deeps.net/item/index.php?d_{director}",
		"deeps.net/item/index.php?s_{series}",
		"deeps.net/item/index.php?c_{category}",
	}
}

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?deeps\.net` +
		`(?:/?$` +
		`|/item/?(?:\?.*)?$` +
		`|/item/index\.php(?:\?.*)?$` +
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

	go func() {
		defer close(work)

		seen := map[string]bool{}

		for page := 1; ; page++ {
			if page > 1 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			scraper.Debugf(1, "deeps: fetching page %d", page)

			pageURL := buildPageURL(studioURL, page)
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
				scraper.Debugf(1, "deeps: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}

			// The listing is date-descending but releases come in same-date
			// batches — five titles shared 2026.9.15 on page one — ordered by
			// product code within the batch. A title added to a batch already
			// half-stored therefore sits *behind* a known sibling, so the stop
			// waits until the page has been scanned in full rather than firing
			// on the first known code.
			newItems, hitKnown := 0, false
			for _, item := range items {
				id := strings.ToUpper(item.code)
				if opts.KnownIDs[id] {
					hitKnown = true
					continue
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				newItems++
				select {
				case work <- item:
				case <-ctx.Done():
					return
				}
			}
			if hitKnown {
				scraper.Debugf(1, "deeps: page %d reached stored scenes, stopping", page)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			if newItems == 0 || maxNavPage(body) <= page {
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
	if path == "" {
		u.Path = "/item/"
		u.RawQuery = ""
		return u.String()
	}
	return studioURL
}

func extractFilter(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil || u.RawQuery == "" {
		return "0_"
	}
	return u.RawQuery
}

func buildPageURL(studioURL string, page int) string {
	if page <= 1 {
		return resolveListingURL(studioURL)
	}
	u, err := url.Parse(resolveListingURL(studioURL))
	if err != nil {
		return studioURL
	}
	return u.Scheme + "://" + u.Host + "/item/index.php?" + extractFilter(studioURL) + "_50_" + strconv.Itoa(page)
}

func buildDetailURL(studioURL, code string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "https://deeps.net/item/detail.php?" + code
	}
	return u.Scheme + "://" + u.Host + "/item/detail.php?" + code
}

// ---- listing page parsing ----

var itemCodeRe = regexp.MustCompile(`href="detail\.php\?([a-z]+-\d+)"`)

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
		items = append(items, listingItem{
			code:  code,
			thumb: thumbPath(code),
		})
	}
	return items
}

func thumbPath(code string) string {
	parts := strings.SplitN(code, "-", 2)
	if len(parts) < 2 {
		return ""
	}
	return "img/" + parts[0] + "/" + code + ".jpg"
}

var (
	filterTotalRe = regexp.MustCompile(`関連の作品は\s+(\d+)\s+タイトル`)
	totalRe       = regexp.MustCompile(`全\s+(\d+)\s+タイトル`)
)

func extractTotal(body []byte) int {
	if m := filterTotalRe.FindSubmatch(body); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	if m := totalRe.FindSubmatch(body); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	return 0
}

var navPageRe = regexp.MustCompile(`class="navbango">(\d+)</a>`)

func maxNavPage(body []byte) int {
	matches := navPageRe.FindAllSubmatch(body, -1)
	maxPage := 0
	for _, m := range matches {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// ---- detail page parsing ----

var (
	titleH1Re    = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	descRe       = regexp.MustCompile(`(?s)<div class="item_content">\s*<p>(.*?)</p>`)
	coverRe      = regexp.MustCompile(`(?s)<figure class="jacket">\s*<img[^>]*src="([^"]+)"`)
	dateRe       = regexp.MustCompile(`(?s)発売日</th>\s*<td>\s*(\d{4})\.(\d{1,2})\.(\d{1,2})`)
	durationRe   = regexp.MustCompile(`(?s)収録時間</th>\s*<td>\s*(\d+)分`)
	priceRe      = regexp.MustCompile(`([\d,]+)\s*円`)
	castTDRe     = regexp.MustCompile(`(?s)出演</th>\s*<td[^>]*>(.*?)</td>`)
	directorTDRe = regexp.MustCompile(`(?s)監督</th>\s*<td[^>]*>(.*?)</td>`)
	seriesTDRe   = regexp.MustCompile(`(?s)シリーズ</th>\s*<td[^>]*>(.*?)</td>`)
	categTDRe    = regexp.MustCompile(`(?s)カテゴリ</th>\s*<td[^>]*>(.*?)</td>`)
	linkTextRe   = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, item listingItem, detailURL string, delay time.Duration) (models.Scene, error) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return models.Scene{}, ctx.Err()
		}
	}

	body, err := s.fetchPage(ctx, detailURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.code, err)
	}

	return parseDetail(body, studioURL, item, detailURL), nil
}

func parseDetail(body []byte, studioURL string, item listingItem, detailURL string) models.Scene {
	scene := models.Scene{
		ID:        strings.ToUpper(item.code),
		SiteID:    "deeps",
		StudioURL: studioURL,
		URL:       detailURL,
		Studio:    "DEEP'S",
		ScrapedAt: time.Now().UTC(),
	}

	if m := titleH1Re.FindSubmatch(body); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	if m := coverRe.FindSubmatch(body); m != nil {
		cover := string(m[1])
		if !strings.HasPrefix(cover, "http") {
			if u, err := url.Parse(detailURL); err == nil {
				cover = u.Scheme + "://" + u.Host + "/item/" + cover
			}
		}
		scene.Thumbnail = cover
	} else if item.thumb != "" {
		if u, err := url.Parse(detailURL); err == nil {
			scene.Thumbnail = u.Scheme + "://" + u.Host + "/item/" + item.thumb
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

	scene.Performers = extractLinkTexts(castTDRe, body)
	if dirs := extractLinkTexts(directorTDRe, body); len(dirs) > 0 {
		scene.Director = dirs[0]
	}
	if series := extractLinkTexts(seriesTDRe, body); len(series) > 0 {
		scene.Series = series[0]
	}
	scene.Tags = extractLinkTexts(categTDRe, body)

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

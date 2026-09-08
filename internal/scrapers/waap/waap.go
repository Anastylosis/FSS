package waap

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteBase = "https://www.waap.co.jp"
	siteID   = "waap"
	pageSize = 45
)

type Scraper struct {
	client *http.Client
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

func init() { scraper.Register(New()) }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:waap\.co\.jp|dt01\.co\.jp)(?:/|$)`)

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"waap.co.jp/",
		"waap.co.jp/work/search.php?...",
		"waap.co.jp/work/item.php?itemcode={code}",
		"dt01.co.jp/",
	}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- listing page parsing ----

var (
	totalRe    = regexp.MustCompile(`検索結果：(\d+)件`)
	itemcodeRe = regexp.MustCompile(`href="item\.php\?itemcode=([^"]+)"`)
)

type listingItem struct {
	code string
}

func parseListingPage(body string) (items []listingItem, total int) {
	if m := totalRe.FindStringSubmatch(body); m != nil {
		total, _ = strconv.Atoi(m[1])
	}

	seen := make(map[string]bool)
	for _, m := range itemcodeRe.FindAllStringSubmatch(body, -1) {
		code := m[1]
		if seen[code] {
			continue
		}
		seen[code] = true
		items = append(items, listingItem{code: code})
	}
	return items, total
}

// ---- detail page parsing ----

var (
	twitterTitleRe = regexp.MustCompile(`twitter:title"\s+content="([^"]+)"`)
	twitterImgRe   = regexp.MustCompile(`twitter:image"\s+content="([^"]+)"`)
	durationRe     = regexp.MustCompile(`収録時間.*?<span>(\d+)分</span>`)
	releaseDateRe  = regexp.MustCompile(`発売月.*?(\d{4})年(\d{2})月`)
	performerRe    = regexp.MustCompile(`serch=2[^>]*>([^<]+)</a>`)
	genreRe        = regexp.MustCompile(`ジャンル[^：]*：[^<]*</span><span[^>]*>(.*?)</span></li>`)
	genreLinkRe    = regexp.MustCompile(`serch=3[^>]*>([^<]+)</a>`)
	labelRe        = regexp.MustCompile(`レーベル[^：]*：[^<]*</span><span[^>]*>(.*?)</span></li>`)
	labelLinkRe    = regexp.MustCompile(`serch=8[^>]*>([^<]+)</a>`)
	makerRe        = regexp.MustCompile(`メーカー[^：]*：[^<]*</span><span[^>]*>(.*?)</span></li>`)
	makerLinkRe    = regexp.MustCompile(`serch=10[^>]*>([^<]+)</a>`)
	seriesRe       = regexp.MustCompile(`シリーズ[^：]*：[^<]*</span><span[^>]*>(.*?)</span></li>`)
	seriesLinkRe   = regexp.MustCompile(`serch=4[^>]*>([^<]+)</a>`)
	descRe         = regexp.MustCompile(`(?s)<div id="title_cmt_all">(.*?)</div>`)
)

type detailData struct {
	title       string
	description string
	performers  []string
	tags        []string
	duration    int
	date        time.Time
	thumbnail   string
	studio      string
	series      string
}

func parseDetailPage(body string) detailData {
	var d detailData

	if m := twitterTitleRe.FindStringSubmatch(body); m != nil {
		raw := html.UnescapeString(m[1])
		parts := strings.SplitN(raw, "／", 2)
		d.title = strings.TrimSpace(parts[0])
	}

	if m := twitterImgRe.FindStringSubmatch(body); m != nil {
		d.thumbnail = strings.Replace(m[1], "http://", "https://", 1)
	}

	if m := durationRe.FindStringSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(m[1])
		d.duration = mins * 60
	}

	if m := releaseDateRe.FindStringSubmatch(body); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		if year > 0 && month >= 1 && month <= 12 {
			d.date = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		}
	}

	for _, m := range performerRe.FindAllStringSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(m[1]))
		if name != "" {
			d.performers = append(d.performers, name)
		}
	}

	if m := genreRe.FindStringSubmatch(body); m != nil {
		for _, gm := range genreLinkRe.FindAllStringSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(gm[1]))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	if m := labelRe.FindStringSubmatch(body); m != nil {
		if lm := labelLinkRe.FindStringSubmatch(m[1]); lm != nil {
			d.studio = strings.TrimSpace(html.UnescapeString(lm[1]))
		}
	}
	if d.studio == "" {
		if m := makerRe.FindStringSubmatch(body); m != nil {
			if mm := makerLinkRe.FindStringSubmatch(m[1]); mm != nil {
				d.studio = strings.TrimSpace(html.UnescapeString(mm[1]))
			}
		}
	}

	if m := seriesRe.FindStringSubmatch(body); m != nil {
		if sm := seriesLinkRe.FindStringSubmatch(m[1]); sm != nil {
			d.series = strings.TrimSpace(html.UnescapeString(sm[1]))
		}
	}

	if m := descRe.FindStringSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	return d
}

// ---- run loop ----

func resolveListingURL(studioURL string) string {
	if strings.Contains(studioURL, "search.php") {
		return studioURL
	}
	return siteBase + "/work/search.php?serch=5&onrls=new&limit=45&pg=1"
}

func resolveBase(listURL string) string {
	if idx := strings.Index(listURL, "/work/"); idx >= 0 {
		return listURL[:idx]
	}
	return siteBase
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listURL := resolveListingURL(studioURL)
	base := resolveBase(listURL)
	scraper.Debugf(1, "waap: listing URL %s", listURL)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range work {
				scene, err := s.fetchDetail(ctx, base, code, studioURL, opts.Delay)
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
		s.enqueueItems(ctx, listURL, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueueItems(ctx context.Context, listURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- string) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}

		u := setPageParam(listURL, page)
		scraper.Debugf(1, "waap: fetching page %d", page)

		body, err := s.fetchShiftJIS(ctx, u)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		items, total := parseListingPage(body)
		if len(items) == 0 {
			if page == 1 {
				// A first page with no items is a broken search, not an empty
				// catalogue — the site sits behind a CloudFront distribution
				// that ignores the query string, so `search.php` can answer
				// with a stale cached result set for a different query.
				// Returning silently would report that as a clean scrape.
				select {
				case out <- scraper.Error(scraper.ParseError(u,
					fmt.Errorf("no items in the search listing"))):
				case <-ctx.Done():
				}
			}
			return
		}

		if page == 1 && total > 0 {
			scraper.Debugf(1, "waap: %d total items", total)
			select {
			case out <- scraper.Progress(total):
			case <-ctx.Done():
				return
			}
		}

		for _, item := range items {
			if opts.KnownIDs[item.code] {
				scraper.Debugf(1, "waap: hit known ID %s, stopping early", item.code)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- item.code:
			case <-ctx.Done():
				return
			}
		}
	}
}

var pgRe = regexp.MustCompile(`pg=\d+`)

func setPageParam(u string, page int) string {
	if pgRe.MatchString(u) {
		return pgRe.ReplaceAllString(u, fmt.Sprintf("pg=%d", page))
	}
	if strings.Contains(u, "?") {
		return u + fmt.Sprintf("&pg=%d", page)
	}
	return u + fmt.Sprintf("?pg=%d", page)
}

func (s *Scraper) fetchDetail(ctx context.Context, base, code, studioURL string, delay time.Duration) (models.Scene, error) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return models.Scene{}, ctx.Err()
		}
	}

	u := base + "/work/item.php?itemcode=" + code
	body, err := s.fetchShiftJIS(ctx, u)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", code, err)
	}

	d := parseDetailPage(body)
	now := time.Now().UTC()

	scene := models.Scene{
		ID:          code,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       d.title,
		Description: d.description,
		URL:         u,
		Duration:    d.duration,
		Date:        d.date,
		Performers:  d.performers,
		Tags:        d.tags,
		Thumbnail:   d.thumbnail,
		Studio:      d.studio,
		Series:      d.series,
		ScrapedAt:   now,
	}

	return scene, nil
}

func (s *Scraper) fetchShiftJIS(ctx context.Context, rawURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentChrome,
		},
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	return decodeShiftJIS(raw, resp.Header.Get("Content-Type")), nil
}

var metaShiftJISRe = regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?\s*shift[-_]?jis`)

// decodeShiftJIS converts a page body to UTF-8. The charset is taken from the
// Content-Type header when it names one, from the page's own <meta> tag when it
// does not — which is how this site labels most of its pages — and finally from
// the bytes themselves: a body that is not valid UTF-8 is Shift-JIS here, and
// decoding it is strictly better than emitting a page of replacement
// characters. A body that *is* valid UTF-8 is returned untouched, so an
// ASCII-only or genuinely UTF-8 page is never mangled by the fallback.
func decodeShiftJIS(raw []byte, contentType string) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "shift-jis"), strings.Contains(ct, "shift_jis"), strings.Contains(ct, "sjis"):
	case strings.Contains(ct, "utf-8"):
		return string(raw)
	case metaShiftJISRe.Match(raw):
	case utf8.Valid(raw):
		return string(raw)
	}
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(raw)
	if err != nil {
		return string(raw)
	}
	return string(decoded)
}

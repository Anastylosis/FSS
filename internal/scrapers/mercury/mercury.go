package mercury

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// mercury-2005.com is geo-blocked; scrape from the diary.to archive instead.
const siteBase = "https://mercury.diary.to"

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// redirect them. Output fields (Scene.URL, Thumbnail) keep the const: a
	// scene's public address must not become a 127.0.0.1 URL because a test
	// happened to fetch it from there.
	base string
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

func init() { scraper.Register(New()) }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:mercury\.diary\.to|mercury-2005\.com)(?:/|$)`)

func (s *Scraper) ID() string { return "mercury" }
func (s *Scraper) Patterns() []string {
	return []string{
		"mercury.diary.to",
		"mercury.diary.to/archives/cat_{id}.html",
	}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listItem struct {
	articleID int
	permalink string
	title     string
	label     string
	date      string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	baseURL := resolveListingBase(studioURL)
	scraper.Debugf(1, "mercury: listing base %s", baseURL)

	work := make(chan listItem, opts.Workers)
	// Scene.ID is the product code, which is only readable from the detail
	// page — the listing exposes nothing but the article ID. So the KnownIDs
	// check has to happen after the detail fetch; the listing loop reads
	// hitKnown between pages to stop paginating.
	var hitKnown atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ok, err := s.fetchDetail(ctx, item, studioURL)
				if err != nil {
					select {
					case out <- scraper.Error(fmt.Errorf("detail %d: %w", item.articleID, err)):
					case <-ctx.Done():
						return
					}
					continue
				}
				if !ok {
					continue
				}
				if opts.KnownIDs[scene.ID] {
					scraper.Debugf(1, "mercury: detail %s already known, stopping early", scene.ID)
					hitKnown.Store(true)
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

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}

		u := pageURL(baseURL, page)
		scraper.Debugf(1, "mercury: fetching page %d", page)

		body, err := s.fetchHTML(ctx, u)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			break
		}

		// A worker has already reached a known scene, so every later page is
		// known too.
		if hitKnown.Load() {
			break
		}

		cancelled := false
		for _, item := range items {
			select {
			case work <- item:
			case <-ctx.Done():
				cancelled = true
			}
			if ctx.Err() != nil {
				break
			}
		}
		if cancelled {
			break
		}
	}

	close(work)
	wg.Wait()

	// Emitted once, after every worker has finished sending, so the signal
	// cannot interleave with scenes still in flight.
	if hitKnown.Load() {
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
}

var (
	catPageRe     = regexp.MustCompile(`cat_(\d+)\.html`)
	mercury2005Re = regexp.MustCompile(`(?i)(?:^https?://(?:www\.)?|://)mercury-2005\.com(?:/|$)`)
)

func resolveListingBase(studioURL string) string {
	base := strings.TrimRight(studioURL, "/")
	if mercury2005Re.MatchString(base) {
		base = siteBase
	}
	if strings.Contains(base, "/archives/cat_") {
		if m := catPageRe.FindStringSubmatch(base); m != nil {
			return siteBase + "/archives/cat_" + m[1] + ".html"
		}
	}
	if strings.Contains(base, "diary.to") {
		return siteBase
	}
	return base
}

func pageURL(base string, page int) string {
	if page <= 1 {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "p=" + strconv.Itoa(page)
}

// ---- Listing page parsing ----

var (
	articleBlockRe = regexp.MustCompile(`(?s)articles\.push\(\{(.+?)\}\)`)
	fieldIDRe      = regexp.MustCompile(`\bid\s*:\s*'?(\d+)'?`)
	fieldLinkRe    = regexp.MustCompile(`permalink\s*:\s*["']([^"']+)["']`)
	fieldTitleRe   = regexp.MustCompile(`title\s*:\s*["']((?:[^"'\\]|\\.)*)["']`)
	fieldDateRe    = regexp.MustCompile(`date\s*:\s*["']([^"']+)["']`)
	fieldCatRe     = regexp.MustCompile(`categories\s*:\s*\[([^\]]+)\]`)
	catNameRe      = regexp.MustCompile(`name\s*:\s*["']([^"']+)["']`)
)

func parseListingPage(body []byte) []listItem {
	blocks := articleBlockRe.FindAllSubmatch(body, -1)
	items := make([]listItem, 0, len(blocks))

	for _, block := range blocks {
		content := block[1]

		var item listItem

		if m := fieldIDRe.FindSubmatch(content); m != nil {
			item.articleID, _ = strconv.Atoi(string(m[1]))
		}
		if item.articleID == 0 {
			continue
		}

		if m := fieldLinkRe.FindSubmatch(content); m != nil {
			item.permalink = string(m[1])
		}
		if m := fieldTitleRe.FindSubmatch(content); m != nil {
			item.title = html.UnescapeString(string(m[1]))
		}
		if m := fieldDateRe.FindSubmatch(content); m != nil {
			item.date = string(m[1])
		}
		if m := fieldCatRe.FindSubmatch(content); m != nil {
			if nm := catNameRe.FindSubmatch(m[1]); nm != nil {
				item.label = string(nm[1])
			}
		}

		items = append(items, item)
	}
	return items
}

// ---- Detail page parsing ----

var (
	productCodeAltRe  = regexp.MustCompile(`alt="([A-Z]+-\d+)"`)
	productCodeTextRe = regexp.MustCompile(`品番[：:]\s*([A-Za-z]+-\d+)`)
	durationRe        = regexp.MustCompile(`収録時間[：:]\s*(\d+)\s*分`)
	priceTaxRe        = regexp.MustCompile(`税込\s*([\d,]+)\s*円`)
	priceBaseRe       = regexp.MustCompile(`価格[：:]\s*([\d,]+)\s*円`)
	tagLinkRe         = regexp.MustCompile(`<a[^>]*>#([^<]+)</a>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, item listItem, studioURL string) (models.Scene, bool, error) {
	u := item.permalink
	if u == "" {
		u = fmt.Sprintf("%s/archives/%d.html", s.base, item.articleID)
	}

	body, err := s.fetchHTML(ctx, u)
	if err != nil {
		return models.Scene{}, false, err
	}

	code := extractProductCode(body)
	if code == "" {
		return models.Scene{}, false, nil
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:        code,
		SiteID:    "mercury",
		StudioURL: studioURL,
		Title:     item.title,
		URL:       u,
		Studio:    "Mercury",
		ScrapedAt: now,
	}

	if item.label != "" {
		scene.Series = item.label
	}

	if d, ok := parseDate(item.date); ok {
		scene.Date = d
	}

	if m := durationRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		scene.Duration = mins * 60
	}

	if og := parseutil.OpenGraph(body); og["og:image"] != "" {
		scene.Thumbnail = og["og:image"]
	}

	var tags []string
	for _, m := range tagLinkRe.FindAllSubmatch(body, -1) {
		tag := strings.TrimSpace(string(m[1]))
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	scene.Tags = tags

	if price := extractPrice(body); price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: price,
		})
	}

	return scene, true, nil
}

func extractProductCode(body []byte) string {
	if m := productCodeAltRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	if m := productCodeTextRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

func extractPrice(body []byte) float64 {
	if m := priceTaxRe.FindSubmatch(body); m != nil {
		return parseYen(string(m[1]))
	}
	if m := priceBaseRe.FindSubmatch(body); m != nil {
		return parseYen(string(m[1]))
	}
	return 0
}

func parseYen(s string) float64 {
	s = strings.ReplaceAll(s, ",", "")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

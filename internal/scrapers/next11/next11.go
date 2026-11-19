package next11

import (
	"context"
	"encoding/json"
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

const (
	siteID      = "next11"
	defaultBase = "https://next11.co.jp"
	studioName  = "NEXT"
	// pageSize is what the listing serves per page; the 2026-11 rebuild dropped
	// the disp_number selector that once raised it to 100.
	pageSize = 30
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?next11\.co\.jp(?:/|$)`)

type Scraper struct {
	client   *http.Client
	siteBase string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		siteBase: defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string         { return siteID }
func (s *Scraper) Patterns() []string { return []string{"next11.co.jp"} }
func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingItem struct {
	productID string
	code      string
}

var (
	totalRe     = regexp.MustCompile(`<span class="pagenumber">([\d,]+)</span>`)
	listBlockRe = regexp.MustCompile(`(?s)<div class="listphoto">(.*?)</li>`)
	pidRe       = regexp.MustCompile(`product_id=(\d+)`)
	codeRe      = regexp.MustCompile(`<dt>商品：</dt>\s*<dd>([^<]+)</dd>`)
)

func parseListingPage(body []byte) (items []listingItem, total int) {
	if m := totalRe.FindSubmatch(body); m != nil {
		total, _ = strconv.Atoi(strings.ReplaceAll(string(m[1]), ",", ""))
	}

	for _, b := range listBlockRe.FindAllSubmatch(body, -1) {
		pm := pidRe.FindSubmatch(b[1])
		cm := codeRe.FindSubmatch(b[0])
		if pm == nil || cm == nil {
			continue
		}
		if code := strings.TrimSpace(string(cm[1])); code != "" {
			items = append(items, listingItem{productID: string(pm[1]), code: code})
		}
	}
	return items, total
}

var (
	titleRe     = regexp.MustCompile(`<dt>タイトル</dt>\s*<dd>([^<]+)</dd>`)
	codeDetRe   = regexp.MustCompile(`<dt>品番</dt>\s*<dd>([^<]+)</dd>`)
	dateRe      = regexp.MustCompile(`<dt>発売日</dt>\s*<dd>(?:<time[^>]*>)?(\d{4}-\d{2}-\d{2})`)
	durationRe  = regexp.MustCompile(`<dt>収録時間</dt>\s*<dd>(\d+)分</dd>`)
	performerRe = regexp.MustCompile(`(?s)<dt>出演</dt>\s*<dd>(.*?)</dd>`)
	actorLinkRe = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	genreRe     = regexp.MustCompile(`(?s)<dt>ジャンル</dt>\s*<dd>(.*?)</dd>`)
	labelRe     = regexp.MustCompile(`(?s)<dt>レーベル</dt>\s*<dd>(.*?)</dd>`)
	seriesRe    = regexp.MustCompile(`(?s)<dt>シリーズ</dt>\s*<dd>(.*?)</dd>`)
	thumbRe     = regexp.MustCompile(`<div class="detail1">\s*<a href="([^"]+)"`)
	ldJSONRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
)

type detailData struct {
	title       string
	code        string
	description string
	date        time.Time
	duration    int
	performers  []string
	tags        []string
	label       string
	series      string
	thumbnail   string
}

func linkTexts(re *regexp.Regexp, body []byte) []string {
	m := re.FindSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, am := range actorLinkRe.FindAllSubmatch(m[1], -1) {
		if name := strings.TrimSpace(string(am[1])); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := titleRe.FindSubmatch(body); m != nil {
		d.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}
	if m := codeDetRe.FindSubmatch(body); m != nil {
		d.code = strings.TrimSpace(string(m[1]))
	}
	if m := dateRe.FindSubmatch(body); m != nil {
		if t, err := time.Parse("2006-01-02", string(m[1])); err == nil {
			d.date = t.UTC()
		}
	}
	if m := durationRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		d.duration = mins * 60
	}
	d.performers = linkTexts(performerRe, body)
	d.tags = linkTexts(genreRe, body)
	if l := linkTexts(labelRe, body); len(l) > 0 {
		d.label = l[0]
	}
	if l := linkTexts(seriesRe, body); len(l) > 0 {
		d.series = l[0]
	}
	if m := thumbRe.FindSubmatch(body); m != nil {
		d.thumbnail = string(m[1])
	}

	// The synopsis exists only in the schema.org Movie block.
	for _, m := range ldJSONRe.FindAllSubmatch(body, -1) {
		var ld struct {
			Type        string `json:"@type"`
			Description string `json:"description"`
		}
		if json.Unmarshal(m[1], &ld) == nil && ld.Type == "Movie" {
			d.description = strings.TrimSpace(ld.Description)
			break
		}
	}
	return d
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: fetching first page", siteID)
	body, err := s.fetchListing(ctx, 1)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("fetch listing: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	items, total := parseListingPage(body)
	scraper.Debugf(1, "%s: total %d items", siteID, total)
	if total == 0 || len(items) == 0 {
		select {
		case out <- scraper.Error(scraper.ParseError(s.siteBase+"/products/list.php", fmt.Errorf("no products on the first listing page"))):
		case <-ctx.Done():
		}
		return
	}

	select {
	case out <- scraper.Progress(total):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching details with %d workers", siteID, workers)

	work := make(chan listingItem)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, studioURL, item)
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

	stopped := s.dispatchItems(ctx, opts, items, work, out)

	pages := (total + pageSize - 1) / pageSize
	for page := 2; page <= pages && !stopped; page++ {
		if ctx.Err() != nil {
			break
		}
		if !scraper.Pace(ctx, opts.Delay) {
			break
		}

		scraper.Debugf(1, "%s: fetching page %d/%d", siteID, page, pages)
		body, err := s.fetchListing(ctx, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("fetch page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		items, _ := parseListingPage(body)
		if len(items) == 0 {
			break
		}

		stopped = s.dispatchItems(ctx, opts, items, work, out)
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) dispatchItems(ctx context.Context, opts scraper.ListOpts, items []listingItem, work chan<- listingItem, out chan<- scraper.SceneResult) bool {
	for _, item := range items {
		if opts.KnownIDs[item.code] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.code)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return true
		}
		select {
		case work <- item:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			return false
		}
	}
	return false
}

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, item listingItem) (models.Scene, error) {
	pageURL := fmt.Sprintf("%s/products/detail.php?product_id=%s", s.siteBase, item.productID)

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.code, err)
	}

	d := parseDetailPage(body)

	title := d.title
	if title == "" {
		title = item.code
	}

	code := d.code
	if code == "" {
		code = item.code
	}

	thumb := d.thumbnail
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		// The page sets <base href="/">, so a bare path is rooted.
		thumb = s.siteBase + "/" + strings.TrimPrefix(thumb, "/")
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:          code,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         pageURL,
		Thumbnail:   thumb,
		Date:        d.date,
		Duration:    d.duration,
		Performers:  d.performers,
		Tags:        d.tags,
		Description: d.description,
		Studio:      studioName,
		ScrapedAt:   now,
	}

	return scene, nil
}

// fetchListing requests one page of the product listing, newest release first.
//
// Until 2026-11 the listing paginated by POSTing its form1 and ignored the
// `?pageno=N` query, so a GET walk received page one every time. The rebuilt
// site is the reverse: GET honours `pageno` and a POST body is ignored.
func (s *Scraper) fetchListing(ctx context.Context, page int) ([]byte, error) {
	return s.fetchPage(ctx, fmt.Sprintf("%s/products/list.php?pageno=%d", s.siteBase, page))
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentChrome)
	headers["Cookie"] = "adult_check=1"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: headers,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

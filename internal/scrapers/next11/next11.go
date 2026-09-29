package next11

import (
	"context"
	"fmt"
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

const (
	siteID      = "next11"
	defaultBase = "https://next11.co.jp"
	studioName  = "NEXT"
	// pageSize is the largest value the site's own disp_number selector offers
	// (20/50/100). At 100 the ~6.7k catalogue is 68 pages instead of 338.
	pageSize = 100
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
	totalRe     = regexp.MustCompile(`<span class="pagenumber">(\d+)</span>`)
	listBlockRe = regexp.MustCompile(`(?s)<div class="listphoto">(.*?)</li>`)
	pidRe       = regexp.MustCompile(`product_id=(\d+)`)
	codeRe      = regexp.MustCompile(`商品：</td>\s*<td class="center">([^<]+)</td>`)
)

func parseListingPage(body []byte) (items []listingItem, total int) {
	if m := totalRe.FindSubmatch(body); m != nil {
		total, _ = strconv.Atoi(string(m[1]))
	}

	blocks := listBlockRe.FindAllSubmatch(body, -1)
	for _, b := range blocks {
		block := b[1]
		pm := pidRe.FindSubmatch(block)
		if pm == nil {
			continue
		}
		pid := string(pm[1])

		var code string
		// Code is in the listrightblock after the listphoto, so search the full <li> block
		if cm := codeRe.FindSubmatch(b[0]); cm != nil {
			code = strings.TrimSpace(string(cm[1]))
		}

		if code != "" {
			items = append(items, listingItem{productID: pid, code: code})
		}
	}
	return items, total
}

var (
	titleRe     = regexp.MustCompile(`(?s)<span itemprop="name">([^<]+)</span>`)
	pidCodeRe   = regexp.MustCompile(`<span itemprop="productID" content="([^"]+)">`)
	dateRe      = regexp.MustCompile(`<dt>発売日：</dt>\s*<dd>(\d{4}-\d{2}-\d{2})</dd>`)
	durationRe  = regexp.MustCompile(`<dt>収録時間：</dt>\s*<dd>(\d+)分</dd>`)
	performerRe = regexp.MustCompile(`(?s)<dt>出演：</dt>\s*<dd>(.*?)</dd>`)
	actorLinkRe = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	genreRe     = regexp.MustCompile(`(?s)<dt>ジャンル：</dt>\s*<dd>(.*?)</dd>`)
	labelRe     = regexp.MustCompile(`(?s)<dt>レーベル：</dt>\s*<dd>(.*?)</dd>`)
	seriesRe    = regexp.MustCompile(`(?s)<dt>シリーズ：</dt>\s*<dd>(.*?)</dd>`)
	thumbRe     = regexp.MustCompile(`<div id="detail1">\s*<img src="([^"]+)"`)
	priceRe     = regexp.MustCompile(`(?s)itemprop="price">\s*([\d,]+)\s*\n?\s*円`)
)

type detailData struct {
	title      string
	code       string
	date       time.Time
	duration   int
	performers []string
	tags       []string
	label      string
	series     string
	thumbnail  string
	price      int
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := titleRe.FindSubmatch(body); m != nil {
		d.title = strings.TrimSpace(string(m[1]))
	}

	if m := pidCodeRe.FindSubmatch(body); m != nil {
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

	if m := performerRe.FindSubmatch(body); m != nil {
		for _, am := range actorLinkRe.FindAllSubmatch(m[1], -1) {
			name := strings.TrimSpace(string(am[1]))
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}

	if m := genreRe.FindSubmatch(body); m != nil {
		for _, gm := range actorLinkRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(string(gm[1]))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	if m := labelRe.FindSubmatch(body); m != nil {
		if lm := actorLinkRe.FindSubmatch(m[1]); lm != nil {
			d.label = strings.TrimSpace(string(lm[1]))
		}
	}

	if m := seriesRe.FindSubmatch(body); m != nil {
		if sm := actorLinkRe.FindSubmatch(m[1]); sm != nil {
			d.series = strings.TrimSpace(string(sm[1]))
		}
	}

	if m := thumbRe.FindSubmatch(body); m != nil {
		d.thumbnail = string(m[1])
	}

	if m := priceRe.FindSubmatch(body); m != nil {
		ps := strings.ReplaceAll(string(m[1]), ",", "")
		d.price, _ = strconv.Atoi(ps)
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
		thumb = s.siteBase + thumb
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
		Description: "",
		Studio:      studioName,
		ScrapedAt:   now,
	}

	if d.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: float64(d.price),
		})
	}

	return scene, nil
}

// fetchListing requests one page of the product listing.
//
// The listing paginates by POSTing the page's own form1 — the `?pageno=N` query
// parameter is *ignored* by the site, so every GET returns page 1. The scraper
// used to walk `ceil(total/20)` GETs and receive the same 20 products every
// time; because the store is keyed on (id, site_id) those duplicates collapsed
// at Save, so a --full run reported thousands of scenes and stored 20.
//
// disp_number is the site's own per-page selector; 100 is the largest option it
// offers.
func (s *Scraper) fetchListing(ctx context.Context, page int) ([]byte, error) {
	form := url.Values{
		"pageno":      {strconv.Itoa(page)},
		"mode":        {""},
		"orderby":     {"date"},
		"disp_number": {strconv.Itoa(pageSize)},
	}
	headers := httpx.BrowserHeaders(httpx.UserAgentChrome)
	headers["Cookie"] = "adult_check=1"
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	headers["Referer"] = s.siteBase + "/products/list.php"

	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method:  http.MethodPost,
		URL:     s.siteBase + "/products/list.php",
		Body:    []byte(form.Encode()),
		Headers: headers,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
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

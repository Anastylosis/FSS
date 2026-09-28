package hmp

import (
	"context"
	"fmt"
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
	siteID      = "hmp"
	defaultBase = "https://www.hmp.jp"
	studioName  = "h.m.p"
	pageSize    = 16
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?hmp\.jp(?:/|$)`)

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
func (s *Scraper) Patterns() []string { return []string{"hmp.jp"} }
func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingItem struct {
	code string
}

var (
	goodsLinkRe = regexp.MustCompile(`goods/([A-Z0-9]+-[A-Z0-9]+)/`)
	totalRe     = regexp.MustCompile(`合計<span>(\d+)</span>件`)
)

func parseListingPage(body []byte) (items []listingItem, total int) {
	if m := totalRe.FindSubmatch(body); m != nil {
		total, _ = strconv.Atoi(string(m[1]))
	}

	seen := make(map[string]bool)
	for _, m := range goodsLinkRe.FindAllSubmatch(body, -1) {
		code := string(m[1])
		if !seen[code] {
			seen[code] = true
			items = append(items, listingItem{code: code})
		}
	}
	return items, total
}

var (
	titleRe     = regexp.MustCompile(`(?s)<h1 id="itemTitle">\s*(.*?)\s*</h1>`)
	dateRe      = regexp.MustCompile(`<th>発売日：</th>\s*<td>(\d{4})\.(\d{2})\.(\d{2})</td>`)
	durationRe  = regexp.MustCompile(`<th>時間：</th>\s*<td>(\d+)\s*分</td>`)
	performerRe = regexp.MustCompile(`(?s)<th>出演：</th>\s*<td>(.*?)</td>`)
	actorLinkRe = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	genreRe     = regexp.MustCompile(`(?s)<th>ジャンル：</th>\s*<td>(.*?)</td>`)
	seriesRe    = regexp.MustCompile(`(?s)<th>シリーズ：</th>\s*<td>(.*?)</td>`)
	labelRe     = regexp.MustCompile(`(?s)<th>レーベル：</th>\s*<td>(.*?)</td>`)
	priceRe     = regexp.MustCompile(`<th>税込金額：</th>\s*<td>([0-9,]+)円</td>`)
	descRe      = regexp.MustCompile(`(?s)<p id="explain">(.*?)</p>`)
	thumbRe     = regexp.MustCompile(`id="itemMainPhoto"[^>]*>.*?<img[^>]+src="([^"]+)"`)
)

type detailData struct {
	title       string
	date        time.Time
	duration    int
	performers  []string
	tags        []string
	series      string
	label       string
	price       int
	description string
	thumbnail   string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := titleRe.FindSubmatch(body); m != nil {
		d.title = strings.TrimSpace(string(m[1]))
	}

	if m := dateRe.FindSubmatch(body); m != nil {
		ds := fmt.Sprintf("%s-%s-%s", m[1], m[2], m[3])
		if t, err := time.Parse("2006-01-02", ds); err == nil {
			d.date = t.UTC()
		}
	}

	if m := durationRe.FindSubmatch(body); m != nil {
		d.duration, _ = strconv.Atoi(string(m[1]))
		d.duration *= 60
	}

	if m := performerRe.FindSubmatch(body); m != nil {
		td := string(m[1])
		for _, am := range actorLinkRe.FindAllStringSubmatch(td, -1) {
			name := strings.TrimSpace(am[1])
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
		// Some performers are plain text (not linked), separated by 、
		stripped := actorLinkRe.ReplaceAllString(td, "")
		for _, part := range strings.Split(stripped, "、") {
			name := strings.TrimSpace(part)
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

	if m := seriesRe.FindSubmatch(body); m != nil {
		if sm := actorLinkRe.FindSubmatch(m[1]); sm != nil {
			d.series = strings.TrimSpace(string(sm[1]))
		}
	}

	if m := labelRe.FindSubmatch(body); m != nil {
		if lm := actorLinkRe.FindSubmatch(m[1]); lm != nil {
			d.label = strings.TrimSpace(string(lm[1]))
		}
	}

	if m := priceRe.FindSubmatch(body); m != nil {
		ps := strings.ReplaceAll(string(m[1]), ",", "")
		d.price, _ = strconv.Atoi(ps)
	}

	if m := descRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(string(m[1]))
	}

	if m := thumbRe.FindSubmatch(body); m != nil {
		d.thumbnail = string(m[1])
	}

	return d
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: fetching first page", siteID)
	body, err := s.fetchPage(ctx, s.siteBase+"/portal/catalog/?scd=10&p=1")
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
		// The catalogue is never legitimately empty, so a page that parses to
		// nothing is a redesign or an outage — hmp.jp serves a whole-site
		// maintenance notice from every path — and returning silently makes
		// that indistinguishable from success. Reporting it is what marks the
		// traversal incomplete.
		select {
		case out <- scraper.Error(scraper.ParseError(s.siteBase+"/portal/catalog/?scd=10&p=1",
			fmt.Errorf("no catalogue items on the first page"))):
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
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
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
		if opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				break
			}
			if ctx.Err() != nil {
				break
			}
		}

		scraper.Debugf(1, "%s: fetching page %d/%d", siteID, page, pages)
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/portal/catalog/?scd=10&p=%d", s.siteBase, page))
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

// dispatchItems queues one listing page, reporting whether the walk should end.
// Like the other Japanese DVD catalogues (deeps, waap), releases arrive in
// same-date batches whose internal order is by product code, so a title added
// to a batch already half-stored sits *behind* a known sibling. The page is
// therefore scanned in full before the stop fires, which costs at most one
// page of detail fetches and cannot truncate a batch.
func (s *Scraper) dispatchItems(ctx context.Context, opts scraper.ListOpts, items []listingItem, work chan<- listingItem, out chan<- scraper.SceneResult) bool {
	hitKnown := false
	for _, item := range items {
		if opts.KnownIDs[item.code] {
			hitKnown = true
			continue
		}
		select {
		case work <- item:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			return false
		}
	}
	if hitKnown {
		scraper.Debugf(1, "%s: page reached stored items, stopping", siteID)
		select {
		case out <- scraper.StoppedEarly():
		case <-ctx.Done():
		}
	}
	return hitKnown
}

func (s *Scraper) fetchDetail(ctx context.Context, studioURL string, item listingItem) (models.Scene, error) {
	pageURL := fmt.Sprintf("%s/portal/catalog/goods/%s/", s.siteBase, item.code)

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.code, err)
	}

	d := parseDetailPage(body)

	title := d.title
	if title == "" {
		title = item.code
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:          item.code,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         pageURL,
		Thumbnail:   d.thumbnail,
		Date:        d.date,
		Duration:    d.duration,
		Performers:  d.performers,
		Tags:        d.tags,
		Description: d.description,
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

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentChrome)
	headers["Accept-Language"] = "ja,en;q=0.5"
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

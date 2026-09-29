package indiesav

import (
	"context"
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
	defaultBase = "https://www.indies-av.co.jp"
	siteID      = "indiesav"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?indies-av\.co\.jp(?:/|$)`)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"indies-av.co.jp/lables/{code}/",
		"indies-av.co.jp/genre/{slug}/",
		"indies-av.co.jp/actress/{slug}/",
	}
}
func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func resolveBase(studioURL string) string {
	for _, prefix := range []string{"https://www.", "https://", "http://www.", "http://"} {
		if strings.HasPrefix(studioURL, prefix) {
			if idx := strings.Index(studioURL[len(prefix):], "/"); idx >= 0 {
				return studioURL[:len(prefix)+idx]
			}
			return studioURL
		}
	}
	return defaultBase
}

func resolveListingURL(studioURL, base string) string {
	for _, seg := range []string{"/lables/", "/genre/", "/actress/"} {
		if strings.Contains(studioURL, seg) {
			return studioURL
		}
	}
	return base + "/lables/ymdd/"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := resolveBase(studioURL)
	listingURL := resolveListingURL(studioURL, base)
	if !strings.HasSuffix(listingURL, "/") {
		listingURL += "/"
	}
	scraper.Debugf(1, "%s: listing URL %s", siteID, listingURL)

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
				scene, err := s.fetchDetail(ctx, base, item, studioURL, opts.Delay)
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
		s.enqueuePages(ctx, base, listingURL, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueuePages(ctx context.Context, base, listingURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingItem) {
	sentProgress := false
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		pageURL := listingURL
		if page > 1 {
			pageURL = listingURL + fmt.Sprintf("page/%d/", page)
		}
		scraper.Debugf(1, "%s: fetching page %d", siteID, page)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		items := parseListingPage(body)
		if len(items) == 0 {
			return
		}

		if !sentProgress {
			lastPage := extractLastPage(body)
			if lastPage > 0 {
				total := (lastPage-1)*len(items) + len(items)
				scraper.Debugf(1, "%s: ~%d total items (%d pages)", siteID, total, lastPage)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
			sentProgress = true
		}

		for _, item := range items {
			if item.url == "" {
				item.url = base + "/title/" + strings.ToLower(strings.ReplaceAll(item.sku, "-", "")) + "/"
			}
			if opts.KnownIDs[item.sku] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.sku)
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

		if !hasNextPage(body, page) {
			return
		}
	}
}

type listingItem struct {
	sku         string
	url         string
	title       string
	date        string
	description string
	image       string
	price       float64
}

var (
	packageRe = regexp.MustCompile(`(?s)<li class="col-md-3 package">(.*?)</li>`)
	skuRe     = regexp.MustCompile(`itemprop="sku" content="([^"]+)"`)
	nameRe    = regexp.MustCompile(`itemprop="name" content="([^"]+)"`)
	dateRe    = regexp.MustCompile(`itemprop="releaseDate" content="([^"]+)"`)
	descRe    = regexp.MustCompile(`itemprop="description" content="([^"]+)"`)
	urlRe     = regexp.MustCompile(`itemprop="url" content="([^"]+)"`)
	imageRe   = regexp.MustCompile(`itemprop="image" src="([^"]+)"`)
	priceRe   = regexp.MustCompile(`itemprop="price" content="([^"]+)"`)
	pageNumRe = regexp.MustCompile(`/page/(\d+)/`)
)

func parseListingPage(body []byte) []listingItem {
	blocks := packageRe.FindAllSubmatch(body, -1)
	items := make([]listingItem, 0, len(blocks))

	for _, b := range blocks {
		block := b[1]
		item := listingItem{}

		if m := skuRe.FindSubmatch(block); m != nil {
			item.sku = string(m[1])
		}
		if item.sku == "" {
			continue
		}
		if m := nameRe.FindSubmatch(block); m != nil {
			item.title = html.UnescapeString(string(m[1]))
		}
		if m := dateRe.FindSubmatch(block); m != nil {
			item.date = string(m[1])
		}
		if m := descRe.FindSubmatch(block); m != nil {
			item.description = html.UnescapeString(string(m[1]))
		}
		if m := urlRe.FindSubmatch(block); m != nil {
			item.url = string(m[1])
		}
		if m := imageRe.FindSubmatch(block); m != nil {
			item.image = string(m[1])
		}
		if m := priceRe.FindSubmatch(block); m != nil {
			item.price, _ = strconv.ParseFloat(string(m[1]), 64)
		}

		items = append(items, item)
	}
	return items
}

func extractLastPage(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

func hasNextPage(body []byte, current int) bool {
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > current {
			return true
		}
	}
	return false
}

var (
	performerRe = regexp.MustCompile(`(?s)女優名</span>\s*</div>\s*<div[^>]*>\s*(?:<[^>]*>)*\s*<span class="h6">([^<]+)</span>`)
	durationRe  = regexp.MustCompile(`(?s)収録時間</span>\s*</div>\s*<div[^>]*>\s*<span[^>]*>(\d+)分</span>`)
	genreLinkRe = regexp.MustCompile(`(?s)<span style=display:none>ジャンル</span>(.*?)</span>`)
	genreTagRe  = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	labelLinkRe = regexp.MustCompile(`(?s)<span style=display:none>レーベル</span><a[^>]*>([^<]+)</a>`)
)

type detailData struct {
	performers []string
	duration   int
	genres     []string
	label      string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := performerRe.FindSubmatch(body); m != nil {
		raw := strings.TrimSpace(string(m[1]))
		for _, name := range strings.Split(raw, "/") {
			name = strings.TrimSpace(name)
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}

	if m := durationRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		d.duration = mins * 60
	}

	if m := genreLinkRe.FindSubmatch(body); m != nil {
		for _, gm := range genreTagRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(string(gm[1]))
			if tag != "" {
				d.genres = append(d.genres, tag)
			}
		}
	}

	if m := labelLinkRe.FindSubmatch(body); m != nil {
		d.label = strings.TrimSpace(string(m[1]))
	}

	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, _ string, item listingItem, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:          item.sku,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Description: item.description,
		Thumbnail:   item.image,
		Studio:      "Momotaro Eizo",
		ScrapedAt:   now,
	}

	if item.date != "" {
		if t, err := time.Parse("2006-01-02", item.date); err == nil {
			scene.Date = t.UTC()
		}
	}

	if item.price > 0 {
		scene.AddPrice(models.PriceSnapshot{
			Date:    now,
			Regular: item.price,
		})
	}

	body, err := s.fetchPage(ctx, item.url)
	if err != nil {
		return scene, nil
	}

	detail := parseDetailPage(body)
	scene.Performers = detail.performers
	scene.Duration = detail.duration
	scene.Tags = detail.genres
	if detail.label != "" {
		scene.Series = detail.label
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

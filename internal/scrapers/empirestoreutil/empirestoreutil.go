package empirestoreutil

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	ListingURL string
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		Client: httpx.NewClient(30 * time.Second),
	}
}

func resolveBase(studioURL, domain string) string {
	for _, prefix := range []string{"https://www.", "https://", "http://www.", "http://"} {
		if strings.HasPrefix(studioURL, prefix) {
			if idx := strings.Index(studioURL[len(prefix):], "/"); idx >= 0 {
				return studioURL[:len(prefix)+idx]
			}
			return studioURL
		}
	}
	return "https://" + domain
}

func (s *Scraper) Run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := resolveBase(studioURL, s.cfg.Domain)
	listingURL := resolveListingURL(studioURL, base, s.cfg.ListingURL)
	scraper.Debugf(1, "%s: listing URL %s", s.cfg.SiteID, listingURL)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, base, ls, studioURL, opts.Delay)
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
		s.enqueuePages(ctx, base, listingURL, opts, out, work)
	}()

	wg.Wait()
}

// studioPathRe matches the shop's studio page, whose id is the only part of it
// worth keeping.
var studioPathRe = regexp.MustCompile(`/(\d+)/studio/`)

// resolveListingURL maps an operator URL onto the listing to walk.
//
// A `/{id}/studio/…` URL is **not** a listing: on the store domains it
// redirects to the store's front page, so passing it through scraped the
// store's newest scenes under the studio's name. The store serves that
// studio's scenes at `watch-streaming-video-by-scene.html?studio={id}`, which
// is what the id is rewritten to.
func resolveListingURL(studioURL, base, defaultListing string) string {
	if m := studioPathRe.FindStringSubmatch(studioURL); m != nil {
		return base + "/watch-streaming-video-by-scene.html?studio=" + m[1]
	}
	if strings.Contains(studioURL, "/scenes/") || strings.Contains(studioURL, "-scene") {
		return studioURL
	}
	return base + defaultListing
}

func (s *Scraper) enqueuePages(ctx context.Context, base, listingURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)

		pageURL := listingURL
		if page > 1 {
			sep := "?"
			if strings.Contains(listingURL, "?") {
				sep = "&"
			}
			pageURL = fmt.Sprintf("%s%spage=%d", listingURL, sep, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		scenes := ParseListingPage(body, base)
		if len(scenes) == 0 {
			return
		}

		if page == 1 {
			total := ExtractTotal(body)
			if total > 0 {
				scraper.Debugf(1, "%s: %d total scenes", s.cfg.SiteID, total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, ls := range scenes {
			if opts.KnownIDs[ls.ID] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- ls:
			case <-ctx.Done():
				return
			}
		}

		if !HasPagination(body) {
			return
		}

		if !HasNextPage(body, page) {
			return
		}
	}
}

type listingScene struct {
	ID         string
	URL        string
	Title      string
	Performers []string
	Duration   int
	Thumb      string
}

var (
	widgetRe    = regexp.MustCompile(`(?s)<article class="scene-widget[^"]*"\s*data-scene-id="(\d+)".*?</article>`)
	sceneLinkRe = regexp.MustCompile(`<a class="scene-title"\s+href="([^"]+)"`)
	titleRe     = regexp.MustCompile(`<a class="scene-title"[^>]*>\s*<h6>\s*(.*?)\s*</h6>`)
	performerRe = regexp.MustCompile(`<p class="scene-performer-names">\s*(.*?)\s*</p>`)
	durationRe  = regexp.MustCompile(`<p class="scene-length">\s*(\d+)\s*min`)
	thumbRe     = regexp.MustCompile(`data-src="(https://caps1cdn[^"]+)"`)
	// The two storefront templates render the result count differently, and one
	// alternation covering both could pair the opening of either with the
	// closing of the other. They are matched separately, newest template first.
	totalHeadingRe = regexp.MustCompile(`<h4>([\d,]+)\s+Results</h4>`)
	totalBoldRe    = regexp.MustCompile(`font-weight-bold">([\d,]+)</span>\s*Results`)
	paginationRe   = regexp.MustCompile(`class="pagination`)
	pageNumRe      = regexp.MustCompile(`[?&]page=(\d+)`)
)

func ParseListingPage(body []byte, base string) []listingScene {
	matches := widgetRe.FindAllSubmatch(body, -1)
	scenes := make([]listingScene, 0, len(matches))

	for _, m := range matches {
		block := string(m[0])
		id := string(m[1])

		ls := listingScene{ID: id}

		if sm := sceneLinkRe.FindStringSubmatch(block); sm != nil {
			href := sm[1]
			if strings.HasPrefix(href, "/") {
				href = base + href
			}
			ls.URL = href
		}

		if sm := titleRe.FindStringSubmatch(block); sm != nil {
			ls.Title = strings.TrimSpace(html.UnescapeString(sm[1]))
		}

		if sm := performerRe.FindStringSubmatch(block); sm != nil {
			raw := strings.TrimSpace(html.UnescapeString(sm[1]))
			for _, name := range strings.Split(raw, ",") {
				name = strings.TrimSpace(name)
				if name != "" {
					ls.Performers = append(ls.Performers, name)
				}
			}
		}

		if sm := durationRe.FindStringSubmatch(block); sm != nil {
			mins, _ := strconv.Atoi(sm[1])
			ls.Duration = mins * 60
		}

		if sm := thumbRe.FindStringSubmatch(block); sm != nil {
			ls.Thumb = sm[1]
		}

		scenes = append(scenes, ls)
	}
	return scenes
}

func ExtractTotal(body []byte) int {
	m := totalHeadingRe.FindSubmatch(body)
	if m == nil {
		m = totalBoldRe.FindSubmatch(body)
	}
	if m != nil {
		s := strings.ReplaceAll(string(m[1]), ",", "")
		n, _ := strconv.Atoi(s)
		return n
	}
	return 0
}

func HasPagination(body []byte) bool {
	return paginationRe.Match(body)
}

func HasNextPage(body []byte, current int) bool {
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > current {
			return true
		}
	}
	return false
}

// ---- detail page parsing ----

var (
	releaseDateRe = regexp.MustCompile(`(?s)Released:</span>\s*(.*?)(?:</div>|<br)`)
	tagsBlockRe   = regexp.MustCompile(`(?s)(?:Tags|Attributes):?\s*</(?:span|strong)>\s*(.*?)</div>`)
	tagLinkRe     = regexp.MustCompile(`>([^<]+)</a>`)
	studioRe      = regexp.MustCompile(`(?s)Studio:</span>\s*<span>(.*?)</span>`)
	seriesRe      = regexp.MustCompile(`(?s)Series:</span>\s*<a[^>]*>(.*?)</a>`)
	scenePriceRe  = regexp.MustCompile(`\$(\d+\.\d+)`)
	// The synopsis block is absent entirely on sites that publish no scene
	// text (Vouyer Media, Bob's Videos and the rest), so a miss here means the
	// site has nothing to say rather than that the parse broke.
	synopsisRe = regexp.MustCompile(`(?s)<div class="synopsis">(.*?)</div>`)
	tagStripRe = regexp.MustCompile(`<[^>]*>`)
)

type DetailData struct {
	Date        time.Time
	Tags        []string
	Studio      string
	Series      string
	Price       float64
	Description string
}

func ParseDetailPage(body []byte) DetailData {
	var d DetailData
	page := string(body)

	if m := releaseDateRe.FindStringSubmatch(page); m != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "January 2, 2006", "Jan 2, 2006"); err == nil {
			d.Date = t.UTC()
		}
	}

	if m := tagsBlockRe.FindStringSubmatch(page); m != nil {
		for _, tm := range tagLinkRe.FindAllStringSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(tm[1]))
			if tag != "" {
				d.Tags = append(d.Tags, tag)
			}
		}
	}

	if m := studioRe.FindStringSubmatch(page); m != nil {
		d.Studio = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := seriesRe.FindStringSubmatch(page); m != nil {
		d.Series = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := synopsisRe.FindStringSubmatch(page); m != nil {
		// The block wraps one or more <p>; strip them rather than keeping the
		// markup, and collapse the whitespace they leave behind.
		text := html.UnescapeString(tagStripRe.ReplaceAllString(m[1], " "))
		d.Description = strings.Join(strings.Fields(text), " ")
	}

	idx := strings.Index(page, "Buy This Scene")
	if idx > 0 {
		start := idx - 500
		if start < 0 {
			start = 0
		}
		ctx := page[start:idx]
		prices := scenePriceRe.FindAllStringSubmatch(ctx, -1)
		if len(prices) > 0 {
			d.Price, _ = strconv.ParseFloat(prices[len(prices)-1][1], 64)
		}
	}

	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, _ string, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:         ls.ID,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Title:      ls.Title,
		URL:        ls.URL,
		Duration:   ls.Duration,
		Performers: ls.Performers,
		Thumbnail:  ls.Thumb,
		Studio:     s.cfg.StudioName,
		ScrapedAt:  now,
	}

	if ls.URL != "" {
		body, err := s.fetchPage(ctx, ls.URL)
		if err != nil {
			return models.Scene{}, fmt.Errorf("detail %s: %w", ls.ID, err)
		}
		detail := ParseDetailPage(body)
		scene.Date = detail.Date
		scene.Tags = detail.Tags
		if detail.Studio != "" {
			scene.Studio = detail.Studio
		}
		if detail.Series != "" {
			scene.Series = detail.Series
		}
		scene.Description = detail.Description
		if detail.Price > 0 {
			scene.AddPrice(models.PriceSnapshot{
				Date:    now,
				Regular: detail.Price,
			})
		}
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL: url,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentChrome)
			h["Cookie"] = "AgeConfirmed=true"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

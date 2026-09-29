package maturefetish

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

const siteBase = "https://maturefetish.com"

type Scraper struct {
	client *http.Client
	// base is the site root. Overridable so offline tests can point every
	// fetch — listing *and* detail — at an httptest server. Without it the
	// detail fetches keep using the siteBase const and walk out of the test
	// onto the live site.
	base string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "maturefetish" }

func (s *Scraper) Patterns() []string {
	return []string{
		"maturefetish.com/en/updates",
		"maturefetish.com/en/model/{id}/{page}/{slug}",
		"maturefetish.com/en/niche/{id}/{page}/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?maturefetish\.com/en/(updates|model/\d+|niche/\d+)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type urlKind int

const (
	kindUpdates urlKind = iota
	kindModel
	kindNiche
)

var (
	modelURLRe = regexp.MustCompile(`/en/model/(\d+)`)
	nicheURLRe = regexp.MustCompile(`/en/niche/(\d+)`)
)

func classifyURL(u string) (urlKind, string) {
	if m := modelURLRe.FindStringSubmatch(u); m != nil {
		return kindModel, m[1]
	}
	if m := nicheURLRe.FindStringSubmatch(u); m != nil {
		return kindNiche, m[1]
	}
	return kindUpdates, ""
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	kind, id := classifyURL(studioURL)

	switch kind {
	case kindModel:
		s.runIDList(ctx, studioURL, studioURL, opts, out)
	case kindNiche:
		s.runPaginated(ctx, studioURL, opts, out, func(page int) string {
			return fmt.Sprintf("%s/en/niche/%s/%d", s.base, id, page)
		})
	default:
		s.runPaginated(ctx, studioURL, opts, out, func(page int) string {
			return fmt.Sprintf("%s/en/updates/%d", s.base, page)
		})
	}
}

// runPaginated walks paginated listing pages, extracts update IDs, and
// fetches each detail page via a worker pool.
func (s *Scraper) runPaginated(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, pageURL func(int) string) {
	work := make(chan string, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for updateID := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetailScene(ctx, studioURL, updateID)
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

	sentTotal := false
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}
		scraper.Debugf(1, "maturefetish: fetching page %d", page)

		body, err := s.fetch(ctx, pageURL(page))
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		ids := parseListingIDs(body)
		if len(ids) == 0 {
			break
		}

		if !sentTotal {
			sentTotal = true
			total := estimateTotal(body, len(ids))
			if total > 0 {
				scraper.Debugf(1, "maturefetish: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
				}
			}
		}

		cancelled := false
		hitKnown := false
		for _, id := range ids {
			if opts.KnownIDs[id] {
				hitKnown = true
				break
			}
			select {
			case work <- id:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				scraper.Debugf(1, "maturefetish: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}

		lastPage := parseLastPage(body)
		if lastPage > 0 && page >= lastPage {
			break
		}
	}

	close(work)
	wg.Wait()
}

// runIDList fetches a single page (model page), extracts all update IDs,
// and fetches detail pages via a worker pool.
func (s *Scraper) runIDList(ctx context.Context, studioURL string, pageURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetch(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	ids := parseListingIDs(body)
	if len(ids) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(ids)):
	case <-ctx.Done():
		return
	}

	work := make(chan string, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for updateID := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ferr := s.fetchDetailScene(ctx, studioURL, updateID)
				if ferr != nil {
					select {
					case out <- scraper.Error(ferr):
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

	cancelled := false
	for _, uid := range ids {
		if opts.KnownIDs[uid] {
			scraper.Debugf(1, "maturefetish: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
		select {
		case work <- uid:
		case <-ctx.Done():
			cancelled = true
		}
		if cancelled {
			break
		}
	}

	close(work)
	wg.Wait()
}

func (s *Scraper) fetch(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) fetchDetailScene(ctx context.Context, studioURL string, updateID string) (models.Scene, error) {
	now := time.Now().UTC()
	url := fmt.Sprintf("%s/en/update/%s", s.base, updateID)

	body, err := s.fetch(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("update %s: %w", updateID, err)
	}

	d := parseDetailPage(body)

	scene := models.Scene{
		ID:          updateID,
		SiteID:      "maturefetish",
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         url,
		Thumbnail:   d.thumbnail,
		Preview:     d.preview,
		Description: d.description,
		Performers:  d.performers,
		Tags:        d.tags,
		Duration:    d.duration,
		Date:        d.date,
		ScrapedAt:   now,
	}
	scene.AddPrice(models.PriceSnapshot{Date: now, IsFree: false})
	return scene, nil
}

// --- Listing page parsing ---

var updateIDRe = regexp.MustCompile(`/en/update/(\d+)`)

func parseListingIDs(body []byte) []string {
	matches := updateIDRe.FindAllSubmatch(body, -1)
	seen := make(map[string]bool, len(matches))
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		id := string(m[1])
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

var (
	updatesPageRe = regexp.MustCompile(`href="/en/updates/(\d+)"`)
	nichePageRe   = regexp.MustCompile(`href="/en/niche/\d+/(\d+)/`)
)

func parseLastPage(body []byte) int {
	maxPage := 0
	for _, re := range []*regexp.Regexp{updatesPageRe, nichePageRe} {
		for _, m := range re.FindAllSubmatch(body, -1) {
			n, _ := strconv.Atoi(string(m[1]))
			if n > maxPage {
				maxPage = n
			}
		}
	}
	return maxPage
}

func estimateTotal(body []byte, firstPageCount int) int {
	lastPage := parseLastPage(body)
	if lastPage > 0 {
		return lastPage * firstPageCount
	}
	return firstPageCount
}

// --- Detail page parsing ---

type detailPage struct {
	title       string
	thumbnail   string
	preview     string
	description string
	performers  []string
	tags        []string
	date        time.Time
	duration    int
}

var (
	titleRe   = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	posterRe  = regexp.MustCompile(`poster="(https?://[^"]*)"`)
	trailerRe = regexp.MustCompile(`(https?://l\.cdn\.mature\.nl/[^"]*?trailer[^"]*\.mp4[^"]*)`)
	// The date, runtime and photo count share one info line under the title.
	// Both values are read out of that block rather than the whole page: a
	// bare d-m-Y or m:ss pattern also matches sidebar promos and the player's
	// own markup, and whichever came first would win.
	infoLineRe  = regexp.MustCompile(`(?s)<div class="text-page-text-alt[^"]*">(.*?)</div>`)
	dateRe      = regexp.MustCompile(`(\d{1,2}-\d{1,2}-\d{4})`)
	durationRe  = regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?)`)
	modelLinkRe = regexp.MustCompile(`href="/en/model/\d+[^"]*">([^<]+)</a>`)
	nicheTagRe  = regexp.MustCompile(`href="/en/niche/[^"]*"[^>]*>([^<]+)</a>`)
	metaDescRe  = regexp.MustCompile(`<meta\s+name="description"\s+content="([^"]*)"`)
)

func parseDetailPage(body []byte) detailPage {
	d := detailPage{}

	if m := titleRe.FindSubmatch(body); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	if m := posterRe.FindSubmatch(body); m != nil {
		d.thumbnail = html.UnescapeString(string(m[1]))
	}
	if m := trailerRe.FindSubmatch(body); m != nil {
		d.preview = html.UnescapeString(string(m[1]))
	}
	if info := infoLineRe.FindSubmatch(body); info != nil {
		if m := dateRe.FindSubmatch(info[1]); m != nil {
			d.date = parseDate(string(m[1]))
		}
		if m := durationRe.FindSubmatch(info[1]); m != nil {
			d.duration = parseutil.ParseDurationColon(string(m[1]))
		}
	}

	for _, m := range modelLinkRe.FindAllSubmatch(body, -1) {
		name := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if name != "" {
			d.performers = appendUnique(d.performers, name)
		}
	}

	for _, m := range nicheTagRe.FindAllSubmatch(body, -1) {
		tag := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if tag != "" {
			d.tags = appendUnique(d.tags, tag)
		}
	}

	if m := metaDescRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	return d
}

// --- Helpers ---

func parseDate(s string) time.Time {
	t, err := time.Parse("2-1-2006", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

package faleno

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

const siteID = "faleno"

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?(falenogroup\.com|dahlia-av\.jp)(?:/|$)`)

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
		"falenogroup.com/",
		"falenogroup.com/work/",
		"falenogroup.com/makers/{slug}/",
		"dahlia-av.jp/",
		"dahlia-av.jp/work/",
		"dahlia-av.jp/actress/{name}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	makerRe   = regexp.MustCompile(`/(?:makers/([^/]+)|botan|noskn)/?$`)
	actressRe = regexp.MustCompile(`/actress/[^/]+/?$`)
)

func siteBase(studioURL string) string {
	if strings.Contains(studioURL, "dahlia-av.jp") {
		return "https://dahlia-av.jp"
	}
	return "https://falenogroup.com"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := siteBase(studioURL)

	if makerRe.MatchString(studioURL) || actressRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: scraping single page %s", siteID, studioURL)
		s.scrapeSinglePage(ctx, studioURL, base, opts, out)
	} else {
		scraper.Debugf(1, "%s: scraping paginated listing at %s", siteID, base)
		s.scrapeListing(ctx, studioURL, base, opts, out)
	}
}

func (s *Scraper) scrapeSinglePage(ctx context.Context, studioURL, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	pageURL := studioURL
	if !strings.HasPrefix(pageURL, "http") {
		pageURL = base + pageURL
	}

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		sendResult(ctx, out, scraper.Error(err))
		return
	}

	urls := parseWorkURLs(body, base)
	if len(urls) == 0 {
		return
	}

	scraper.Debugf(1, "%s: found %d works, fetching details", siteID, len(urls))
	sendResult(ctx, out, scraper.Progress(len(urls)))

	s.fetchDetails(ctx, studioURL, base, opts, out, urls)
}

func (s *Scraper) scrapeListing(ctx context.Context, studioURL, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	var allURLs []string
	totalPages := 0

	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}

		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		pageURL := base + "/work/"
		if page > 1 {
			pageURL += fmt.Sprintf("page/%d/", page)
		}
		scraper.Debugf(1, "%s: fetching listing page %d", siteID, page)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			// A sister site can intermittently return a WordPress 500 on its
			// /work/ listing while the rest of the site is up (observed on
			// dahlia-av.jp). Fall back to the work links on the homepage so
			// the scrape degrades gracefully instead of returning nothing.
			if page == 1 {
				if home, herr := s.fetchPage(ctx, base+"/"); herr == nil {
					if urls := parseWorkURLs(home, base); len(urls) > 0 {
						scraper.Debugf(1, "%s: /work/ listing failed (%v); using %d works from homepage", siteID, err, len(urls))
						allURLs = append(allURLs, urls...)
						break
					}
				}
			}
			sendResult(ctx, out, scraper.Error(err))
			return
		}

		urls := parseWorkURLs(body, base)
		if len(urls) == 0 {
			break
		}

		if page == 1 {
			totalPages = parseLastPage(body)
			if totalPages > 0 {
				total := totalPages * len(urls)
				scraper.Debugf(1, "%s: ~%d total works (%d pages)", siteID, total, totalPages)
				sendResult(ctx, out, scraper.Progress(total))
			}
		}

		allURLs = append(allURLs, urls...)

		if totalPages > 0 && page >= totalPages {
			break
		}
	}

	if len(allURLs) == 0 {
		return
	}

	scraper.Debugf(1, "%s: fetching %d detail pages", siteID, len(allURLs))
	s.fetchDetails(ctx, studioURL, base, opts, out, allURLs)
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL, base string, opts scraper.ListOpts, out chan<- scraper.SceneResult, urls []string) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(urls), workers)

	work := make(chan string)
	var wg sync.WaitGroup
	now := time.Now().UTC()

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for detailURL := range work {
				if ctx.Err() != nil {
					return
				}

				if !scraper.Pace(ctx, opts.Delay) {
					return
				}

				scene, err := s.fetchAndParseDetail(ctx, detailURL, studioURL, base, now)
				if err != nil {
					if !sendResult(ctx, out, scraper.Error(err)) {
						return
					}
					continue
				}
				if !sendResult(ctx, out, scraper.Scene(scene)) {
					return
				}
			}
		}()
	}

	// The /work/ listing is grouped by label, not by release date — live, its
	// first four entries ran 2026/09/04, 2026/08/28, 2026/08/14, 2026/09/11 —
	// so a stop at the first stored code would abort at an arbitrary position
	// and hide the rest permanently. Every page is walked before this point
	// anyway, so skipping known codes costs only their detail fetches.
	go func() {
		defer close(work)
		skipped := 0
		for _, u := range urls {
			code := strings.ToUpper(codeFromURL(u))
			if opts.KnownIDs[code] {
				skipped++
				continue
			}
			select {
			case work <- u:
			case <-ctx.Done():
				return
			}
		}
		if skipped > 0 {
			scraper.Debugf(1, "%s: skipped %d already-stored work(s)", siteID, skipped)
			sendResult(ctx, out, scraper.StoppedEarly())
		}
	}()

	wg.Wait()
}

func (s *Scraper) fetchAndParseDetail(ctx context.Context, detailURL, studioURL, _ string, now time.Time) (models.Scene, error) {
	// dahlia-av.jp intermittently emits a WordPress notice that flips the
	// HTTP status to 500 while still rendering the full work page (same
	// pattern as SexMex Pro). Use DoWithStatus so the body is parsed
	// regardless of status; bail only if no title could be extracted.
	body, status, err := s.fetchPageAnyStatus(ctx, detailURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", detailURL, err)
	}

	code := codeFromURL(detailURL)
	d := parseDetailPage(body)
	if d.title == "" {
		return models.Scene{}, fmt.Errorf("detail %s: HTTP %d with no parseable content", detailURL, status)
	}

	sc := models.Scene{
		ID:          strings.ToUpper(code),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         detailURL,
		Thumbnail:   d.cover,
		Duration:    d.duration,
		Date:        d.date,
		Performers:  d.performers,
		Director:    d.director,
		Studio:      d.maker,
		Description: d.description,
		Tags:        d.genres,
		ScrapedAt:   now,
	}

	return sc, nil
}

// HTML parsing.

var (
	workURLRe  = regexp.MustCompile(`href="(https?://[^"]+/works/[^"]+/)"`)
	lastPageRe = regexp.MustCompile(`class="last"[^>]+href="[^"]*page/(\d+)/"`)
	pagesRe    = regexp.MustCompile(`class='pages'>(\d+)\s*/\s*(\d+)</span>`)
)

func parseWorkURLs(body []byte, _ string) []string {
	seen := map[string]bool{}
	var urls []string
	for _, m := range workURLRe.FindAllSubmatch(body, -1) {
		u := string(m[1])
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}

func parseLastPage(body []byte) int {
	if m := lastPageRe.FindSubmatch(body); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	if m := pagesRe.FindSubmatch(body); m != nil {
		n, _ := strconv.Atoi(string(m[2]))
		return n
	}
	return 0
}

func codeFromURL(u string) string {
	u = strings.TrimSuffix(u, "/")
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

var (
	h1Re    = regexp.MustCompile(`<h1[^>]*>(.*?)</h1>`)
	coverRe = regexp.MustCompile(`(?s)<div class="box_works01_img">.*?<img[^>]+src="([^"]+)"`)
	descRe  = regexp.MustCompile(`(?s)<div class="box_works01_text">\s*<p>(.*?)</p>`)
	metaRe  = regexp.MustCompile(`(?s)<li class="clearfix"><span>([^<]+)</span>\s*<p>(.*?)</p>`)
	genreRe = regexp.MustCompile(`href=['"][^'"]*?/genre/[^'"]+['"][^>]*>([^<]+)<`)
)

type detailData struct {
	title       string
	cover       string
	description string
	performers  []string
	duration    int
	date        time.Time
	director    string
	maker       string
	genres      []string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := h1Re.FindSubmatch(body); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	if m := coverRe.FindSubmatch(body); m != nil {
		d.cover = string(m[1])
	}

	if m := descRe.FindSubmatch(body); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}

	for _, m := range metaRe.FindAllSubmatch(body, -1) {
		label := string(m[1])
		value := strings.TrimSpace(html.UnescapeString(string(m[2])))
		if value == "" {
			continue
		}
		switch label {
		case "出演女優":
			d.performers = splitPerformers(value)
		case "収録時間":
			d.duration = parseDurationMin(value)
		case "監督":
			d.director = value
		case "メーカー":
			d.maker = value
		case "配信開始日":
			if d.date.IsZero() {
				d.date = parseFalenoDate(value)
			}
		case "発売日":
			if d.date.IsZero() {
				d.date = parseFalenoDate(value)
			}
		}
	}

	seen := map[string]bool{}
	for _, m := range genreRe.FindAllSubmatch(body, -1) {
		g := strings.TrimSpace(html.UnescapeString(string(m[1])))
		if g == "" || g == "GENRE" || g == "ジャンル" || seen[g] {
			continue
		}
		seen[g] = true
		d.genres = append(d.genres, g)
	}

	return d
}

func splitPerformers(s string) []string {
	var result []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '、' || r == '　'
	}) {
		if v := strings.TrimSpace(p); v != "" {
			result = append(result, v)
		}
	}
	return result
}

var durationMinRe = regexp.MustCompile(`(\d+)分`)

func parseDurationMin(s string) int {
	if m := durationMinRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n * 60
	}
	return 0
}

var dateLayouts = []string{
	"2006/1/2",
	"2006/01/02",
}

func parseFalenoDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func sendResult(ctx context.Context, ch chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case ch <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// fetchPageAnyStatus fetches a page and returns its body and HTTP status
// without classifying non-2xx as an error. Used for dahlia-av.jp work pages,
// which render full content under a WordPress-error HTTP 500.
func (s *Scraper) fetchPageAnyStatus(ctx context.Context, url string) ([]byte, int, error) {
	resp, err := httpx.DoWithStatus(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	return body, resp.StatusCode, err
}

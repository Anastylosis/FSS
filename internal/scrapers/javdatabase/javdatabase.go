package javdatabase

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

// maxPages bounds the listing walk. The largest studio on the site is a few
// hundred pages; this is well above that and exists only so a misbehaving
// origin cannot spin.
const maxPages = 2000

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "javdatabase" }

func (s *Scraper) Patterns() []string {
	return []string{
		"javdatabase.com/studios/{slug}",
		"javdatabase.com/movies/",
		"javdatabase.com/uncensored/",
		"javdatabase.com/genres/{slug}",
		"javdatabase.com/series/{slug}",
		"javdatabase.com/idols/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?javdatabase\.com/(?:studios|movies|uncensored|genres|series|idols)/`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

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
				scene, err := s.fetchDetail(ctx, item, studioURL, opts.Delay)
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

		seen := map[string]bool{}

		// The walk already skips ids it has seen, but that alone does not end
		// it: an origin that clamps `/page/N/` back to page 1 would serve the
		// same items forever while the loop kept incrementing. A page with no
		// id the walk has not already emitted is that clamp, and ends it;
		// maxPages is the backstop for anything stranger.
		for page := 1; page <= maxPages; page++ {
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
			scraper.Debugf(1, "javdatabase: fetching page %d", page)

			pageURL := buildPageURL(studioURL, page)
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

			if page == 1 {
				total := parseTotal(body)
				if total > 0 {
					scraper.Debugf(1, "javdatabase: %d total scenes", total)
					select {
					case out <- scraper.Progress(total):
					case <-ctx.Done():
						return
					}
				}
			}

			fresh := 0
			for _, item := range items {
				if seen[item.id] {
					continue
				}
				seen[item.id] = true
				fresh++

				if opts.KnownIDs[item.id] {
					scraper.Debugf(1, "javdatabase: hit known ID, stopping early")
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

			if fresh == 0 {
				scraper.Debugf(1, "javdatabase: page %d repeated a page already seen, stopping", page)
				return
			}

			if page == 1 && !hasPagination(body) {
				return
			}

			lastPage := parseLastPage(body)
			if lastPage > 0 && page >= lastPage {
				return
			}
		}
	}()

	wg.Wait()
}

// ---- URL helpers ----

func buildPageURL(studioURL string, page int) string {
	base := strings.TrimRight(studioURL, "/")
	if page <= 1 {
		return base + "/"
	}
	return base + "/page/" + strconv.Itoa(page) + "/"
}

var slugRe = regexp.MustCompile(`/(?:movies|uncensored)/([^/]+)`)

func extractSlug(u string) string {
	if m := slugRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
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

// ---- listing parsing ----

type listingItem struct {
	id         string
	title      string
	url        string
	date       string
	thumb      string
	studio     string
	uncensored bool
}

var (
	pcardRe         = regexp.MustCompile(`(?s)<p class="display-6 pcard">\s*<a\s+href="([^"]+)"[^>]*>(.*?)</a>`)
	cardThumbRe     = regexp.MustCompile(`(?s)movie-cover-thumb.*?<img[^>]+src="([^"]+)"`)
	cardTitleDateRe = regexp.MustCompile(`(?s)mt-auto[^>]*>.*?<a[^>]*>\s*(.*?)\s*</a>\s*(\d{4}-\d{2}-\d{2})`)
	cardStudioRe    = regexp.MustCompile(`(?s)btn-primary btn-sm[^>]*>\s*<a[^>]+>\s*([^<]+?)\s*</a>`)
	lastPageRe      = regexp.MustCompile(`href="[^"]*?/page/(\d+)/?"[^>]*aria-label="Last Page"`)
	totalRe         = regexp.MustCompile(`name="twitter:data1"\s+content="([\d,]+)"`)
	paginationRe    = regexp.MustCompile(`aria-label="Last Page"`)
)

func parseListingPage(body []byte) []listingItem {
	locs := pcardRe.FindAllSubmatchIndex(body, -1)
	var items []listingItem

	for i, loc := range locs {
		url := string(body[loc[2]:loc[3]])
		label := string(body[loc[4]:loc[5]])

		if !strings.Contains(url, "/movies/") && !strings.Contains(url, "/uncensored/") {
			continue
		}

		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		chunk := body[loc[1]:end]

		item := parseCard(url, label, chunk)
		if item.id != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseCard(rawURL, label string, chunk []byte) listingItem {
	uncensored := strings.Contains(rawURL, "/uncensored/")

	item := listingItem{
		url:        rawURL,
		uncensored: uncensored,
	}

	if uncensored {
		item.id = extractSlug(rawURL)
	} else {
		item.id = strings.ToUpper(normalizeSpace(html.UnescapeString(stripTags(label))))
	}

	if m := cardThumbRe.FindSubmatch(chunk); m != nil {
		item.thumb = string(m[1])
	}

	if m := cardTitleDateRe.FindSubmatch(chunk); m != nil {
		item.title = normalizeSpace(html.UnescapeString(string(m[1])))
		item.date = string(m[2])
	}

	if m := cardStudioRe.FindSubmatch(chunk); m != nil {
		item.studio = normalizeSpace(html.UnescapeString(string(m[1])))
	}

	return item
}

func parseLastPage(body []byte) int {
	if m := lastPageRe.FindSubmatch(body); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	return 0
}

func parseTotal(body []byte) int {
	if m := totalRe.FindSubmatch(body); m != nil {
		s := strings.ReplaceAll(string(m[1]), ",", "")
		n, _ := strconv.Atoi(s)
		return n
	}
	return 0
}

func hasPagination(body []byte) bool {
	return paginationRe.Match(body)
}

// ---- detail parsing ----

type detailInfo struct {
	title      string
	date       string
	duration   int
	performers []string
	tags       []string
	studio     string
	director   string
	series     string
}

var (
	detailFieldRe = regexp.MustCompile(`(?s)<p class="mb-1"><b>([^<]+?):\s*</b>\s*(.*?)</p>`)
	tableRowRe    = regexp.MustCompile(`(?s)<td class="tablelabel"><b>([^<]+?):?\s*</b></td>\s*<td class="tablevalue"[^>]*>(.*?)</td>`)
	linkTextRe    = regexp.MustCompile(`>\s*([^<]+?)\s*</a>`)
	runtimeRe     = regexp.MustCompile(`(\d+)\s*min`)
	idolLinkRe    = regexp.MustCompile(`href="/idols/[^"]+/?"[^>]*>\s*([^<]+?)\s*</a>`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, item listingItem, studioURL string, delay time.Duration) (models.Scene, error) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return models.Scene{}, ctx.Err()
		}
	}

	body, err := s.fetchPage(ctx, item.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", item.id, err)
	}

	var info detailInfo
	if item.uncensored {
		info = parseUncensoredDetail(body)
	} else {
		info = parseCensoredDetail(body)
	}

	scene := models.Scene{
		ID:         item.id,
		SiteID:     "javdatabase",
		StudioURL:  studioURL,
		URL:        item.url,
		Thumbnail:  item.thumb,
		Performers: info.performers,
		Tags:       info.tags,
		Duration:   info.duration,
		Director:   info.director,
		Series:     info.series,
		ScrapedAt:  time.Now().UTC(),
	}

	scene.Title = info.title
	if scene.Title == "" {
		scene.Title = item.title
	}
	if scene.Title == "" {
		scene.Title = item.id
	}

	scene.Studio = info.studio
	if scene.Studio == "" {
		scene.Studio = item.studio
	}

	dateStr := info.date
	if dateStr == "" {
		dateStr = item.date
	}
	if dateStr != "" {
		if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			scene.Date = t.UTC()
		}
	}

	scene.AddPrice(models.PriceSnapshot{
		Date:   scene.ScrapedAt,
		IsFree: true,
	})

	return scene, nil
}

func parseCensoredDetail(body []byte) detailInfo {
	var info detailInfo

	for _, field := range detailFieldRe.FindAllSubmatch(body, -1) {
		label := strings.TrimSpace(string(field[1]))
		value := string(field[2])

		switch {
		case label == "Title":
			info.title = normalizeSpace(html.UnescapeString(stripTags(value)))
		case label == "Release Date":
			info.date = strings.TrimSpace(stripTags(value))
		case label == "Runtime":
			if m := runtimeRe.FindStringSubmatch(value); m != nil {
				mins, _ := strconv.Atoi(m[1])
				info.duration = mins * 60
			}
		case label == "Studio":
			if texts := extractLinkTexts(value); len(texts) > 0 {
				info.studio = texts[0]
			}
		case label == "Director":
			d := normalizeSpace(html.UnescapeString(stripTags(value)))
			if d != "" {
				info.director = d
			}
		case strings.HasPrefix(label, "Genre"):
			info.tags = extractLinkTexts(value)
		case strings.Contains(label, "Idol") || strings.Contains(label, "Actress"):
			info.performers = extractLinkTexts(value)
		case strings.Contains(label, "Series"):
			if texts := extractLinkTexts(value); len(texts) > 0 {
				info.series = texts[0]
			}
		}
	}

	return info
}

func parseUncensoredDetail(body []byte) detailInfo {
	var info detailInfo

	for _, row := range tableRowRe.FindAllSubmatch(body, -1) {
		label := strings.TrimSpace(string(row[1]))
		value := string(row[2])

		switch {
		case label == "Title":
			info.title = normalizeSpace(html.UnescapeString(stripTags(value)))
		case label == "Release Date":
			info.date = strings.TrimSpace(stripTags(value))
		case label == "Runtime":
			if m := runtimeRe.FindStringSubmatch(value); m != nil {
				mins, _ := strconv.Atoi(m[1])
				info.duration = mins * 60
			}
		case label == "Studio":
			if texts := extractLinkTexts(value); len(texts) > 0 {
				info.studio = texts[0]
			}
		case strings.HasPrefix(label, "Genre"):
			info.tags = extractLinkTexts(value)
		case label == "Series":
			if texts := extractLinkTexts(value); len(texts) > 0 {
				info.series = texts[0]
			}
		}
	}

	seen := map[string]bool{}
	for _, m := range idolLinkRe.FindAllSubmatch(body, -1) {
		name := normalizeSpace(html.UnescapeString(string(m[1])))
		if name != "" && !seen[name] {
			seen[name] = true
			info.performers = append(info.performers, name)
		}
	}

	return info
}

// ---- utilities ----

func extractLinkTexts(s string) []string {
	matches := linkTextRe.FindAllStringSubmatch(s, -1)
	var result []string
	seen := map[string]bool{}
	for _, m := range matches {
		text := normalizeSpace(html.UnescapeString(m[1]))
		if text != "" && !seen[text] {
			seen[text] = true
			result = append(result, text)
		}
	}
	return result
}

func stripTags(s string) string {
	return tagRe.ReplaceAllString(s, "")
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

package rawhole

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "rawhole"
	studioName = "Raw Hole"
	defaultURL = "https://www.rawhole.com"
	// The catalogue comes from the sitemap as one flat list, so the walk
	// invents its own pages: one chunk of detail fetches at a time.
	chunkSize      = 20
	defaultWorkers = 6
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), base: defaultURL}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"rawhole.com",
		"rawhole.com/free-videos.html",
		"rawhole.com/{category}/free-videos.html",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?rawhole\.com(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- discovery ----

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

var (
	videoPathRe = regexp.MustCompile(`(?i)/free-video/([^/"?]+)\.html`)
	categoryRe  = regexp.MustCompile(`(?i)^/([a-z0-9-]+)/free-videos\.html$`)
)

// videoURLs lists every scene page. `/free-videos.html` shows 24 cards and its
// `?page=` parameter is ignored — every page returns the same 24 — so the
// sitemap is the only complete listing the tour publishes.
func (s *Scraper) videoURLs(ctx context.Context) ([]string, error) {
	u := s.base + "/sitemap.xml"
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var set urlset
	if err := xml.Unmarshal(body, &set); err != nil {
		return nil, scraper.ParseError(u, err)
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range set.URLs {
		if !videoPathRe.MatchString(e.Loc) || seen[e.Loc] {
			continue
		}
		seen[e.Loc] = true
		out = append(out, e.Loc)
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(u, fmt.Errorf("sitemap names no scene pages"))
	}
	return out, nil
}

// categoryVideoURLs reads a `/{category}/free-videos.html` page. It is a single
// page with no pager, which is the whole of that category's public listing.
func (s *Scraper) categoryVideoURLs(ctx context.Context, pageURL string) ([]string, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range videoPathRe.FindAllStringSubmatch(string(body), -1) {
		u := s.base + "/free-video/" + m[1] + ".html"
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no scenes linked"))
	}
	return out, nil
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- run ----

func categoryPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	if categoryRe.MatchString(u.Path) {
		return u.Path
	}
	return ""
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var (
		urls []string
		err  error
	)
	if cat := categoryPath(studioURL); cat != "" {
		scraper.Debugf(1, "%s: scraping category %s", siteID, cat)
		urls, err = s.categoryVideoURLs(ctx, s.base+cat)
	} else {
		scraper.Debugf(1, "%s: scraping full catalogue from sitemap", siteID)
		urls, err = s.videoURLs(ctx)
	}
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("%s: %w", siteID, err)):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scene pages to fetch", siteID, len(urls))

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}

	// The sitemap is not in date order and the tour publishes no ordered feed,
	// so a stored id never means "everything after this is stored too" —
	// KnownIDs cannot stop this walk early and is deliberately not consulted.
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		start := (page - 1) * chunkSize
		if start >= len(urls) {
			return scraper.PageResult{Done: true}, nil
		}
		end := min(start+chunkSize, len(urls))
		scenes := s.fetchChunk(ctx, urls[start:end], studioURL, workers, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Total:    len(urls),
			Done:     end >= len(urls),
			Continue: true,
		}, nil
	})
}

func (s *Scraper) fetchChunk(ctx context.Context, urls []string, studioURL string, workers int, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(urls))
	errs := make([]error, len(urls))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(urls)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				body, err := s.get(ctx, urls[i])
				if err != nil {
					errs[i] = err
					continue
				}
				sc, err := parseScene(body, urls[i], studioURL)
				if err != nil {
					errs[i] = err
					continue
				}
				results[i] = sc
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range urls {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	var scenes []models.Scene
	for i, sc := range results {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("%s: %s: %w", siteID, urls[i], errs[i])):
			case <-ctx.Done():
				return scenes
			}
			continue
		}
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

// ---- parsing ----

var (
	h1Re       = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	descRe     = regexp.MustCompile(`(?is)<div class="description">(.*?)</div>`)
	addedRe    = regexp.MustCompile(`(?i)Added:\s*([A-Za-z]+\.? \d{1,2}, \d{4})`)
	lengthRe   = regexp.MustCompile(`(?i)Length:\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	catBlockRe = regexp.MustCompile(`(?is)<li class="list-group-item clearfix cat">(.*?)</li>`)
	catLinkRe  = regexp.MustCompile(`(?is)<a[^>]*>#?\s*(.*?)</a>`)
	modelRe    = regexp.MustCompile(`(?is)<div class="model-v">.*?<h1[^>]*>(.*?)</h1>`)
	ogImageRe  = regexp.MustCompile(`(?i)<meta property="og:image" content="([^"]+)"`)
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

func parseScene(body []byte, sceneURL, studioURL string) (*models.Scene, error) {
	h1s := h1Re.FindAllSubmatch(body, -1)
	if len(h1s) == 0 {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no <h1> on the page"))
	}
	title := cleanText(string(h1s[0][1]))
	if title == "" {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("empty title"))
	}

	sc := &models.Scene{
		ID:        slugOf(sceneURL),
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       sceneURL,
		ScrapedAt: time.Now().UTC(),
	}

	if m := descRe.FindSubmatch(body); m != nil {
		sc.Description = cleanText(string(m[1]))
	}
	if m := addedRe.FindSubmatch(body); m != nil {
		if t, ok := parseAPDate(string(m[1])); ok {
			sc.Date = t
		}
	}
	if m := lengthRe.FindSubmatch(body); m != nil {
		sc.Duration = parseutil.ParseDurationColon(string(m[1]))
	}
	if m := ogImageRe.FindSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(string(m[1]))
	}

	// The cast is rendered as one profile card per performer further down the
	// page, each headed by its own <h1> — which is why the title is taken from
	// the first <h1> specifically rather than the only one.
	seen := map[string]bool{}
	for _, m := range modelRe.FindAllSubmatch(body, -1) {
		name := cleanText(string(m[1]))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		sc.Performers = append(sc.Performers, name)
	}

	if m := catBlockRe.FindSubmatch(body); m != nil {
		seenCat := map[string]bool{}
		for _, c := range catLinkRe.FindAllSubmatch(m[1], -1) {
			name := strings.TrimPrefix(cleanText(string(c[1])), "#")
			if name == "" || seenCat[name] {
				continue
			}
			seenCat[name] = true
			sc.Categories = append(sc.Categories, name)
		}
	}
	return sc, nil
}

// apMonths maps the AP-style month names Django's `N` filter emits. Four of
// them are neither a full month name nor Go's three-letter form — "Sept." in
// particular parses under no Go layout — so the month is resolved by name
// before the day and year are handed to time.Parse.
var apMonths = map[string]time.Month{
	"jan": time.January, "january": time.January,
	"feb": time.February, "february": time.February,
	"mar": time.March, "march": time.March,
	"apr": time.April, "april": time.April,
	"may":  time.May,
	"jun":  time.June,
	"june": time.June,
	"jul":  time.July,
	"july": time.July,
	"aug":  time.August, "august": time.August,
	"sept": time.September, "sep": time.September, "september": time.September,
	"oct": time.October, "october": time.October,
	"nov": time.November, "november": time.November,
	"dec": time.December, "december": time.December,
}

var apDateRe = regexp.MustCompile(`(?i)^([A-Za-z]+)\.?\s+(\d{1,2}),\s*(\d{4})$`)

func parseAPDate(s string) (time.Time, bool) {
	m := apDateRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, false
	}
	mon, ok := apMonths[strings.ToLower(m[1])]
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("1 2, 2006", fmt.Sprintf("%d %s, %s", int(mon), m[2], m[3]))
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func slugOf(sceneURL string) string {
	if m := videoPathRe.FindStringSubmatch(sceneURL); m != nil {
		return m[1]
	}
	return sceneURL
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

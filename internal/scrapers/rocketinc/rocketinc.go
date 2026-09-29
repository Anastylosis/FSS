package rocketinc

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
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

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "rocketinc" }

func (s *Scraper) Patterns() []string {
	return []string{
		"rocket-inc.net/works/",
		"rocket-inc.net/works_actress/{slug}/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?rocket-inc\.net/(?:works/?$|works_actress/[^/]+)`)

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

	work := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for slug := range work {
				detailURL := buildDetailURL(studioURL, slug)
				scene, err := s.fetchDetail(ctx, studioURL, slug, detailURL, opts.Delay)
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
		baseURL := normalizeListURL(studioURL)
		for page := 1; ; page++ {
			if page > 1 && !scraper.Pace(ctx, opts.Delay) {
				return
			}
			scraper.Debugf(1, "rocketinc: fetching page %d", page)

			pageURL := buildPageURL(baseURL, page)
			body, err := s.fetchPage(ctx, pageURL)
			if err != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
				case <-ctx.Done():
				}
				return
			}

			slugs := parseListingSlugs(body)
			if len(slugs) == 0 {
				return
			}

			if page == 1 {
				maxPage := extractMaxPage(body)
				select {
				case out <- scraper.Progress(len(slugs) * maxPage):
				case <-ctx.Done():
					return
				}
			}

			for _, slug := range slugs {
				if opts.KnownIDs[slug] {
					scraper.Debugf(1, "rocketinc: hit known ID, stopping early")
					select {
					case out <- scraper.StoppedEarly():
					case <-ctx.Done():
					}
					return
				}
				select {
				case work <- slug:
				case <-ctx.Done():
					return
				}
			}

			if !hasNextPage(body) {
				return
			}
		}
	}()

	wg.Wait()
}

// ---- URL helpers ----

var pagePathRe = regexp.MustCompile(`/page/\d+/?$`)

func normalizeListURL(u string) string {
	u = pagePathRe.ReplaceAllString(u, "/")
	if !strings.HasSuffix(u, "/") {
		u += "/"
	}
	return u
}

func buildPageURL(baseURL string, page int) string {
	if page == 1 {
		return baseURL
	}
	return baseURL + "page/" + strconv.Itoa(page) + "/"
}

func buildDetailURL(studioURL, slug string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "https://rocket-inc.net/works/" + slug + "/"
	}
	u.Path = "/works/" + slug + "/"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// ---- listing page parsing ----

var (
	worksItemRe    = regexp.MustCompile(`(?s)<div[^>]*class="works-item"[^>]*>.*?</div>`)
	slugFromHrefRe = regexp.MustCompile(`href="[^"]*/works/([\w-]+)/"`)
)

func parseListingSlugs(body []byte) []string {
	blocks := worksItemRe.FindAll(body, -1)
	seen := map[string]bool{}
	var slugs []string
	for _, block := range blocks {
		m := slugFromHrefRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		slug := string(m[1])
		if seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs
}

var maxPageRe = regexp.MustCompile(`/page/(\d+)/`)

func extractMaxPage(body []byte) int {
	matches := maxPageRe.FindAllSubmatch(body, -1)
	best := 1
	for _, m := range matches {
		n, _ := strconv.Atoi(string(m[1]))
		if n > best {
			best = n
		}
	}
	return best
}

var nextPageRe = regexp.MustCompile(`class="next page-numbers"`)

func hasNextPage(body []byte) bool {
	return nextPageRe.Match(body)
}

// ---- detail page parsing ----

var (
	h1Re   = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	descRe = regexp.MustCompile(`(?s)class="[^"]*work-introduction[^"]*"[^>]*>.*?<p>(.*?)</p>`)

	actressFieldRe    = regexp.MustCompile(`(?s)女優名\s*</th>\s*<td[^>]*>(.*?)</td>`)
	directorFieldRe   = regexp.MustCompile(`(?s)監督名\s*</th>\s*<td[^>]*>(.*?)</td>`)
	genreFieldRe      = regexp.MustCompile(`(?s)ジャンル名\s*</th>\s*<td[^>]*>(.*?)</td>`)
	seriesFieldRe     = regexp.MustCompile(`(?s)シリーズ名\s*</th>\s*<td[^>]*>(.*?)</td>`)
	durationFieldRe   = regexp.MustCompile(`(?s)収録時間\s*</th>\s*<td[^>]*>\s*(\d+)分`)
	dvdDateFieldRe    = regexp.MustCompile(`(?s)DVD発売日\s*</th>\s*<td[^>]*>\s*(\d{4})年(\d{1,2})月(\d{1,2})日`)
	streamDateFieldRe = regexp.MustCompile(`(?s)先行配信日\s*</th>\s*<td[^>]*>\s*(\d{4})年(\d{1,2})月(\d{1,2})日`)
	linkTextRe        = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
)

func (s *Scraper) fetchDetail(ctx context.Context, studioURL, slug, detailURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	body, err := s.fetchPage(ctx, detailURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", slug, err)
	}

	return parseDetail(body, studioURL, slug, detailURL), nil
}

func parseDetail(body []byte, studioURL, slug, detailURL string) models.Scene {
	scene := models.Scene{
		ID:        slug,
		SiteID:    "rocketinc",
		StudioURL: studioURL,
		URL:       detailURL,
		Studio:    "Rocket",
		ScrapedAt: time.Now().UTC(),
	}

	if m := h1Re.FindSubmatch(body); m != nil {
		scene.Title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}

	og := parseutil.OpenGraph(body)
	if v := og["og:image"]; v != "" {
		scene.Thumbnail = v
	}

	if m := descRe.FindSubmatch(body); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(string(m[1])))
	} else if v := og["og:description"]; v != "" {
		scene.Description = html.UnescapeString(strings.TrimSpace(v))
	}

	scene.Performers = extractLinkTexts(actressFieldRe, body)
	if dirs := extractLinkTexts(directorFieldRe, body); len(dirs) > 0 {
		scene.Director = dirs[0]
	}
	scene.Tags = extractLinkTexts(genreFieldRe, body)
	if series := extractLinkTexts(seriesFieldRe, body); len(series) > 0 {
		scene.Series = series[0]
	}

	if m := durationFieldRe.FindSubmatch(body); m != nil {
		mins, _ := strconv.Atoi(string(m[1]))
		scene.Duration = mins * 60
	}

	if m := streamDateFieldRe.FindSubmatch(body); m != nil {
		scene.Date = parseJaDate(m[1], m[2], m[3])
	}
	if scene.Date.IsZero() {
		if m := dvdDateFieldRe.FindSubmatch(body); m != nil {
			scene.Date = parseJaDate(m[1], m[2], m[3])
		}
	}

	return scene
}

func extractLinkTexts(fieldRe *regexp.Regexp, body []byte) []string {
	m := fieldRe.FindSubmatch(body)
	if m == nil {
		return nil
	}
	tdContent := string(m[1])
	links := linkTextRe.FindAllStringSubmatch(tdContent, -1)
	var names []string
	for _, lm := range links {
		name := strings.TrimSpace(html.UnescapeString(lm[1]))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func parseJaDate(yb, mb, db []byte) time.Time {
	y, _ := strconv.Atoi(string(yb))
	mo, _ := strconv.Atoi(string(mb))
	d, _ := strconv.Atoi(string(db))
	if y == 0 || mo == 0 || d == 0 {
		return time.Time{}
	}
	return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

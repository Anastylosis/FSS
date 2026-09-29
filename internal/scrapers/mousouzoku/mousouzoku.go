package mousouzoku

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

const siteBase = "https://www.mousouzoku-av.com"

type Scraper struct {
	client  *http.Client
	baseURL string
}

func New() *Scraper {
	return &Scraper{
		client:  httpx.NewClient(30 * time.Second),
		baseURL: siteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "mousouzoku" }

func (s *Scraper) Patterns() []string {
	return []string{
		"mousouzoku-av.com",
		"mousouzoku-av.com/works/list/maker/{id}",
		"mousouzoku-av.com/works/detail/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?mousouzoku-av\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	makerPathRe = regexp.MustCompile(`/works/list/maker/(\d+)`)
	itemRe      = regexp.MustCompile(`(?s)<li class="c-list-works-item">(.*?)</li>`)
	itemHrefRe  = regexp.MustCompile(`href="/works/detail/([^/]+)/"`)
	totalRe     = regexp.MustCompile(`全([\d,]+)作品中`)

	titleRe    = regexp.MustCompile(`(?s)<h1 class="ttl-works">(.*?)</h1>`)
	descRe     = regexp.MustCompile(`(?s)<p class="tx-intro">(.*?)</p>`)
	durationRe = regexp.MustCompile(`(?s)収録時間.*?<p class="tx-info">(\d+)分</p>`)
	dateRe     = regexp.MustCompile(`/works/list/date/(\d{8})/`)
	makerRe    = regexp.MustCompile(`(?s)メーカー.*?<p class="tx-btn">(.*?)</p>`)
	perfDDRe   = regexp.MustCompile(`(?s)出演者.*?</dd>`)
	tagDDRe    = regexp.MustCompile(`(?s)ジャンル.*?</dd>`)
	btnTextRe  = regexp.MustCompile(`<p class="tx-btn">(.*?)</p>`)
	coverRe    = regexp.MustCompile(`src="(/contents/works/[^"]+pl\.jpg)[^"]*"`)
	datePathRe = regexp.MustCompile(`href="/works/list/date/(\d{8})/?"`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := makerPathRe.FindStringSubmatch(studioURL); m != nil {
		s.runPaginated(ctx, fmt.Sprintf("/works/list/maker/%s/", m[1]), opts, out)
		return
	}

	s.runDateArchive(ctx, opts, out)
}

func (s *Scraper) runDateArchive(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.fetch(ctx, "/works/date/")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("date archive: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	matches := datePathRe.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		return
	}

	seen := map[string]bool{}
	var dates []string
	for _, m := range matches {
		d := m[1]
		if !seen[d] {
			seen[d] = true
			dates = append(dates, "/works/list/date/"+d+"/")
		}
	}

	progressSent := false
	for i, datePath := range dates {
		if ctx.Err() != nil {
			return
		}
		if i > 0 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		stopped := s.runPaginatedInner(ctx, datePath, opts, out, &progressSent)
		if stopped {
			return
		}
	}
}

func (s *Scraper) runPaginated(ctx context.Context, basePath string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	progressSent := false
	s.runPaginatedInner(ctx, basePath, opts, out, &progressSent)
}

func (s *Scraper) runPaginatedInner(ctx context.Context, basePath string, opts scraper.ListOpts, out chan<- scraper.SceneResult, progressSent *bool) bool {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	stoppedEarly := false
	scraper.Paginate(ctx, opts, "mousouzoku", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pagePath := basePath
		if page > 1 {
			pagePath = strings.TrimRight(basePath, "/") + "/" + strconv.Itoa(page) + "/"
		}

		body, err := s.fetch(ctx, pagePath)
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("page %s: %w", pagePath, err)
		}

		var total int
		if !*progressSent {
			*progressSent = true
			total = parseTotal(string(body))
		}

		slugs := parseListingSlugs(string(body))
		if len(slugs) == 0 {
			return scraper.PageResult{}, nil
		}

		// Check KnownIDs before fetching details.
		var work []string
		hitKnown := false
		for _, slug := range slugs {
			if opts.KnownIDs[slug] {
				hitKnown = true
				break
			}
			work = append(work, slug)
		}

		results := s.fetchDetails(ctx, work, opts.Delay, workers)
		var scenes []models.Scene
		for _, r := range results {
			if r.err != nil {
				select {
				case out <- scraper.Error(r.err):
				case <-ctx.Done():
				}
				continue
			}
			scenes = append(scenes, r.scene)
		}

		if hitKnown {
			stoppedEarly = true
			// Return scenes collected before the known ID. Paginate will
			// send them, then see Done and stop. We handle StoppedEarly
			// ourselves since the known ID was on a slug, not a returned scene.
			scraper.Debugf(1, "mousouzoku: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return scraper.PageResult{Scenes: scenes, Total: total, Done: true}, nil
		}

		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
	return stoppedEarly
}

func parseListingSlugs(body string) []string {
	items := itemRe.FindAllStringSubmatch(body, -1)
	var slugs []string
	seen := map[string]bool{}
	for _, m := range items {
		href := itemHrefRe.FindStringSubmatch(m[1])
		if href == nil {
			continue
		}
		slug := href[1]
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

func parseTotal(body string) int {
	m := totalRe.FindStringSubmatch(body)
	if m == nil {
		return 0
	}
	s := strings.ReplaceAll(m[1], ",", "")
	n, _ := strconv.Atoi(s)
	return n
}

type sceneResult struct {
	scene models.Scene
	err   error
}

func (s *Scraper) fetchDetails(ctx context.Context, slugs []string, delay time.Duration, workers int) []sceneResult {
	results := make([]sceneResult, len(slugs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, slug := range slugs {
		if ctx.Err() != nil {
			// Breaking here stops launching goroutines, so every remaining slot
			// would keep its zero value — and the consumer reads `err == nil` as
			// success and appends an empty scene for each. Mark them instead.
			for j := i; j < len(slugs); j++ {
				results[j] = sceneResult{err: ctx.Err()}
			}
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, sl string) {
			defer wg.Done()
			defer func() { <-sem }()
			if !scraper.Pace(ctx, delay) {
				// Record the cancellation rather than returning silently.
				// results is index-addressed, so an unwritten slot stays a
				// zero value — and the consumer treats `err == nil` as
				// success, appending an empty models.Scene as if it were a
				// real scrape result.
				results[idx] = sceneResult{err: ctx.Err()}
				return
			}
			scene, err := s.fetchDetail(ctx, sl)
			if err != nil {
				results[idx] = sceneResult{err: fmt.Errorf("detail %s: %w", sl, err)}
				return
			}
			results[idx] = sceneResult{scene: scene}
		}(i, slug)
	}
	wg.Wait()
	return results
}

func (s *Scraper) fetchDetail(ctx context.Context, slug string) (models.Scene, error) {
	body, err := s.fetch(ctx, "/works/detail/"+slug+"/")
	if err != nil {
		return models.Scene{}, err
	}
	return parseDetail(slug, string(body), s.baseURL), nil
}

func parseDetail(slug, body, base string) models.Scene {
	sc := models.Scene{
		ID:        slug,
		SiteID:    "mousouzoku",
		StudioURL: base + "/works/list/release/",
		URL:       base + "/works/detail/" + slug + "/",
		ScrapedAt: time.Now().UTC(),
	}

	if m := titleRe.FindStringSubmatch(body); m != nil {
		sc.Title = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := durationRe.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(m[1])
		sc.Duration = n * 60
	}

	if m := dateRe.FindStringSubmatch(body); m != nil {
		if t, err := time.Parse("20060102", m[1]); err == nil {
			sc.Date = t.UTC()
		}
	}

	if m := makerRe.FindStringSubmatch(body); m != nil {
		sc.Studio = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	if m := perfDDRe.FindStringSubmatch(body); m != nil {
		for _, p := range btnTextRe.FindAllStringSubmatch(m[0], -1) {
			name := strings.TrimSpace(html.UnescapeString(p[1]))
			if name != "" && name != "-" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}

	if m := tagDDRe.FindStringSubmatch(body); m != nil {
		for _, t := range btnTextRe.FindAllStringSubmatch(m[0], -1) {
			tag := strings.TrimSpace(html.UnescapeString(t[1]))
			if tag != "" {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}

	if m := coverRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = base + m[1]
	}

	return sc
}

func (s *Scraper) fetch(ctx context.Context, path string) ([]byte, error) {
	u := path
	if !strings.HasPrefix(u, "http") {
		u = s.baseURL + u
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: u,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Cookie"] = "age_verification=off"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

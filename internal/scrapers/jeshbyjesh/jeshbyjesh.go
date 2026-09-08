// Package jeshbyjesh scrapes jeshbyjesh.com.
package jeshbyjesh

import (
	"context"
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
	siteID     = "jeshbyjesh"
	studioName = "Jesh by Jesh"
	defaultURL = "https://www.jeshbyjesh.com"
	tourPath   = "/tour"
	// maxPages backstops the walk. The listing clamps rather than 404s past
	// the end, so the real stop is a page with nothing new on it.
	maxPages       = 200
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
		"jeshbyjesh.com",
		"jeshbyjesh.com/tour/categories/movies_{N}_d.html",
		"jeshbyjesh.com/tour/series/{slug}.html",
		"jeshbyjesh.com/tour/categories/{slug}.html",
		"jeshbyjesh.com/tour/models/{slug}.html",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?jeshbyjesh\.com(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
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

// ---- listing ----

var (
	// The grid, the hero slider and the "you may also like" carousel all use
	// card-link, so a page carries more links than its grid holds. That is
	// harmless — every one of them is a real scene and the walk deduplicates —
	// but it is why the end of the catalogue is a page with nothing *new* on
	// it rather than a page with no links.
	cardLinkRe   = regexp.MustCompile(`(?is)<a\s+href="([^"]*/trailers/[^"]+\.html)"[^>]*class="card-link"`)
	filterPathRe = regexp.MustCompile(`(?i)^/tour/(?:series|categories|models)/[^/]+\.html$`)
	moviesPathRe = regexp.MustCompile(`(?i)^/tour/categories/movies(?:_\d+_[a-z])?\.html$`)
)

func (s *Scraper) listingURL(filter string, page int) string {
	if filter != "" {
		return s.base + filter
	}
	return fmt.Sprintf("%s%s/categories/movies_%d_d.html", s.base, tourPath, page)
}

// filterPath returns the single page to read for a series/category/model URL,
// or "" for the paged catalogue. Those pages list their whole set at once.
func filterPath(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	if moviesPathRe.MatchString(u.Path) {
		return ""
	}
	if filterPathRe.MatchString(u.Path) {
		return u.Path
	}
	return ""
}

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	filter := filterPath(studioURL)
	if filter != "" {
		scraper.Debugf(1, "%s: scraping %s", siteID, filter)
	} else {
		scraper.Debugf(1, "%s: scraping the full catalogue", siteID)
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > maxPages {
			scraper.Debugf(1, "%s: stopped at the %d-page cap", siteID, maxPages)
			return scraper.PageResult{Done: true}, nil
		}
		u := s.listingURL(filter, page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}

		var fresh []string
		for _, m := range cardLinkRe.FindAllSubmatch(body, -1) {
			ref := absURL(u, html.UnescapeString(string(m[1])))
			id := slugOf(ref)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			fresh = append(fresh, ref)
		}

		if len(fresh) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no scene cards on the first page"))
			}
			// Past the end the listing clamps back to a page already walked
			// rather than 404ing, so "nothing new" is the only end marker.
			scraper.Debugf(1, "%s: page %d repeated a page already walked, stopping", siteID, page)
			return scraper.PageResult{Done: true}, nil
		}

		scenes := s.fetchDetails(ctx, fresh, studioURL, workers, out)
		return scraper.PageResult{
			Scenes: scenes,
			Done:   filter != "",
		}, nil
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, urls []string, studioURL string, workers int, out chan<- scraper.SceneResult) []models.Scene {
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
				sc, err := s.parseScene(body, urls[i], studioURL)
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
	titleRe       = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	ogTitleRe     = regexp.MustCompile(`(?i)<meta property="og:title" content="([^"]*)"`)
	infoRe        = regexp.MustCompile(`(?is)<span class="update-info-title">\s*(.*?)\s*</span>\s*<span class="update-info-value[^"]*">(.*?)</span>`)
	seriesRe      = regexp.MustCompile(`(?is)<a[^>]+href="[^"]*/tour/series/[^"]+"[^>]*>(.*?)</a>`)
	tagRe         = regexp.MustCompile(`(?is)<a[^>]+href="[^"]*/tour/categories/[^"]+"[^>]*>(.*?)</a>`)
	modelNameRe   = regexp.MustCompile(`(?is)<span class="model-list-name">(.*?)</span>`)
	blogContentRe = regexp.MustCompile(`(?is)<div class="blog-content">(.*?)</div>`)
	descRe        = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	thumbRe       = regexp.MustCompile(`(?is)<img[^>]+class="[^"]*video_placeholder[^"]*"[^>]*\ssrc="([^"]+)"`)
	slugPathRe    = regexp.MustCompile(`(?i)/trailers/([^/?#]+)\.html`)
	tagStripRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe          = regexp.MustCompile(`\s+`)
)

func (s *Scraper) parseScene(body []byte, sceneURL, studioURL string) (*models.Scene, error) {
	id := slugOf(sceneURL)
	if id == "" {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no slug in the URL"))
	}
	title := ""
	if m := titleRe.FindSubmatch(body); m != nil {
		title = cleanText(string(m[1]))
	}
	if title == "" {
		if m := ogTitleRe.FindSubmatch(body); m != nil {
			title = cleanText(string(m[1]))
		}
	}
	if title == "" {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no title"))
	}

	sc := &models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       sceneURL,
		ScrapedAt: time.Now().UTC(),
	}

	// The meta strip is a list of labelled blocks rather than fixed fields, so
	// each is matched on its own label.
	for _, m := range infoRe.FindAllSubmatch(body, -1) {
		label := strings.ToUpper(cleanText(string(m[1])))
		value := m[2]
		switch {
		case strings.HasPrefix(label, "RELEASE DATE"):
			if t, err := parseutil.TryParseDate(cleanText(string(value)), "January 2, 2006"); err == nil {
				sc.Date = t.UTC()
			}
		case strings.HasPrefix(label, "SCENE LENGTH"):
			sc.Duration = parseutil.ParseDurationColon(cleanText(string(value)))
		case strings.HasPrefix(label, "TAGS"):
			sc.Tags = names(tagRe, value)
		}
	}
	// The series block carries no label of its own.
	if m := seriesRe.FindSubmatch(body); m != nil {
		sc.Series = cleanText(string(m[1]))
	}
	sc.Performers = names(modelNameRe, body)
	if m := thumbRe.FindSubmatch(body); m != nil {
		sc.Thumbnail = absURL(sceneURL, html.UnescapeString(string(m[1])))
	}
	sc.Description = description(body)
	return sc, nil
}

func names(re *regexp.Regexp, body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range re.FindAllSubmatch(body, -1) {
		n := cleanText(string(m[1]))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// description joins the scene copy, which sits in the page's one
// `blog-content` block as a run of bare <p> elements. Taking every long <p> on
// the page instead would pick up the members-only overlay, whose blurb clears
// any sensible length threshold.
func description(body []byte) string {
	block := blogContentRe.FindSubmatch(body)
	if block == nil {
		return ""
	}
	var paras []string
	for _, m := range descRe.FindAllSubmatch(block[1], -1) {
		if txt := cleanText(string(m[1])); txt != "" {
			paras = append(paras, txt)
		}
	}
	return strings.Join(paras, "\n\n")
}

func slugOf(sceneURL string) string {
	if m := slugPathRe.FindStringSubmatch(sceneURL); m != nil {
		return m[1]
	}
	return ""
}

func absURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(u).String()
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(html.UnescapeString(s))
	s = strings.ReplaceAll(s, " ", " ")
	s = strings.ReplaceAll(s, "•", " ")
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

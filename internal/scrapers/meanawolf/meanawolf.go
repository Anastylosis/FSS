// Package meanawolf scrapes Meana Wolf (meanawolf.com), a solo-creator site on
// the ElevatedX CMS. The /updates/page_{N}.html listing carries the scene URL
// and poster thumbnail on each card; the per-scene /scenes/{slug}_vids.html
// detail page carries the title, runtime, date, performers and categories.
package meanawolf

import (
	"context"
	"fmt"
	"html"
	"net/http"
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
	siteID     = "meanawolf"
	studioName = "Meana Wolf"
	siteBase   = "https://meanawolf.com"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?meanawolf\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// drive the whole walk instead of only the parsers.
	base string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"meanawolf.com",
		"meanawolf.com/updates/page_{N}.html",
		"meanawolf.com/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingScene struct {
	slug  string
	url   string
	thumb string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	scraper.Debugf(1, "meanawolf: fetching detail pages with %d workers", workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, ls, studioURL, opts.Delay)
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

	isModel := strings.Contains(studioURL, "/models/")

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		if isModel {
			s.enqueueModelPage(ctx, studioURL, opts, out, work)
		} else {
			s.enqueueListing(ctx, opts, out, work)
		}
	}()

	wg.Wait()
}

func (s *Scraper) enqueueListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "meanawolf: fetching page %d", page)
		pageURL := fmt.Sprintf("%s/updates/page_%d.html", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}
		scenes := parseListing(s.base, body)
		if len(scenes) == 0 {
			return
		}
		if !s.enqueue(ctx, scenes, opts, out, work) {
			return
		}
	}
}

func (s *Scraper) enqueueModelPage(ctx context.Context, modelURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	scraper.Debugf(1, "meanawolf: scraping model page %s", modelURL)
	body, err := s.fetchPage(ctx, modelURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scenes := parseListing(s.base, body)
	if len(scenes) == 0 {
		return
	}
	select {
	case out <- scraper.Progress(len(scenes)):
	case <-ctx.Done():
		return
	}
	s.enqueue(ctx, scenes, opts, out, work)
}

// enqueue sends scenes to the work channel, honouring KnownIDs early-stop.
// Returns false when the caller should stop paginating.
func (s *Scraper) enqueue(ctx context.Context, scenes []listingScene, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) bool {
	for _, ls := range scenes {
		if opts.KnownIDs[ls.slug] {
			scraper.Debugf(1, "meanawolf: hit known ID %s, stopping early", ls.slug)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return false
		}
		select {
		case work <- ls:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

var (
	cardRe   = regexp.MustCompile(`data-setid="\d+"`)
	sceneRe  = regexp.MustCompile(`href="((?:https?://[^"]*)?/?scenes/([A-Za-z0-9_-]+)_vids\.html)"`)
	posterRe = regexp.MustCompile(`data-videoposter="([^"]+)"`)

	h1Re        = regexp.MustCompile(`<h1[^>]*>([^<]+)</h1>`)
	runtimeRe   = regexp.MustCompile(`RUNTIME:</span>\s*([0-9:]+)`)
	featuredRe  = regexp.MustCompile(`FEATURED:</span>\s*([A-Za-z]+ \d{1,2}, \d{4})`)
	featuringRe = regexp.MustCompile(`(?s)FEATURING:</span>(.*?)</li>`)
	categoryRe  = regexp.MustCompile(`(?s)CATEGORIES:</span>(.*?)</li>`)
	modelLinkRe = regexp.MustCompile(`href="[^"]*models/[^"]*"[^>]*>([^<]+)</a>`)
	tagLinkRe   = regexp.MustCompile(`href="[^"]*categories/[^"]*"[^>]*>([^<]+)</a>`)
)

// parseListing extracts the scene URL + poster thumbnail from each card.
func parseListing(base string, body []byte) []listingScene {
	page := string(body)
	locs := cardRe.FindAllStringIndex(page, -1)
	scenes := make([]listingScene, 0, len(locs))
	seen := map[string]bool{}

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]

		m := sceneRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		slug := m[2]
		if seen[slug] {
			continue
		}
		seen[slug] = true

		ls := listingScene{slug: slug, url: absURL(base, m[1])}
		if pm := posterRe.FindStringSubmatch(block); pm != nil {
			ls.thumb = pm[1]
		}
		scenes = append(scenes, ls)
	}
	return scenes
}

type detailData struct {
	title      string
	duration   int
	date       time.Time
	performers []string
	tags       []string
}

func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)

	if m := h1Re.FindStringSubmatch(page); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := runtimeRe.FindStringSubmatch(page); m != nil {
		d.duration = parseutil.ParseDurationColon(m[1])
	}
	if m := featuredRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("January 2, 2006", strings.TrimSpace(m[1])); err == nil {
			d.date = t.UTC()
		}
	}
	if m := featuringRe.FindStringSubmatch(page); m != nil {
		for _, pm := range modelLinkRe.FindAllStringSubmatch(m[1], -1) {
			name := strings.TrimSpace(html.UnescapeString(pm[1]))
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}
	if m := categoryRe.FindStringSubmatch(page); m != nil {
		for _, tm := range tagLinkRe.FindAllStringSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(tm[1]))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	scene := models.Scene{
		ID:        ls.slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       ls.url,
		Thumbnail: ls.thumb,
		Studio:    studioName,
		ScrapedAt: time.Now().UTC(),
	}

	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.slug, err)
	}
	d := parseDetail(body)
	scene.Title = d.title
	scene.Duration = d.duration
	scene.Date = d.date
	scene.Performers = d.performers
	scene.Tags = d.tags
	if scene.Title == "" {
		scene.Title = slugToTitle(ls.slug)
	}
	return scene, nil
}

func slugToTitle(slug string) string {
	return strings.ReplaceAll(slug, "-", " ")
}

func absURL(base, u string) string {
	if strings.HasPrefix(u, "http") {
		return u
	}
	if strings.HasPrefix(u, "/") {
		return base + u
	}
	return base + "/" + u
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

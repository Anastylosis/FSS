package girlsoutwest

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

const (
	siteID   = "girlsoutwest"
	siteBase = "https://tour.girlsoutwest.com"
	pageSize = 16
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:tour\.|www\.)?girlsoutwest\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/models/([^/?#]+)\.html`)
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		Client: httpx.NewClient(30 * time.Second),
		base:   siteBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"girlsoutwest.com/",
		"girlsoutwest.com/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping model page %s", siteID, m[1])
		s.runModel(ctx, studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "%s: scraping main listing", siteID)
	s.runListing(ctx, studioURL, opts, out)
}

func (s *Scraper) runListing(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, ls, studioURL)
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
		s.enqueuePages(ctx, studioURL, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueuePages(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", siteID, page)

		u := s.base + "/categories/Movies.html"
		if page > 1 {
			u = fmt.Sprintf("%s/categories/Movies_%d_d.html", s.base, page)
		}

		body, err := s.fetchPage(ctx, u)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		scenes := parseListingPage(body, s.base)
		if len(scenes) == 0 {
			return
		}

		if page == 1 {
			maxPage := parseMaxPage(body)
			if maxPage > 0 {
				total := maxPage * len(scenes)
				scraper.Debugf(1, "%s: ~%d total scenes (%d pages)", siteID, total, maxPage)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}

		for _, ls := range scenes {
			if opts.KnownIDs != nil && opts.KnownIDs[ls.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, ls.id)
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

		if len(scenes) < pageSize {
			return
		}
	}
}

func (s *Scraper) runModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	modelName := parseModelName(body)
	modelID := parseModelID(body)
	scenes := parseListingPage(body, s.base)
	scraper.Debugf(1, "%s: model %s, page 1: %d scenes", siteID, modelName, len(scenes))

	if modelID != "" {
		for page := 2; ; page++ {
			if ctx.Err() != nil {
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}

			u := fmt.Sprintf("%s/sets.php?id=%s&page=%d&sw=&s=", s.base, modelID, page)
			body, err := s.fetchPage(ctx, u)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}

			pageScenes := parseListingPage(body, s.base)
			if len(pageScenes) == 0 {
				break
			}
			scraper.Debugf(1, "%s: model %s, page %d: %d scenes", siteID, modelName, page, len(pageScenes))
			scenes = append(scenes, pageScenes...)
		}
	}

	if len(scenes) > 0 {
		select {
		case out <- scraper.Progress(len(scenes)):
		case <-ctx.Done():
			return
		}
	}

	for _, ls := range scenes {
		if opts.KnownIDs != nil && opts.KnownIDs[ls.id] {
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		scene := models.Scene{
			ID:         ls.id,
			SiteID:     siteID,
			StudioURL:  studioURL,
			Title:      ls.title,
			URL:        ls.url,
			Studio:     "Girls Out West",
			Thumbnail:  ls.thumb,
			Duration:   ls.duration,
			Date:       ls.date,
			Performers: ls.performers,
			Likes:      ls.rating,
			ScrapedAt:  now,
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: defaultHeaders(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string) (models.Scene, error) {
	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.id, err)
	}

	det := parseDetailPage(body)

	scene := models.Scene{
		ID:         ls.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      ls.title,
		URL:        ls.url,
		Studio:     "Girls Out West",
		Thumbnail:  ls.thumb,
		Duration:   ls.duration,
		Date:       ls.date,
		Performers: ls.performers,
		Likes:      ls.rating,
		ScrapedAt:  time.Now().UTC(),
	}

	if det.description != "" {
		scene.Description = det.description
	}
	if len(det.tags) > 0 {
		scene.Tags = det.tags
	}
	if len(det.performers) > 0 {
		scene.Performers = det.performers
	}
	if det.duration > 0 && scene.Duration == 0 {
		scene.Duration = det.duration
	}
	if !det.date.IsZero() && scene.Date.IsZero() {
		scene.Date = det.date
	}

	return scene, nil
}

func defaultHeaders() map[string]string {
	return httpx.BrowserHeaders(httpx.UserAgentFirefox)
}

// ---- listing page parsing ----

type listingScene struct {
	id         string
	url        string
	title      string
	thumb      string
	duration   int
	date       time.Time
	performers []string
	rating     int
}

var (
	sceneStartRe = regexp.MustCompile(`<div class="iLatestScene">`)
	titleLinkRe  = regexp.MustCompile(`<a href="([^"]*?/trailers/[^"]+\.html)"[^>]*title="([^"]*)"`)
	featuringRe  = regexp.MustCompile(`(?s)<div class="featuring">Featuring:\s*(.*?)</div>`)
	modelLinkRe  = regexp.MustCompile(`<a href="[^"]*?/models/[^"]+">([^<]+)</a>`)
	clockRe      = regexp.MustCompile(`fa-clock"></i>\s*(\d+:\d+(?::\d+)?)`)
	calendarRe   = regexp.MustCompile(`fa-calendar"></i>\s*([^<]+)`)
	starRatingRe = regexp.MustCompile(`fa-star"></i>\s*([\d.]+)`)
	thumbRe      = regexp.MustCompile(`(?:poster|src)="([^"]+contentthumbs/[^"]+)"`)
	pageHrefRe   = regexp.MustCompile(`_(\d+)_d\.html`)
)

func parseListingPage(body []byte, base string) []listingScene {
	page := string(body)
	locs := sceneStartRe.FindAllStringIndex(page, -1)
	var scenes []listingScene

	for i, loc := range locs {
		start := loc[0]
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[start:end]
		ls := parseListingEntry(block, base)
		if ls.url == "" || ls.id == "" {
			continue
		}
		scenes = append(scenes, ls)
	}
	return scenes
}

func parseListingEntry(block, base string) listingScene {
	var ls listingScene

	if m := titleLinkRe.FindStringSubmatch(block); m != nil {
		href := m[1]
		if !strings.HasPrefix(href, "http") {
			href = base + href
		}
		ls.url = href
		ls.title = strings.TrimSpace(html.UnescapeString(m[2]))
	}

	if !strings.Contains(ls.url, "/trailers/") {
		return listingScene{}
	}

	ls.id = extractSlugID(ls.url)
	if ls.id == "" {
		return listingScene{}
	}

	if m := featuringRe.FindStringSubmatch(block); m != nil {
		for _, pm := range modelLinkRe.FindAllStringSubmatch(m[1], -1) {
			name := strings.TrimSpace(html.UnescapeString(pm[1]))
			if name != "" {
				ls.performers = append(ls.performers, name)
			}
		}
	}

	if m := clockRe.FindStringSubmatch(block); m != nil {
		ls.duration = parseutil.ParseDurationColon(strings.TrimSpace(m[1]))
	}

	if m := calendarRe.FindStringSubmatch(block); m != nil {
		ls.date = parseDate(strings.TrimSpace(m[1]))
	}

	if m := starRatingRe.FindStringSubmatch(block); m != nil {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			ls.rating = int(f * 10)
		}
	}

	if m := thumbRe.FindStringSubmatch(block); m != nil {
		thumb := m[1]
		if strings.HasPrefix(thumb, "/") {
			thumb = base + thumb
		}
		ls.thumb = thumb
	}

	return ls
}

var slugIDRe = regexp.MustCompile(`/trailers/([^/.]+)\.html`)

func extractSlugID(u string) string {
	if m := slugIDRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("Jan 2, 2006", s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("January 2, 2006", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func parseMaxPage(body []byte) int {
	page := string(body)
	maxPage := 1
	for _, pm := range pageHrefRe.FindAllStringSubmatch(page, -1) {
		n, _ := strconv.Atoi(pm[1])
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// ---- detail page parsing ----

type detailData struct {
	description string
	date        time.Time
	duration    int
	tags        []string
	performers  []string
}

var (
	detDescRe    = regexp.MustCompile(`(?s)<div class="description">\s*<h4>Description</h4>\s*<p>(.*?)</p>`)
	detDateRe    = regexp.MustCompile(`(?s)<h5>Added:</h5>\s*<p>([^<]+)</p>`)
	detDurRe     = regexp.MustCompile(`(?s)<h5>Runtime:</h5>\s*(\S+)`)
	detTagSecRe  = regexp.MustCompile(`(?s)<h5>Tags:</h5>\s*<ul[^>]*>(.*?)</ul>`)
	detTagRe     = regexp.MustCompile(`<a href="[^"]*categories/[^"]*">(?:<i[^>]*></i>)?([^<]+)</a>`)
	detFeatSecRe = regexp.MustCompile(`(?s)<h5>Featuring:</h5>\s*<ul[^>]*>(.*?)</ul>`)
	detModelRe   = regexp.MustCompile(`<a href="[^"]*models/[^"]*">(?:<i[^>]*></i>)?([^<]+)</a>`)
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
)

func parseDetailPage(body []byte) detailData {
	var d detailData
	page := string(body)

	if m := detDescRe.FindStringSubmatch(page); m != nil {
		raw := htmlTagRe.ReplaceAllString(m[1], " ")
		d.description = strings.Join(strings.Fields(html.UnescapeString(raw)), " ")
	}

	if m := detDateRe.FindStringSubmatch(page); m != nil {
		d.date = parseDate(strings.TrimSpace(m[1]))
	}

	if m := detDurRe.FindStringSubmatch(page); m != nil {
		d.duration = parseutil.ParseDurationColon(strings.TrimSpace(m[1]))
	}

	if m := detTagSecRe.FindStringSubmatch(page); m != nil {
		for _, tm := range detTagRe.FindAllStringSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(tm[1]))
			if tag != "" {
				d.tags = append(d.tags, tag)
			}
		}
	}

	if m := detFeatSecRe.FindStringSubmatch(page); m != nil {
		for _, pm := range detModelRe.FindAllStringSubmatch(m[1], -1) {
			name := strings.TrimSpace(html.UnescapeString(pm[1]))
			if name != "" {
				d.performers = append(d.performers, name)
			}
		}
	}

	return d
}

// ---- model page helpers ----

var (
	modelNameRe = regexp.MustCompile(`(?s)<div class="bioInfo">.*?<h1>([^<]+)</h1>`)
	modelIDRe   = regexp.MustCompile(`sets\.php\?id=(\d+)`)
)

func parseModelName(body []byte) string {
	if m := modelNameRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	return ""
}

func parseModelID(body []byte) string {
	if m := modelIDRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

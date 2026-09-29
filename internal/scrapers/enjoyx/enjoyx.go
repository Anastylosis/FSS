// Package enjoyx scrapes EnjoyX (enjoyx.com), a European studio site whose
// listing is plain HTML but whose detail pages carry a complete schema.org
// VideoObject — name, description, uploadDate, duration, thumbnail, actor[]
// and genre[] — so every field comes from the JSON-LD rather than markup.
//
// The catalogue is /video?page=N, 13 per page, and a page past the end comes
// back with no cards at all. Model pages (/model/{slug}) serve the same card
// markup, so they paginate identically.
package enjoyx

import (
	"context"
	"fmt"
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
	siteID     = "enjoyx"
	studioName = "EnjoyX"
	siteBase   = "https://enjoyx.com"
	// defaultWorkers is how many detail pages are fetched at once when the
	// operator names no number.
	defaultWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?enjoyx\.com(?:/|$)`)
	// modelRe matches on the path alone: MatchesURL has already established the
	// host, and a host-anchored pattern here would not recognise the model URL
	// an offline test drives through its own server.
	modelRe = regexp.MustCompile(`/model/([\w-]+)`)
	// cardRe pulls scene slugs out of listing markup. `best` and `new` are
	// sort tabs living at the same path depth, not scenes.
	cardRe = regexp.MustCompile(`href="(?:https://enjoyx\.com)?/video/([a-z0-9][a-z0-9-]*)"`)
)

// sortTabs are /video/{name} links that are not scenes.
var sortTabs = map[string]bool{"best": true, "new": true, "popular": true}

type Scraper struct {
	client *http.Client
	// base is the site root used for fetches, a field so offline tests drive
	// the whole walk rather than only the parsers.
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
		"enjoyx.com",
		"enjoyx.com/video",
		"enjoyx.com/model/{slug}",
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

	listPath := "/video"
	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		listPath = "/model/" + m[1]
		scraper.Debugf(1, "enjoyx: scraping model page %s", m[1])
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", s.base, listPath, page)
		slugs, err := s.fetchSlugs(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(slugs) == 0 {
			return scraper.PageResult{}, nil
		}
		scraper.Debugf(1, "enjoyx: page %d has %d scenes, fetching details with %d workers", page, len(slugs), workers)
		return scraper.PageResult{Scenes: s.fetchDetails(ctx, slugs, studioURL, workers, opts.Delay, now, out)}, nil
	})
}

// fetchSlugs returns the scene slugs on one listing page, in page order.
func (s *Scraper) fetchSlugs(ctx context.Context, pageURL string) ([]string, error) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var slugs []string
	for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
		slug := m[1]
		if sortTabs[slug] || seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs, nil
}

// fetchDetails resolves each slug to a scene, keeping the listing's order so a
// KnownIDs early-stop still sees the newest scenes first. A detail page that
// fails is reported and skipped rather than aborting the page.
func (s *Scraper) fetchDetails(ctx context.Context, slugs []string, studioURL string, workers int, delay time.Duration, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(slugs))
	var wg sync.WaitGroup
	work := make(chan int)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				if !scraper.Pace(ctx, delay) {
					return
				}
				scene, err := s.fetchScene(ctx, slugs[idx], studioURL, now)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				results[idx] = scene
			}
		}()
	}

	for i := range slugs {
		select {
		case work <- i:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return nil
		}
	}
	close(work)
	wg.Wait()

	scenes := make([]models.Scene, 0, len(slugs))
	for _, sc := range results {
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

func (s *Scraper) fetchScene(ctx context.Context, slug, studioURL string, now time.Time) (*models.Scene, error) {
	sceneURL := s.base + "/video/" + slug
	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return nil, err
	}
	vo := parseutil.ExtractVideoObject([]byte(body))
	if vo == nil {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no VideoObject on the page"))
	}
	scene := toScene(*vo, slug, studioURL, now)
	// The public address is the site's own, never the test server's.
	scene.URL = siteBase + "/video/" + slug
	return &scene, nil
}

func toScene(vo parseutil.VideoObject, slug, studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          slug,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       strings.TrimSpace(vo.Name),
		URL:         vo.URL,
		Studio:      studioName,
		Description: strings.TrimSpace(vo.Description),
		Thumbnail:   vo.ThumbnailURL,
		Duration:    parseutil.ParseDurationISO(vo.Duration),
		Performers:  vo.Actors,
		Tags:        tagsFor(vo),
		ScrapedAt:   now,
	}
	if scene.Title == "" {
		scene.Title = slug
	}
	published := vo.UploadDate
	if published == "" {
		published = vo.DatePublished
	}
	if t, err := parseutil.TryParseDate(published, time.RFC3339, "2006-01-02"); err == nil {
		scene.Date = t.UTC()
	}
	return scene
}

// tagsFor prefers genre[] over keywords[]: EnjoyX publishes its tag taxonomy as
// genre, while keywords is that list with the performers mixed in.
func tagsFor(vo parseutil.VideoObject) []string {
	if len(vo.Genres) > 0 {
		return vo.Genres
	}
	var tags []string
	for _, k := range strings.Split(vo.Keywords, ",") {
		if k = strings.TrimSpace(k); k != "" {
			tags = append(tags, k)
		}
	}
	return tags
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

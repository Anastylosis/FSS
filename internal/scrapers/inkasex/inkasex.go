// Package inkasex scrapes InkaSex (inkasex.com), a Spanish-language PlayTube
// site. See docs/scrapers.md.
package inkasex

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
	siteID         = "inkasex"
	studioName     = "InkaSex"
	siteBase       = "https://www.inkasex.com"
	defaultWorkers = 4
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:[a-z]{2}\.|www\.)?inkasex\.com(?:/|$)`)
	sceneRe    = regexp.MustCompile(`href="[^"]*?/video/([^"/]+)\.html"`)
	categoryRe = regexp.MustCompile(`/videos/category/([a-z][a-z0-9_-]*)`)
	listPathRe = regexp.MustCompile(`/videos/(latest|top|trending)`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"inkasex.com",
		"inkasex.com/videos/latest",
		"inkasex.com/videos/category/{slug}",
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

	listPath := "/videos/latest"
	if m := categoryRe.FindStringSubmatch(studioURL); m != nil {
		listPath = "/videos/category/" + m[1]
		scraper.Debugf(1, "inkasex: scraping category %s", m[1])
	} else if m := listPathRe.FindStringSubmatch(studioURL); m != nil {
		listPath = "/videos/" + m[1]
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page_id=%d", s.base, listPath, page)
		slugs, err := s.fetchSlugs(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(slugs) == 0 {
			return scraper.PageResult{}, nil
		}
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
	for _, m := range sceneRe.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		slugs = append(slugs, m[1])
	}
	return slugs, nil
}

// fetchDetails resolves each slug to a scene, preserving listing order. A page
// that fails is reported and skipped rather than ending the walk.
func (s *Scraper) fetchDetails(ctx context.Context, slugs []string, studioURL string, workers int, delay time.Duration, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(slugs))
	work := make(chan int)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				if delay > 0 {
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						return
					}
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
	pageURL := s.base + "/video/" + slug + ".html"
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	vo := parseutil.ExtractVideoObject([]byte(body))
	if vo == nil {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no VideoObject on the page"))
	}
	scene := toScene(*vo, slug, studioURL, body, now)
	scene.URL = siteBase + "/video/" + slug + ".html"
	return &scene, nil
}

func toScene(vo parseutil.VideoObject, slug, studioURL, body string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          sceneID(slug),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       strings.TrimSpace(vo.Name),
		URL:         vo.URL,
		Description: strings.TrimSpace(vo.Description),
		Thumbnail:   vo.ThumbnailURL,
		Duration:    parseutil.ParseDurationISO(vo.Duration),
		Performers:  vo.Actors,
		Categories:  categories(body),
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

// sceneID keeps the trailing PlayTube id, which is stable across a rename; the
// slug in front of it is the title and moves.
func sceneID(slug string) string {
	if i := strings.LastIndex(slug, "_"); i > 0 && i < len(slug)-1 {
		return slug[i+1:]
	}
	return slug
}

// categories reads the named category links; the slug separates words with
// underscores, and the theme also links categories by numeric id.
func categories(body string) []string {
	seen := map[string]bool{}
	var out []string
	replacer := strings.NewReplacer("_", " ", "-", " ")
	for _, m := range categoryRe.FindAllStringSubmatch(body, -1) {
		name := replacer.Replace(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
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

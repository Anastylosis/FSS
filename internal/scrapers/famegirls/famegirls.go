// Package famegirls scrapes Famegirls (famegirls.net), a KVS-style tube whose
// detail pages carry OpenGraph video metadata. See docs/scrapers.md.
package famegirls

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
	siteID         = "famegirls"
	studioName     = "Famegirls"
	siteBase       = "https://famegirls.net"
	defaultWorkers = 4
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?famegirls\.net(?:/|$)`)
	sceneRe    = regexp.MustCompile(`/videos/(\d+)/([a-z0-9-]+)/`)
	modelRe    = regexp.MustCompile(`/models/([a-z][a-z0-9-]*)/`)
	titleRe    = regexp.MustCompile(`<meta property="og:title" content="([^"]*)"`)
	descRe     = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
	imageRe    = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	releaseRe  = regexp.MustCompile(`<meta property="video:release_date" content="([^"]*)"`)
	durationRe = regexp.MustCompile(`<meta property="video:duration" content="(\d+)"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
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
	return []string{"famegirls.net", "famegirls.net/videos/", "famegirls.net/models/{slug}/"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type sceneRef struct{ id, slug string }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listPath := "/videos"
	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		listPath = "/models/" + m[1]
		scraper.Debugf(1, "famegirls: scraping model %s", m[1])
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		refs, err := s.fetchRefs(ctx, fmt.Sprintf("%s%s/%d/", s.base, listPath, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		fresh := refs[:0]
		for _, r := range refs {
			if seen[r.id] {
				continue
			}
			seen[r.id] = true
			fresh = append(fresh, r)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{}, nil
		}
		return scraper.PageResult{Scenes: s.fetchDetails(ctx, fresh, studioURL, workers, opts.Delay, now, out)}, nil
	})
}

func (s *Scraper) fetchRefs(ctx context.Context, pageURL string) ([]sceneRef, error) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var refs []sceneRef
	for _, m := range sceneRe.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		refs = append(refs, sceneRef{id: m[1], slug: m[2]})
	}
	return refs, nil
}

func (s *Scraper) fetchDetails(ctx context.Context, refs []sceneRef, studioURL string, workers int, pause time.Duration, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(refs))
	work := make(chan int)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range work {
				if pause > 0 {
					select {
					case <-time.After(pause):
					case <-ctx.Done():
						return
					}
				}
				pageURL := fmt.Sprintf("%s/videos/%s/%s/", s.base, refs[idx].id, refs[idx].slug)
				body, err := s.fetchPage(ctx, pageURL)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				scene, err := parseScene(body, refs[idx], studioURL, now)
				if err != nil {
					select {
					case out <- scraper.Error(scraper.ParseError(pageURL, err)):
					case <-ctx.Done():
						return
					}
					continue
				}
				results[idx] = &scene
			}
		}()
	}
	for i := range refs {
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

	scenes := make([]models.Scene, 0, len(refs))
	for _, sc := range results {
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

// parseScene reads the OpenGraph video block: `video:release_date` and
// `video:duration` are the only precise date and runtime on the page — the
// visible ones are a relative "Added" string and a formatted clock.
func parseScene(body string, ref sceneRef, studioURL string, now time.Time) (models.Scene, error) {
	title := ""
	if m := titleRe.FindStringSubmatch(body); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, fmt.Errorf("no og:title on the page")
	}

	scene := models.Scene{
		ID:        ref.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       fmt.Sprintf("%s/videos/%s/%s/", siteBase, ref.id, ref.slug),
		ScrapedAt: now,
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := imageRe.FindStringSubmatch(body); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}
	if m := releaseRe.FindStringSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(m[1], time.RFC3339, "2006-01-02"); err == nil {
			scene.Date = t.UTC()
		}
	}
	if m := durationRe.FindStringSubmatch(body); m != nil {
		if secs, err := strconv.Atoi(m[1]); err == nil {
			scene.Duration = secs
		}
	}
	seen := map[string]bool{}
	for _, m := range modelRe.FindAllStringSubmatch(body, -1) {
		name := titleCase(strings.ReplaceAll(m[1], "-", " "))
		if seen[name] {
			continue
		}
		seen[name] = true
		scene.Performers = append(scene.Performers, name)
	}
	return scene, nil
}

// titleCase capitalises each word of a model slug, which is the only form the
// site publishes a name in.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
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

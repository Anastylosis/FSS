// Package puzzyfun scrapes PuzzyFun (puzzyfun.com), a ShopMaker storefront
// where each collection is a scene. See docs/scrapers.md.
package puzzyfun

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
	siteID         = "puzzyfun"
	studioName     = "PuzzyFun"
	siteBase       = "https://www.puzzyfun.com"
	defaultWorkers = 4
	maxPages       = 200
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?puzzyfun\.com(?:/|$)`)
	// Collection links come in two forms; the /buy variant is the same scene.
	collectionRe = regexp.MustCompile(`href="/collections/([a-z0-9][a-z0-9-]{3,})"`)
	titleRe      = regexp.MustCompile(`<meta property="og:title" content="([^"]*)"`)
	descRe       = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
	imageRe      = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	dateRe       = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
	durationRe   = regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?)\s*minutes`)
	modelRe      = regexp.MustCompile(`href="/models/([a-z0-9-]+)"[^>]*>\s*([^<]{2,40}?)\s*</a>`)
	tagRe        = regexp.MustCompile(`/collections\?tag=([^"&]{2,40})`)
	scriptRe     = regexp.MustCompile(`(?s)<script.*?</script>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
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
	return []string{"puzzyfun.com", "puzzyfun.com/collections"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > maxPages {
			return scraper.PageResult{Done: true}, nil
		}
		slugs, err := s.fetchSlugs(ctx, s.listingURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		fresh := slugs[:0]
		for _, slug := range slugs {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			fresh = append(fresh, slug)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{}, nil
		}
		return scraper.PageResult{Scenes: s.fetchDetails(ctx, fresh, studioURL, workers, opts.Delay, now, out)}, nil
	})
}

func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		return s.base + "/collections"
	}
	return fmt.Sprintf("%s/collections/page/%d", s.base, page)
}

// fetchSlugs reads the collection slugs on a listing page, ignoring the
// hashed-id and /buy variants of the same link.
func (s *Scraper) fetchSlugs(ctx context.Context, pageURL string) ([]string, error) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var slugs []string
	for _, m := range collectionRe.FindAllStringSubmatch(body, -1) {
		slug := m[1]
		if slug == "page" || isHash(slug) || seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs, nil
}

// isHash reports whether a slug is the 32-hex internal id the player markup
// uses rather than the readable one the link carries.
func isHash(slug string) bool {
	if len(slug) != 32 {
		return false
	}
	for _, r := range slug {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (s *Scraper) fetchDetails(ctx context.Context, slugs []string, studioURL string, workers int, pause time.Duration, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(slugs))
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
				pageURL := s.base + "/collections/" + slugs[idx]
				body, err := s.fetchPage(ctx, pageURL)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				scene, err := parseScene(body, slugs[idx], studioURL, now)
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

// parseScene reads the collection page: OpenGraph for the title, description
// and still, and the byline for the date, cast and runtime.
func parseScene(body, slug, studioURL string, now time.Time) (models.Scene, error) {
	page := scriptRe.ReplaceAllString(body, "")

	title := ""
	if m := titleRe.FindStringSubmatch(body); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, fmt.Errorf("no og:title on the page")
	}

	scene := models.Scene{
		ID:        slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       siteBase + "/collections/" + slug,
		ScrapedAt: now,
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := imageRe.FindStringSubmatch(body); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}
	if m := dateRe.FindStringSubmatch(page); m != nil {
		if t, err := parseutil.TryParseDate(m[1], "2006-01-02"); err == nil {
			scene.Date = t.UTC()
		}
	}
	if m := durationRe.FindStringSubmatch(page); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}
	seen := map[string]bool{}
	for _, m := range modelRe.FindAllStringSubmatch(page, -1) {
		name := cleanText(m[2])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		scene.Performers = append(scene.Performers, name)
	}
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		tag := cleanText(strings.ReplaceAll(m[1], "+", " "))
		if tag != "" && !seen["#"+tag] {
			seen["#"+tag] = true
			scene.Tags = append(scene.Tags, tag)
		}
	}
	return scene, nil
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

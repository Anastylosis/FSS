package pissinghd

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
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

func (s *Scraper) ID() string { return "pissinghd" }

func (s *Scraper) Patterns() []string {
	return []string{"pissinghd.com"}
}

var matchRe = regexp.MustCompile(`^https?://(?:(?:www|tour)\.)?pissinghd\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

const tourBase = "https://tour.pissinghd.com"

var (
	cardRe   = regexp.MustCompile(`(?s)<div class="col-md-4 col-xs-12 col-sm-6"><!-- Thumbs -->.*?<!-- End Thumbs -->\s*</div>`)
	idRe     = regexp.MustCompile(`data-id="(\d+)"`)
	thumbRe  = regexp.MustCompile(`<img src="(https?://[^"]+)" class="img-responsive thumb"`)
	titleRe  = regexp.MustCompile(`(?s)<div class="tit-title[^"]*">\s*<div[^>]*>([^<]+)</div>`)
	descRe   = regexp.MustCompile(`(?s)<div id="episodedesc\d+"[^>]*>\s*<p[^>]*>(.*?)</p>`)
	tagClean = regexp.MustCompile(`<[^>]+>`)
)

type sceneItem struct {
	id          string
	title       string
	thumbnail   string
	description string
}

func parseListingPage(body []byte) []sceneItem {
	cards := cardRe.FindAll(body, -1)
	items := make([]sceneItem, 0, len(cards))
	for _, card := range cards {
		block := string(card)

		var item sceneItem

		if m := idRe.FindStringSubmatch(block); m != nil {
			item.id = m[1]
		}
		if item.id == "" {
			continue
		}

		if m := titleRe.FindStringSubmatch(block); m != nil {
			item.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			item.thumbnail = m[1]
		}

		if m := descRe.FindStringSubmatch(block); m != nil {
			desc := tagClean.ReplaceAllString(m[1], "")
			item.description = strings.TrimSpace(html.UnescapeString(desc))
		}

		items = append(items, item)
	}
	return items
}

var baseURLRe = regexp.MustCompile(`^(https?://[^/]+)`)

func resolveBase(studioURL string) string {
	if m := baseURLRe.FindString(studioURL); m != "" && !strings.Contains(m, "pissinghd.com") {
		return m
	}
	return tourBase
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := resolveBase(studioURL)
	now := time.Now().UTC()
	seen := make(map[string]bool)

	scraper.Paginate(ctx, opts, "pissinghd", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/videos?page=%d", base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)
		var scenes []models.Scene
		for _, item := range items {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			scenes = append(scenes, models.Scene{
				ID:          item.id,
				SiteID:      "pissinghd",
				StudioURL:   studioURL,
				Title:       item.title,
				Thumbnail:   item.thumbnail,
				Description: item.description,
				URL:         fmt.Sprintf("%s/videos#inline%s", tourBase, item.id),
				Studio:      "Pissing",
				ScrapedAt:   now,
			})
		}
		// The tour has no per-scene page: each card plays inline from an anchor
		// on the listing, and /video/{id}, /videos/{id} and /scene/{id} all 404.
		// The scene's own anchor is the closest stable address there is —
		// `?page=N` is a real URL but the page a scene sits on shifts as the
		// catalogue grows, so every stored URL silently starts naming a
		// different scene.
		//
		// No catalogue total is exposed anywhere in the listing markup, and
		// reporting the page size as the total made the progress line claim the
		// scrape was finished after page one.
		return scraper.PageResult{Scenes: scenes}, nil
	})
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

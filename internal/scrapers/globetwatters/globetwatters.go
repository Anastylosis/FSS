// Package globetwatters scrapes the Globe Twatters network — Asian Sex Diary,
// Trike Patrol, MILF Trip, TukTuk Patrol and Hello Ladyboy — which share one
// WordPress theme whose scenes live in a custom post type. See docs/scrapers.md.
package globetwatters

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig is one network site.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

// perPage is WP's maximum page size.
const perPage = 100

var tagStripRe = regexp.MustCompile(`<[^>]+>`)

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

// New builds a scraper for one network site.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/videos/"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// item is the subset of the custom post type's REST payload that carries scene
// metadata.
type item struct {
	ID      int    `json:"id"`
	DateGMT string `json:"date_gmt"`
	Link    string `json:"link"`
	Slug    string `json:"slug"`
	Title   struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
	Categories []int `json:"categories"`
	Embedded   struct {
		FeaturedMedia []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	categories := s.categoryNames(ctx)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		items, err := s.fetchPage(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			scenes = append(scenes, toScene(it, s.cfg, studioURL, categories, now))
		}
		return scraper.PageResult{Scenes: scenes, Done: len(items) < perPage}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, page int) ([]item, error) {
	u := fmt.Sprintf("%s/wp-json/wp/v2/rest-content?per_page=%d&page=%d&orderby=date&order=desc&_embed=wp:featuredmedia",
		s.base, perPage, page)
	var items []item
	if err := s.getJSON(ctx, u, &items); err != nil {
		if end := endOfList(err, page); end != nil {
			return nil, end
		}
		return nil, nil
	}
	return items, nil
}

// categoryNames resolves the category ids the payload carries. One request per
// 100 categories, fetched once per run; an unresolvable id is simply dropped.
func (s *Scraper) categoryNames(ctx context.Context) map[int]string {
	names := map[int]string{}
	for page := 1; page <= 10; page++ {
		var cats []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		u := fmt.Sprintf("%s/wp-json/wp/v2/categories?per_page=%d&page=%d&_fields=id,name", s.base, perPage, page)
		if err := s.getJSON(ctx, u, &cats); err != nil || len(cats) == 0 {
			break
		}
		for _, c := range cats {
			names[c.ID] = cleanText(c.Name)
		}
		if len(cats) < perPage {
			break
		}
	}
	scraper.Debugf(1, "%s: resolved %d category names", s.cfg.SiteID, len(names))
	return names
}

func toScene(it item, cfg SiteConfig, studioURL string, categories map[int]string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          strconv.Itoa(it.ID),
		SiteID:      cfg.SiteID,
		StudioURL:   studioURL,
		Studio:      cfg.StudioName,
		Title:       cleanText(it.Title.Rendered),
		URL:         it.Link,
		Description: cleanText(it.Content.Rendered),
		ScrapedAt:   now,
	}
	if scene.Title == "" {
		scene.Title = strings.ReplaceAll(it.Slug, "-", " ")
	}
	if scene.URL == "" {
		scene.URL = fmt.Sprintf("https://%s/videos/%s/", cfg.Domain, it.Slug)
	}
	if media := it.Embedded.FeaturedMedia; len(media) > 0 {
		scene.Thumbnail = media[0].SourceURL
	}
	if t, err := time.Parse("2006-01-02T15:04:05", it.DateGMT); err == nil {
		scene.Date = t.UTC()
	}
	for _, id := range it.Categories {
		if name := categories[id]; name != "" {
			scene.Categories = append(scene.Categories, name)
		}
	}
	return scene
}

// endOfList turns WP's past-the-end 400 into a clean stop; every other failure
// stays an error, so a 502 cannot truncate the catalogue.
func endOfList(err error, page int) error {
	var status *httpx.StatusError
	if page > 1 && errors.As(err, &status) && status.StatusCode == http.StatusBadRequest {
		return nil
	}
	return err
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) error {
	return httpx.DoJSON(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	}, v)
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

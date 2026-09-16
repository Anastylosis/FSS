// Package pinkyxxx scrapes Pinky XXX (pinkyxxx.com). Scenes are a WordPress
// custom post type read through the REST API; the pages themselves are
// login-walled. See docs/scrapers.md.
package pinkyxxx

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

const (
	siteID     = "pinkyxxx"
	studioName = "Pinky XXX"
	siteBase   = "https://pinkyxxx.com"
	// perPage is WP's maximum.
	perPage = 100
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?pinkyxxx\.com(?:/|$)`)
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
	return []string{"pinkyxxx.com", "pinkyxxx.com/video/{slug}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// video is the subset of the WP REST payload the `video` post type populates.
type video struct {
	ID      int    `json:"id"`
	DateGMT string `json:"date_gmt"`
	Link    string `json:"link"`
	Slug    string `json:"slug"`
	Title   struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Embedded struct {
		FeaturedMedia []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		videos, err := s.fetchPage(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(videos))
		for _, v := range videos {
			scenes = append(scenes, toScene(v, studioURL, now))
		}
		return scraper.PageResult{Scenes: scenes, Done: len(videos) < perPage}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, page int) ([]video, error) {
	u := fmt.Sprintf("%s/wp-json/wp/v2/video?per_page=%d&page=%d&orderby=date&order=desc&_embed=wp:featuredmedia",
		s.base, perPage, page)
	var videos []video
	err := httpx.DoJSON(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	}, &videos)
	if err != nil {
		// WP answers 400 past the last page rather than an empty list. Only
		// that exact status ends the walk — any other error is a real failure.
		var status *httpx.StatusError
		if page > 1 && errors.As(err, &status) && status.StatusCode == http.StatusBadRequest {
			return nil, nil
		}
		return nil, err
	}
	return videos, nil
}

func toScene(v video, studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        strconv.Itoa(v.ID),
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     cleanText(v.Title.Rendered),
		URL:       v.Link,
		ScrapedAt: now,
	}
	if scene.Title == "" {
		scene.Title = strings.ReplaceAll(v.Slug, "-", " ")
	}
	if scene.URL == "" {
		scene.URL = siteBase + "/video/" + v.Slug + "/"
	}
	if media := v.Embedded.FeaturedMedia; len(media) > 0 {
		scene.Thumbnail = media[0].SourceURL
	}
	if t, err := time.Parse("2006-01-02T15:04:05", v.DateGMT); err == nil {
		scene.Date = t.UTC()
	}
	return scene
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

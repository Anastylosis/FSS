// Package putricinta scrapes Putri Cinta (putricinta.com) through the film
// custom post type of its WordPress REST API. See docs/scrapers.md.
package putricinta

import (
	"context"
	"encoding/json"
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
	siteID     = "putricinta"
	studioName = "Putri Cinta"
	perPage    = 100
)

var siteBase = "https://putricinta.com"

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?putricinta\.com(?:/|$)`)
	filmSuffix = regexp.MustCompile(`\s*\(Film\)\s*$`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"putricinta.com"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type term struct {
	Name     string `json:"name"`
	Taxonomy string `json:"taxonomy"`
}

type film struct {
	ID      int    `json:"id"`
	DateGMT string `json:"date_gmt"`
	Link    string `json:"link"`
	Title   struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Embedded struct {
		Media []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
		Terms [][]term `json:"wp:term"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wp/v2/film?per_page=%d&page=%d&orderby=date&order=desc&_embed=wp:featuredmedia,wp:term", siteBase, perPage, page)
		var films []film
		total, pages, err := s.getJSON(ctx, u, &films)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(films))
		for _, f := range films {
			scenes = append(scenes, toScene(f, studioURL, now))
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(films) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (total, pages int, err error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, siteBase)})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	total, _ = strconv.Atoi(resp.Header.Get("X-WP-Total"))
	pages, _ = strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		var se *json.SyntaxError
		if errors.As(err, &se) {
			return 0, 0, scraper.ParseError(u, err)
		}
		return 0, 0, err
	}
	return total, pages, nil
}

func toScene(f film, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        strconv.Itoa(f.ID),
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     strings.TrimSpace(filmSuffix.ReplaceAllString(html.UnescapeString(f.Title.Rendered), "")),
		URL:       f.Link,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", f.DateGMT); err == nil {
		sc.Date = t.UTC()
	}
	if len(f.Embedded.Media) > 0 {
		sc.Thumbnail = f.Embedded.Media[0].SourceURL
	}
	for _, group := range f.Embedded.Terms {
		for _, t := range group {
			if t.Taxonomy == "post_tag" {
				if name := strings.TrimSpace(strings.TrimPrefix(html.UnescapeString(t.Name), "#")); name != "" {
					sc.Tags = append(sc.Tags, name)
				}
			}
		}
	}
	return sc
}

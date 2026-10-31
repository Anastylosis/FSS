// Package avamoore scrapes Ava Moore (ava-moore.com), a Nuxt front end over a
// Laravel JSON API. See docs/scrapers.md.
package avamoore

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "avamoore"
	studioName = "Ava Moore"
)

var siteBase = "https://www.ava-moore.com"

var matchRe = regexp.MustCompile(`^https?://(?:www\.|video\.)?ava-moore\.com(?:/|$)`)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"ava-moore.com", "ava-moore.com/videos"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type video struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Slug        string  `json:"slug"`
	Description string  `json:"description"`
	Duration    int64   `json:"duration"`
	Price       float64 `json:"price"`
	Views       int     `json:"views_count"`
	CreatedAt   string  `json:"created_at"`
}

type listing struct {
	Data struct {
		CurrentPage int     `json:"current_page"`
		LastPage    int     `json:"last_page"`
		Total       int     `json:"total"`
		Data        []video `json:"data"`
	} `json:"data"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/api/v1/videos-pornos?search=active%%3A1&searchFields=active%%3A%%3D&page=%d", siteBase, page)
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, "https://www.ava-moore.com")})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		var l listing
		if err := httpx.DecodeJSON(resp.Body, &l); err != nil {
			return scraper.PageResult{}, scraper.ParseError(u, err)
		}
		scenes := make([]models.Scene, 0, len(l.Data.Data))
		for _, v := range l.Data.Data {
			scenes = append(scenes, toScene(v, studioURL, now))
		}
		res := scraper.PageResult{Scenes: scenes, Done: page >= l.Data.LastPage}
		if page == 1 {
			res.Total = l.Data.Total
		}
		return res, nil
	})
}

func toScene(v video, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          v.ID,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       v.Title,
		URL:         "https://www.ava-moore.com/videos/" + v.Slug,
		Description: v.Description,
		Duration:    int(v.Duration / 1000),
		Studio:      studioName,
		Views:       v.Views,
		Performers:  []string{studioName},
		ScrapedAt:   now,
	}
	if t, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
		sc.Date = t.UTC()
	}
	if v.Price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: v.Price})
	} else {
		sc.AddPrice(models.PriceSnapshot{Date: now, IsFree: true})
	}
	return sc
}

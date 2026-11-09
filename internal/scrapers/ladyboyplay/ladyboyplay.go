// Package ladyboyplay scrapes ladyboyplay.com, an xMember Angular SPA whose
// public JSON API lists every video in one request.
// See docs/scrapers.md.
package ladyboyplay

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "ladyboyplay"
	studioName = "LadyboyPlay"
	defaultURL = "https://ladyboyplay.com"
	// pageSize is far above the catalogue (~160); the API honours it.
	pageSize = 1000
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"ladyboyplay.com"} }

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?ladyboyplay\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, out)
	return out, nil
}

type video struct {
	ID          string   `json:"_id"`
	Alias       string   `json:"alias"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Thumb       string   `json:"imageFullPath"`
	Medium      string   `json:"imageMediumPath"`
	Status      string   `json:"status"`
	Type        string   `json:"type"`
	CreatedAt   string   `json:"createdAt"`
	Performer   []string `json:"performer"`
}

type performer struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: fetching performers and videos in one request each", siteID)
	var performers []performer
	if err := s.getJSON(ctx, "/api/v1/performers", &performers); err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	names := make(map[string]string, len(performers))
	for _, p := range performers {
		names[p.ID] = strings.TrimSpace(p.Name)
	}

	var videos []video
	if err := s.getJSON(ctx, "/api/v1/videos", &videos); err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	if !send(ctx, out, scraper.Progress(len(videos))) {
		return
	}

	now := time.Now().UTC()
	for _, v := range videos {
		// Inactive rows are withdrawn; "upcoming" ones are announced, not released.
		if v.Status != "active" || v.Type != "recent" || v.Name == "" {
			continue
		}
		if !send(ctx, out, scraper.Scene(s.toScene(v, names, studioURL, now))) {
			return
		}
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) getJSON(ctx context.Context, path string, v any) error {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     fmt.Sprintf("%s%s?limit=%d&offset=0", s.base, path, pageSize),
		Headers: map[string]string{"User-Agent": httpx.UserAgentFirefox, "Accept": "application/json"},
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		return scraper.ParseError(path, err)
	}
	return nil
}

func (s *Scraper) toScene(v video, names map[string]string, studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          v.ID,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       strings.TrimSpace(v.Name),
		URL:         fmt.Sprintf("%s/movies/%s/%s", s.base, v.Alias, v.ID),
		Description: strings.TrimSpace(v.Description),
		ScrapedAt:   now,
	}
	if thumb := firstNonEmpty(v.Thumb, v.Medium); thumb != "" {
		scene.Thumbnail = s.base + thumb
	}
	if t, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
		scene.Date = t.UTC()
	}
	for _, id := range v.Performer {
		if n := names[id]; n != "" {
			scene.Performers = append(scene.Performers, n)
		}
	}
	return scene
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

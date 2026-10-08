// Package gayerdar scrapes gayerdar.com (Model Media), a Nuxt SPA over a public
// JSON API. See docs/scrapers.md.
package gayerdar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "gayerdar"
	studioName = "Gayerdar"
	// detailWorkers is small: the API is a single origin and a page is 20 scenes.
	detailWorkers = 4
	webLocale     = "en-US"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?gayerdar\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/models/(\d+)(?:/|$)`)
)

// Scraper implements scraper.StudioScraper for gayerdar.com.
type Scraper struct {
	client *http.Client
	api    string
	web    string
}

// New builds the Gayerdar scraper.
func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		api:    "https://api.gayerdar.com",
		web:    "https://gayerdar.com",
	}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"gayerdar.com",
		"gayerdar.com/{locale}/models/{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type envelope[T any] struct {
	Status bool `json:"status"`
	Data   T    `json:"data"`
}

type video struct {
	Serial      string `json:"serial_number"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Cover       string `json:"cover"`
	Preview     string `json:"preview_video"`
	Duration    int    `json:"duration"`
	Views       int    `json:"views"`
	PublishedAt int64  `json:"published_at"`
	Models      []struct {
		Name string `json:"name"`
	} `json:"models"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
}

type listPage struct {
	List       []video `json:"list"`
	Pagination struct {
		TotalRecords int `json:"total_records"`
		TotalPages   int `json:"total_pages"`
	} `json:"pagination"`
}

type modelPage struct {
	Videos []video `json:"videos"`
}

// modelID returns the performer id of a model page URL, or "".
func modelID(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	if m := modelRe.FindStringSubmatch(u.Path); m != nil {
		return m[1]
	}
	return ""
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	model := modelID(studioURL)
	if model != "" {
		scraper.Debugf(1, "%s: scraping model %s", siteID, model)
	}
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var (
			vids  []video
			total int
			done  bool
		)
		if model != "" {
			var env envelope[modelPage]
			u := fmt.Sprintf("%s/api/v2/models/%s", s.api, model)
			if err := s.getJSON(ctx, u, &env); err != nil {
				return scraper.PageResult{}, err
			}
			vids, total, done = env.Data.Videos, len(env.Data.Videos), true
		} else {
			var env envelope[listPage]
			u := fmt.Sprintf("%s/api/v2/videos?page=%d", s.api, page)
			if err := s.getJSON(ctx, u, &env); err != nil {
				return scraper.PageResult{}, err
			}
			p := env.Data.Pagination
			vids, total = env.Data.List, p.TotalRecords
			done = p.TotalPages > 0 && page >= p.TotalPages
		}

		scenes := make([]models.Scene, len(vids))
		for i, v := range vids {
			scenes[i] = s.scene(v, studioURL, now)
		}
		var fresh []int
		for i := range scenes {
			if !opts.KnownIDs[scenes[i].ID] {
				fresh = append(fresh, i)
			}
		}
		s.enrich(ctx, scenes, fresh, opts, out)
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

func (s *Scraper) scene(v video, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          v.Serial,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Studio:      studioName,
		Title:       strings.TrimSpace(v.Title),
		URL:         fmt.Sprintf("%s/%s/videos/%s", s.web, webLocale, v.Serial),
		Description: strings.TrimSpace(v.Description),
		Thumbnail:   v.Cover,
		Preview:     v.Preview,
		Duration:    v.Duration,
		Views:       v.Views,
		ScrapedAt:   now,
	}
	if v.PublishedAt > 0 {
		sc.Date = time.UnixMilli(v.PublishedAt).UTC()
	}
	applyCredits(&sc, v)
	return sc
}

func applyCredits(sc *models.Scene, v video) {
	for _, m := range v.Models {
		if n := strings.TrimSpace(m.Name); n != "" {
			sc.Performers = append(sc.Performers, n)
		}
	}
	for _, t := range v.Tags {
		if n := strings.TrimSpace(t.Name); n != "" {
			sc.Tags = append(sc.Tags, n)
		}
	}
}

// enrich fetches the detail record for the scenes at idx; list items carry no
// cast or tags.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, idx []int, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(idx) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(idx), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, i := range idx {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			var env envelope[video]
			u := fmt.Sprintf("%s/api/v2/videos/%s", s.api, url.PathEscape(scenes[i].ID))
			if err := s.getJSON(ctx, u, &env); err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyCredits(&scenes[i], env.Data)
		}(i)
	}
	wg.Wait()
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) error {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		return scraper.ParseError(u, err)
	}
	return nil
}

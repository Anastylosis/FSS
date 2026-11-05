// Package dirtycinema scrapes the Voodoo Media WordPress sites that publish
// scenes as a `videos` custom post type: Dirty Cinema and MilfUCKD. The cast
// is only on the detail page. See docs/scrapers.md.
package dirtycinema

import (
	"context"
	"errors"
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
	"github.com/Anastylosis/FSS/scraper"
)

const perPage = 100

type siteConfig struct {
	siteID  string
	studio  string
	domain  string
	base    string
	castRe  *regexp.Regexp
	matchRe *regexp.Regexp
}

var sites = []siteConfig{
	{
		siteID:  "dirtycinema",
		studio:  "Dirty Cinema",
		domain:  "dirtycinema.com",
		base:    "https://dirtycinema.com",
		castRe:  regexp.MustCompile(`single-video__cast-list-item"[^>]*data-balloon="([^"]+)"`),
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?dirtycinema\.com(?:/|$)`),
	},
	{
		siteID:  "milfuckd",
		studio:  "MilfUCKD",
		domain:  "milfuckd.com",
		base:    "https://milfuckd.com",
		castRe:  regexp.MustCompile(`<a href="[^"]*/performers/[^"]+" aria-label="Read more about ([^"]+)"`),
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?milfuckd\.com(?:/|$)`),
	},
}

var stripRe = regexp.MustCompile(`<[^>]+>`)

type Scraper struct {
	Client *http.Client
	cfg    siteConfig
	base   string
}

func New(cfg siteConfig) *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), cfg: cfg, base: cfg.base}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	for _, c := range sites {
		scraper.Register(New(c))
	}
}

func (s *Scraper) ID() string { return s.cfg.siteID }

func (s *Scraper) Patterns() []string { return []string{s.cfg.domain} }

func (s *Scraper) MatchesURL(u string) bool { return s.cfg.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type video struct {
	ID      int    `json:"id"`
	DateGMT string `json:"date_gmt"`
	Link    string `json:"link"`
	Title   struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
	Embedded struct {
		Media []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
		Terms [][]struct {
			Name     string `json:"name"`
			Taxonomy string `json:"taxonomy"`
		} `json:"wp:term"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	workers := scraper.WorkerCount(opts, 4)

	scraper.Paginate(ctx, opts, s.cfg.siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wp/v2/videos?per_page=%d&page=%d&_embed=1", s.base, perPage, page)
		var vids []video
		total, pages, err := s.getJSON(ctx, u, &vids)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.siteID, len(vids), workers)
		scenes := make([]models.Scene, len(vids))
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, v := range vids {
			scenes[i] = s.toScene(v, studioURL, now)
			if opts.KnownIDs[scenes[i].ID] {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				body, err := s.fetchPage(ctx, v.Link)
				if err != nil {
					scraper.Debugf(1, "%s: detail %s: %v", s.cfg.siteID, v.Link, err)
					return
				}
				scenes[i].Performers = s.parseCast(body)
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return scraper.PageResult{}, err
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(vids) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (total, pages int, err error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, s.base)})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	total, _ = strconv.Atoi(resp.Header.Get("X-WP-Total"))
	pages, _ = strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		return 0, 0, scraper.ParseError(u, err)
	}
	return total, pages, nil
}

func (s *Scraper) fetchPage(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	return string(b), err
}

func (s *Scraper) parseCast(page string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range s.cfg.castRe.FindAllStringSubmatch(page, -1) {
		n := strings.TrimSpace(html.UnescapeString(m[1]))
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names
}

func (s *Scraper) toScene(v video, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          strconv.Itoa(v.ID),
		SiteID:      s.cfg.siteID,
		StudioURL:   studioURL,
		Title:       clean(v.Title.Rendered),
		URL:         v.Link,
		Description: clean(v.Content.Rendered),
		Studio:      s.cfg.studio,
		ScrapedAt:   now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", v.DateGMT); err == nil {
		sc.Date = t.UTC()
	}
	if len(v.Embedded.Media) > 0 {
		sc.Thumbnail = v.Embedded.Media[0].SourceURL
	}
	for _, group := range v.Embedded.Terms {
		for _, t := range group {
			if t.Taxonomy == "video-category" {
				sc.Tags = append(sc.Tags, html.UnescapeString(t.Name))
			}
		}
	}
	return sc
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

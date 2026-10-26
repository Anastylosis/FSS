// Package strapondreamer scrapes StraponDreamer (strapondreamer.com), a small
// catalogue site whose purchases go through mediastore.cloud. See
// docs/scrapers.md.
package strapondreamer

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

const (
	siteID         = "strapondreamer"
	studioName     = "StraponDreamer"
	defaultWorkers = 3
)

var siteBase = "https://strapondreamer.com"

var (
	matchRe  = regexp.MustCompile(`^https?://(?:www\.)?strapondreamer\.com(?:/|$)`)
	cardRe   = regexp.MustCompile(`href="(/videos/(NV\d+)/[^"]+)"`)
	h1Re     = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	detailRe = regexp.MustCompile(`(?s)<div class="details dimmed">([^<:]+):\s*</div>\s*<div class="details medium">([^<]*)</div>`)
	descRe   = regexp.MustCompile(`(?s)<div id="description"[^>]*>(.*?)</div>`)
	coverRe  = regexp.MustCompile(`(?s)<div class="videoCover">\s*<img src="([^"]+)"`)
	tagStrip = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"strapondreamer.com", "strapondreamer.com/videos"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct{ id, path string }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := siteBase + "/videos"
		if page > 1 {
			u = fmt.Sprintf("%s/videos?page=%d", siteBase, page)
		}
		body, err := s.get(ctx, u)
		if err != nil {
			// One page past the last answers 404.
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		var cards []card
		for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
			if !seen[m[2]] {
				seen[m[2]] = true
				cards = append(cards, card{id: m[2], path: m[1]})
			}
		}
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(u, errors.New("no video cards"))
		}
		if len(cards) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		next := strings.Contains(body, fmt.Sprintf(`/videos?page=%d"`, page+1))
		scenes := s.details(ctx, cards, studioURL, opts, now, out)
		return scraper.PageResult{Scenes: scenes, Done: !next}, nil
	})
}

// details fetches each card's page. Known IDs keep a stub so Paginate's
// early stop sees them without a request; a failed fetch is reported.
func (s *Scraper) details(ctx context.Context, cards []card, studioURL string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(cards), workers)
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		if opts.KnownIDs[c.id] {
			scenes[i] = models.Scene{ID: c.id, SiteID: siteID}
			continue
		}
		wg.Add(1)
		go func(i int, c card) {
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
			u := siteBase + c.path
			body, err := s.get(ctx, u)
			if err == nil {
				var sc models.Scene
				if sc, err = parseDetail(body, c.id, u, studioURL, now); err == nil {
					scenes[i] = sc
					return
				}
			}
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
		}(i, c)
	}
	wg.Wait()
	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

func parseDetail(body, id, pageURL, studioURL string, now time.Time) (models.Scene, error) {
	sc := models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       pageURL,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if m := h1Re.FindStringSubmatch(body); m != nil {
		sc.Title = clean(m[1])
	}
	if sc.Title == "" {
		return sc, scraper.ParseError(pageURL, errors.New("no title"))
	}
	for _, m := range detailRe.FindAllStringSubmatch(body, -1) {
		v := clean(m[2])
		switch strings.TrimSpace(m[1]) {
		case "Runtime":
			if n, err := strconv.Atoi(v); err == nil {
				sc.Duration = n * 60
			}
		case "Theme":
			if v != "" {
				sc.Tags = append(sc.Tags, v)
			}
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := coverRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(m[1])
	}
	return sc, nil
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	return string(b), err
}

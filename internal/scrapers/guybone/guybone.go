// Package guybone scrapes GuyBone (guybone.com). See docs/scrapers.md.
package guybone

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
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
	siteID         = "guybone"
	studioName     = "GuyBone"
	pageSize       = 36
	defaultWorkers = 3
)

var siteBase = "https://guybone.com"

var (
	matchRe  = regexp.MustCompile(`^https?://(?:www\.)?guybone\.com(?:/|$)`)
	cardRe   = regexp.MustCompile(`(?s)<a href="/video/([^"]+)">\s*<div class="image fit captioned">\s*<img src="([^"]*)"\s*/?>\s*<h4>([^<]*)</h4>`)
	headRe   = regexp.MustCompile(`(?s)<header class="major">\s*<h2>(.*?)</h2>\s*<p>([^<]*)</p>`)
	posterRe = regexp.MustCompile(`#hlsjsvod\s*\{[^}]*background-image:\s*url\(([^)]+)\)`)
	modelRe  = regexp.MustCompile(`<h4><a href="/model/[^"]+">([^<]+)</a></h4>`)
	descRe   = regexp.MustCompile(`(?is)<!-- Scene Description -->.*?<h2>.*?</h2>(.*?)</div>`)
	dateRe   = regexp.MustCompile(`^(\d{1,2})-(\d{1,2})-(\d{4})$`)
	stripRe  = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"guybone.com", "guybone.com/list_videos"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct{ slug, thumb, title string }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	seen := map[string]bool{}
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		// The listing pages through a POST form: start=0, 36, 72 …
		body, err := s.fetch(ctx, http.MethodPost, siteBase+"/list_videos", url.Values{"start": {strconv.Itoa((page - 1) * pageSize)}})
		if err != nil {
			return scraper.PageResult{}, err
		}
		var cards []card
		for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				cards = append(cards, card{slug: m[1], thumb: html.UnescapeString(m[2]), title: clean(m[3])})
			}
		}
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(siteBase+"/list_videos", errors.New("no video cards"))
		}
		if len(cards) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.details(ctx, cards, studioURL, opts, now, out)}, nil
	})
}

// details fetches each scene page for date, cast and description. Known IDs
// keep a stub so Paginate stops after the page; a failed fetch is reported
// and the card's title and thumbnail kept.
func (s *Scraper) details(ctx context.Context, cards []card, studioURL string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(cards), workers)
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		scenes[i] = models.Scene{
			ID:        c.slug,
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     c.title,
			URL:       siteBase + "/video/" + c.slug,
			Thumbnail: c.thumb,
			Studio:    studioName,
			ScrapedAt: now,
		}
		if opts.KnownIDs[c.slug] {
			continue
		}
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
			body, err := s.fetch(ctx, http.MethodGet, scenes[i].URL, nil)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
	return scenes
}

func applyDetail(sc *models.Scene, body string) {
	if m := headRe.FindStringSubmatch(body); m != nil {
		if t := clean(m[1]); t != "" {
			sc.Title = t
		}
		if d := dateRe.FindStringSubmatch(strings.TrimSpace(m[2])); d != nil {
			mo, _ := strconv.Atoi(d[1])
			da, _ := strconv.Atoi(d[2])
			y, _ := strconv.Atoi(d[3])
			sc.Date = time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
		}
	}
	if m := posterRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = strings.TrimSpace(m[1])
	}
	for _, m := range modelRe.FindAllStringSubmatch(body, -1) {
		if n := clean(m[1]); n != "" {
			sc.Performers = append(sc.Performers, n)
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

// fetch issues a GET, or a form POST when form is set.
func (s *Scraper) fetch(ctx context.Context, method, u string, form url.Values) (string, error) {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	req := httpx.Request{Method: method, URL: u, Headers: h}
	if form != nil {
		h["Content-Type"] = "application/x-www-form-urlencoded"
		req.Body = []byte(form.Encode())
	}
	resp, err := httpx.Do(ctx, s.Client, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	return string(b), err
}

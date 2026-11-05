// Package asianstreetmeat scrapes Asian Street Meat (asianstreetmeat.com),
// whose whole catalogue is a single HTML page. See docs/scrapers.md.
package asianstreetmeat

import (
	"context"
	"errors"
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
	siteID     = "asianstreetmeat"
	studioName = "Asian Street Meat"
)

var siteBase = "http://asianstreetmeat.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?asianstreetmeat\.com(?:/|$)`)
	anchorRe  = regexp.MustCompile(`(?s)(<div class="blurb_v3">)?<a href="/join/(\d{8,}\.[^/"]+)/"[^>]*>\s*(.*?)\s*</a>`)
	minutesRe = regexp.MustCompile(`(\d+)\s*minutes`)
	thumbRe   = regexp.MustCompile(`data-original="([^"]+/photogroup/([^/"]+)/1\.jpg)"`)
	trailerRe = regexp.MustCompile(`src="([^"]+/photogroup/([^/"]+)/trailor\.mp4)"`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(60 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"asianstreetmeat.com"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, _ int) (scraper.PageResult, error) {
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: siteBase + "/", Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := httpx.ReadBody(resp.Body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parsePage(string(body), studioURL, now)
		if len(scenes) == 0 {
			return scraper.PageResult{}, scraper.ParseError(siteBase+"/", errNoEntries)
		}
		return scraper.PageResult{Scenes: scenes, Total: len(scenes), Done: true}, nil
	})
}

var errNoEntries = errors.New("no /join/ entries on the page")

// parsePage returns the scenes in page order, which is newest first.
func parsePage(page, studioURL string, now time.Time) []models.Scene {
	byID := map[string]*models.Scene{}
	var order []string
	for _, m := range anchorRe.FindAllStringSubmatch(page, -1) {
		blurb, id, text := m[1] != "", m[2], strings.TrimSpace(m[3])
		sc := byID[id]
		if sc == nil {
			sc = &models.Scene{
				ID:        id,
				SiteID:    siteID,
				StudioURL: studioURL,
				URL:       siteBase + "/join/" + id + "/",
				Studio:    studioName,
				ScrapedAt: now,
			}
			if len(id) >= 8 {
				if t, err := time.Parse("20060102", id[:8]); err == nil {
					sc.Date = t
				}
			}
			byID[id] = sc
			order = append(order, id)
		}
		switch {
		case blurb:
			sc.Description = clean(text)
		case minutesRe.MatchString(text):
			n, _ := strconv.Atoi(minutesRe.FindStringSubmatch(text)[1])
			sc.Duration = n * 60
		case sc.Title == "" && isName(text):
			sc.Title = titleCase(clean(text))
		}
	}
	for _, m := range thumbRe.FindAllStringSubmatch(page, -1) {
		if sc := byID[m[2]]; sc != nil && sc.Thumbnail == "" {
			sc.Thumbnail = m[1]
		}
	}
	for _, m := range trailerRe.FindAllStringSubmatch(page, -1) {
		if sc := byID[m[2]]; sc != nil && sc.Preview == "" {
			sc.Preview = m[1]
		}
	}
	scenes := make([]models.Scene, 0, len(order))
	for _, id := range order {
		sc := byID[id]
		if sc.Title == "" {
			continue
		}
		sc.Performers = []string{sc.Title}
		scenes = append(scenes, *sc)
	}
	return scenes
}

func isName(text string) bool {
	if text == "" || strings.Contains(text, "<") || len(text) > 60 {
		return false
	}
	for _, skip := range []string{"Preview The Video", "photos", "definition"} {
		if strings.Contains(text, skip) {
			return false
		}
	}
	return true
}

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

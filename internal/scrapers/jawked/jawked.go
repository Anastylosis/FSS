// Package jawked scrapes jawked.com. See docs/scrapers.md.
package jawked

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "jawked"
	studioName = "Jawked"
	// detailWorkers: the card has no cast or synopsis, and the pages are tiny.
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?jawked\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`^/models/([^/?#]+-\d+)\.html$`)

	cardRe = regexp.MustCompile(`(?s)<a href="[^"]*/video/([^"/]+-(\d+))\.html">\s*<div class="thumb">\s*<img src="([^"]*)".*?<span class="title">(.*?)</span>\s*(?:<span class="date"><span>([^<]*)</span>)?`)

	descRe     = regexp.MustCompile(`(?s)<div class="tour-detail-description-body">(.*?)</div>`)
	castRe     = regexp.MustCompile(`(?s)<span class="models">.*?<span class="content">(.*?)</span>`)
	castNameRe = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	posterRe   = regexp.MustCompile(`<img src="([^"]+)" class="poster"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)

	dateLayouts = []string{"02 Jan 2006", "2 Jan 2006"}
)

// Scraper implements scraper.StudioScraper for jawked.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Jawked scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.jawked.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"jawked.com",
		"jawked.com/videos/",
		"jawked.com/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id    string
	path  string
	title string
	date  time.Time
	thumb string
}

// modelSlug returns the "{name}-{id}" of a model page URL, or "".
func modelSlug(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	if m := modelRe.FindStringSubmatch(u.Path); m != nil {
		return m[1]
	}
	return ""
}

func (s *Scraper) listingURL(model string, page int) string {
	switch {
	case model != "":
		return fmt.Sprintf("%s/models/%s.html", s.base, model)
	case page == 1:
		return s.base + "/videos/"
	default:
		return fmt.Sprintf("%s/videos/page%d.html", s.base, page)
	}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	model := modelSlug(studioURL)
	if model != "" {
		scraper.Debugf(1, "%s: scraping model page %s", siteID, model)
	}
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(model, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 && model == "" {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		// An out-of-range page serves page 1 again, so a fully-seen page is the end.
		fresh := cards[:0]
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := make([]models.Scene, 0, len(fresh))
		for _, c := range fresh {
			scenes = append(scenes, s.baseScene(c, studioURL, now))
		}
		var todo []*models.Scene
		for i := range scenes {
			if !opts.KnownIDs[scenes[i].ID] {
				todo = append(todo, &scenes[i])
			}
		}
		s.enrich(ctx, todo, opts, out)
		return scraper.PageResult{Scenes: scenes, Done: model != ""}, nil
	})
}

func parseListing(body string) []card {
	var cards []card
	for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
		c := card{id: m[2], path: "/video/" + m[1] + ".html", title: cleanText(m[4]), thumb: html.UnescapeString(m[3])}
		if d, err := parseutil.TryParseDate(strings.TrimSpace(m[5]), dateLayouts...); err == nil {
			c.date = d
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     c.title,
		URL:       s.base + c.path,
		Date:      c.date,
		Thumbnail: c.thumb,
		ScrapedAt: now,
	}
}

func (s *Scraper) enrich(ctx context.Context, scenes []*models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, sc := range scenes {
		wg.Add(1)
		go func(sc *models.Scene) {
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
			body, err := s.fetch(ctx, sc.URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(sc, body)
		}(sc)
	}
	wg.Wait()
}

func applyDetail(sc *models.Scene, body string) {
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = strings.TrimSpace(html.UnescapeString(tagStripRe.ReplaceAllString(m[1], "")))
	}
	if m := castRe.FindStringSubmatch(body); m != nil {
		for _, n := range castNameRe.FindAllStringSubmatch(m[1], -1) {
			if name := cleanText(n[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
	if p := posterRe.FindStringSubmatch(body); p != nil {
		sc.Thumbnail = html.UnescapeString(p[1])
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["Cookie"] = "warningHidden=hide"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: pageURL, Headers: headers})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

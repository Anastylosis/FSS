// Package showybeauty scrapes Showy Beauty (showybeauty.com), a static-HTML
// glamour site from the Amour Angels network. The /videos.html listing and its
// /videos_{N}.html follow-on pages carry every field the public site exposes
// (title, date, photographer, runtime, model), so no detail pages are fetched.
// Model pages (/model_{id}.html) list that model's photo and video sets on one
// page; only the video sets are returned.
package showybeauty

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const pageSize = 16

// siteBase is a var so tests can point it at an httptest server.
var siteBase = "http://showybeauty.com"

type Scraper struct {
	Client *http.Client
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "showybeauty" }

func (s *Scraper) Patterns() []string {
	return []string{
		"showybeauty.com",
		"showybeauty.com/videos.html",
		"showybeauty.com/model_{id}.html",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?showybeauty\.com(?:/|$)`)
	modelRe = regexp.MustCompile(`/model_(\d+)\.html`)

	itemSplitRe  = regexp.MustCompile(`<div class="item">`)
	coverRe      = regexp.MustCompile(`<a href="/?cover_(\d+)\.html"[^>]*class="title">([^<]*)</a>`)
	thumbRe      = regexp.MustCompile(`<img src="(/cm_cvl/[^"]+)"`)
	addedRe      = regexp.MustCompile(`class="added">\s*Added\s+(\d{4}-\d{2}-\d{2})`)
	resolutionRe = regexp.MustCompile(`class="resolution">([^<]*)<`)
	photogRe     = regexp.MustCompile(`BY <a href="/photographer_\d+\.html"[^>]*>([^<]+)</a>`)
	countRe      = regexp.MustCompile(`class="count">\s*(\d{1,2}(?::\d{2}){1,2})\s*min`)
	performerRe  = regexp.MustCompile(`(?i)<a href='/model_\d+\.html'>([^<]+)</a>`)
	modelNameRe  = regexp.MustCompile(`<strong>Name:</strong>\s*([^<]+)<`)
	videoSuffix  = regexp.MustCompile(`(?i)\s+video$`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "showybeauty: scraping model page %s", m[1])
		pageURL := fmt.Sprintf("%s/model_%s.html", siteBase, m[1])
		scraper.Paginate(ctx, opts, "showybeauty", out, func(ctx context.Context, _ int) (scraper.PageResult, error) {
			body, err := s.get(ctx, pageURL)
			if err != nil {
				return scraper.PageResult{}, err
			}
			performer := ""
			if mm := modelNameRe.FindStringSubmatch(body); mm != nil {
				performer = html.UnescapeString(strings.TrimSpace(mm[1]))
			}
			scenes := parseItems(body, studioURL, performer, now)
			return scraper.PageResult{Scenes: scenes, Total: len(scenes), Done: true}, nil
		})
		return
	}

	scraper.Paginate(ctx, opts, "showybeauty", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.get(ctx, listingURL(page))
		if err != nil {
			// A 404 past the last listing page is the normal end of pagination.
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		cards := strings.Count(body, `class="title">`)
		scenes := parseItems(body, studioURL, "", now)
		if page == 1 && cards > 0 && len(scenes) == 0 {
			return scraper.PageResult{}, scraper.ParseError(listingURL(page), errors.New("video cards present but none parsed"))
		}
		return scraper.PageResult{Scenes: scenes, Done: cards < pageSize}, nil
	})
}

func listingURL(page int) string {
	if page <= 1 {
		return siteBase + "/videos.html"
	}
	return fmt.Sprintf("%s/videos_%d.html", siteBase, page)
}

// parseItems extracts the video sets from a listing or model page. Photo sets
// (whose resolution cell names a pixel size rather than "Video") are skipped.
// fallbackPerformer is used when a card carries no model link (model pages).
func parseItems(body, studioURL, fallbackPerformer string, now time.Time) []models.Scene {
	var scenes []models.Scene
	seen := map[string]bool{}
	for _, chunk := range itemSplitRe.Split(body, -1)[1:] {
		if i := strings.Index(chunk, "<!-- spacer"); i >= 0 {
			chunk = chunk[:i]
		}
		cm := coverRe.FindStringSubmatch(chunk)
		if cm == nil {
			continue
		}
		res := resolutionRe.FindStringSubmatch(chunk)
		if res == nil || !strings.EqualFold(strings.TrimSpace(res[1]), "video") {
			continue
		}
		id := cm[1]
		if seen[id] {
			continue
		}
		seen[id] = true

		title := html.UnescapeString(strings.TrimSpace(cm[2]))
		title = strings.TrimSpace(videoSuffix.ReplaceAllString(title, ""))
		sc := models.Scene{
			ID:        id,
			SiteID:    "showybeauty",
			StudioURL: studioURL,
			Title:     title,
			URL:       fmt.Sprintf("%s/cover_%s.html", siteBase, id),
			Studio:    "Showy Beauty",
			ScrapedAt: now,
		}
		if sc.Title == "" {
			sc.Title = "Showy Beauty " + id
		}
		if m := thumbRe.FindStringSubmatch(chunk); m != nil {
			sc.Thumbnail = siteBase + m[1]
		}
		if m := addedRe.FindStringSubmatch(chunk); m != nil {
			if d, err := parseutil.TryParseDate(m[1], "2006-01-02"); err == nil {
				sc.Date = d
			}
		}
		if m := photogRe.FindStringSubmatch(chunk); m != nil {
			sc.Director = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		if m := countRe.FindStringSubmatch(chunk); m != nil {
			sc.Duration = parseutil.ParseDurationColon(m[1])
		}
		for _, m := range performerRe.FindAllStringSubmatch(chunk, -1) {
			if name := html.UnescapeString(strings.TrimSpace(m[1])); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
		if len(sc.Performers) == 0 && fallbackPerformer != "" {
			sc.Performers = []string{fallbackPerformer}
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

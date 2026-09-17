// Package nerdpervert scrapes Nerd Pervert (nerdpervert.com). Listing-only:
// the tour publishes its latest 30 episodes and paginates nowhere.
// See docs/scrapers.md.
package nerdpervert

import (
	"context"
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

const (
	siteID     = "nerdpervert"
	studioName = "Nerd Pervert"
	siteBase   = "https://nerdpervert.com"
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?nerdpervert\.com(?:/|$)`)
	cardStartRe = regexp.MustCompile(`<div class="item video-item">`)
	titleRe     = regexp.MustCompile(`(?s)<h3 class="item-title"><a[^>]*>(.*?)</a>`)
	linkRe      = regexp.MustCompile(`/tour/trailers/([^"]+)\.html`)
	setIDRe     = regexp.MustCompile(`id="set-target-(\d+)"`)
	thumbRe     = regexp.MustCompile(`src0_1x="([^"]+)"`)
	// The publish date is rendered only as an HTML comment in the card.
	dateRe     = regexp.MustCompile(`<!--\s*(\d{4}-\d{2}-\d{2})\s*-->`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"nerdpervert.com", "nerdpervert.com/tour/"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, out chan<- scraper.SceneResult) {
	defer close(out)

	body, err := s.fetchPage(ctx, s.base+"/tour/")
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	scenes := parseScenes(body, studioURL, time.Now().UTC())
	if len(scenes) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(s.base+"/tour/", fmt.Errorf("no episode cards on the tour"))))
		return
	}
	scraper.Debugf(1, "nerdpervert: %d episodes on the tour", len(scenes))
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}
	for _, scene := range scenes {
		if !send(ctx, out, scraper.Scene(scene)) {
			return
		}
	}
}

func parseScenes(body, studioURL string, now time.Time) []models.Scene {
	starts := cardStartRe.FindAllStringIndex(body, -1)
	scenes := make([]models.Scene, 0, len(starts))
	seen := map[string]bool{}
	for i, loc := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := body[loc[0]:end]

		link := linkRe.FindStringSubmatch(card)
		if link == nil {
			continue
		}
		scene := models.Scene{
			ID:        link[1],
			SiteID:    siteID,
			StudioURL: studioURL,
			Studio:    studioName,
			URL:       siteBase + "/tour/trailers/" + link[1] + ".html",
			ScrapedAt: now,
		}
		if id := setIDRe.FindStringSubmatch(card); id != nil {
			scene.ID = id[1]
		}
		if seen[scene.ID] {
			continue
		}
		seen[scene.ID] = true

		if t := titleRe.FindStringSubmatch(card); t != nil {
			scene.Title = cleanText(t[1])
		}
		if scene.Title == "" {
			scene.Title = strings.ReplaceAll(link[1], "-", " ")
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			scene.Thumbnail = absURL(th[1])
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if parsed, err := parseutil.TryParseDate(d[1], "2006-01-02"); err == nil {
				scene.Date = parsed.UTC()
			}
		}
		scenes = append(scenes, scene)
	}
	return scenes
}

func absURL(u string) string {
	switch {
	case u == "":
		return ""
	case strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return siteBase + u
	default:
		return siteBase + "/tour/" + u
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
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

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

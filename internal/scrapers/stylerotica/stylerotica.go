// Package stylerotica scrapes Stylerotica (stylerotica.com). Listing-only: the
// site's own nav links, its detail pages and its model pages all 404, so the
// homepage carousel is the whole public catalogue. See docs/scrapers.md.
package stylerotica

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
	siteID     = "stylerotica"
	studioName = "Stylerotica"
	siteBase   = "https://www.stylerotica.com"
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?stylerotica\.com(?:/|$)`)
	cardRe     = regexp.MustCompile(`(?s)<div class="updateItem">(.*?)</div>\s*</div>`)
	linkRe     = regexp.MustCompile(`/updates/([^"]+)\.html"`)
	titleRe    = regexp.MustCompile(`(?s)<h4>\s*<a[^>]*>\s*(.*?)\s*</a>`)
	modelRe    = regexp.MustCompile(`/models/[^"]+\.html"[^>]*>\s*([^<]{1,60}?)\s*</a>`)
	dateRe     = regexp.MustCompile(`<span class="availdate">\s*(\d{2}/\d{2}/\d{4})\s*</span>`)
	thumbRe    = regexp.MustCompile(`src0_1x="([^"]+)"`)
	dateLayout = "01/02/2006"
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

func (s *Scraper) Patterns() []string { return []string{"stylerotica.com"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, out chan<- scraper.SceneResult) {
	defer close(out)

	body, err := s.fetchPage(ctx, s.base+"/")
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	scenes := parseScenes(body, studioURL, time.Now().UTC())
	if len(scenes) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(s.base+"/", fmt.Errorf("no update cards on the homepage"))))
		return
	}
	scraper.Debugf(1, "stylerotica: %d scenes on the homepage", len(scenes))
	if !send(ctx, out, scraper.Progress(len(scenes))) {
		return
	}
	for _, scene := range scenes {
		if !send(ctx, out, scraper.Scene(scene)) {
			return
		}
	}
}

// parseScenes reads the homepage update cards, which the carousel repeats.
func parseScenes(body, studioURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	seen := map[string]bool{}
	for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
		card := m[1]
		link := linkRe.FindStringSubmatch(card)
		if link == nil || seen[link[1]] {
			continue
		}
		seen[link[1]] = true

		scene := models.Scene{
			ID:        link[1],
			SiteID:    siteID,
			StudioURL: studioURL,
			Studio:    studioName,
			URL:       siteBase + "/updates/" + link[1] + ".html",
			ScrapedAt: now,
		}
		if t := titleRe.FindStringSubmatch(card); t != nil {
			scene.Title = cleanText(t[1])
		}
		if scene.Title == "" {
			scene.Title = strings.ReplaceAll(link[1], "-", " ")
		}
		if mm := modelRe.FindAllStringSubmatch(card, -1); mm != nil {
			for _, model := range mm {
				scene.Performers = append(scene.Performers, cleanText(model[1]))
			}
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if parsed, err := parseutil.TryParseDate(d[1], dateLayout); err == nil {
				scene.Date = parsed.UTC()
			}
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			scene.Thumbnail = absURL(th[1])
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
		return siteBase + "/" + u
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
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

// Package latinsandra scrapes Latin Sandra (latinsandra.com). The site's
// only public listing is one page of titles with their trailer files. See
// docs/scrapers.md.
package latinsandra

import (
	"context"
	"errors"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "latinsandra"
	studioName = "Latin Sandra"
	performer  = "Latin Sandra"
)

var siteBase = "https://www.latinsandra.com"

var (
	matchRe  = regexp.MustCompile(`^https?://(?:www\.)?latinsandra\.com(?:/|$)`)
	cardRe   = regexp.MustCompile(`(?s)<div class="col-md-3 name-scort">.*?<p>([^<]*)</p>`)
	sourceRe = regexp.MustCompile(`<source src="([^"]+)"`)
	slugRe   = regexp.MustCompile(`[^a-z0-9]+`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"latinsandra.com", "latinsandra.com/user/view_more_video"}
}

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
		u := siteBase + "/user/view_more_video"
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := httpx.ReadBody(resp.Body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseVideos(string(body), studioURL, u, now)
		if len(scenes) == 0 {
			return scraper.PageResult{}, scraper.ParseError(u, errors.New("no video cards"))
		}
		return scraper.PageResult{Scenes: scenes, Total: len(scenes), Done: true}, nil
	})
}

// parseVideos reads the title cards. There are no per-scene pages, IDs,
// dates or durations; the title slug is the ID and the scene URL is the
// listing with that slug as its fragment.
func parseVideos(body, studioURL, listURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	seen := map[string]bool{}
	for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
		title := strings.Join(strings.Fields(html.UnescapeString(m[1])), " ")
		id := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(title), "-"), "-")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		sc := models.Scene{
			ID:         id,
			SiteID:     siteID,
			StudioURL:  studioURL,
			Title:      title,
			URL:        listURL + "#" + id,
			Studio:     studioName,
			Performers: []string{performer},
			ScrapedAt:  now,
		}
		if src := sourceRe.FindStringSubmatch(m[0]); src != nil {
			sc.Preview = strings.ReplaceAll(html.UnescapeString(src[1]), " ", "%20")
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

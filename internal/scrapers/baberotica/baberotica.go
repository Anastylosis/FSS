// Package baberotica scrapes Baberotica and BaberoticaVR, two WordPress sites
// on one theme whose scenes are the `vms_videos` post type.
// See docs/scrapers.md.
package baberotica

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/scrapers/wputil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(baberotica(?:vr)?)\.com(?:/|$)`)

type site struct{ id, name string }

var sites = map[string]site{
	"baberotica":   {"baberotica", "Baberotica"},
	"baberoticavr": {"baberoticavr", "BaberoticaVR"},
}

type Scraper struct {
	client *http.Client
	// base overrides the site origin; tests point it at an httptest server.
	base string
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "baberotica" }

func (s *Scraper) Patterns() []string {
	return []string{"baberotica.com", "baberoticavr.com", "baberotica.com/{slug}", "baberoticavr.com/{slug}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	st := siteFor(studioURL)
	base := s.base
	if base == "" {
		base = "https://" + st.id + ".com"
	}
	scraper.Debugf(1, "baberotica: walking the %s vms_videos sitemap", st.id)
	sitemaps := s.sitemaps(ctx, base)
	wputil.RunWorkerPool(ctx, s.client, headers(), sitemaps, studioURL, opts,
		func(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, bool, error) {
			scene, err := parseScene(body, st, studioURL, pageURL, now)
			return scene, false, err
		}, out)
}

func siteFor(studioURL string) site {
	if m := matchRe.FindStringSubmatch(studioURL); m != nil {
		return sites[m[1]]
	}
	return sites["baberotica"]
}

func (s *Scraper) sitemaps(ctx context.Context, base string) []string {
	fallback := []string{base + "/wp-sitemap-posts-vms_videos-1.xml"}
	all, err := wputil.FetchSitemapIndex(ctx, s.client, base+"/wp-sitemap.xml", headers())
	if err != nil {
		return fallback
	}
	var out []string
	for _, u := range all {
		if strings.Contains(u, "vms_videos") {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func headers() map[string]string { return httpx.BrowserHeaders(httpx.UserAgentFirefox) }

func parseScene(body []byte, st site, studioURL, pageURL string, now time.Time) (models.Scene, error) {
	vo := parseutil.ExtractVideoObject(body)
	if vo == nil || strings.TrimSpace(vo.Name) == "" {
		return models.Scene{}, scraper.ParseError(pageURL, fmt.Errorf("no VideoObject JSON-LD on the page"))
	}
	scene := models.Scene{
		ID:          wputil.SlugFromURL(pageURL),
		SiteID:      st.id,
		StudioURL:   studioURL,
		Studio:      st.name,
		Title:       html.UnescapeString(strings.TrimSpace(vo.Name)),
		URL:         pageURL,
		Description: html.UnescapeString(strings.TrimSpace(vo.Description)),
		Thumbnail:   vo.ThumbnailURL,
		Duration:    parseutil.ParseDurationISO(vo.Duration),
		Performers:  vo.Actors,
		ScrapedAt:   now,
	}
	if t, err := parseutil.TryParseDate(vo.UploadDate, time.RFC3339); err == nil {
		scene.Date = t.UTC()
	}
	return scene, nil
}

// Package avjiali scrapes AV Jiali (avjiali.com), a WordPress site whose scenes
// are the `vms_videos` post type and so have their own sitemap.
// See docs/scrapers.md.
package avjiali

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

const (
	siteID     = "avjiali"
	studioName = "AV Jiali"
	siteBase   = "https://avjiali.com"
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?avjiali\.com(?:/|$)`)
	titleRe    = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	ogDescRe   = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	ogImageRe  = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	durationRe = regexp.MustCompile(`(?s)class="video-duration"[^>]*>.*?(\d{1,2}:\d{2}(?::\d{2})?)`)
	dateRe     = regexp.MustCompile(`(?s)class="video-date"[^>]*>.*?([A-Z][a-z]+ \d{1,2})[a-z]{2}, (\d{4})`)
	modelRe    = regexp.MustCompile(`href="[^"]*?/models?/([^"/]+)/?"[^>]*>\s*([^<]{2,40}?)\s*<`)
	scriptRe   = regexp.MustCompile(`(?s)<script.*?</script>`)
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

func (s *Scraper) Patterns() []string { return []string{"avjiali.com", "avjiali.com/{slug}"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "avjiali: walking the vms_videos sitemap")
	wputil.RunWorkerPool(ctx, s.client, headers(), s.sitemaps(ctx), studioURL, opts, s.parsePage, out)
}

// sitemaps returns every vms_videos sitemap in the index; the post type is
// paginated once it passes 2000 entries.
func (s *Scraper) sitemaps(ctx context.Context) []string {
	all, err := wputil.FetchSitemapIndex(ctx, s.client, s.base+"/wp-sitemap.xml", headers())
	if err != nil {
		return []string{s.base + "/wp-sitemap-posts-vms_videos-1.xml"}
	}
	var out []string
	for _, u := range all {
		if strings.Contains(u, "vms_videos") {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		out = append(out, s.base+"/wp-sitemap-posts-vms_videos-1.xml")
	}
	return out
}

func headers() map[string]string { return httpx.BrowserHeaders(httpx.UserAgentFirefox) }

func (s *Scraper) parsePage(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, bool, error) {
	scene, err := parseScene(string(body), studioURL, pageURL, now)
	if err != nil {
		return models.Scene{}, false, err
	}
	return scene, false, nil
}

// parseScene reads the theme's own blocks: the runtime and date sit in
// `video-duration` and `video-date` spans, and the cast is a /models/ link.
func parseScene(body, studioURL, pageURL string, now time.Time) (models.Scene, error) {
	page := scriptRe.ReplaceAllString(body, "")

	title := ""
	if m := titleRe.FindStringSubmatch(page); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, scraper.ParseError(pageURL, fmt.Errorf("no <h1> title on the page"))
	}

	scene := models.Scene{
		ID:        wputil.SlugFromURL(pageURL),
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       pageURL,
		ScrapedAt: now,
	}
	if m := ogDescRe.FindStringSubmatch(body); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}
	if m := durationRe.FindStringSubmatch(page); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}
	if m := dateRe.FindStringSubmatch(page); m != nil {
		// The theme writes an ordinal day ("May 13th, 2022").
		if t, err := parseutil.TryParseDate(m[1]+", "+m[2], "January 2, 2006"); err == nil {
			scene.Date = t.UTC()
		}
	}
	seen := map[string]bool{}
	for _, m := range modelRe.FindAllStringSubmatch(page, -1) {
		name := cleanText(m[2])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		scene.Performers = append(scene.Performers, name)
	}
	return scene, nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

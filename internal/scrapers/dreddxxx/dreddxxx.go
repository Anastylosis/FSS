// Package dreddxxx scrapes The Official Dredd XXX (officialdreddxxx.com), a
// WordPress site whose scenes are a post type of their own with their own
// sitemap. See docs/scrapers.md.
package dreddxxx

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/scrapers/wputil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "dreddxxx"
	studioName = "Dredd XXX"
	siteBase   = "https://officialdreddxxx.com"
)

var (
	matchRe     = regexp.MustCompile(`^https?://(?:www\.)?officialdreddxxx\.com(?:/|$)`)
	performerRe = regexp.MustCompile(`/pornstar/([a-z0-9-]+)/?"[^>]*>([^<]{1,80})<`)
	categoryRe  = regexp.MustCompile(`/scene-category/([a-z0-9-]+)/?"[^>]*>([^<]{1,80})<`)
	jsonLDRe    = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*?)</script>`)
	titleRe     = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	ogDescRe    = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
	ogImageRe   = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
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

func (s *Scraper) Patterns() []string {
	return []string{"officialdreddxxx.com", "officialdreddxxx.com/scene/{slug}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "dreddxxx: walking the scene sitemap")
	wputil.RunWorkerPool(ctx, s.client, headers(), []string{s.base + "/scene-sitemap.xml"},
		studioURL, opts, s.parsePage, out)
}

// headers describes a page navigation; anything else is answered with a 403.
func headers() map[string]string {
	return httpx.BrowserHeaders(httpx.UserAgentFirefox)
}

func (s *Scraper) parsePage(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, bool, error) {
	scene, err := parseScene(studioURL, pageURL, body, now)
	if err != nil {
		return models.Scene{}, false, err
	}
	return scene, false, nil
}

func parseScene(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, error) {
	page := string(body)

	title := ""
	if m := titleRe.FindStringSubmatch(page); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, scraper.ParseError(pageURL, fmt.Errorf("no <h1> title on the page"))
	}

	scene := models.Scene{
		ID:         wputil.SlugFromURL(pageURL),
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      title,
		URL:        pageURL,
		Date:       publishedAt(page),
		Performers: taxonomy(performerRe, page),
		Categories: taxonomy(categoryRe, page),
		ScrapedAt:  now,
	}
	if m := ogDescRe.FindStringSubmatch(page); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(page); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}
	return scene, nil
}

// publishedAt reads datePublished from the JSON-LD graph. dateModified is not a
// fallback — see docs/scrapers.md.
func publishedAt(page string) time.Time {
	for _, m := range jsonLDRe.FindAllStringSubmatch(page, -1) {
		var doc struct {
			Graph []struct {
				Type          string `json:"@type"`
				DatePublished string `json:"datePublished"`
			} `json:"@graph"`
		}
		if json.Unmarshal([]byte(m[1]), &doc) != nil {
			continue
		}
		for _, node := range doc.Graph {
			if node.DatePublished == "" {
				continue
			}
			if t, err := time.Parse(time.RFC3339, node.DatePublished); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Time{}
}

// taxonomy collects the names behind taxonomy links, de-duplicated, in page order.
func taxonomy(re *regexp.Regexp, page string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		name := cleanText(m[2])
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out
}

func cleanText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

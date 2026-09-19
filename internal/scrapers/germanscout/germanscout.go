// Package germanscout scrapes German Scout (german-scout.com), a WordPress
// site whose scenes are ordinary posts. See docs/scrapers.md.
package germanscout

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
	siteID     = "germanscout"
	studioName = "German Scout"
	siteBase   = "https://www.german-scout.com"
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?german-scout\.com(?:/|$)`)
	// englishRe marks the /en/ mirror of a post, which the sitemap lists
	// alongside the German original.
	englishRe  = regexp.MustCompile(`^https?://[^/]+/en/`)
	slugRe     = regexp.MustCompile(`/(\d{4})/(\d{2})/(\d{2})/([^/]+)/?$`)
	jsonLDRe   = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*)?</script>`)
	ogTitleRe  = regexp.MustCompile(`<meta property="og:title" content="([^"]*)"`)
	ogDescRe   = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
	ogImageRe  = regexp.MustCompile(`<meta property="og:image" content="([^"]*)"`)
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

func (s *Scraper) Patterns() []string {
	return []string{"german-scout.com", "german-scout.com/{yyyy}/{mm}/{dd}/{slug}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "germanscout: walking the post sitemap")
	wputil.RunWorkerPool(ctx, s.client, headers(), []string{s.base + "/post-sitemap.xml"},
		studioURL, opts, s.parsePage, out)
}

func headers() map[string]string { return httpx.BrowserHeaders(httpx.UserAgentFirefox) }

func (s *Scraper) parsePage(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, bool, error) {
	// The sitemap lists each post twice, German and /en/; the German original
	// is the canonical one.
	if englishRe.MatchString(pageURL) {
		return models.Scene{}, true, nil
	}
	scene, err := parseScene(string(body), studioURL, pageURL, now)
	if err != nil {
		return models.Scene{}, false, err
	}
	return scene, false, nil
}

// parseScene reads the post's OpenGraph title and description plus the
// JSON-LD Article node, which is where the publish date lives.
func parseScene(body, studioURL, pageURL string, now time.Time) (models.Scene, error) {
	title := ""
	if m := ogTitleRe.FindStringSubmatch(body); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, scraper.ParseError(pageURL, fmt.Errorf("no og:title on the page"))
	}

	scene := models.Scene{
		ID:        sceneID(pageURL),
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       pageURL,
		Date:      publishedAt(body, pageURL),
		ScrapedAt: now,
	}
	if m := ogDescRe.FindStringSubmatch(body); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		scene.Thumbnail = html.UnescapeString(m[1])
	}
	return scene, nil
}

// sceneID is the post slug; the date in the permalink is not part of it, so a
// re-dated post keeps its id.
func sceneID(pageURL string) string {
	if m := slugRe.FindStringSubmatch(pageURL); m != nil {
		return m[4]
	}
	return wputil.SlugFromURL(pageURL)
}

// publishedAt prefers the JSON-LD Article date and falls back to the date in
// the permalink, which WordPress writes from the same value.
func publishedAt(body, pageURL string) time.Time {
	if m := jsonLDRe.FindStringSubmatch(body); m != nil {
		var doc struct {
			Graph []struct {
				Type          string `json:"@type"`
				DatePublished string `json:"datePublished"`
			} `json:"@graph"`
		}
		if json.Unmarshal([]byte(m[1]), &doc) == nil {
			for _, node := range doc.Graph {
				if node.Type != "Article" || node.DatePublished == "" {
					continue
				}
				if t, err := time.Parse(time.RFC3339, node.DatePublished); err == nil {
					return t.UTC()
				}
			}
		}
	}
	if m := slugRe.FindStringSubmatch(pageURL); m != nil {
		if t, err := time.Parse("2006/01/02", m[1]+"/"+m[2]+"/"+m[3]); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

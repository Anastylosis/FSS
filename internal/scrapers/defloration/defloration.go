// Package defloration scrapes Defloration (defloration.com). The public
// free-tour feed (/freetour.php?language=en) renders the entire catalog as a
// single page of ~101 <article class="feed-card"> blocks — there is no
// pagination, no JSON API, and no per-scene page. Each card carries an item id,
// a title, a story description, and either an inline <video> with a poster
// image or a still <img>. No dates, durations, or performers are exposed.
package defloration

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
	"github.com/Anastylosis/FSS/scraper"
)

var siteBase = "https://www.defloration.com"

const siteID = "defloration"

type Scraper struct{ Client *http.Client }

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var (
	_ scraper.StudioScraper = (*Scraper)(nil)
	_ scraper.MultiLingual  = (*Scraper)(nil)
)

func init() { scraper.Register(New()) }

// supportedLanguages is the feed's own set, default first; an unrecognised
// value is served as English rather than refused. See docs/scrapers.md.
var supportedLanguages = []string{"en", "de", "es", "fr", "jp", "zh"}

// Languages implements scraper.MultiLingual.
func (s *Scraper) Languages() []string { return supportedLanguages }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{"defloration.com", "defloration.com/freetour.php"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?defloration\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe = regexp.MustCompile(`<article class="feed-card"`)
	itemIDRe    = regexp.MustCompile(`data-item-id="([^"]+)"`)
	strongRe    = regexp.MustCompile(`(?s)<strong>(.*?)</strong>`)
	storyFullRe = regexp.MustCompile(`(?s)<p class="story-full"[^>]*>(.*?)</p>`)
	storyPrevRe = regexp.MustCompile(`(?s)<p class="story-preview"[^>]*>(.*?)</p>`)
	imgPosterRe = regexp.MustCompile(`<img class="feed-media"[^>]*src="([^"]+)"`)
	vidPosterRe = regexp.MustCompile(`(?s)<video[^>]*poster="([^"]+)"`)
	buttonRe    = regexp.MustCompile(`(?s)<button[^>]*>.*?</button>`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	wsRe        = regexp.MustCompile(`\s+`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	language := opts.Language
	if language == "" {
		language = supportedLanguages[0]
	}
	listURL := siteBase + "/freetour.php?language=" + language
	scraper.Debugf(1, "%s: fetching free-tour feed %s", siteID, listURL)
	cards, err := s.fetchCards(ctx, listURL)
	if err != nil {
		s.send(ctx, out, scraper.Error(fmt.Errorf("fetching feed: %w", err)))
		return
	}

	scraper.Debugf(1, "%s: parsed %d feed cards", siteID, len(cards))
	s.send(ctx, out, scraper.Progress(len(cards)))

	now := time.Now().UTC()
	for _, card := range cards {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if sc, ok := toScene(studioURL, listURL, card, now); ok {
			s.send(ctx, out, scraper.Scene(sc))
		}
	}
}

func (s *Scraper) fetchCards(ctx context.Context, pageURL string) ([]string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	body, err := func() ([]byte, error) {
		defer func() { _ = resp.Body.Close() }()
		return httpx.ReadBody(resp.Body)
	}()
	if err != nil {
		return nil, err
	}
	parts := cardSplitRe.Split(string(body), -1)
	if len(parts) <= 1 {
		return nil, nil
	}
	return parts[1:], nil
}

func toScene(studioURL, listURL, card string, now time.Time) (models.Scene, bool) {
	m := itemIDRe.FindStringSubmatch(card)
	if m == nil {
		return models.Scene{}, false
	}
	scene := models.Scene{
		ID:        m[1],
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       listURL,
		Studio:    "Defloration",
		ScrapedAt: now,
	}
	if t := strongRe.FindStringSubmatch(card); t != nil {
		scene.Title = cleanText(t[1])
	}
	if scene.Title == "" {
		scene.Title = m[1]
	}
	if d := storyFullRe.FindStringSubmatch(card); d != nil {
		scene.Description = cleanText(d[1])
	} else if d := storyPrevRe.FindStringSubmatch(card); d != nil {
		scene.Description = cleanText(d[1])
	}
	if p := imgPosterRe.FindStringSubmatch(card); p != nil {
		scene.Thumbnail = absURL(p[1])
	} else if p := vidPosterRe.FindStringSubmatch(card); p != nil {
		scene.Thumbnail = absURL(p[1])
	}
	return scene, true
}

// absURL turns a relative poster path (e.g. "imgs/foo.jpg") into a full URL.
func absURL(src string) string {
	if src == "" || strings.HasPrefix(src, "http") {
		return src
	}
	return siteBase + "/" + strings.TrimPrefix(src, "/")
}

// cleanText strips embedded buttons and tags, unescapes entities, and collapses
// whitespace.
func cleanText(s string) string {
	s = buttonRe.ReplaceAllString(s, "")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

func (s *Scraper) send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) {
	select {
	case out <- r:
	case <-ctx.Done():
	}
}

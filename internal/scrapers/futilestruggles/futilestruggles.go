// Package futilestruggles scrapes Futile Struggles (futilestruggles.com), an
// Elevated X Classic trial tour whose listing cards carry the whole record.
// See docs/scrapers.md.
package futilestruggles

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "futilestruggles"
	studioName = "Futile Struggles"
	siteBase   = "https://www.futilestruggles.com"
	tourPath   = "/trial"
	// maxPages bounds the walk; the tour runs to ~101 pages.
	maxPages = 300
)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?futilestruggles\.com(?:/|$)`)
	cardRe     = regexp.MustCompile(`<div class="update_details" data-setid="(\d+)">`)
	titleRe    = regexp.MustCompile(`(?s)gallery\.php\?id=\d+&(?:amp;)?type=vids"[^>]*>\s*([^<]{2,160}?)\s*</a>`)
	modelsRe   = regexp.MustCompile(`(?s)<span class="update_models">(.*?)</span>`)
	linkTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	thumbRe    = regexp.MustCompile(`<img[^>]+class="update_thumb[^"]*"[^>]+src="([^"]+)"`)
	minutesRe  = regexp.MustCompile(`(\d+)\s*(?:&nbsp;|\s)*min`)
	// The date sits behind an HTML comment inside its own cell.
	dateRe     = regexp.MustCompile(`(?s)<div class="cell update_date">.*?(\d{2}/\d{2}/\d{4})`)
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
	return []string{"futilestruggles.com", "futilestruggles.com/trial/"}
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
	seen := map[string]bool{}
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > maxPages {
			return scraper.PageResult{Done: true}, nil
		}
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s%s/index.php?page=%d", s.base, tourPath, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseScenes(body, studioURL, now)
		fresh := scenes[:0]
		for _, sc := range scenes {
			if seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			fresh = append(fresh, sc)
		}
		return scraper.PageResult{Scenes: fresh, Done: len(fresh) == 0}, nil
	})
}

// parseScenes reads the update cards: id, title, cast, runtime, date and
// thumbnail all live there, and the gallery page behind them is members-only.
func parseScenes(body, studioURL string, now time.Time) []models.Scene {
	// Cards are sliced between their opening markers: closing one with a regex
	// that matches the next card's opening tag consumes it, dropping every
	// second card — RE2 has no lookahead.
	starts := cardRe.FindAllStringSubmatchIndex(body, -1)
	scenes := make([]models.Scene, 0, len(starts))
	for i, m := range starts {
		id := body[m[2]:m[3]]
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := body[m[1]:end]
		scene := models.Scene{
			ID:        id,
			SiteID:    siteID,
			StudioURL: studioURL,
			Studio:    studioName,
			URL:       fmt.Sprintf("%s%s/gallery.php?id=%s&type=vids", siteBase, tourPath, id),
			ScrapedAt: now,
		}
		if t := titleRe.FindStringSubmatch(card); t != nil {
			scene.Title = cleanText(t[1])
		}
		if scene.Title == "" {
			scene.Title = "Set " + id
		}
		if mm := modelsRe.FindStringSubmatch(card); mm != nil {
			for _, name := range linkTextRe.FindAllStringSubmatch(mm[1], -1) {
				if n := cleanText(name[1]); n != "" {
					scene.Performers = append(scene.Performers, n)
				}
			}
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			scene.Thumbnail = absURL(th[1])
		}
		// The card gives whole minutes ("15 min of video").
		if mins := minutesRe.FindStringSubmatch(card); mins != nil {
			if n, err := strconv.Atoi(mins[1]); err == nil {
				scene.Duration = n * 60
			}
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if parsed, err := parseutil.TryParseDate(d[1], "01/02/2006"); err == nil {
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
		return siteBase + tourPath + "/" + u
	}
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
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

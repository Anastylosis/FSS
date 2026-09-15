// Package mentalpass scrapes the Mental Pass network — Bitch Stop, CZasting
// and Czech GFS — which share one hand-rolled tour. See docs/scrapers.md.
package mentalpass

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

// SiteConfig is one site on the network.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

const (
	// perPage is the tour's page stride: ?next= counts scenes, not pages.
	perPage = 10
	// maxPages bounds the walk; the largest site runs to ~12 pages.
	maxPages = 60
)

var (
	articleRe = regexp.MustCompile(`(?s)<article>(.*?)</article>`)
	// The heading is "<Site> <episode> - <a …>Performer</a>", with the site
	// name broken up by markup on some sites.
	headingRe  = regexp.MustCompile(`(?s)<h2>(.*?)</h2>`)
	linkTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	dirRe      = regexp.MustCompile(`\./category/([^/"]+)/`)
	imgRe      = regexp.MustCompile(`<img[^>]+src="(\./category/[^"]+)"`)
	// The description runs to the end of the article: the first </div> inside
	// it closes the join call-to-action, not the text.
	textRe      = regexp.MustCompile(`(?s)<div id="Text">(.*)`)
	getAccessRe = regexp.MustCompile(`(?s)<div class="getAccess">.*?</div>`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

// New builds a scraper for one network site.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string { return []string{s.cfg.Domain} }

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	seen := map[string]bool{}
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > maxPages {
			return scraper.PageResult{Done: true}, nil
		}
		body, err := s.fetchPage(ctx, s.pageURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseScenes(body, s.cfg, studioURL, time.Now().UTC())
		fresh := scenes[:0]
		for _, sc := range scenes {
			if seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			fresh = append(fresh, sc)
		}
		// Past the end the tour serves the shell with no scene blocks, and a
		// repeat of an already-seen page means the same thing.
		return scraper.PageResult{Scenes: fresh, Done: len(fresh) == 0}, nil
	})
}

// pageURL builds the listing URL: ?next= is an offset in scenes, and page 1 is
// the bare path.
func (s *Scraper) pageURL(page int) string {
	if page <= 1 {
		return s.base + "/"
	}
	return fmt.Sprintf("%s/?next=%d", s.base, (page-1)*perPage+1)
}

// parseScenes reads the scene articles. There is no per-scene page — every link
// points at the join form — so the listing article is the whole record.
func parseScenes(body string, cfg SiteConfig, studioURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	for _, m := range articleRe.FindAllStringSubmatch(body, -1) {
		article := m[1]
		dir := dirRe.FindStringSubmatch(article)
		if dir == nil {
			continue
		}
		scene := models.Scene{
			ID:        dir[1],
			SiteID:    cfg.SiteID,
			StudioURL: studioURL,
			Studio:    cfg.StudioName,
			URL:       "https://www." + cfg.Domain + "/",
			ScrapedAt: now,
		}
		if h := headingRe.FindStringSubmatch(article); h != nil {
			scene.Title, scene.Performers = parseHeading(h[1])
		}
		if scene.Title == "" {
			scene.Title = strings.ReplaceAll(dir[1], "-", " ")
		}
		if im := imgRe.FindStringSubmatch(article); im != nil {
			scene.Thumbnail = "https://www." + cfg.Domain + "/" + strings.TrimPrefix(im[1], "./")
		}
		if tx := textRe.FindStringSubmatch(article); tx != nil {
			scene.Description = cleanText(getAccessRe.ReplaceAllString(tx[1], " "))
		}
		scenes = append(scenes, scene)
	}
	return scenes
}

// parseHeading splits "<Site> <episode> - <a>Performer</a>" into the full title
// and the performer the link names.
func parseHeading(heading string) (title string, performers []string) {
	title = cleanText(heading)
	if link := linkTextRe.FindStringSubmatch(heading); link != nil {
		if name := cleanText(link[1]); name != "" {
			performers = []string{name}
		}
	}
	return title, performers
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "<!--", " ")
	s = strings.ReplaceAll(s, "-->", " ")
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
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

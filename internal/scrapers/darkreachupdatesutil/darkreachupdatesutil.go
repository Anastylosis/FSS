// Package darkreachupdatesutil scrapes Darkreach Communications performer
// sites running the "updates clear" affiliate-marketing template
// (clubkayden, thelisaann, theavaaddams, kortneykane).
//
// Card markup:
//
//	<div class="updates clear">
//	  <div class="model">
//	    <a href="https://join.{site}.com/signup/signup.php?nats=…">
//	      <img src="/content/{slug}/0.jpg" />
//	    </a>
//	  </div>
//	  <div class="modelDetails">
//	    <h3><a href="…signup.php?nats=…">Scene Title</a></h3>
//	    <div class="date"></div>
//	    <p>Description text… <a href="…">join now</a></p>
//	  </div>
//	</div>
//
// All scene anchors are affiliate `/signup.php?nats=...` URLs — there are no
// public detail pages. Scene IDs are synthesised from the thumbnail path
// (`/content/{slug}/0.jpg` → slug used as ID); URLs as `{base}/#scene-{id}`.
//
// Pagination: `/updates/page_{N}.html` (page 1 is `/` or `/updates/`).
package darkreachupdatesutil

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
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	ID       string
	SiteBase string
	Studio   string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardStartRe = regexp.MustCompile(`<div class="updates clear">`)
	// Thumb path doubles as the scene ID source — `/content/{slug}/0.jpg`.
	thumbRe = regexp.MustCompile(`<img\s+src="(/?content/([A-Za-z0-9_-]+)/[^"]+)"`)
	titleH3 = regexp.MustCompile(`(?s)<h3>\s*<a[^>]*>\s*([^<]+?)\s*</a>`)
	// Description paragraph (first <p> in the card before pagination markup).
	descRe = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	// Pagination URLs `/updates/page_N.html`.
	pageLinkRe = regexp.MustCompile(`/updates/page_(\d+)\.html`)
)

type sceneItem struct {
	id          string
	title       string
	thumb       string
	description string
}

func parseListing(body []byte) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		var item sceneItem
		if m := thumbRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
			item.id = m[2]
		}
		if item.id == "" || seen[item.id] {
			continue
		}
		seen[item.id] = true

		if m := titleH3.FindStringSubmatch(block); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		if m := descRe.FindStringSubmatch(block); m != nil {
			// Strip inline anchors (e.g. "join now") from the description text.
			text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[1], "")
			text = strings.TrimSpace(html.UnescapeString(text))
			// Many entries end with a "join now" call-to-action — drop it.
			text = strings.TrimSuffix(text, "join now")
			text = strings.TrimSpace(text)
			item.description = text
		}

		items = append(items, item)
	}
	return items
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range pageLinkRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		// Page 1 lives at the bare root — `/updates/page_1.html` typically
		// redirects there too.
		return s.cfg.SiteBase + "/"
	}
	return fmt.Sprintf("%s/updates/page_%d.html", s.cfg.SiteBase, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "%s: scraping full catalog", s.cfg.ID)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := estimateTotal(body, len(items))
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(s.cfg.ID, s.cfg.SiteBase, studioURL, s.cfg.Studio, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (item sceneItem) toScene(siteID, siteBase, studioURL, studio string, now time.Time) models.Scene {
	thumb := item.thumb
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		thumb = siteBase + "/" + strings.TrimLeft(thumb, "/")
	}
	return models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         fmt.Sprintf("%s/#scene-%s", siteBase, item.id),
		Thumbnail:   thumb,
		Description: item.description,
		Studio:      studio,
		ScrapedAt:   now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

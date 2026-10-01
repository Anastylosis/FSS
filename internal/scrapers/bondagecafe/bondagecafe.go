// Package bondagecafe scrapes Bondage Cafe (bondagecafe.com), a custom tour
// CMS. The /updates/page_N.html listing is fully server-rendered: each update
// <li> carries the scene id (wmbcv-NNNN), the title, the featured models and a
// thumbnail, so no detail-page fetch is needed. The listing has no publish date
// or runtime, so those are left zero.
package bondagecafe

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

var siteBase = "https://www.bondagecafe.com"

const siteID = "bondagecafe"

type Scraper struct{ Client *http.Client }

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{"bondagecafe.com", "bondagecafe.com/updates", "bondagecafe.com/updates/page_{n}.html"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?bondagecafe\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	itemRe    = regexp.MustCompile(`(?s)<li>(.*?)</li>`)
	titleRe   = regexp.MustCompile(`(?s)<h3>\s*<a[^>]*>\s*(wmbcv-\d+):\s*(.*?)\s*</a>`)
	posterRe  = regexp.MustCompile(`src0_3x="([^"]+)"`)
	srcRe     = regexp.MustCompile(`<img[^>]*\bsrc="([^"]+)"`)
	tloadRe   = regexp.MustCompile(`tload\('([^']+)'\)`)
	modelsRe  = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	modelLink = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/updates/page_%d.html", siteBase, page)
		body, err := s.get(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		all := parseListing(string(body), studioURL, now)
		// Beyond the last page only the CTA tile remains (parsed out already),
		// and pages can repeat trailing items — dedup and stop when nothing new.
		fresh := all[:0]
		for _, sc := range all {
			if !seen[sc.ID] {
				seen[sc.ID] = true
				fresh = append(fresh, sc)
			}
		}
		return scraper.PageResult{Scenes: fresh}, nil
	})
}

func parseListing(doc, studioURL string, now time.Time) []models.Scene {
	var scenes []models.Scene
	for _, m := range itemRe.FindAllStringSubmatch(doc, -1) {
		if sc, ok := toScene(m[1], studioURL, now); ok {
			scenes = append(scenes, sc)
		}
	}
	return scenes
}

func toScene(block, studioURL string, now time.Time) (models.Scene, bool) {
	tm := titleRe.FindStringSubmatch(block)
	if tm == nil {
		return models.Scene{}, false
	}
	id := tm[1]
	sc := models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     cleanText(tm[2]),
		Studio:    "Bondage Cafe",
		ScrapedAt: now,
	}
	if sc.Title == "" {
		sc.Title = id
	}
	if tl := tloadRe.FindStringSubmatch(block); tl != nil {
		sc.URL = absURL(tl[1])
	} else {
		sc.URL = siteBase + "/updates/"
	}
	if p := posterRe.FindStringSubmatch(block); p != nil {
		sc.Thumbnail = absURL(p[1])
	} else if p := srcRe.FindStringSubmatch(block); p != nil {
		sc.Thumbnail = absURL(p[1])
	}
	if mb := modelsRe.FindStringSubmatch(block); mb != nil {
		for _, ml := range modelLink.FindAllStringSubmatch(mb[1], -1) {
			name := cleanText(ml[1])
			if name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
	return sc, true
}

func absURL(p string) string {
	if strings.HasPrefix(p, "http") {
		return p
	}
	return siteBase + "/" + strings.TrimPrefix(p, "/")
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

var tagStripRe = regexp.MustCompile(`<[^>]+>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

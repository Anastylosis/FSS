// Package diapergirls scrapes Diaper Girls (diapergirls.com), whose public
// tour is a listing with no detail pages. See docs/scrapers.md.
package diapergirls

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

const (
	siteID     = "diapergirls"
	studioName = "Diaper Girls"
	perPage    = 40
)

var siteBase = "https://www.diapergirls.com"

var (
	matchRe  = regexp.MustCompile(`^https?://(?:www\.)?(?:uk)?diapergirls\.com(?:/|$)`)
	itemRe   = regexp.MustCompile(`<div class="updateItem">`)
	idRe     = regexp.MustCompile(`alt="content/([^/"]+)/`)
	thumbRe  = regexp.MustCompile(`<img[^>]*?[\s"]src="([^"]+)"`)
	titleRe  = regexp.MustCompile(`(?s)<h4>\s*<a[^>]*>\s*(.*?)\s*</a>`)
	modelsRe = regexp.MustCompile(`(?s)tour_update_models">(.*?)</span>`)
	nameRe   = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	dateRe   = regexp.MustCompile(`<span>(\d{2})/(\d{2})/(\d{4})</span>`)
	pagesRe  = regexp.MustCompile(`Page \d+ of (\d+)`)
	stripRe  = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"diapergirls.com", "ukdiapergirls.com", "diapergirls.com/categories/movies_{N}_d.html"}
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

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/categories/movies_%d_d.html", siteBase, page)
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := httpx.ReadBody(resp.Body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes, pages := parsePage(string(body), studioURL, now)
		if len(scenes) == 0 && page == 1 {
			return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no updateItem cards"))
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(scenes) < perPage || (pages > 0 && page >= pages)}
		if page == 1 && pages > 0 {
			res.Total = pages * perPage
		}
		return res, nil
	})
}

func parsePage(page, studioURL string, now time.Time) ([]models.Scene, int) {
	pages := 0
	if m := pagesRe.FindStringSubmatch(page); m != nil {
		pages, _ = strconv.Atoi(m[1])
	}
	cards := itemRe.Split(page, -1)
	var scenes []models.Scene
	for _, c := range cards[1:] {
		id := idRe.FindStringSubmatch(c)
		if id == nil {
			continue
		}
		sc := models.Scene{
			ID:        id[1],
			SiteID:    siteID,
			StudioURL: studioURL,
			URL:       siteBase + "/categories/movies.html",
			Studio:    studioName,
			ScrapedAt: now,
		}
		if m := titleRe.FindStringSubmatch(c); m != nil {
			sc.Title = clean(m[1])
		}
		if sc.Title == "" {
			sc.Title = id[1]
		}
		if m := thumbRe.FindStringSubmatch(c); m != nil {
			sc.Thumbnail = html.UnescapeString(m[1])
		}
		if m := modelsRe.FindStringSubmatch(c); m != nil {
			for _, n := range nameRe.FindAllStringSubmatch(m[1], -1) {
				if name := clean(n[1]); name != "" {
					sc.Performers = append(sc.Performers, name)
				}
			}
		}
		if m := dateRe.FindStringSubmatch(c); m != nil {
			d, _ := strconv.Atoi(m[1])
			mo, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			sc.Date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		}
		scenes = append(scenes, sc)
	}
	return scenes, pages
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

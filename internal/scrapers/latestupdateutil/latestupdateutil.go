// Package latestupdateutil scrapes sites running the Elevated X tour theme
// whose listing cards are `<div class="latestUpdateB" data-setid="N">` blocks:
//
//	<div class="latestUpdateB" data-setid="494">
//	  <div class="videoPic"><a href="…/scenes/Slug_vids.html">
//	    <img class="update_thumb thumbs stdimage" src0_4x="/content//contentthumbs/09/95/995-4x.jpg" /></a></div>
//	  <h4 class="link_bright"><a href="…/scenes/Slug_vids.html">Title</a></h4>
//	  <p class="link_light"><a class="link_bright infolink" href="…/models/Name.html">Name</a></p>
//	  <ul class="videoInfo">
//	    <li><!-- Date --> 08/27/2026</li>
//	    <li><i class="fas fa-video"></i>23 min</li>
//	  </ul>
//	</div>
//
// Everything is on the card, so there is no detail fetch. Three listing modes
// are supported: the catalogue (`/categories/movies.html`, then
// `movies_{N}.html`), a model (`/models/{Name}.html`, paginated through
// `sets.php?id={id}&page={N}` with the numeric id read off the first page's
// pager) and a DVD (`/dvds/{slug}.html`, a single page).
package latestupdateutil

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

type urlKind int

const (
	kindUpdates urlKind = iota
	kindModel
	kindDVD
)

// SiteConfig describes one site served by this template.
type SiteConfig struct {
	SiteID     string
	SiteBase   string // e.g. "https://sofiemariexxx.com" — no trailing slash
	StudioName string
	// AltDomains are extra hosts that serve the same catalogue. Requests still
	// go to SiteBase; these only widen URL matching.
	AltDomains []string
	Patterns   []string
}

type Scraper struct {
	cfg      SiteConfig
	client   *http.Client
	siteBase string
	matchRe  *regexp.Regexp
}

// New builds a registration-ready scraper for one site.
func New(cfg SiteConfig) *Scraper {
	hosts := append([]string{hostOf(cfg.SiteBase)}, cfg.AltDomains...)
	for i, h := range hosts {
		hosts[i] = regexp.QuoteMeta(strings.TrimPrefix(h, "www."))
	}
	return &Scraper{
		cfg:      cfg,
		client:   httpx.NewClient(30 * time.Second),
		siteBase: cfg.SiteBase,
		matchRe:  regexp.MustCompile(`^https?://(?:www\.)?(?:` + strings.Join(hosts, "|") + `)(?:/|$)`),
	}
}

// hostOf returns the bare host of a site base URL.
func hostOf(base string) string {
	h := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	return strings.TrimPrefix(h, "www.")
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string { return s.cfg.Patterns }

var (
	modelRe = regexp.MustCompile(`/models/([^/?#]+)\.html`)
	dvdRe   = regexp.MustCompile(`/dvds/([^/?#]+)\.html`)
)

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func classifyURL(u string) urlKind {
	if modelRe.MatchString(u) {
		return kindModel
	}
	if dvdRe.MatchString(u) {
		return kindDVD
	}
	return kindUpdates
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	switch classifyURL(studioURL) {
	case kindModel:
		s.runModel(ctx, studioURL, opts, out)
	case kindDVD:
		s.runDVD(ctx, studioURL, opts, out)
	default:
		s.runUpdates(ctx, studioURL, opts, out)
	}
}

func (s *Scraper) runUpdates(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var pageURL string
		if page == 1 {
			pageURL = s.siteBase + "/categories/movies.html"
		} else {
			pageURL = fmt.Sprintf("%s/categories/movies_%d.html", s.siteBase, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		parsed := parseSceneBlocks(body)
		if len(parsed) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = estimateTotal(body, len(parsed))
		}

		scenes := make([]models.Scene, len(parsed))
		for i, ps := range parsed {
			scenes[i] = toScene(ps, s.siteBase, s.cfg.SiteID, s.cfg.StudioName, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total, Done: !hasNextPage(body, page)}, nil
	})
}

func (s *Scraper) runModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

	var modelID string
	var maxPage int
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var pageURL string
		if page == 1 {
			pageURL = studioURL
		} else {
			if modelID == "" || page > maxPage {
				return scraper.PageResult{}, nil
			}
			pageURL = fmt.Sprintf("%s/sets.php?id=%s&page=%d", s.siteBase, modelID, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		parsed := parseSceneBlocks(body)
		if len(parsed) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			modelID, maxPage = extractModelPagination(body)
			if maxPage > 0 {
				total = len(parsed) * maxPage
			}
		}

		scenes := make([]models.Scene, len(parsed))
		for i, ps := range parsed {
			scenes[i] = toScene(ps, s.siteBase, s.cfg.SiteID, s.cfg.StudioName, studioURL, now)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   modelID == "" || maxPage <= 1 || page >= maxPage,
		}, nil
	})
}

func (s *Scraper) runDVD(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > 1 {
			return scraper.PageResult{}, nil
		}

		body, err := s.fetchPage(ctx, studioURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		parsed := parseSceneBlocks(body)
		if len(parsed) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := make([]models.Scene, len(parsed))
		for i, ps := range parsed {
			scenes[i] = toScene(ps, s.siteBase, s.cfg.SiteID, s.cfg.StudioName, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: len(scenes), Done: true}, nil
	})
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

type parsedScene struct {
	id         string
	title      string
	relURL     string
	thumbnail  string
	performers []string
	date       string
	duration   string
}

var (
	sceneStartRe = regexp.MustCompile(`<div\s+class="latestUpdateB"\s+data-setid="(\d+)">`)
	titleRe      = regexp.MustCompile(`(?s)<h4\s+class="link_bright">\s*<a\s+[^>]*href="([^"]*)"[^>]*>([^<]+)</a>`)
	thumbRe      = regexp.MustCompile(`src0_4x="([^"]+)"`)
	thumbFallRe  = regexp.MustCompile(`src0_1x="([^"]+)"`)
	performerRe  = regexp.MustCompile(`<a\s+class="[^"]*infolink[^"]*"\s+href="[^"]*">([^<]+)</a>`)
	dateRe       = regexp.MustCompile(`<!-- Date -->\s*(\d{2}/\d{2}/\d{4})`)
	durationRe   = regexp.MustCompile(`fa-video"></i>(\d+)\s*min`)
	paginationRe = regexp.MustCompile(`(?s)<div\s+class="pagination[^"]*">(.*?)</div>`)
	maxPageRe    = regexp.MustCompile(`(?:movies_|page=)(\d+)`)
	modelIDRe    = regexp.MustCompile(`sets\.php\?id=(\d+)`)
)

func parseSceneBlocks(body []byte) []parsedScene {
	locs := sceneStartRe.FindAllSubmatchIndex(body, -1)
	scenes := make([]parsedScene, 0, len(locs))

	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		ps := parsedScene{
			id: string(body[loc[2]:loc[3]]),
		}

		if tm := titleRe.FindSubmatch(block); tm != nil {
			ps.relURL = string(tm[1])
			ps.title = html.UnescapeString(strings.TrimSpace(string(tm[2])))
		}

		if tm := thumbRe.FindSubmatch(block); tm != nil {
			ps.thumbnail = string(tm[1])
		} else if tm := thumbFallRe.FindSubmatch(block); tm != nil {
			ps.thumbnail = string(tm[1])
		}

		for _, pm := range performerRe.FindAllSubmatch(block, -1) {
			name := strings.TrimSpace(string(pm[1]))
			if name != "" {
				ps.performers = append(ps.performers, name)
			}
		}

		if dm := dateRe.FindSubmatch(block); dm != nil {
			ps.date = string(dm[1])
		}

		if dm := durationRe.FindSubmatch(block); dm != nil {
			ps.duration = string(dm[1])
		}

		scenes = append(scenes, ps)
	}

	return scenes
}

func estimateTotal(body []byte, firstPageCount int) int {
	pm := paginationRe.FindSubmatch(body)
	if pm == nil {
		return firstPageCount
	}

	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(pm[1], -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	return maxPage * firstPageCount
}

func hasNextPage(body []byte, currentPage int) bool {
	pm := paginationRe.FindSubmatch(body)
	if pm == nil {
		return false
	}

	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(pm[1], -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	return currentPage < maxPage
}

func extractModelPagination(body []byte) (modelID string, maxPage int) {
	pm := paginationRe.FindSubmatch(body)
	if pm == nil {
		return "", 0
	}

	if m := modelIDRe.FindSubmatch(pm[1]); m != nil {
		modelID = string(m[1])
	}

	maxPage = 1
	for _, m := range maxPageRe.FindAllSubmatch(pm[1], -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	return modelID, maxPage
}

func toScene(ps parsedScene, siteBase, siteID, studioName, studioURL string, now time.Time) models.Scene {
	sceneURL := ps.relURL
	if sceneURL != "" && !strings.HasPrefix(sceneURL, "http") {
		sceneURL = siteBase + "/" + strings.TrimPrefix(sceneURL, "/")
	}

	thumb := ps.thumbnail
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		if !strings.HasPrefix(thumb, "/") {
			thumb = "/" + thumb
		}
		thumb = siteBase + thumb
	}

	scene := models.Scene{
		ID:         ps.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      ps.title,
		URL:        sceneURL,
		Thumbnail:  thumb,
		Performers: ps.performers,
		Date:       parseDate(ps.date),
		Duration:   parseDuration(ps.duration),
		Studio:     studioName,
		ScrapedAt:  now,
	}
	scene.AddPrice(models.PriceSnapshot{Date: now, IsFree: false})
	return scene
}

func parseDate(s string) time.Time {
	t, err := time.Parse("01/02/2006", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func parseDuration(s string) int {
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n * 60
}

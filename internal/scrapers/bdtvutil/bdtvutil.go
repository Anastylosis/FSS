// Package bdtvutil scrapes the pay-per-minute VOD storefront behind bdtv.com
// and harmonyvideo.tv, one config row per studio. See docs/scrapers.md.
package bdtvutil

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// detailWorkers sizes the per-page detail pool; every field but title and
// cover is on the movie's own page.
const detailWorkers = 4

// platformHosts are the domains serving the one shared catalogue. A row
// claims a bare host through SiteConfig.Domains; any of them also match with
// the row's studio id in the URL.
var platformHosts = []string{"bdtv.com", "harmonyvideo.tv"}

// SiteConfig is one studio of the shared catalogue.
type SiteConfig struct {
	SiteID     string
	StudioName string
	StudioID   int
	// Domains are the hosts whose unfiltered URLs belong to this row.
	Domains []string
	// Base is the origin fetched; harmonyvideo.tv serves an expired
	// certificate, so every row reads the same catalogue from bdtv.com.
	Base string
}

// Scraper implements scraper.StudioScraper for one studio.
type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

// New builds the scraper for one studio row.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(45 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	studio := fmt.Sprintf("/studio-display?studio_id=%d", s.cfg.StudioID)
	p := slices.Clone(s.cfg.Domains)
	for _, d := range s.cfg.Domains {
		p = append(p, d+studio)
	}
	if !slices.Contains(s.cfg.Domains, "bdtv.com") {
		p = append(p, "bdtv.com"+studio)
	}
	for _, d := range s.cfg.Domains {
		p = append(p,
			d+"/actor/{id}",
			d+"/display-movies/category/{id}",
			d+"/display-movies/director/{id}",
		)
	}
	return p
}

var (
	studioIDRe = regexp.MustCompile(`(?:[?&]studio_id=|/display-movies/studio/)(\d+)`)
	filterRe   = regexp.MustCompile(`^/(?:actor|display-movies/(?:category|director))/\d+/?$`)
)

func (s *Scraper) MatchesURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if !slices.Contains(platformHosts, host) {
		return false
	}
	if m := studioIDRe.FindStringSubmatch(u.RequestURI()); m != nil {
		return m[1] == fmt.Sprint(s.cfg.StudioID)
	}
	return slices.Contains(s.cfg.Domains, host)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry.
type card struct {
	id    string
	title string
	thumb string
}

var (
	cardRe  = regexp.MustCompile(`<a href='[^']*/movie-details/(\d+)' class="title_x" title="([^"]*)">\s*<img src="([^"]*)"`)
	totalRe = regexp.MustCompile(`out of <strong>(\d+)</strong> entries`)

	h1Re       = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	castRe     = regexp.MustCompile(`(?s)FULL CAST LIST:</strong>(.*?)<br`)
	actorRe    = regexp.MustCompile(`(?s)/actor/\d+'>(.*?)</a>`)
	studioRe   = regexp.MustCompile(`(?s)<strong>STUDIO</strong>:\s*(.*?)<br`)
	directorRe = regexp.MustCompile(`(?s)DIRECTOR:</strong>\s*<a [^>]*>(.*?)</a>`)
	dateRe     = regexp.MustCompile(`MOVIE RELEASE DATE:</b>\s*(\d{2}/\d{2}/\d{4})`)
	categoryRe = regexp.MustCompile(`(?s)/display-movies/category/\d+'>(.*?)</a>`)
	coverRe    = regexp.MustCompile(`<img src="(/img/movieimages/[^"]+/covers/[^"]+)"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

// parseListing reads the movie cards and the total. The sidebar's "Top 10"
// reuses the card markup, so only the content before it counts.
func parseListing(body string) ([]card, int) {
	if i := strings.Index(body, `id="right_side"`); i >= 0 {
		body = body[:i]
	}
	var cards []card
	for _, m := range cardRe.FindAllStringSubmatch(body, -1) {
		cards = append(cards, card{id: m[1], title: clean(m[2]), thumb: m[3]})
	}
	total := 0
	// The pager repeats its sentence inside an HTML comment with the
	// catalogue-wide count; the live one comes last.
	if all := totalRe.FindAllStringSubmatch(body, -1); len(all) > 0 {
		_, _ = fmt.Sscan(all[len(all)-1][1], &total)
	}
	return cards, total
}

// applyDetail folds a movie page into the scene and reports its studio.
func applyDetail(sc *models.Scene, body, base string) (studio string) {
	if m := h1Re.FindStringSubmatch(body); m != nil {
		if t := clean(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := castRe.FindStringSubmatch(body); m != nil {
		for _, a := range actorRe.FindAllStringSubmatch(m[1], -1) {
			if name := clean(a[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
	if m := studioRe.FindStringSubmatch(body); m != nil {
		studio = clean(m[1])
	}
	if m := directorRe.FindStringSubmatch(body); m != nil {
		if d := clean(m[1]); d != "" && !strings.EqualFold(d, "unknown") {
			sc.Director = d
		}
	}
	if m := dateRe.FindStringSubmatch(body); m != nil {
		if d, err := parseutil.TryParseDate(m[1], "01/02/2006"); err == nil {
			sc.Date = d
		}
	}
	for _, c := range categoryRe.FindAllStringSubmatch(body, -1) {
		if tag := clean(c[1]); tag != "" {
			sc.Tags = append(sc.Tags, tag)
		}
	}
	if m := coverRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = base + m[1]
	}
	return studio
}

func (s *Scraper) listingURL(studioURL string, page int) (listURL string, filtered bool) {
	if u, err := url.Parse(studioURL); err == nil && filterRe.MatchString(u.Path) {
		return fmt.Sprintf("%s%s?page=%d", s.cfg.Base, strings.TrimSuffix(u.Path, "/"), page), true
	}
	return fmt.Sprintf("%s/studio-display?studio_id=%d&page=%d", s.cfg.Base, s.cfg.StudioID, page), false
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := map[string]bool{}
	_, filtered := s.listingURL(studioURL, 1)
	scraper.Debugf(1, "%s: studio %d, filtered listing=%t", s.cfg.SiteID, s.cfg.StudioID, filtered)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL, _ := s.listingURL(studioURL, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards, total := parseListing(body)
		if page == 1 && len(cards) == 0 && !filtered {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no movie cards on the first page"))
		}
		var fresh []card
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := s.fetchDetails(ctx, fresh, studioURL, now, filtered, opts, out)
		// Past the last page the site answers 404 rather than an empty page.
		return scraper.PageResult{Scenes: scenes, Total: total, Continue: true, Done: total > 0 && len(seen) >= total}, nil
	})
}

// fetchDetails builds the page's scenes. A movie whose studio differs from
// the row's is dropped: filtered listings mix studios. A failed detail fetch
// keeps a studio listing's card (already studio-scoped) but drops a filtered
// one, which cannot be attributed.
func (s *Scraper) fetchDetails(ctx context.Context, cards []card, studioURL string, now time.Time, filtered bool,
	opts scraper.ListOpts, out chan<- scraper.SceneResult,
) []models.Scene {
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(cards), workers)

	scenes := make([]models.Scene, len(cards))
	keep := make([]bool, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		scenes[i] = models.Scene{
			ID:        c.id,
			SiteID:    s.cfg.SiteID,
			StudioURL: studioURL,
			Studio:    s.cfg.StudioName,
			Title:     c.title,
			URL:       s.cfg.Base + "/movie-details/" + c.id,
			Thumbnail: s.cfg.Base + c.thumb,
			ScrapedAt: now,
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				keep[i] = !filtered
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			studio := applyDetail(&scenes[i], body, s.cfg.Base)
			keep[i] = studio == "" || strings.EqualFold(studio, s.cfg.StudioName)
		}(i)
	}
	wg.Wait()

	kept := scenes[:0]
	for i, sc := range scenes {
		if keep[i] {
			kept = append(kept, sc)
		}
	}
	return kept
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

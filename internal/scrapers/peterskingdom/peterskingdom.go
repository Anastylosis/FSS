// Package peterskingdom scrapes the Peter's Kingdom network — six Bricks-theme
// WordPress sites that expose their catalogue through the WP REST API.
// See docs/scrapers.md.
package peterskingdom

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	// Host is the spelling the site serves on, when it differs from Domain
	// (most of the network answers on www, two do not).
	Host string
}

var sites = []SiteConfig{
	{SiteID: "peterskingdom", Domain: "peterskingdom.com", StudioName: "Peter's Kingdom"},
	{SiteID: "rawwhitemeat", Domain: "rawwhitemeat.com", StudioName: "Raw White Meat"},
	{SiteID: "slutsaroundtown", Domain: "slutsaroundtown.com", StudioName: "Sluts Around Town"},
	{SiteID: "passionsonly", Domain: "passionsonly.com", StudioName: "Passions Only", Host: "www.passionsonly.com"},
	{SiteID: "mypovfam", Domain: "mypovfam.com", StudioName: "My POV Fam", Host: "www.mypovfam.com"},
	{SiteID: "pervertedpov", Domain: "pervertedpov.com", StudioName: "Perverted POV"},
}

const (
	// perPage is deliberately well under the API's maximum of 100: each page's
	// scenes are enriched from their own pages before any of them is sent, so
	// a smaller page streams results sooner for the same total request count.
	// Past the last page the API answers HTTP 400 rather than an empty list,
	// so the walk stops on a short page.
	perPage = 24
	// detailWorkers caps the per-page detail pool. The API carries everything
	// but the cast and the trailer, which only the page itself has.
	detailWorkers = 4
)

var (
	castRe      = regexp.MustCompile(`(?s)<div class="[^"]*vid-single-info__cast[^"]*">(.*?)</div>`)
	performerRe = regexp.MustCompile(`(?s)<a[^>]+href="[^"]*/performers/[^"]*"[^>]*>(.*?)</a>`)
	previewRe   = regexp.MustCompile(`<video src="([^"]+)"`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for one network site.
type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

// New builds a scraper for one network site.
func New(cfg SiteConfig) *Scraper {
	host := cfg.Host
	if host == "" {
		host = cfg.Domain
	}
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(45 * time.Second),
		base:    "https://" + host,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string { return []string{s.cfg.Domain, s.cfg.Domain + "/videos/"} }

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- REST payload ----

type rendered struct {
	Rendered string `json:"rendered"`
}

type videoEntry struct {
	ID       int      `json:"id"`
	Date     string   `json:"date_gmt"`
	Link     string   `json:"link"`
	Title    rendered `json:"title"`
	Content  rendered `json:"content"`
	Embedded struct {
		FeaturedMedia []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
		Terms [][]struct {
			Name     string `json:"name"`
			Taxonomy string `json:"taxonomy"`
		} `json:"wp:term"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		apiURL := fmt.Sprintf("%s/wp-json/wp/v2/videos?per_page=%d&page=%d&_embed=1", s.base, perPage, page)
		var entries []videoEntry
		if err := s.fetchJSON(ctx, apiURL, &entries); err != nil {
			return scraper.PageResult{}, err
		}
		if page == 1 && len(entries) == 0 {
			return scraper.PageResult{}, scraper.ParseError(apiURL, fmt.Errorf("the videos endpoint returned nothing"))
		}

		scenes := make([]models.Scene, len(entries))
		for i, e := range entries {
			scenes[i] = s.toScene(e, studioURL, now)
		}
		enrichTo := len(scenes)
		for i, sc := range scenes {
			if opts.KnownIDs[sc.ID] {
				enrichTo = i
				break
			}
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)

		// The API is date-descending and answers HTTP 400 past the last page,
		// so a short page is the end.
		return scraper.PageResult{Scenes: scenes, Done: len(entries) < perPage}, nil
	})
}

func (s *Scraper) toScene(e videoEntry, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          strconv.Itoa(e.ID),
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Studio:      s.cfg.StudioName,
		Title:       cleanText(e.Title.Rendered),
		URL:         s.siteURL(e.Link),
		Description: cleanText(e.Content.Rendered),
		ScrapedAt:   now,
	}
	if m := e.Embedded.FeaturedMedia; len(m) > 0 {
		sc.Thumbnail = m[0].SourceURL
	}
	for _, group := range e.Embedded.Terms {
		for _, t := range group {
			if t.Taxonomy == "video-category" && t.Name != "" {
				sc.Categories = append(sc.Categories, t.Name)
			}
		}
	}
	if d, err := time.Parse("2006-01-02T15:04:05", e.Date); err == nil {
		sc.Date = d.UTC()
	}
	return sc
}

// siteURL re-homes the absolute link the API returns onto the host actually
// being scraped, so a test server sees the detail fetch too.
func (s *Scraper) siteURL(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Path == "" {
		return link
	}
	return s.base + u.Path
}

// enrich fetches each scene's page for the cast and the trailer, which the
// REST payload does not carry — performers are a separate post type with no
// relation exposed on the video.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
}

// applyDetail folds the page's cast and trailer into a scene built from the API.
func applyDetail(sc *models.Scene, body string) {
	if c := castRe.FindStringSubmatch(body); c != nil {
		for _, m := range performerRe.FindAllStringSubmatch(c[1], -1) {
			if name := cleanText(m[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
	if p := previewRe.FindStringSubmatch(body); p != nil {
		sc.Preview = html.UnescapeString(p[1])
	}
}

func (s *Scraper) fetchJSON(ctx context.Context, apiURL string, v any) error {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Accept"] = "application/json"
	return httpx.DoJSON(ctx, s.client, httpx.Request{URL: apiURL, Headers: h}, v)
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
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

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

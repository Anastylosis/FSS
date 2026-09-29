// Package blurredmedia scrapes the Blurred Media gay network — a set of sites
// that share a single Next.js front-end backed by a Laravel JSON API hosted at
// api.<domain>. All sites expose the same public endpoints:
//
//	GET https://api.<domain>/api/videos?page=N   (listing, newest-first, 24/page)
//	GET https://api.<domain>/api/video?slug=...   (detail: description, tags, date)
//
// Both require a numeric "SITE" header identifying the brand (the value is
// hard-coded per site in the front-end bundle). The listing returns a Laravel
// paginator wrapped under "videos"; the detail returns {tags, video}.
//
// One package, table-driven: every brand in the sites slice registers its own
// scraper instance sharing this code.
package blurredmedia

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// siteConfig describes one Blurred Media brand.
type siteConfig struct {
	SiteID     string // stable lowercase identifier, also the domain stem
	Domain     string // public host without scheme/www (e.g. "hotguysfuck.com")
	StudioName string // display name
	SiteHeader string // numeric value sent in the "SITE" API header
}

// sites is the registry of Blurred Media brands with a public listing.
var sites = []siteConfig{
	{SiteID: "hotguysfuck", Domain: "hotguysfuck.com", StudioName: "HotGuysFuck", SiteHeader: "2"},
	{SiteID: "biguysfuck", Domain: "biguysfuck.com", StudioName: "BiGuysFuck", SiteHeader: "5"},
	{SiteID: "gayhoopla", Domain: "gayhoopla.com", StudioName: "GayHoopla", SiteHeader: "1"},
	{SiteID: "sugardaddyporn", Domain: "sugardaddyporn.com", StudioName: "SugarDaddyPorn", SiteHeader: "4"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newFor(cfg.SiteID))
	}
}

// newFor builds the scraper for a registered site ID. Used by init() and tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return newScraper(cfg)
		}
	}
	return nil
}

// Scraper scrapes a single Blurred Media brand.
type Scraper struct {
	Client  *http.Client
	cfg     siteConfig
	apiBase string // overridable in tests
	matchRe *regexp.Regexp
}

func newScraper(cfg siteConfig) *Scraper {
	escaped := regexp.QuoteMeta(cfg.Domain)
	return &Scraper{
		Client:  httpx.NewClient(30 * time.Second),
		cfg:     cfg,
		apiBase: "https://api." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/videos",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- API response shapes ----

type apiModel struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type apiVideo struct {
	ID          int        `json:"id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Duration    string     `json:"duration"`
	DateRelease string     `json:"dateRelease"`
	Description string     `json:"description"`
	MainPhoto   string     `json:"mainPhoto"`
	Models      []apiModel `json:"models"`
}

type listResponse struct {
	Videos struct {
		Data        []apiVideo `json:"data"`
		CurrentPage int        `json:"current_page"`
		LastPage    int        `json:"last_page"`
		PerPage     int        `json:"per_page"`
		Total       int        `json:"total"`
	} `json:"videos"`
}

type detailResponse struct {
	Tags  []apiModel `json:"tags"` // {name, slug}
	Video apiVideo   `json:"video"`
}

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping video listing via %s/api/videos", s.cfg.SiteID, s.apiBase)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		lr, err := s.fetchListing(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(lr.Videos.Data) == 0 {
			return scraper.PageResult{}, nil
		}

		total := lr.Videos.Total
		scenes := s.fetchDetails(ctx, lr.Videos.Data, opts, now)
		done := lr.Videos.LastPage > 0 && lr.Videos.CurrentPage >= lr.Videos.LastPage
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

func (s *Scraper) fetchListing(ctx context.Context, page int) (listResponse, error) {
	u := fmt.Sprintf("%s/api/videos?page=%d", s.apiBase, page)
	var lr listResponse
	if err := s.getJSON(ctx, u, &lr); err != nil {
		return listResponse{}, err
	}
	return lr, nil
}

// fetchDetails enriches each listing entry with its detail page (description +
// tags) using a worker pool. Order is preserved so Paginate's KnownIDs
// early-stop works: known IDs become lightweight stubs (no detail fetch), and
// detail-fetch failures fall back to a scene built from the listing row alone.
func (s *Scraper) fetchDetails(ctx context.Context, items []apiVideo, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	results := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		id := idStr(it.ID)
		if opts.KnownIDs[id] {
			results[i] = models.Scene{ID: id, SiteID: s.cfg.SiteID}
			continue
		}
		wg.Add(1)
		go func(idx int, item apiVideo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			detail, err := s.fetchDetail(ctx, item.Slug)
			if err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (using listing data)", s.cfg.SiteID, item.Slug, err)
				results[idx] = s.toScene(item, nil, now)
				return
			}
			results[idx] = s.toScene(detail.Video, detail.Tags, now)
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" { // ctx cancelled before this slot was filled
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func (s *Scraper) fetchDetail(ctx context.Context, slug string) (detailResponse, error) {
	u := fmt.Sprintf("%s/api/video?slug=%s", s.apiBase, url.QueryEscape(slug))
	var dr detailResponse
	if err := s.getJSON(ctx, u, &dr); err != nil {
		return detailResponse{}, err
	}
	return dr, nil
}

// toScene builds a Scene from a video row. When detail is fetched, pass its
// richer video object and tags; for the listing-only fallback, pass nil tags.
func (s *Scraper) toScene(v apiVideo, tags []apiModel, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          idStr(v.ID),
		SiteID:      s.cfg.SiteID,
		StudioURL:   "https://www." + s.cfg.Domain + "/",
		Studio:      s.cfg.StudioName,
		Title:       strings.TrimSpace(v.Title),
		URL:         fmt.Sprintf("https://www.%s/video/%s", s.cfg.Domain, v.Slug),
		Description: strings.TrimSpace(v.Description),
		Thumbnail:   v.MainPhoto,
		Duration:    parseutil.ParseDurationColon(v.Duration),
		ScrapedAt:   now,
	}

	for _, m := range v.Models {
		if name := strings.TrimSpace(m.Name); name != "" {
			sc.Performers = append(sc.Performers, name)
		}
	}

	seen := make(map[string]bool)
	for _, t := range tags {
		name := strings.TrimSpace(t.Name)
		if name != "" && !seen[name] {
			seen[name] = true
			sc.Tags = append(sc.Tags, name)
		}
	}

	// dateRelease is "YYYY-MM-DD" on detail pages; listing rows carry a short
	// "Jun 19" form, so only the full form parses (best-effort).
	if t, err := parseutil.TryParseDate(strings.TrimSpace(v.DateRelease), "2006-01-02"); err == nil {
		sc.Date = t.UTC()
	}

	return sc
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) error {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["Accept"] = "application/json"
	headers["SITE"] = s.cfg.SiteHeader
	headers["Referer"] = "https://www." + s.cfg.Domain + "/"
	headers["Origin"] = "https://www." + s.cfg.Domain

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: headers})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

func idStr(id int) string { return fmt.Sprintf("%d", id) }

// Package goddessfootdomination scrapes tours running the bunny-cms.com
// platform.
//
// It replaces the old `feetondemand` package. That one targeted a custom CMS
// whose listing was an AJAX shell (`/?mb=VmlkZW9zfHw=&p={offset}`); the site
// migrated off it during 2026 and the old paths now return a 500 from the
// origin's proxy. The three sibling tours that shared that CMS
// (footfetishcardates, footfetishaffiliates, goddessbrianna) went dark
// entirely rather than migrating — see docs/scrapers.md.
//
// The platform is table-driven here even though only one site runs it today:
// bunny-cms is hosted software, so a second tour is a config row rather than a
// package. It is deliberately NOT a shared `*util` package yet — that needs a
// second site to compare against, per CONTRIBUTING.
package goddessfootdomination

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one bunny-cms tour.
type SiteConfig struct {
	ID       string
	BaseURL  string // no trailing slash
	SiteName string
	Patterns []string
	MatchRe  *regexp.Regexp
}

// Apex hosts only. The certificate on goddessfootdomination.com carries a
// single SAN for the apex, so an https request to the www host fails
// verification before it is sent. MatchRe still accepts a pasted www URL —
// only the host we fetch is pinned.
var sites = []SiteConfig{
	{
		ID:       "goddessfootdomination",
		BaseURL:  "https://goddessfootdomination.com",
		SiteName: "Goddess Foot Domination",
		Patterns: []string{
			"goddessfootdomination.com/",
			"goddessfootdomination.com/all/video",
			"goddessfootdomination.com/category/{slug}",
			"goddessfootdomination.com/actor/{slug}",
		},
		MatchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?goddessfootdomination\.com(?:/|$)`),
	},
}

const (
	// RecommendedDelay is conservative: the listing is cheap but every scene
	// costs a detail fetch, and this is a single-operator tour.
	RecommendedDelay = 500 * time.Millisecond

	defaultWorkers = 4
	maxPages       = 500 // backstop; the walk normally ends on an empty page
)

// Scraper scrapes one configured bunny-cms tour.
type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

// New builds a scraper for cfg.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(60 * time.Second)}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// ID returns the stable scraper identifier.
func (s *Scraper) ID() string { return s.cfg.ID }

// Patterns returns the URL shapes this scraper handles.
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }

// MatchesURL reports whether this scraper handles studioURL.
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

// ListScenes streams every scene the tour publishes.
func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// Cards link the scene twice (thumbnail and title); dedup on the walk.
	sceneHrefRe = regexp.MustCompile(`href="https?://[^"]*?/v/([0-9]+)-([a-z0-9-]+)"`)
	durationRe  = regexp.MustCompile(`(?s)<small class="duration[^"]*">\s*([0-9:]+)\s*</small>`)
	ldJSONRe    = regexp.MustCompile(`(?s)<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>`)
)

// sceneRef is one card found on a listing page.
type sceneRef struct {
	id   string
	slug string
	url  string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.WarnDelayBelow(s.cfg.ID, opts.Delay, RecommendedDelay)

	listing := s.listingURL(studioURL)
	scraper.Debugf(1, "%s: walking %s", s.cfg.ID, listing)

	refs, err := s.walkListing(ctx, listing, opts, out)
	if err != nil || len(refs) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(refs)):
	case <-ctx.Done():
		return
	}

	s.fetchDetails(ctx, refs, studioURL, opts, out)
}

// listingURL maps the operator's URL onto the listing to walk. A bare host
// scrapes the whole catalogue; a category or actor page scrapes just that
// filtered view, which is the same pager with the same card markup.
func (s *Scraper) listingURL(studioURL string) string {
	u := strings.TrimRight(studioURL, "/")
	lower := strings.ToLower(u)
	switch {
	case strings.Contains(lower, "/category/"), strings.Contains(lower, "/actor/"):
		scraper.Debugf(1, "%s: scraping filtered view %s", s.cfg.ID, u)
		return u
	case strings.HasSuffix(lower, "/all/video"), strings.HasSuffix(lower, "/all/movie"):
		return u
	default:
		return s.cfg.BaseURL + "/all/video"
	}
}

// walkListing pages the listing newest-first, collecting scene refs. It stops
// on the first page with no cards, and honours the KnownIDs early stop: the
// default order is date-descending, so the first known ID means everything
// after it is already stored.
func (s *Scraper) walkListing(ctx context.Context, listing string, opts scraper.ListOpts, out chan<- scraper.SceneResult) ([]sceneRef, error) {
	var refs []sceneRef
	seen := map[string]bool{}

	for page := 1; page <= maxPages; page++ {
		if ctx.Err() != nil {
			return refs, ctx.Err()
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return refs, ctx.Err()
		}

		pageURL := listing
		if page > 1 {
			sep := "?"
			if strings.Contains(pageURL, "?") {
				sep = "&"
			}
			pageURL = fmt.Sprintf("%s%spage=%d", pageURL, sep, page)
		}

		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return refs, err
		}

		found := s.parseListing(body, seen)
		scraper.Debugf(1, "%s: page %d yielded %d new scenes", s.cfg.ID, page, len(found))
		if len(found) == 0 {
			return refs, nil
		}

		for _, r := range found {
			if opts.KnownIDs[r.id] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, r.id)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return refs, nil
			}
			refs = append(refs, r)
		}
	}
	return refs, nil
}

// parseListing extracts the scene refs on one page, skipping ones already
// seen (each card links the scene from both its thumbnail and its title).
func (s *Scraper) parseListing(body []byte, seen map[string]bool) []sceneRef {
	var refs []sceneRef
	for _, m := range sceneHrefRe.FindAllSubmatch(body, -1) {
		id, slug := string(m[1]), string(m[2])
		if seen[id] {
			continue
		}
		seen[id] = true
		refs = append(refs, sceneRef{
			id:   id,
			slug: slug,
			url:  fmt.Sprintf("%s/v/%s-%s", s.cfg.BaseURL, id, slug),
		})
	}
	return refs
}

func (s *Scraper) fetchDetails(ctx context.Context, refs []sceneRef, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	if workers > len(refs) {
		workers = len(refs)
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(refs), workers)

	jobs := make(chan sceneRef)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchScene(ctx, ref, studioURL)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(*scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, ref := range refs {
			select {
			case jobs <- ref:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
}

func (s *Scraper) fetchScene(ctx context.Context, ref sceneRef, studioURL string) (*models.Scene, error) {
	body, err := s.fetch(ctx, ref.url)
	if err != nil {
		return nil, fmt.Errorf("scene %s: %w", ref.id, err)
	}
	scene, err := s.toScene(body, ref, studioURL)
	if err != nil {
		return nil, scraper.ParseError(ref.url, err)
	}
	return scene, nil
}

// movieLD is the schema.org Movie block bunny-cms puts on every scene page. It
// carries everything worth storing, so the HTML around it is only read for the
// duration.
type movieLD struct {
	Type          string   `json:"@type"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	DatePublished string   `json:"datePublished"`
	Genre         []string `json:"genre"`
	Actor         []struct {
		Name string `json:"name"`
	} `json:"actor"`
	ProductionCompany struct {
		Name string `json:"name"`
	} `json:"productionCompany"`
	// image is a string or an array of them, the same shape parseutil warns
	// about for thumbnailUrl, so it is decoded loosely and the first kept.
	Image json.RawMessage `json:"image"`
}

func (s *Scraper) toScene(body []byte, ref sceneRef, studioURL string) (*models.Scene, error) {
	ld := findMovie(body)
	if ld == nil {
		return nil, fmt.Errorf("no schema.org Movie block on the page")
	}

	title := strings.TrimSpace(html.UnescapeString(ld.Name))
	if title == "" {
		return nil, fmt.Errorf("schema.org Movie block has no name")
	}

	scene := models.Scene{
		ID:          ref.id,
		SiteID:      s.cfg.ID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         ref.url,
		Description: strings.TrimSpace(html.UnescapeString(ld.Description)),
		Studio:      s.cfg.SiteName,
		Categories:  cleanNames(ld.Genre),
		Thumbnail:   firstImage(ld.Image),
		ScrapedAt:   time.Now().UTC(),
	}

	if t, err := parseutil.TryParseDate(ld.DatePublished, time.RFC3339, "2006-01-02T15:04:05-07:00", "2006-01-02"); err == nil {
		scene.Date = t.UTC()
	}

	for _, a := range ld.Actor {
		if n := strings.TrimSpace(html.UnescapeString(a.Name)); n != "" {
			scene.Performers = append(scene.Performers, n)
		}
	}

	if dm := durationRe.FindSubmatch(body); dm != nil {
		scene.Duration = parseutil.ParseDurationColon(strings.TrimSpace(string(dm[1])))
	}

	return &scene, nil
}

// findMovie returns the first Movie block on the page. The pages also carry
// WebPage and WebSite blocks, and a VideoObject nested under the Movie's
// hasPart that describes the *teaser* rather than the scene — decoding the
// first ld+json script, or reaching for the VideoObject, would store the
// trailer's metadata.
func findMovie(body []byte) *movieLD {
	for _, m := range ldJSONRe.FindAllSubmatch(body, -1) {
		var ld movieLD
		if err := json.Unmarshal(m[1], &ld); err != nil {
			continue
		}
		if ld.Type == "Movie" {
			return &ld
		}
	}
	return nil
}

func firstImage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		return many[0]
	}
	return ""
}

func cleanNames(in []string) []string {
	var out []string
	for _, v := range in {
		if n := strings.TrimSpace(html.UnescapeString(v)); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

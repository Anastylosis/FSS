// Package realitylovers scrapes the Reality Lovers VR network — Reality Lovers
// itself plus TS Virtual Lovers, Play Girl Stories and We Are Crazy — four
// sites on one custom server-rendered CMS (static2.rlcontent.com assets, no
// framework fingerprint).
//
// KinkVR, the network's fifth StashDB brand, now redirects to
// kink.com/channel/kink-vr and is covered by the `kink` scraper.
//
// Enumeration walks `/videos/pageN/`, 12 scenes per page. Every request carries
// an `agreedToDisclaimer=true` cookie; without it the site serves only a ~6 KB
// age-gate splash with no cards at all.
//
// Each scene appears more than once per page (the template renders a grid view
// and a list view of the same set), so cards are deduped by id.
//
// Detail pages embed the whole record as a `const videoDetails = {…}` literal —
// title, ISO release date, untruncated description, cast and categories — so
// the detail fetch reads JSON rather than scraping HTML. The one field the
// network does not publish anywhere is **duration**.
package realitylovers

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	detailWorkers = 4
	dateLayout    = "2006-01-02"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []siteConfig{
	{"realitylovers", "realitylovers.com", "Reality Lovers"},
	{"tsvirtuallovers", "tsvirtuallovers.com", "TS Virtual Lovers"},
	{"playgirlstories", "playgirlstories.com", "Play Girl Stories"},
	{"wearecrazy", "wearecrazy.com", "We Are Crazy"},
}

// Scraper implements scraper.StudioScraper for one Reality Lovers site.
type Scraper struct {
	cfg     siteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/videos/",
		s.cfg.Domain + "/videos/page{N}/",
		s.cfg.Domain + "/vd/{id}/{slug}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, s.listingURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		// Each scene is rendered twice per page (grid and list views), so the
		// dedup here is load-bearing, not just a pagination guard.
		items := parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, studioURL, fresh, now, opts.Delay, out)}, nil
	})
}

// listingURL builds the paginated listing. Page 1 has no page segment.
func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		return s.base + "/videos/"
	}
	return fmt.Sprintf("%s/videos/page%d/", s.base, page)
}

// ---- listing ----

// sceneLinkRe matches the scene links. Both views link the same /vd/{id}/{slug}/
// shape, so one pattern covers them and dedup collapses the duplicates.
var sceneLinkRe = regexp.MustCompile(`/vd/(\d+)/([^/"]+)/`)

type listItem struct {
	id, slug string
}

func parseListing(body []byte) []listItem {
	page := string(body)
	items := make([]listItem, 0, 24)
	seen := make(map[string]bool)
	for _, m := range sceneLinkRe.FindAllStringSubmatch(page, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		items = append(items, listItem{id: m[1], slug: m[2]})
	}
	return items
}

// ---- detail enrichment ----

// enrich fetches each scene's detail page. A failure keeps the listing-derived
// scene and reports the error, so a transient block costs metadata rather than
// the scene itself — and an all-failed page still returns scenes, which is what
// keeps Paginate from reading it as the end of the catalogue.
func (s *Scraper) enrich(ctx context.Context, studioURL string, items []listItem, now time.Time, delay time.Duration, out chan<- scraper.SceneResult) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), detailWorkers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, detailWorkers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scene, err := s.toScene(ctx, studioURL, it, now)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
					return
				}
			}
			scenes[i] = scene
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

// videoDetailsRe lifts the detail page's embedded record. The site publishes no
// JSON-LD at all, so this literal is the only structured source.
var videoDetailsRe = regexp.MustCompile(`(?s)const videoDetails = (\{.*?\});\s*\n`)

type namedRef struct {
	Name string `json:"name"`
}

type videoDetails struct {
	ContentID   int64      `json:"contentId"`
	Title       string     `json:"title"`
	ReleaseDate string     `json:"releaseDate"`
	Description string     `json:"description"`
	Starring    []namedRef `json:"starring"`
	Categories  []namedRef `json:"categories"`
	MainImages  []struct {
		ImgSrcSet string `json:"imgSrcSet"`
	} `json:"mainImages"`
}

// toScene builds a scene from the listing item, enriched from its detail page.
// On a detail failure the listing-derived scene is returned alongside the error.
func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) (models.Scene, error) {
	sceneURL := fmt.Sprintf("%s/vd/%s/%s/", s.base, it.id, it.slug)
	base := models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     titleFromSlug(it.slug),
		URL:       sceneURL,
		Studio:    s.cfg.StudioName,
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return base, err
	}
	vd := parseVideoDetails(string(body))
	if vd == nil {
		return base, scraper.ParseError(sceneURL, fmt.Errorf("no video details on the page"))
	}

	scene := models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       cleanText(vd.Title),
		URL:         sceneURL,
		Description: cleanText(vd.Description),
		Studio:      s.cfg.StudioName,
		Thumbnail:   firstSrcSetURL(vd),
		ScrapedAt:   now,
	}
	if t, err := time.Parse(dateLayout, strings.TrimSpace(vd.ReleaseDate)); err == nil {
		scene.Date = t.UTC()
	}
	scene.Performers = names(vd.Starring)
	scene.Categories = names(vd.Categories)

	return scene, nil
}

// titleFromSlug is the fallback title when the detail page cannot be read.
func titleFromSlug(slug string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(slug, "-", " ")), " ")
}

func parseVideoDetails(detail string) *videoDetails {
	m := videoDetailsRe.FindStringSubmatch(detail)
	if m == nil {
		return nil
	}
	var vd videoDetails
	if err := json.Unmarshal([]byte(m[1]), &vd); err != nil {
		return nil
	}
	return &vd
}

func names(refs []namedRef) []string {
	var out []string
	seen := make(map[string]bool)
	for _, r := range refs {
		n := cleanText(r.Name)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// firstSrcSetURL takes the first candidate out of the main image's srcset,
// which is a comma-separated "url width" list.
func firstSrcSetURL(vd *videoDetails) string {
	if len(vd.MainImages) == 0 {
		return ""
	}
	first, _, _ := strings.Cut(vd.MainImages[0].ImgSrcSet, ",")
	url, _, _ := strings.Cut(strings.TrimSpace(first), " ")
	return url
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	// Without this the site serves only the age-gate splash, which has no
	// scene links at all.
	headers["Cookie"] = "agreedToDisclaimer=true"

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: rawURL, Headers: headers})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

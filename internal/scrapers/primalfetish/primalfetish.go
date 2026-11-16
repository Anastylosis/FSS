// Package primalfetish scrapes the Primal Fetish Network paysites
// (primalfetishnetwork.com), a WordPress site hosting several fetish paysites
// that share one listing template. Every scene's metadata (title, performers,
// date, thumbnail) is published on the listing cards, so the scraper is
// listing-only.
//
// Each paysite is registered as its own FSS scraper (its own SiteID). The
// per-paysite listing lives at /paysite/{slug}/ (page 1) and
// /paysite/{slug}/page/{N}/ (page N >= 2), newest first, so KnownIDs
// early-stop works. The site was rebuilt in 2026; the old
// /paysites/{id}/{slug}/videos/ URLs still match so stored studios resolve.
package primalfetish

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const siteBase = "https://primalfetishnetwork.com"

// SiteConfig describes one Primal Fetish paysite.
type SiteConfig struct {
	SiteID     string // FSS site identifier, e.g. "primalstaboofamily"
	PaysiteID  int    // numeric id of the retired CMS, kept so old studio URLs still match
	Slug       string // paysite slug, e.g. "primals-taboo-relations"
	StudioName string // human-readable studio name
}

var sites = []SiteConfig{
	{SiteID: "primalstaboofamily", PaysiteID: 14, Slug: "primals-taboo-relations", StudioName: "Primal's Taboo Family Relations"},
	{SiteID: "primalscosplay", PaysiteID: 32, Slug: "cosplay", StudioName: "Primal's Cosplay"},
	{SiteID: "primalsfootfantasies", PaysiteID: 25, Slug: "foot-fantasies-by-primal", StudioName: "Primal's FOOT FANTASIES"},
	{SiteID: "primalsfootjobs", PaysiteID: 4, Slug: "primals-footjob-fantasies", StudioName: "Primal's FOOTJOBS"},
	{SiteID: "primalshandjobs", PaysiteID: 23, Slug: "primals-handjob-fantasies", StudioName: "Primal's HANDJOBS"},
	{SiteID: "primalspovfamilylust", PaysiteID: 12, Slug: "primals-pov-family-lust", StudioName: "Primal's POV Family Lust"},
	{SiteID: "primalswrestlingsex", PaysiteID: 16, Slug: "primals-wrestling-sex", StudioName: "Primals Wrestling Sex"},
	{SiteID: "primaleroticmassage", PaysiteID: 1, Slug: "erotic-massage-master", StudioName: "Erotic Massage Institute"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// Scraper scrapes a single Primal Fetish paysite.
type Scraper struct {
	cfg     SiteConfig
	matchRe *regexp.Regexp
	Client  *http.Client
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New constructs a scraper for one paysite config.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg: cfg,
		// New-style /paysite/{slug}, or the retired /paysites/{id}/ form.
		matchRe: regexp.MustCompile(fmt.Sprintf(
			`primalfetishnetwork\.com/paysite/%s(?:/|$|\?)|primalfetishnetwork\.com/paysites/%d/`,
			regexp.QuoteMeta(cfg.Slug), cfg.PaysiteID)),
		Client: httpx.NewClient(30 * time.Second),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		fmt.Sprintf("primalfetishnetwork.com/paysite/%s/", s.cfg.Slug),
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "primalfetish: scraping paysite %s (%s)", s.cfg.Slug, s.cfg.SiteID)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, s.pageURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := s.parseListing(body, studioURL)
		// WordPress answers 404 past the last page, so stop on the missing "next" link.
		return scraper.PageResult{
			Scenes: scenes,
			Done:   len(scenes) == 0 || !nextRe.Match(body),
		}, nil
	})
}

// pageURL builds the listing URL for a 1-based page number.
func (s *Scraper) pageURL(page int) string {
	base := fmt.Sprintf("%s/paysite/%s/", siteBase, s.cfg.Slug)
	if page <= 1 {
		return base
	}
	return fmt.Sprintf("%spage/%d/", base, page)
}

var (
	cardRe     = regexp.MustCompile(`<article class="main__videoElement`)
	titleRe    = regexp.MustCompile(`(?s)<a href="(https://primalfetishnetwork\.com/videos/[^"]+)"[^>]*class="main__videoTitle"[^>]*>\s*(.*?)\s*</a>`)
	idRe       = regexp.MustCompile(`name="video-id" value="(\d+)"`)
	modelRe    = regexp.MustCompile(`<a class="video__listModel[^"]*" title="([^"]*)"`)
	dateRe     = regexp.MustCompile(`(?s)<div class="date">\s*Date:\s*(.*?)\s*</div>`)
	thumbRe    = regexp.MustCompile(`<img[^>]*src="([^"]+)"`)
	nextRe     = regexp.MustCompile(`class="next page-numbers"`)
	dateLayout = "January 2, 2006"
)

// parseListing extracts every scene card from a listing page.
func (s *Scraper) parseListing(body []byte, studioURL string) []models.Scene {
	page := string(body)
	now := time.Now().UTC()

	locs := cardRe.FindAllStringIndex(page, -1)
	scenes := make([]models.Scene, 0, len(locs))

	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[loc[0]:end]

		tm := titleRe.FindStringSubmatch(block)
		im := idRe.FindStringSubmatch(block)
		if tm == nil || im == nil {
			continue
		}
		scene := models.Scene{
			ID:        im[1],
			SiteID:    s.cfg.SiteID,
			StudioURL: studioURL,
			Studio:    s.cfg.StudioName,
			Title:     strings.TrimSpace(html.UnescapeString(tm[2])),
			URL:       tm[1],
			ScrapedAt: now,
		}

		for _, m := range modelRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name != "" {
				scene.Performers = append(scene.Performers, name)
			}
		}

		if m := dateRe.FindStringSubmatch(block); m != nil {
			raw := parseutil.StripOrdinalSuffix(strings.TrimSpace(m[1]))
			if t, err := time.Parse(dateLayout, raw); err == nil {
				scene.Date = t.UTC()
			}
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			scene.Thumbnail = html.UnescapeString(m[1])
		}

		scenes = append(scenes, scene)
	}
	return scenes
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

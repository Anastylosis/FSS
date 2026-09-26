// Package digitaljmediautil scrapes the Digital J Media / JapanHDV network of
// English-language JAV "sample tour" sites (Fellatio Japan, Cospuri, Cute Butts,
// Cum Buffet, Legs Japan, Tokyo Face Fuck, Handjob Japan, Sperm Mania, Transex
// Japan, Ura Lesbian).
//
// All ten sites share one backend and the same pagination model — a server
// rendered listing at /samples or /en/samples paginated with ?page=N — but each
// site uses different per-scene HTML markup, so every site supplies its own
// parseBlock function via SiteConfig. The whole metadata set is exposed
// anonymously (the paywall only blocks the full video), so no auth or session
// bootstrap is needed.
//
// The stable per-scene identifier lives in the CDN sample path
// (https://cdn.{domain}/{seg}/{id}/sample.mp4) where {seg} is "preview",
// "samples" or "tour" depending on the site. Out-of-range pages clamp/repeat the
// last page rather than 404ing, so the pagination loop keeps a global seen-set
// and stops when a page yields no new IDs.
package digitaljmediautil

import (
	"context"
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

const detailWorkers = 4

// SiteConfig describes one network site. parse turns a listing-page body into
// scenes; detailParse (optional) enriches a scene from its own detail page.
type SiteConfig struct {
	SiteID   string
	Studio   string
	Base     string // e.g. "https://fellatiojapan.com"
	ListPath string // e.g. "/en/samples" or "/samples"
	Patterns []string
	MatchRe  *regexp.Regexp

	parse       func(cfg SiteConfig, body, studioURL string, now time.Time) []models.Scene
	detailParse func(scene *models.Scene, body string) // nil = listing-only
}

// Scraper implements scraper.StudioScraper for one network site.
type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

// New builds a scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string               { return s.cfg.SiteID }
func (s *Scraper) Patterns() []string       { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	listURL := s.cfg.Base + s.cfg.ListPath

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.get(ctx, fmt.Sprintf("%s?page=%d", listURL, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		parsed := s.cfg.parse(s.cfg, string(body), studioURL, now)

		// Drop empty/duplicate IDs. Out-of-range pages repeat the last page, so
		// a page with no new IDs means we have reached the end.
		fresh := parsed[:0]
		for _, sc := range parsed {
			if sc.ID == "" || seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			fresh = append(fresh, sc)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		if s.cfg.detailParse != nil {
			s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), fresh, opts.Delay)
		}
		return scraper.PageResult{Scenes: fresh}, nil
	})
}

// enrich fetches each scene's detail page concurrently and applies detailParse.
func (s *Scraper) enrich(ctx context.Context, workers int, scenes []models.Scene, delay time.Duration) {
	scraper.Debugf(1, "%s: enriching %d scenes with %d workers", s.cfg.SiteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		if scenes[i].URL == "" {
			continue
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
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			body, err := s.get(ctx, scenes[i].URL)
			if err != nil {
				return
			}
			s.cfg.detailParse(&scenes[i], string(body))
		}(i)
	}
	wg.Wait()
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- shared helpers ----

var (
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
	wsRe       = regexp.MustCompile(`\s+`)
	hMinRe     = regexp.MustCompile(`(\d+)H(\d+)`)
	cdnHostRe  = regexp.MustCompile(`^https?://`)
)

// cdnBase returns the CDN host for a site base, e.g.
// "https://fellatiojapan.com" -> "https://cdn.fellatiojapan.com".
func cdnBase(base string) string {
	return cdnHostRe.ReplaceAllString(base, "https://cdn.")
}

// cleanText strips tags, unescapes entities and collapses whitespace.
func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

// splitModels splits a "A, B & C" performer string into trimmed names.
func splitModels(s string) []string {
	s = html.UnescapeString(s)
	s = strings.NewReplacer(" & ", ",", " and ", ",").Replace(s)
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// dedupTrim trims, unescapes and de-duplicates a tag/name list.
func dedupTrim(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(html.UnescapeString(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// parseHMRuntime parses an "1H16" style runtime into seconds.
func parseHMRuntime(s string) int {
	if m := hMinRe.FindStringSubmatch(s); m != nil {
		return atoi(m[1])*3600 + atoi(m[2])*60
	}
	return parseutil.ParseDurationColon(s)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

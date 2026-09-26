// Package teendreams scrapes Teen Dreams (teendreams.com) and LesArchive
// (lesarchive.com), two Elevated X tours on the `/t4/` prefix sharing one card
// skin (`content-item` blocks).
//
// teen-depot.com, which StashDB records as Teen Dreams' parent, is not a
// separate catalogue: it serves byte-identical listings whose cards link to
// teendreams.com. It is registered as an alias domain rather than a second
// site, so the same scenes are not ingested twice under two SiteIDs.
//
// The card is thin — only id, title, URL and thumbnail — so every scene needs a
// detail fetch for its date, duration and performer. Three things the markup
// forces:
//
//   - The card's `src` is a 1×1 placeholder; the real image is in `src0_1x`.
//   - Duration is only in the player chrome, as "0:00 / 20:28". The first value
//     is the playhead, so the runtime is the one after the slash.
//   - The detail page's `<p class="description">` is the MODEL's bio, not a
//     scene synopsis — the whole metadata block is model-centric. It is not
//     used as the scene description, which the site does not publish.
//
// LesArchive is thinner still: it files each set under a "model" page named
// after the set itself, so a derived name that merely restates the title is
// dropped, and it publishes no runtime at all.
package teendreams

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

const (
	detailWorkers = 4
	dateLayout    = "2006-01-02"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// Aliases are mirror domains serving this same catalogue. They only widen
	// URL matching; requests still go to Domain.
	Aliases []string
}

var sites = []siteConfig{
	{"teendreams", "teendreams.com", "Teen Dreams", []string{"teen-depot.com"}},
	{"lesarchive", "lesarchive.com", "LesArchive", nil},
}

// Scraper implements scraper.StudioScraper for one `/t4/` site.
type Scraper struct {
	cfg     siteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: matchReFor(cfg),
	}
}

// matchReFor accepts the site's own domain plus any mirror serving the same
// catalogue.
func matchReFor(cfg siteConfig) *regexp.Regexp {
	domains := append([]string{cfg.Domain}, cfg.Aliases...)
	for i, d := range domains {
		domains[i] = strings.ReplaceAll(d, ".", `\.`)
	}
	return regexp.MustCompile(`^https?://(?:www\.)?(?:` + strings.Join(domains, "|") + `)(?:/|$)`)
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	p := []string{
		s.cfg.Domain,
		s.cfg.Domain + "/t4/categories/movies_{N}_d.html",
		s.cfg.Domain + "/t4/trailers/{slug}.html",
		s.cfg.Domain + "/t4/models/{Name}.html",
	}
	return append(p, s.cfg.Aliases...)
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
		// The `_d` suffix is the site's own "Newest" sort — `_p`, `_n` and `_o`
		// are popularity, A-Z and oldest, none of which the KnownIDs early-stop
		// would be valid for.
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/t4/categories/movies_%d_d.html", s.base, page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
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
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// ---- listing ----

var (
	cardRe = regexp.MustCompile(`<div class="content-item">`)
	idRe   = regexp.MustCompile(`id="set-target-(\d+)"`)
	// Only the trailer filename is captured so the URL is rebuilt against the
	// site base — cards on the mirror domain link to the canonical host.
	linkRe = regexp.MustCompile(`<a href="[^"]*/t4/trailers/([^"/]+\.html)" title="([^"]*)"`)
	// `src` is a 1x1 placeholder; the real image is in src0_1x.
	thumbRe = regexp.MustCompile(`src0_1x="([^"]+)"`)
)

type listItem struct {
	id, url, title, thumb string
}

func (s *Scraper) parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			url:   s.base + "/t4/trailers/" + m[1],
			title: cleanText(m[2]),
		}
		if idm := idRe.FindStringSubmatch(card); idm != nil {
			it.id = idm[1]
		} else {
			it.id = strings.TrimSuffix(m[1], ".html")
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = s.normalizeURL(th[1])
		}

		items = append(items, it)
	}
	return items
}

func (s *Scraper) normalizeURL(u string) string {
	switch {
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return s.base + u
	case strings.HasPrefix(u, "http"):
		return u
	default:
		return s.base + "/t4/" + u
	}
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
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
			scenes[i] = s.toScene(ctx, studioURL, it, now)
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

var (
	dateRe = regexp.MustCompile(`<div class="content-date"><span>Released:\s*(\d{4}-\d{2}-\d{2})\s*</span>`)
	// The player renders "0:00 / 20:28" — the first value is the playhead.
	durationRe = regexp.MustCompile(`<div class="player-time"><span>[^<]*</span>\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	// The scene's cast is the model whose profile the page links to.
	modelRe = regexp.MustCompile(`/t4/models/([A-Za-z0-9_-]+)\.html"[^>]*class="view-btn"`)
	// Profile filenames are run-together CamelCase, e.g. "LaylaScarlett".
	camelRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Thumbnail: it.thumb,
		Studio:    s.cfg.StudioName,
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		// The card alone is still a usable scene.
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := dateRe.FindStringSubmatch(detail); m != nil {
		if t, err := time.Parse(dateLayout, m[1]); err == nil {
			scene.Date = t.UTC()
		}
	}
	if m := durationRe.FindStringSubmatch(detail); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}
	if m := modelRe.FindStringSubmatch(detail); m != nil {
		// LesArchive files each set under a "model" page named after the set
		// itself ("8-girl-orgy"), so a name that just restates the title is the
		// set, not a performer.
		if name := splitCamel(m[1]); name != "" && !strings.EqualFold(name, scene.Title) {
			scene.Performers = []string{name}
		}
	}
}

// splitCamel turns a "LaylaScarlett" profile filename into "Layla Scarlett".
func splitCamel(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	return cleanText(camelRe.ReplaceAllString(s, "$1 $2"))
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

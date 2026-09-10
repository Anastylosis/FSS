// Package arx scrapes the ARX Bucks network — eleven sites sharing one Next.js
// App Router CMS (Honey Trans, Japan Lust, Les Worship, JOI Babes, POV Masters,
// Cuck Hunter, Nude Yoga Porn, Trans Roommates, Trans Daylight, Trans Midnight
// and the Randy Pass hub).
//
// The sites render with the App Router's RSC flight format rather than
// `__NEXT_DATA__`, so there is no single JSON blob to lift. Enumeration instead
// runs off each site's sitemap (`/sitemap.xml`, ~800 `/scenes/{id}/{slug}`
// entries per site), and a worker pool reads each detail page.
//
// Detail pages carry no JSON-LD, so fields come from OpenGraph meta plus two
// labelled blocks in the body. Those labels matter: the page also renders a
// related-scenes rail whose cards carry their own `/models/` and `/categories/`
// links and their own dates, so cast, categories and date are all read from
// inside the scene's own "Models:" / "Categories:" block or the header, never
// document-wide.
//
// Duration is not usable: the only runtimes on the page belong to short preview
// clips, not the scene.
package arx

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

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

// sites are the ARX network members, taken from the `sites` array the CMS
// embeds in every page.
var sites = []siteConfig{
	{"honeytrans", "honeytrans.com", "Honey Trans"},
	{"japanlust", "japanlust.com", "Japan Lust"},
	{"lesworship", "lesworship.com", "Les Worship"},
	{"joibabes", "joibabes.com", "JOI Babes"},
	{"povmasters", "povmasters.com", "POV Masters"},
	{"cuckhunter", "cuckhunter.com", "Cuck Hunter"},
	{"nudeyogaporn", "nudeyogaporn.com", "Nude Yoga Porn"},
	{"transroommates", "transroommates.com", "Trans Roommates"},

	// Also on the same CMS but absent from the embedded sites array — found
	// via the ARX Bucks studio tree and confirmed by their /sitemap.xml
	// exposing the same /scenes/{id}/{slug} shape. randypass.com is the
	// network hub and its sitemap spans the whole catalogue (~4k scenes).
	{"transdaylight", "transdaylight.com", "Trans Daylight"},
	{"transmidnight", "transmidnight.com", "Trans Midnight"},
	{"randypass", "randypass.com", "Randy Pass"},
}

// Scraper implements scraper.StudioScraper for one ARX site.
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
		s.cfg.Domain + "/scenes",
		s.cfg.Domain + "/scenes/{id}/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- sitemap ----

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// sceneURLRe matches the scene entries in the sitemap; the sitemap also lists
// model and category pages.
var sceneURLRe = regexp.MustCompile(`/scenes/(\d+)/([^/?#]+)`)

type sceneRef struct {
	id, url string
}

func (s *Scraper) fetchSitemap(ctx context.Context) ([]sceneRef, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     s.base + "/sitemap.xml",
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading sitemap: %w", err)
	}

	var us urlset
	if err := parseutil.DecodeXML(body, &us); err != nil {
		return nil, fmt.Errorf("parsing sitemap: %w", err)
	}

	refs := make([]sceneRef, 0, len(us.URLs))
	seen := make(map[string]bool)
	for _, u := range us.URLs {
		m := sceneURLRe.FindStringSubmatch(u.Loc)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		refs = append(refs, sceneRef{id: m[1], url: u.Loc})
	}
	return refs, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	refs, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes in sitemap", s.cfg.SiteID, len(refs))

	select {
	case out <- scraper.Progress(len(refs)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	work := make(chan sceneRef)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching details with %d workers", s.cfg.SiteID, detailWorkers)
	for i := 0; i < detailWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				scene, ok := s.toScene(ctx, studioURL, ref, now)
				if !ok {
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, ref := range refs {
		select {
		case work <- ref:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return
		}
	}
	close(work)
	wg.Wait()
}

// ---- detail ----

var (
	ogTitleRe = regexp.MustCompile(`property="og:title"\s+content="([^"]*)"`)
	ogDescRe  = regexp.MustCompile(`property="og:description"\s+content="([^"]*)"`)
	ogImageRe = regexp.MustCompile(`property="og:image"\s+content="([^"]*)"`)
	// The scene's own header carries its date; related cards further down carry
	// theirs, so the search starts at the <h1>.
	h1Re   = regexp.MustCompile(`<h1 class="tracking-tight`)
	dateRe = regexp.MustCompile(`>([A-Z][a-z]{2} \d{1,2}, \d{4})<`)
	// Cast and categories live in labelled blocks. Scoping to them is what
	// keeps the related-scenes rail out of the scene's own metadata.
	modelsLabelRe = regexp.MustCompile(`>Models:</span>`)
	catsLabelRe   = regexp.MustCompile(`>Categories:</span>`)
	// The scene's own credits are rendered as chips — a bare anchor wrapping a
	// <div>. The related-scenes rail uses `<a class="…" href="…"><h3>` instead,
	// so requiring the chip shape is what actually separates them; the label
	// scoping above is a second guard.
	modelLinkRe = regexp.MustCompile(`<a href="/models/\d+/([a-z0-9-]+)"><div`)
	catLinkRe   = regexp.MustCompile(`<a href="/categories/([a-z0-9-]+)"><div`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, ref sceneRef, now time.Time) (models.Scene, bool) {
	body, err := s.fetchPage(ctx, ref.url)
	if err != nil {
		return models.Scene{}, false
	}
	detail := string(body)

	title := ""
	if m := ogTitleRe.FindStringSubmatch(detail); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		return models.Scene{}, false
	}

	scene := models.Scene{
		ID:        ref.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     title,
		URL:       ref.url,
		Studio:    s.cfg.StudioName,
		ScrapedAt: now,
	}

	if m := ogDescRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := ogImageRe.FindStringSubmatch(detail); m != nil {
		scene.Thumbnail = cleanText(m[1])
	}

	// The first date after the <h1> is the scene's; earlier ones belong to
	// nav/promo and later ones to the related rail.
	if loc := h1Re.FindStringIndex(detail); loc != nil {
		if m := dateRe.FindStringSubmatch(detail[loc[1]:]); m != nil {
			if t, err := time.Parse("Jan 2, 2006", m[1]); err == nil {
				scene.Date = t.UTC()
			}
		}
	}

	scene.Performers = labelledSlugs(detail, modelsLabelRe, modelLinkRe)
	scene.Categories = labelledSlugs(detail, catsLabelRe, catLinkRe)

	return scene, true
}

// labelledSlugs reads the links inside a labelled block, stopping at the next
// block so the related-scenes rail is excluded. Slugs are title-cased because
// the visible chip text is not reliably adjacent to the link.
func labelledSlugs(detail string, label, link *regexp.Regexp) []string {
	loc := label.FindStringIndex(detail)
	if loc == nil {
		return nil
	}
	// A labelled block is short; bound the window so the next block and the
	// related rail cannot leak in.
	rest := detail[loc[1]:]
	if len(rest) > labelWindow {
		rest = rest[:labelWindow]
	}
	// Stop at the next label if one starts inside the window.
	if i := strings.Index(rest, `</span><div class="grid`); i > 0 {
		rest = rest[:i]
	}

	var out []string
	seen := make(map[string]bool)
	for _, m := range link.FindAllStringSubmatch(rest, -1) {
		name := titleCaseSlug(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// labelWindow bounds a labelled block. The blocks hold a handful of chips, each
// a few hundred bytes of Tailwind classes.
const labelWindow = 6000

// titleCaseSlug turns "guilhermina-johansen" into "Guilhermina Johansen".
func titleCaseSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
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

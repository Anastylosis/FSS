// Package nextdooramateur scrapes the Next Door Amateur network of static
// preview sites.
package nextdooramateur

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const defaultWorkers = 6

// SiteConfig describes one site of the network. Every site is the same
// hand-built static tour from the early 2000s: an index of thumbnails linking
// to one preview page per shoot, with no pager, no per-scene title and no
// catalogue feed of any kind.
type SiteConfig struct {
	SiteID     string
	StudioName string
	Host       string
	// IndexPaths are the pages that link to preview pages. Some sites list
	// their whole catalogue on the tour page, others on a separate girls
	// index; the links found across all of them are merged.
	IndexPaths []string
	// PreviewBase overrides where a preview href resolves. West Coast Gangbangs
	// serves its index at /tour/ but writes links that resolve from the site
	// root, so resolving against the index URL there yields 404s.
	PreviewBase string
}

var sites = []SiteConfig{
	{
		SiteID: "nextdooramateur", StudioName: "Next Door Amateur",
		Host:       "www.nextdooramateur.com",
		IndexPaths: []string{"/newhtml2/girlsivefucked.htm", "/newhtml2/"},
	},
	{
		SiteID: "amateurcreampies", StudioName: "Amateur Creampies",
		Host:       "www.amateurcreampies.com",
		IndexPaths: []string{"/tour/"},
	},
	{
		SiteID: "creampieebony", StudioName: "Creampie Ebony",
		Host:       "www.creampieebony.com",
		IndexPaths: []string{"/tour/thegirls.htm", "/tour/"},
	},
	{
		SiteID: "creampiesquad", StudioName: "Creampie Squad",
		Host:       "creampiesquad.com",
		IndexPaths: []string{"/tour/thegirls.htm", "/tour/"},
	},
	{
		SiteID: "girlsclimaxing", StudioName: "Girls Climaxing",
		Host:       "www.girlsclimaxing.com",
		IndexPaths: []string{"/tour/thegirls.htm", "/tour/"},
	},
	{
		SiteID: "hotwivesandgirlfriends", StudioName: "Hot Wives and Girlfriends",
		Host:       "www.hotwivesandgirlfriends.com",
		IndexPaths: []string{"/tour/thegirls.htm", "/tour/"},
	},
	{
		SiteID: "westcoastgangbangs", StudioName: "West Coast Gangbangs",
		Host:        "www.westcoastgangbangs.com",
		IndexPaths:  []string{"/tour/"},
		PreviewBase: "/",
	},
}

type Scraper struct {
	Client *http.Client
	cfg    SiteConfig
	base   string
}

// New builds a scraper for one site of the network.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		Client: httpx.NewClient(45 * time.Second),
		cfg:    cfg,
		base:   "http://" + cfg.Host,
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	bare := strings.TrimPrefix(s.cfg.Host, "www.")
	pats := []string{bare}
	for _, p := range s.cfg.IndexPaths {
		pats = append(pats, bare+p)
	}
	return pats
}

func (s *Scraper) matchRe() *regexp.Regexp {
	bare := regexp.QuoteMeta(strings.TrimPrefix(s.cfg.Host, "www."))
	return regexp.MustCompile(`^https?://(?:www\.)?` + bare + `(?:/.*)?$`)
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe().MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- discovery ----

var previewHrefRe = regexp.MustCompile(`(?i)href="([^"]*previews?/[^"]+\.html?)"`)

type previewRef struct {
	id  string
	url string
}

// previews merges the links found on every index page. A site whose tour and
// girls index disagree (the tour usually shows a subset) is covered by the
// union rather than by whichever page was tried first.
func (s *Scraper) previews(ctx context.Context) ([]previewRef, error) {
	seenURL := map[string]bool{}
	seenID := map[string]bool{}
	var refs []previewRef
	var firstErr error

	for _, path := range s.cfg.IndexPaths {
		idxURL := s.base + path
		body, err := s.get(ctx, idxURL)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("index %s: %w", idxURL, err)
			}
			continue
		}
		resolveBase := idxURL
		if s.cfg.PreviewBase != "" {
			resolveBase = s.base + s.cfg.PreviewBase
		}
		for _, m := range previewHrefRe.FindAllStringSubmatch(string(body), -1) {
			u := absURL(resolveBase, html.UnescapeString(m[1]))
			if u == "" || seenURL[u] {
				continue
			}
			seenURL[u] = true
			// One shoot can be linked twice under different filenames
			// (`previews/cynara/index.html` and `previews/cynara/cynara.html`
			// are the same page), and both resolve to the same shoot id.
			id := slugOf(u)
			if seenID[id] {
				continue
			}
			seenID[id] = true
			refs = append(refs, previewRef{id: id, url: u})
		}
	}

	if len(refs) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		// A tour that suddenly links no previews is a redesign, not an empty
		// catalogue — the difference decides whether --full may delete.
		return nil, scraper.ParseError(s.base+s.cfg.IndexPaths[0], fmt.Errorf("no preview pages linked"))
	}
	return refs, nil
}

// slugOf names the shoot. Sites that put each preview in its own directory
// (`previews/adara/index.html`) are keyed on the directory, not on "index".
func slugOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	last := parts[len(parts)-1]
	if i := strings.LastIndex(last, "."); i > 0 {
		last = last[:i]
	}
	if strings.EqualFold(last, "index") && len(parts) > 1 {
		last = parts[len(parts)-2]
	}
	return last
}

func absURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return b.ResolveReference(r).String()
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
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

// ---- run ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	refs, err := s.previews(ctx)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("%s: %w", s.cfg.SiteID, err)))
		return
	}
	scraper.Debugf(1, "%s: %d preview pages linked", s.cfg.SiteID, len(refs))
	send(ctx, out, scraper.Progress(len(refs)))

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	workers = min(workers, len(refs))

	// The index is alphabetical, not dated, so there is no order in which a
	// stored id means "everything after this is stored too" — KnownIDs cannot
	// stop this walk early and is deliberately not consulted.
	jobs := make(chan previewRef)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				scene, err := s.fetchPreview(ctx, ref, studioURL)
				if err != nil {
					send(ctx, out, scraper.Error(fmt.Errorf("%s: %s: %w", s.cfg.SiteID, ref.id, err)))
					continue
				}
				send(ctx, out, scraper.Scene(*scene))
			}
		}()
	}
	func() {
		defer close(jobs)
		for _, ref := range refs {
			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			select {
			case jobs <- ref:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) {
	select {
	case out <- r:
	case <-ctx.Done():
	}
}

func (s *Scraper) fetchPreview(ctx context.Context, ref previewRef, studioURL string) (*models.Scene, error) {
	body, err := s.get(ctx, ref.url)
	if err != nil {
		return nil, err
	}
	return s.parsePreview(body, ref, studioURL)
}

// ---- parsing ----

var (
	scriptRe  = regexp.MustCompile(`(?is)<(script|style)\b.*?</(?:script|style)>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagSplit  = regexp.MustCompile(`<[^>]*>`)
	wsRe      = regexp.MustCompile(`\s+`)
	nameRe    = regexp.MustCompile(`(?i)\bName\s*:\s*([A-Za-z][A-Za-z.'\- ]{1,40}?)\s*(?:Age\s*:|From\s*:|Location\s*:|The\s*Story|Gangbang|$)`)
	imgSrcRe  = regexp.MustCompile(`(?i)<img[^>]+src="([^"]+)"`)
	datedSlug = regexp.MustCompile(`^(\d{2})(\d{2})(\d{4})_(.+)$`)
	// Most of the network suffixes a slug with the shoot's month and day and
	// no year (`victory_0520`, `dakodabrooks_0630`). It is stripped from a
	// title built out of the slug, and deliberately not turned into a date:
	// without a year there is nothing to store.
	daySuffix = regexp.MustCompile(`_\d{4}$`)
)

// minStoryLen is the shortest text node treated as prose. Every page is a
// hand-built table layout with no story container, so the description is
// assembled from the text nodes long enough not to be navigation.
const minStoryLen = 120

// boilerplate names the long chrome lines that clear minStoryLen on some sites.
var boilerplate = []string{
	"cyber patrol",
	"netnanny",
	"copyright",
	"custodian of records",
	"2257",
	"all models",
	"registered with",
}

func (s *Scraper) parsePreview(body []byte, ref previewRef, studioURL string) (*models.Scene, error) {
	text := textNodes(string(body))
	joined := strings.Join(text, " ")

	name := ""
	if m := nameRe.FindStringSubmatch(joined); m != nil {
		name = strings.TrimSpace(m[1])
	}

	id := ref.id
	title := name
	scene := &models.Scene{
		ID:        id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Studio:    s.cfg.StudioName,
		URL:       ref.url,
		ScrapedAt: time.Now().UTC(),
	}

	// Some sites date the preview filename (MMDDYYYY_name); the rest publish
	// no date anywhere on the page, and one is not invented for them.
	if m := datedSlug.FindStringSubmatch(id); m != nil {
		if t, err := time.Parse("01022006", m[1]+m[2]+m[3]); err == nil {
			scene.Date = t.UTC()
		}
		if title == "" {
			title = prettifySlug(m[4])
		}
	}
	if title == "" {
		title = prettifySlug(daySuffix.ReplaceAllString(id, ""))
	}
	if title == "" {
		return nil, scraper.ParseError(ref.url, fmt.Errorf("no name and no usable slug"))
	}
	// The network publishes no scene titles at all — each preview is headed by
	// the model's name and nothing else — so the name is the title.
	scene.Title = title
	if name != "" {
		scene.Performers = []string{name}
	}
	scene.Description = story(text)
	scene.Thumbnail = thumbnail(string(body), ref.url, ref.id)
	return scene, nil
}

func textNodes(page string) []string {
	page = scriptRe.ReplaceAllString(page, " ")
	page = commentRe.ReplaceAllString(page, " ")
	var out []string
	for _, part := range tagSplit.Split(page, -1) {
		part = strings.ReplaceAll(html.UnescapeString(part), " ", " ")
		part = strings.TrimSpace(wsRe.ReplaceAllString(part, " "))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func story(nodes []string) string {
	var paras []string
	for _, n := range nodes {
		if len(n) < minStoryLen {
			continue
		}
		low := strings.ToLower(n)
		skip := false
		for _, b := range boilerplate {
			if strings.Contains(low, b) {
				skip = true
				break
			}
		}
		if !skip {
			paras = append(paras, n)
		}
	}
	return strings.Join(paras, "\n\n")
}

// galleryDirs are the paths these sites keep shoot stills under. Everything
// else an <img> points at is site furniture — banners, buttons, spacers — and
// storing one of those as the thumbnail is worse than storing none.
var galleryDirs = []string{"/pic/", "/pics/", "/catalog", "/galleries/", "/photos/"}

// thumbnail picks the first shoot still. The network keeps them three
// different ways, so the rules are tried in order of how specific they are:
// a path naming the shoot, a path under a known gallery directory, and finally
// a bare filename sitting beside the preview page. Chrome images always live
// under a directory on every site in the network, so the last rule cannot
// pick up a banner.
func thumbnail(page, pageURL, slug string) string {
	srcs := make([]string, 0, 16)
	for _, m := range imgSrcRe.FindAllStringSubmatch(page, -1) {
		srcs = append(srcs, html.UnescapeString(m[1]))
	}

	lowSlug := strings.ToLower(slug)
	if lowSlug != "" {
		for _, src := range srcs {
			if strings.Contains(strings.ToLower(src), lowSlug) {
				return absURL(pageURL, src)
			}
		}
	}
	for _, src := range srcs {
		low := strings.ToLower(src)
		for _, dir := range galleryDirs {
			if strings.Contains(low, dir) {
				return absURL(pageURL, src)
			}
		}
	}
	for _, src := range srcs {
		if !strings.Contains(src, "/") && !strings.Contains(src, ":") {
			return absURL(pageURL, src)
		}
	}
	return ""
}

func prettifySlug(slug string) string {
	s := strings.NewReplacer("_", " ", "-", " ").Replace(slug)
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

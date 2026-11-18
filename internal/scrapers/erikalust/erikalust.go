// Package erikalust scrapes the three Erika Lust Films storefronts —
// erikalust.com, lustcinema.com and xconfessions.com. They share a Nuxt 2
// platform and a sitemap layout, but not a scene template, so the parser reads
// each field through a short list of alternatives rather than branching on the
// site.
//
// Three things need care.
//
// **The sitemaps are gzip with no `.gz` on the index URL.** `/sitemap.xml` is
// served as `application/gzip` with no `Content-Encoding`, so nothing upstream
// unwraps it; its children are `*.xml.gz`. All of them are decompressed before
// parsing, or the bytes read as XML yield nothing.
//
// **The exact date and runtime are only in the Nuxt payload.** The rendered
// page shows the year and a rounded "15 min". The payload is minified JS with
// variable indirection (`t.release_date=N`), so it is not parsed — instead the
// one `YYYY-MM-DD HH:MM:SS` literal in it is taken, and kept only when its year
// matches the year the page rendered. That cross-check is what makes lifting a
// bare literal out of a minified blob safe.
//
// **The poster comes from the payload, not from the page.** The first `<img>`
// on every template is the site logo, an SVG, so an img-tag fallback stores a
// logo as the thumbnail. `poster_picture` is the real one.
package erikalust

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one storefront.
type SiteConfig struct {
	SiteID string
	Studio string
	Domain string
	// ScenePrefix is the path segment scene pages live under: erikalust.com and
	// xconfessions.com use "film", lustcinema.com uses "movies".
	ScenePrefix string
}

var sites = []SiteConfig{
	{SiteID: "erikalust", Studio: "Erika Lust Films", Domain: "erikalust.com", ScenePrefix: "film"},
	{SiteID: "lustcinema", Studio: "Lust Cinema", Domain: "lustcinema.com", ScenePrefix: "movies"},
	{SiteID: "xconfessions", Studio: "XConfessions", Domain: "xconfessions.com", ScenePrefix: "film"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

type Scraper struct {
	cfg          SiteConfig
	Client       *http.Client
	matchRe      *regexp.Regexp
	scenePathRe  *regexp.Regexp
	baseOverride string
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:         cfg,
		Client:      httpx.NewClient(45 * time.Second),
		matchRe:     regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
		scenePathRe: regexp.MustCompile(`^/` + regexp.QuoteMeta(cfg.ScenePrefix) + `/([^/?#]+)$`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/" + s.cfg.ScenePrefix + "/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// base resolves the origin to fetch from. The operator's own host wins so a
// non-www spelling stays addressable; baseOverride is the test server. Sitemap
// entries are absolute live URLs and are re-based onto this, or an offline test
// would still hit production.
func (s *Scraper) base(studioURL string) string {
	if s.baseOverride != "" {
		return strings.TrimSuffix(s.baseOverride, "/")
	}
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return "https://" + s.cfg.Domain
}

var hrefRe = regexp.MustCompile(`href="([^"]+)"`)

var locRe = regexp.MustCompile(`<loc>\s*([^<\s]+)\s*</loc>`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := s.base(studioURL)

	// A single scene URL is a legitimate thing to point at.
	if p, ok := s.scenePath(studioURL); ok {
		scraper.Debugf(1, "%s: scraping one scene %s", s.cfg.SiteID, p)
		if !send(ctx, out, scraper.Progress(1)) {
			return
		}
		if sc := s.buildScene(ctx, studioURL, base, p, out); sc.ID != "" {
			send(ctx, out, scraper.Scene(sc))
		}
		return
	}

	paths, err := s.discover(ctx, base, opts)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	if len(paths) == 0 {
		scraper.Debugf(1, "%s: sitemaps list no scenes, walking /%s?page=N", s.cfg.SiteID, s.cfg.ScenePrefix)
		paths, err = s.crawlListing(ctx, base, opts)
		if err != nil {
			// Scenes found before a failed page are still worth fetching; the
			// error keeps the run from counting as a complete traversal.
			send(ctx, out, scraper.Error(err))
			if len(paths) == 0 {
				return
			}
		}
	}
	if len(paths) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(base+"/sitemap.xml",
			fmt.Errorf("no /%s/ URLs in the sitemaps", s.cfg.ScenePrefix))))
		return
	}

	scraper.Debugf(1, "%s: %d scenes in the sitemaps, fetching with %d workers", s.cfg.SiteID, len(paths), opts.Workers)
	if !send(ctx, out, scraper.Progress(len(paths))) {
		return
	}
	s.fetchAll(ctx, studioURL, base, paths, opts, out)
}

// discover reads the sitemap index and its children.
func (s *Scraper) discover(ctx context.Context, base string, opts scraper.ListOpts) ([]string, error) {
	index, err := s.fetch(ctx, base+"/sitemap.xml")
	if err != nil {
		return nil, fmt.Errorf("sitemap index: %w", err)
	}

	children := make([]string, 0, 4)
	for _, m := range locRe.FindAllStringSubmatch(string(index), -1) {
		children = append(children, rebase(html.UnescapeString(m[1]), base))
	}
	if len(children) == 0 {
		// Some deployments serve the entries directly rather than an index.
		children = []string{base + "/sitemap.xml"}
	}

	seen := make(map[string]bool)
	var paths []string
	for i, c := range children {
		if ctx.Err() != nil {
			return paths, ctx.Err()
		}
		if i > 0 && !sleep(ctx, opts.Delay) {
			return paths, ctx.Err()
		}
		body, err := s.fetch(ctx, c)
		if err != nil {
			// One unreadable child sitemap is not the catalogue; the others
			// still contribute.
			scraper.Debugf(1, "%s: sitemap %s unavailable: %v", s.cfg.SiteID, c, err)
			continue
		}
		for _, m := range locRe.FindAllStringSubmatch(string(body), -1) {
			p, ok := s.scenePath(html.UnescapeString(m[1]))
			if !ok || seen[p] {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// crawlListing walks the paginated listing for a site whose sitemaps no longer
// enumerate scenes, stopping at the first page that adds none.
func (s *Scraper) crawlListing(ctx context.Context, base string, opts scraper.ListOpts) ([]string, error) {
	const maxPages = 200
	seen := make(map[string]bool)
	var paths []string
	for page := 1; page <= maxPages; page++ {
		if ctx.Err() != nil {
			return paths, ctx.Err()
		}
		if page > 1 && !sleep(ctx, opts.Delay) {
			return paths, ctx.Err()
		}
		listURL := fmt.Sprintf("%s/%s?page=%d", base, s.cfg.ScenePrefix, page)
		body, err := s.fetch(ctx, listURL)
		if err != nil {
			if len(paths) > 0 {
				return paths, scraper.TransportError(listURL, err)
			}
			return nil, err
		}
		added := 0
		for _, m := range hrefRe.FindAllStringSubmatch(string(body), -1) {
			p, ok := s.scenePath(html.UnescapeString(m[1]))
			if !ok || seen[p] {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
			added++
		}
		if added == 0 {
			break
		}
	}
	return paths, nil
}

// scenePath reports whether a URL names a scene page, and returns its path.
// The sitemaps mix scenes with performers, directors, series and confessions,
// and scenes themselves carry `/{prefix}/{slug}/watch/...` sub-pages for
// trailers, chapters and behind-the-scenes clips — only the bare slug is a
// scene, which is why the pattern allows exactly one segment.
func (s *Scraper) scenePath(rawURL string) (string, bool) {
	p := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
		p = u.Path
	}
	p = strings.TrimSuffix(strings.TrimSpace(p), "/")
	if !s.scenePathRe.MatchString(p) {
		return "", false
	}
	return p, true
}

func rebase(rawURL, base string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return rawURL
	}
	return base + u.Path
}

func (s *Scraper) fetchAll(ctx context.Context, studioURL, base string, paths []string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	work := make(chan string)
	var wg sync.WaitGroup
	// LIFO: close(work) ends the workers' range loops, then wg.Wait blocks
	// until they are gone, so a ctx.Done bail below cannot leak them.
	defer wg.Wait()
	defer close(work)

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				if !sleep(ctx, opts.Delay) {
					return
				}
				sc := s.buildScene(ctx, studioURL, base, p, out)
				if sc.ID == "" {
					continue
				}
				if !send(ctx, out, scraper.Scene(sc)) {
					return
				}
			}
		}()
	}

	for _, p := range paths {
		select {
		case work <- p:
		case <-ctx.Done():
			return
		}
	}
}

// buildScene fetches one scene page. A zero-value ID means it could not be read
// and the failure has already been reported.
func (s *Scraper) buildScene(ctx context.Context, studioURL, base, path string, out chan<- scraper.SceneResult) models.Scene {
	sceneURL := base + path

	body, err := s.fetch(ctx, sceneURL)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("scene %s: %w", path, err)))
		return models.Scene{}
	}

	d := parseScene(string(body), s.cfg.Studio)
	if d.title == "" {
		send(ctx, out, scraper.Error(scraper.ParseError(sceneURL, fmt.Errorf("no scene block on the page"))))
		return models.Scene{}
	}

	return models.Scene{
		ID:          strings.TrimPrefix(path, "/"+s.cfg.ScenePrefix+"/"),
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         sceneURL,
		Studio:      s.cfg.Studio,
		Description: d.description,
		Performers:  d.performers,
		Director:    d.director,
		Categories:  d.categories,
		Date:        d.date,
		Duration:    d.duration,
		Thumbnail:   d.thumb,
		ScrapedAt:   time.Now().UTC(),
	}
}

// fetch reads a URL, transparently decompressing a gzip body. The sitemaps are
// gzip served without a `Content-Encoding` header, so the transport does not
// unwrap them and the bytes arrive compressed.
func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	return maybeGunzip(raw), nil
}

// gzipMagic is the two-byte header every gzip stream starts with.
var gzipMagic = []byte{0x1f, 0x8b}

func maybeGunzip(raw []byte) []byte {
	if !bytes.HasPrefix(raw, gzipMagic) {
		return raw
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	defer func() { _ = zr.Close() }()
	out, err := io.ReadAll(zr)
	if err != nil {
		// A truncated stream still yields whatever decoded before the error,
		// which is more useful than the compressed bytes.
		if len(out) > 0 {
			return out
		}
		return raw
	}
	return out
}

// ---- scene page ----

type sceneData struct {
	title       string
	description string
	performers  []string
	director    string
	categories  []string
	date        time.Time
	duration    int
	thumb       string
}

var (
	scriptRe = regexp.MustCompile(`(?s)<script.*?</script>`)
	styleRe  = regexp.MustCompile(`(?s)<style.*?</style>`)
	nuxtRe   = regexp.MustCompile(`(?s)window\.__NUXT__\s*=\s*(.*?)</script>`)

	// Titles: the film template names its heading, the movie template has no
	// h1 at all and only the document title carries the name.
	detailsTitleRe = regexp.MustCompile(`(?s)<h1[^>]*movie-details-title[^>]*>(.*?)</h1>`)
	anyH1Re        = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	docTitleRe     = regexp.MustCompile(`(?s)<title>(.*?)</title>`)

	descBlockRe = regexp.MustCompile(`(?s)<div class="[^"]*description-block[^"]*"[^>]*>(.*?)</div>`)
	// The movie template blurs the synopsis rather than withholding it.
	blurbRe = regexp.MustCompile(`(?s)<div class="text-sm blur-sm[^"]*"[^>]*>(.*?)</div>`)

	catLinkRe  = regexp.MustCompile(`(?s)<a href="/categories/[^"]*"[^>]*>(.*?)</a>`)
	dirLinkRe  = regexp.MustCompile(`(?s)<a href="/(?:collaborators/)?directors/[^"]*"[^>]*>(.*?)</a>`)
	dirTitleRe = regexp.MustCompile(`<a href="/(?:collaborators/)?directors/[^"]*" title="([^"]*)"`)
	perfLinkRe = regexp.MustCompile(`(?s)<a href="/performers/[^"]*"[^>]*>(.*?)</a>`)
	// The movie template links its cast by image, so the name is an attribute.
	castTitleRe = regexp.MustCompile(`<a href="/cast/[^"]*" title="([^"]*)"`)
	// Films without linked cast name them in the line under the title.
	castParaRe = regexp.MustCompile(`(?s)</h1>\s*</div>\s*<div[^>]*>\s*<p class="text-lg">(.*?)</p>`)

	yearRe      = regexp.MustCompile(`<span class="text-neutral-30"[^>]*>\s*(\d{4})\s*</span>`)
	yearParaRe  = regexp.MustCompile(`<p class="[^"]*text-neutral"[^>]*>\s*(\d{4})\s*</p>`)
	minutesRe   = regexp.MustCompile(`<span class="uppercase"[^>]*>\s*(\d+)\s*min\s*</span>`)
	ogImageRe   = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
	posterRe    = regexp.MustCompile(`poster_picture\s*[:=]\s*"([^"]+)"`)
	coverRe     = regexp.MustCompile(`cover_picture\s*[:=]\s*"([^"]+)"`)
	lengthRe    = regexp.MustCompile(`length\s*[:=]\s*"(\d{1,2}:\d{2}:\d{2})"`)
	dateLitRe   = regexp.MustCompile(`"(\d{4})-(\d{2})-(\d{2}) \d{2}:\d{2}:\d{2}"`)
	titleSuffix = regexp.MustCompile(`\s*(?:—|-|\|)\s*[^—\-|]*$`)
)

func parseScene(body, studio string) sceneData {
	payload := firstSubmatch(nuxtRe, body)
	clean := styleRe.ReplaceAllString(scriptRe.ReplaceAllString(body, ""), "")

	d := sceneData{
		title:       cleanText(firstSubmatch(detailsTitleRe, clean)),
		description: firstNonEmpty(descBlockRe, clean),
		director:    cleanText(firstSubmatch(dirLinkRe, clean)),
	}
	if d.title == "" {
		d.title = cleanText(firstSubmatch(anyH1Re, clean))
	}
	if d.title == "" {
		d.title = docTitle(firstSubmatch(docTitleRe, clean), studio)
	}
	if d.description == "" {
		d.description = firstNonEmpty(blurbRe, clean)
	}
	if d.director == "" {
		d.director = cleanText(firstSubmatch(dirTitleRe, clean))
	}
	d.thumb = unescapeJS(firstSubmatch(posterRe, payload))
	if d.thumb == "" {
		d.thumb = unescapeJS(firstSubmatch(coverRe, payload))
	}
	if d.thumb == "" {
		// Never fall back to the first <img>: on every template that is the
		// site logo.
		d.thumb = html.UnescapeString(firstSubmatch(ogImageRe, body))
	}

	for _, m := range catLinkRe.FindAllStringSubmatch(clean, -1) {
		if c := cleanText(m[1]); c != "" {
			d.categories = appendUnique(d.categories, c)
		}
	}
	for _, re := range []*regexp.Regexp{perfLinkRe, castTitleRe} {
		for _, m := range re.FindAllStringSubmatch(clean, -1) {
			if n := cleanText(m[1]); n != "" {
				d.performers = appendUnique(d.performers, n)
			}
		}
	}
	if len(d.performers) == 0 {
		for _, part := range strings.Split(cleanText(firstSubmatch(castParaRe, clean)), ",") {
			if n := cleanText(part); n != "" {
				d.performers = appendUnique(d.performers, n)
			}
		}
	}
	d.performers = dropCompositeCredits(d.performers)

	// The payload's `length` is exact; the rendered "15 min" is rounded.
	if v := firstSubmatch(lengthRe, payload); v != "" {
		d.duration = parseutil.ParseDurationColon(v)
	}
	if d.duration == 0 {
		if v := firstSubmatch(minutesRe, clean); v != "" {
			if mins, err := strconv.Atoi(v); err == nil {
				d.duration = mins * 60
			}
		}
	}

	year := firstSubmatch(yearRe, clean)
	if year == "" {
		year = firstSubmatch(yearParaRe, clean)
	}
	d.date = releaseDate(payload, year)
	return d
}

// docTitle strips the storefront's own name off a document title. Only a
// trailing separator is removed, so a scene whose name contains a dash keeps it.
func docTitle(raw, studio string) string {
	t := cleanText(raw)
	if t == "" {
		return ""
	}
	if trimmed := titleSuffix.ReplaceAllString(t, ""); trimmed != "" && namesStudio(t, studio) {
		return strings.TrimSpace(trimmed)
	}
	return t
}

// namesStudio reports whether a document title ends in the storefront's own
// name. The site spells it without spaces ("LustCinema" for Lust Cinema), so
// the comparison ignores spacing and case.
func namesStudio(title, studio string) bool {
	squash := func(v string) string {
		return strings.ToLower(strings.ReplaceAll(v, " ", ""))
	}
	return strings.Contains(squash(title), squash(studio))
}

// releaseDate lifts the one `YYYY-MM-DD HH:MM:SS` literal out of the minified
// Nuxt payload — the rendered page shows only the year, and the payload assigns
// `release_date` through a variable, so the literal cannot be found by name.
// It is kept only when its year matches the year the page rendered, which is
// what makes taking a bare literal out of a minified blob safe: a second date
// creeping in would have to agree with the displayed year to be accepted.
func releaseDate(payload, renderedYear string) time.Time {
	m := dateLitRe.FindStringSubmatch(payload)
	if m == nil {
		return time.Time{}
	}
	if renderedYear != "" && renderedYear != m[1] {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3])
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// dropCompositeCredits removes a duo entity the site lists alongside its
// members — "Jet Setting Jasmine & King Noire" is a real performer page here,
// and storing it files a third person who does not exist. A composite is only
// dropped when every one of its parts is separately credited on the same scene,
// so a stage name that happens to contain "&" survives.
func dropCompositeCredits(names []string) []string {
	if len(names) < 3 {
		return names
	}
	present := make(map[string]bool, len(names))
	for _, n := range names {
		present[strings.ToLower(n)] = true
	}
	out := names[:0:0]
	for _, n := range names {
		parts := strings.Split(n, "&")
		if len(parts) > 1 && allCredited(parts, present) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func allCredited(parts []string, present map[string]bool) bool {
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || !present[p] {
			return false
		}
	}
	return true
}

// ---- helpers ----

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

// unescapeJS undoes the `/` escaping the Nuxt payload applies to URLs.
func unescapeJS(s string) string {
	if s == "" {
		return ""
	}
	if u, err := strconv.Unquote(`"` + strings.ReplaceAll(s, `"`, `\"`) + `"`); err == nil {
		s = u
	}
	return html.UnescapeString(s)
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

// firstNonEmpty returns the first match whose text is not blank. Both
// templates render an empty placeholder block before the real synopsis, so
// taking the first match outright yields "".
func firstNonEmpty(re *regexp.Regexp, s string) string {
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if v := cleanText(m[1]); v != "" {
			return v
		}
	}
	return ""
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

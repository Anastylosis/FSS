package trixvideo

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

// SiteConfig describes one site on this template. It is exported because the
// template is a vendor one rather than a Trix Video invention — housewifekelly
// runs the same tour and reuses this engine from its own package.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// DateLayout parses the tour's `update_date` value. The template prints a
	// bare `dd/mm/yyyy`-shaped string with no clue which way round it is, and
	// the sites disagree: every Trix Video tour and housewifekelly write
	// MM/DD/YYYY (second component runs past 12, first never does), while
	// justdanica writes DD/MM/YYYY (first component runs to 30, second never
	// past 12). Guessing wrong silently mis-dates every scene whose day is
	// 12 or lower, so it is stated per site. Empty means MM/DD/YYYY.
	DateLayout string
	// TourPath is the tour's directory, without a trailing slash. Empty means
	// "/tour", which is where the vendor installs it; a site that has rebuilt
	// its tour keeps the old one live and numbers the new one (Desperate
	// Amateurs serves "/tour3").
	TourPath string
}

// defaultDateLayout is the MM/DD/YYYY form the majority of these tours use.
const defaultDateLayout = "01/02/2006"

func (c SiteConfig) tourPath() string {
	if c.TourPath == "" {
		return "/tour"
	}
	return c.TourPath
}

func (c SiteConfig) dateLayout() string {
	if c.DateLayout == "" {
		return defaultDateLayout
	}
	return c.DateLayout
}

var sites = []SiteConfig{
	{SiteID: "cherokeedass", Domain: "cherokeedass.com", StudioName: "Cherokee D'Ass"},
	{SiteID: "dallasdiamondz", Domain: "dallasdiamondz.com", StudioName: "Dallas Diamondz"},
	{SiteID: "desperateamateurs", Domain: "desperateamateurs.com", StudioName: "Desperate Amateurs", TourPath: "/tour3"},
	{SiteID: "dixiestrailerpark", Domain: "dixiestrailerpark.com", StudioName: "Dixie's Trailer Park"},
	{SiteID: "grannycumshere", Domain: "grannycumshere.com", StudioName: "Granny Cums Here"},
	{SiteID: "msparisandfriends", Domain: "msparisandfriends.com", StudioName: "Ms Paris & Friends"},
	{SiteID: "suburbantaboo", Domain: "suburbantaboo.com", StudioName: "Suburban Taboo"},
	{SiteID: "swingingbicouples", Domain: "swingingbicouples.com", StudioName: "Swinging Bi Couples"},
	{SiteID: "tampahousewives", Domain: "tampahousewives.com", StudioName: "Tampa Housewives"},
	{SiteID: "whorebaithals", Domain: "whorebaithals.com", StudioName: "Whore Bait Hal's"},
}

type Scraper struct {
	Client       *http.Client
	cfg          SiteConfig
	matchRe      *regexp.Regexp
	baseOverride string
}

// New builds a scraper for one site on this template.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		Client:  httpx.NewClient(30 * time.Second),
		cfg:     cfg,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + s.cfg.tourPath() + "/models/{slug}.html",
		s.cfg.Domain + s.cfg.tourPath() + "/categories/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// base resolves the origin to fetch from. The operator's own host wins so an
// http-only or non-www spelling stays addressable; baseOverride is the test
// server. Every request is built as base + path, never from the absolute URLs
// the pages embed, so pointing base at httptest redirects the whole crawl
// rather than just its first request.
func (s *Scraper) base(studioURL string) string {
	if s.baseOverride != "" {
		return strings.TrimSuffix(s.baseOverride, "/")
	}
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return "https://www." + s.cfg.Domain
}

var (
	modelURLRe    = regexp.MustCompile(`/tour\d*/models/([^/?#]+)\.html`)
	categoryURLRe = regexp.MustCompile(`/tour\d*/categories/([^/?#]+)\.html`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := s.base(studioURL)

	if m := modelURLRe.FindStringSubmatch(studioURL); m != nil && !strings.HasPrefix(m[1], "models") {
		s.runModel(ctx, studioURL, base, m[1], opts, out)
		return
	}
	if m := categoryURLRe.FindStringSubmatch(studioURL); m != nil && m[1] != "Tags" {
		slug := strings.TrimSuffix(m[1], "_1_p")
		scraper.Debugf(1, "%s: scraping category %q", s.cfg.SiteID, slug)
		s.collect(ctx, studioURL, base, s.categoryPages(slug), nil, opts, out)
		return
	}

	scraper.Debugf(1, "%s: scraping full update listing", s.cfg.SiteID)
	s.collect(ctx, studioURL, base, s.updatePages(), nil, opts, out)
}

// runModel filters the full listing rather than parsing the model page's own
// cards. Those cards carry no set id and no detail link, and on all eight sites
// they are a strict subset of what the update listing reaches, so filtering
// yields the same scenes with the same ids as a full scrape.
func (s *Scraper) runModel(ctx context.Context, studioURL, base, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	scraper.Debugf(1, "%s: scraping model %q", s.cfg.SiteID, slug)
	s.collect(ctx, studioURL, base, s.updatePages(), &slug, opts, out)
}

func (s *Scraper) updatePages() func(page int) string {
	return func(page int) string { return fmt.Sprintf("%s/updates/page_%d.html", s.cfg.tourPath(), page) }
}

func (s *Scraper) categoryPages(slug string) func(page int) string {
	return func(page int) string {
		return fmt.Sprintf("%s/categories/%s_%d_p.html", s.cfg.tourPath(), slug, page)
	}
}

const (
	// maxListingPages bounds the walk. Out-of-range pages do not 404 — the CMS
	// clamps them and keeps serving the last page forever, so an unbounded loop
	// would never terminate on its own.
	maxListingPages = 60
	// stalePageLimit is how many consecutive pages may add no new set before
	// the walk stops. One is not enough: the widgets these cards come from
	// rotate, so a page can repeat entirely and still be followed by new sets.
	stalePageLimit = 2
)

// collect walks a paginated listing, then fetches each set's detail page. When
// modelSlug is non-nil only sets crediting that model are fetched.
func (s *Scraper) collect(ctx context.Context, studioURL, base string, pageURL func(int) string, modelSlug *string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	seen := make(map[string]bool)
	seenPath := make(map[string]bool)
	var entries []listEntry
	cardsSeen := 0
	stale := 0

	for page := 1; page <= maxListingPages; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !sleep(ctx, opts.Delay) {
			return
		}

		path := pageURL(page)
		scraper.Debugf(1, "%s: fetching listing %s", s.cfg.SiteID, path)
		body, err := s.fetch(ctx, base+path)
		if err != nil {
			send(ctx, out, scraper.Error(fmt.Errorf("listing page %d: %w", page, err)))
			return
		}

		cards := parseCards(string(body))
		if len(cards) == 0 {
			break
		}

		added := 0
		for _, c := range cards {
			// Both keys matter. Deduping on the id alone is the real pagination
			// stop signal, but a card that carried a set id and no link of its
			// own would take the next link in its chunk, pairing one page under
			// two ids; dropping the repeated path turns that into a miss rather
			// than a duplicated scene.
			if seen[c.id] || seenPath[c.path] {
				continue
			}
			seen[c.id] = true
			seenPath[c.path] = true
			added++
			cardsSeen++
			if modelSlug != nil && !c.creditsModel(*modelSlug) {
				continue
			}
			entries = append(entries, c)
		}
		if added == 0 {
			if stale++; stale >= stalePageLimit {
				break
			}
			continue
		}
		stale = 0
	}

	if len(entries) == 0 {
		if cardsSeen == 0 {
			// A page that fetched cleanly but yielded nothing is reported rather
			// than returned as a silent success: an empty result here is far more
			// likely to be a template change than an empty site.
			send(ctx, out, scraper.Error(scraper.ParseError(base, fmt.Errorf("no update cards parsed"))))
			return
		}
		// The listing parsed; the filter is what emptied it. Left unclassified
		// on purpose — a model with no sets and a mistyped slug look identical
		// from here, and only one of them is harmless.
		send(ctx, out, scraper.Error(fmt.Errorf("no set among the %d found credits model %q", cardsSeen, *modelSlug)))
		return
	}

	scraper.Debugf(1, "%s: %d sets discovered, fetching details with %d workers", s.cfg.SiteID, len(entries), opts.Workers)
	if !send(ctx, out, scraper.Progress(len(entries))) {
		return
	}
	s.fetchDetails(ctx, studioURL, base, entries, opts, out)
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL, base string, entries []listEntry, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	work := make(chan listEntry)
	var wg sync.WaitGroup
	// LIFO: close(work) lets the workers' range loops end, then wg.Wait blocks
	// until they are gone, so a ctx.Done bail below cannot leak them.
	defer wg.Wait()
	defer close(work)

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range work {
				if !sleep(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, studioURL, base, e)
				if err != nil {
					if !send(ctx, out, scraper.Error(err)) {
						return
					}
					continue
				}
				if !send(ctx, out, scraper.Scene(scene)) {
					return
				}
			}
		}()
	}

	for _, e := range entries {
		select {
		case work <- e:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) fetchDetail(ctx context.Context, studioURL, base string, e listEntry) (models.Scene, error) {
	sceneURL := base + e.path
	body, err := s.fetch(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("set %s: %w", e.id, err)
	}

	d := parseDetail(string(body))
	if d.title == "" {
		return models.Scene{}, scraper.ParseError(sceneURL, fmt.Errorf("no update block on detail page"))
	}

	scene := models.Scene{
		ID:          e.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         sceneURL,
		Description: d.description,
		Performers:  d.performers,
		Studio:      s.cfg.StudioName,
		Tags:        d.tags,
		ScrapedAt:   time.Now().UTC(),
	}
	if len(scene.Performers) == 0 {
		scene.Performers = e.performers
	}
	if d.thumbnail != "" {
		scene.Thumbnail = resolveURL(base, s.cfg.tourPath(), d.thumbnail)
	} else if e.thumbnail != "" {
		scene.Thumbnail = resolveURL(base, s.cfg.tourPath(), e.thumbnail)
	}
	if t, ok := parseDate(firstNonEmpty(d.date, e.date), s.cfg.dateLayout()); ok {
		scene.Date = t
	}
	return scene, nil
}

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
	return httpx.ReadBody(resp.Body)
}

// ---- listing cards ----

type listEntry struct {
	id         string
	path       string
	title      string
	performers []string
	modelSlugs []string
	date       string
	thumbnail  string
}

func (e listEntry) creditsModel(slug string) bool {
	for _, s := range e.modelSlugs {
		if strings.EqualFold(s, slug) {
			return true
		}
	}
	return false
}

var (
	cardSplitRe  = regexp.MustCompile(`<div class="update_details"`)
	setIDRe      = regexp.MustCompile(`data-setid="(\d+)"`)
	updatePathRe = regexp.MustCompile(`href="(?:https?://[^"]*?)?(/tour\d*/updates/[^"?#]+\.html)"`)
	cardTitleRe  = regexp.MustCompile(`(?s)<a[^>]*href="[^"]*/tour\d*/updates/[^"]+\.html"[^>]*>([^<]+)</a>`)
	cardModelsRe = regexp.MustCompile(`(?s)<span class="update_models">(.*?)</span>`)
	cardDateRe   = regexp.MustCompile(`(?s)class="cell update_date">.*?(\d{2}/\d{2}/\d{4})`)
	cardThumbRe  = regexp.MustCompile(`\ssrc="((?:[^"]*?/)?content/[^"]+)"`)
	modelHrefRe  = regexp.MustCompile(`/tour\d*/models/([^"/?#]+)\.html`)
	anchorTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
)

// parseCards reads the compact `update_details` cards. They are the only card
// shape carrying both the CMS set id and a link to the detail page; the richer
// `update_block` on the same pages carries neither.
func parseCards(body string) []listEntry {
	chunks := cardSplitRe.Split(body, -1)
	var out []listEntry
	for i, chunk := range chunks {
		if i == 0 {
			continue // preamble before the first card
		}
		id := firstSubmatch(setIDRe, chunk)
		path := firstSubmatch(updatePathRe, chunk)
		if id == "" || path == "" {
			continue // category tiles and join-gated cards have neither
		}
		e := listEntry{
			id:        id,
			path:      path,
			title:     cleanText(firstSubmatch(cardTitleRe, chunk)),
			date:      firstSubmatch(cardDateRe, chunk),
			thumbnail: firstSubmatch(cardThumbRe, chunk),
		}
		if span := firstSubmatch(cardModelsRe, chunk); span != "" {
			e.performers = anchorTexts(span)
			e.modelSlugs = modelSlugs(span)
		}
		out = append(out, e)
	}
	return out
}

// ---- detail page ----

type detail struct {
	title       string
	date        string
	description string
	performers  []string
	tags        []string
	thumbnail   string
}

var (
	titleRe      = regexp.MustCompile(`(?s)<span class="update_title">(.*?)</span>`)
	richDateRe   = regexp.MustCompile(`<span class="update_date">\s*(\d{2}/\d{2}/\d{4})`)
	descRe       = regexp.MustCompile(`(?s)<span class="latest_update_description">(.*?)</span>`)
	richModelsRe = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	richTagsRe   = regexp.MustCompile(`(?s)<span class="tour_update_tags">(.*?)</span>`)
	richThumbRe  = regexp.MustCompile(`class="[^"]*large_update_thumb[^"]*"\s+src="([^"]+)"`)
)

func parseDetail(body string) detail {
	d := detail{
		title:       cleanText(firstSubmatch(titleRe, body)),
		date:        firstSubmatch(richDateRe, body),
		description: cleanText(firstSubmatch(descRe, body)),
		thumbnail:   firstSubmatch(richThumbRe, body),
	}
	if span := firstSubmatch(richModelsRe, body); span != "" {
		d.performers = anchorTexts(span)
	}
	if span := firstSubmatch(richTagsRe, body); span != "" {
		d.tags = anchorTexts(span)
	}
	return d
}

// ---- helpers ----

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

func anchorTexts(span string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range anchorTextRe.FindAllStringSubmatch(span, -1) {
		t := cleanText(m[1])
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func modelSlugs(span string) []string {
	var out []string
	for _, m := range modelHrefRe.FindAllStringSubmatch(span, -1) {
		out = append(out, m[1])
	}
	return out
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseDate(s, layout string) (time.Time, bool) {
	t, err := time.Parse(layout, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// resolveURL turns the tour's relative `content/...` image paths into absolute
// ones. The pages carry a <base href> of "<origin><tourPath>/", which is what
// the relative form is written against.
func resolveURL(base, tourPath, ref string) string {
	switch {
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return base + tourPath + "/" + ref
	}
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

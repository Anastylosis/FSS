// Package adultempirestore scrapes the AdultEmpire white-label storefronts that
// several studios run as their official site.
package adultempirestore

import (
	"context"
	"fmt"
	"html"
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

const (
	scenesPath = "/shop-streaming-video-by-scene.html"
	// The storefront's own "Yes, I am 18+" button sets this; sending it makes
	// the same self-declaration a visitor makes by clicking. It is not a login
	// and gates nothing but the interstitial.
	ageCookie = "ageConfirmed=1"

	defaultWorkers = 6
	// maxListingPages caps the page count read off the pager, so a mangled
	// "Go To Page" link cannot turn into an unbounded walk. A page with no
	// cards ends the filter before this in every normal run.
	maxListingPages = 500
)

// StudioFilter names one `?studio=` id the storefront sells under. A white-label
// store carries third-party titles too — Forbidden Fruits Films' unfiltered
// scene listing includes Xprime — so the walk is always filtered, or scenes
// would be stored under the wrong studio.
type StudioFilter struct {
	ID   string
	Name string
}

type SiteConfig struct {
	SiteID     string
	StudioName string
	Host       string
	Studios    []StudioFilter
}

var sites = []SiteConfig{
	{
		SiteID: "forbiddenfruitsfilms", StudioName: "Forbidden Fruits Films",
		Host:    "www.forbiddenfruitsfilms.com",
		Studios: []StudioFilter{{ID: "93785", Name: "Forbidden Fruits Films"}},
	},
	{
		SiteID: "rodneymoore", StudioName: "Rodney Moore",
		Host: "rodneymoorestore.com",
		Studios: []StudioFilter{
			{ID: "92960", Name: "Rodney Moore"},
			{ID: "94085", Name: "Rodney Moore Clips"},
		},
	},
}

type Scraper struct {
	Client *http.Client
	cfg    SiteConfig
	base   string
}

// New builds a scraper for one storefront.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		Client: httpx.NewClient(45 * time.Second),
		cfg:    cfg,
		base:   "https://" + cfg.Host,
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
	return []string{bare, bare + scenesPath, bare + scenesPath + "?studio={id}"}
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

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Cookie"] = ageCookie
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: h})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- listing ----

var (
	cardStartRe  = regexp.MustCompile(`<article class="scene-widget[^"]*"`)
	sceneIDRe    = regexp.MustCompile(`data-scene-id="(\d+)"`)
	sceneHrefRe  = regexp.MustCompile(`href="([^"]*streaming-scene-video\.html)"`)
	sceneTitleRe = regexp.MustCompile(`(?is)class="scene-title"[^>]*>\s*<h6>\s*(.*?)\s*</h6>`)
	thumbRe      = regexp.MustCompile(`(?is)<img[^>]*class="screenshot[^"]*"[^>]*\sdata-src="([^"]+)"`)
	perfRe       = regexp.MustCompile(`(?is)class="scene-performer-names"[^>]*>(.*?)</p>`)
	cardLenRe    = regexp.MustCompile(`(?is)class="scene-length"[^>]*>\s*(\d+)\s*min`)
	pagerRe      = regexp.MustCompile(`title="Go To Page (\d+)"`)
	tagStripRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe         = regexp.MustCompile(`\s+`)
)

type sceneRef struct {
	id         string
	url        string
	title      string
	thumbnail  string
	performers []string
	duration   int
	studioName string
}

func (s *Scraper) listingURL(studioID string, page int) string {
	q := url.Values{}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	q.Set("studio", studioID)
	return s.base + scenesPath + "?" + q.Encode()
}

// lastPage reads the highest page the pager names. The storefront renders a
// window of neighbours plus the final page, so the last "Go To Page N" link is
// the page count.
func lastPage(body []byte) int {
	last := 1
	for _, m := range pagerRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > last {
			last = n
		}
	}
	return min(last, maxListingPages)
}

func splitCards(page string) []string {
	starts := cardStartRe.FindAllStringIndex(page, -1)
	if len(starts) == 0 {
		return nil
	}
	blocks := make([]string, len(starts))
	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		blocks[i] = page[loc[0]:end]
	}
	return blocks
}

func parseCard(card, base, studioName string) (sceneRef, bool) {
	id := sceneIDRe.FindStringSubmatch(card)
	if id == nil {
		return sceneRef{}, false
	}
	ref := sceneRef{id: id[1], studioName: studioName}
	if m := sceneHrefRe.FindStringSubmatch(card); m != nil {
		ref.url = absURL(base, html.UnescapeString(m[1]))
	}
	if m := sceneTitleRe.FindStringSubmatch(card); m != nil {
		ref.title = cleanText(m[1])
	}
	if m := thumbRe.FindStringSubmatch(card); m != nil {
		ref.thumbnail = html.UnescapeString(m[1])
	}
	if m := perfRe.FindStringSubmatch(card); m != nil {
		ref.performers = splitNames(cleanText(m[1]))
	}
	if m := cardLenRe.FindStringSubmatch(card); m != nil {
		n, _ := strconv.Atoi(m[1])
		ref.duration = n * 60
	}
	if ref.title == "" || ref.url == "" {
		return sceneRef{}, false
	}
	return ref, true
}

// ---- detail ----

var (
	releasedRe = regexp.MustCompile(`(?is)Released:</span>\s*([^<]+)`)
	lengthRe   = regexp.MustCompile(`(?is)Length:</span>\s*(\d+)\s*min`)
	seriesRe   = regexp.MustCompile(`(?is)Series:</span>\s*<a[^>]*>\s*(.*?)\s*</a>`)
	directorRe = regexp.MustCompile(`(?is)Director:</span>\s*<a[^>]*>\s*(.*?)\s*</a>`)
	studioRe   = regexp.MustCompile(`(?is)Studio:</span>\s*<a[^>]*>\s*(.*?)\s*</a>`)
)

// applyDetail fills in what the listing card cannot carry. The storefront
// publishes no synopsis — its meta description is generated boilerplate — so no
// description is stored rather than a sentence the site made up.
func applyDetail(sc *models.Scene, body []byte) {
	if m := releasedRe.FindSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(string(m[1])),
			"Jan 2, 2006", "January 2, 2006", "2006-01-02"); err == nil {
			sc.Date = t.UTC()
		}
	}
	if sc.Duration == 0 {
		if m := lengthRe.FindSubmatch(body); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			sc.Duration = n * 60
		}
	}
	if m := seriesRe.FindSubmatch(body); m != nil {
		sc.Series = cleanText(string(m[1]))
	}
	if m := directorRe.FindSubmatch(body); m != nil {
		sc.Director = cleanText(string(m[1]))
	}
	if m := studioRe.FindSubmatch(body); m != nil {
		if name := cleanText(string(m[1])); name != "" {
			sc.Studio = name
		}
	}
}

// ---- run ----

var studioParamRe = regexp.MustCompile(`(?i)^\d+$`)

// filtersFor resolves which `?studio=` ids a run covers. A bare site URL walks
// every studio the storefront is the official site for; an explicit
// `?studio=` URL walks just that one, which is how a sub-brand is scraped on
// its own.
func (s *Scraper) filtersFor(studioURL string) []StudioFilter {
	u, err := url.Parse(studioURL)
	if err != nil {
		return s.cfg.Studios
	}
	id := u.Query().Get("studio")
	if id == "" || !studioParamRe.MatchString(id) {
		return s.cfg.Studios
	}
	for _, f := range s.cfg.Studios {
		if f.ID == id {
			return []StudioFilter{f}
		}
	}
	return []StudioFilter{{ID: id, Name: s.cfg.StudioName}}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	filters := s.filtersFor(studioURL)
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}

	// The walk is interleaved rather than discover-then-fetch: a listing page
	// here is ~1 MB and Rodney Moore has 66 of them, so scanning the whole
	// catalogue before emitting anything would cost 66 MB and delay every
	// scene behind it. Fetching each page's details as that page arrives also
	// lets the KnownIDs early-stop cut the listing walk, not just the details.
	//
	// Paginate's page number is a running index across every studio filter;
	// w tracks where in that sequence the walk currently is.
	w := &walk{filters: filters, page: 1, lastPage: 1, seen: map[string]bool{}}

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if w.filter >= len(w.filters) {
			return scraper.PageResult{Done: true}, nil
		}
		f := w.filters[w.filter]
		u := s.listingURL(f.ID, w.page)

		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("%s: studio %s page %d: %w", s.cfg.SiteID, f.Name, w.page, err)
		}
		if w.page == 1 {
			w.lastPage = lastPage(body)
			scraper.Debugf(1, "%s: studio %s has %d listing pages", s.cfg.SiteID, f.Name, w.lastPage)
		}

		var refs []sceneRef
		cards := 0
		for _, card := range splitCards(string(body)) {
			ref, ok := parseCard(card, s.base, f.Name)
			if !ok {
				continue
			}
			cards++
			// A scene can appear under two of a store's studio filters.
			if w.seen[ref.id] {
				continue
			}
			w.seen[ref.id] = true
			refs = append(refs, ref)
		}
		if cards == 0 && page == 1 {
			return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no scene cards on the first listing page"))
		}

		scenes := s.fetchChunk(ctx, refs, studioURL, workers, out)

		w.advance(cards)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  w.total(),
			Done:   w.filter >= len(w.filters),
			// A page whose every card was already seen under another filter is
			// not the end of the catalogue.
			Continue: true,
		}, nil
	})
}

// walk is the position of an interleaved listing walk across a store's studio
// filters: which filter, which page within it, and how many pages that filter
// turned out to have.
type walk struct {
	filters  []StudioFilter
	filter   int
	page     int
	lastPage int
	seen     map[string]bool
	counted  int
}

func (w *walk) advance(cards int) {
	w.counted += cards
	if w.page == 0 {
		w.page = 1
	}
	if cards == 0 || w.page >= w.lastPage {
		w.filter++
		w.page = 1
		w.lastPage = 1
		return
	}
	w.page++
}

// total estimates the catalogue size for the progress line from the current
// filter's page count. It is an estimate: later filters have not been asked
// how many pages they have yet.
func (w *walk) total() int {
	if w.page <= 1 || w.counted == 0 {
		return 0
	}
	perPage := w.counted / (w.page - 1)
	return perPage * w.lastPage
}

func (s *Scraper) fetchChunk(ctx context.Context, refs []sceneRef, studioURL string, workers int, out chan<- scraper.SceneResult) []models.Scene {
	scenes := make([]models.Scene, len(refs))
	failed := make([]error, len(refs))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(refs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				sc := refs[i].scene(studioURL, s.cfg.SiteID)
				body, err := s.get(ctx, refs[i].url)
				if err != nil {
					failed[i] = err
				} else {
					applyDetail(&sc, body)
				}
				scenes[i] = sc
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range refs {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	// A detail page that did not arrive costs the release date, not the scene:
	// the listing card already carries title, thumbnail, cast and runtime. The
	// scene is emitted with what is known and the failure is reported, so the
	// run is still marked incomplete and cannot authoritatively delete.
	for i := range refs {
		if failed[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("%s: detail %s: %w", s.cfg.SiteID, refs[i].url, failed[i])):
			case <-ctx.Done():
				return scenes
			}
		}
	}
	return scenes
}

func (r sceneRef) scene(studioURL, siteID string) models.Scene {
	return models.Scene{
		ID:         r.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     r.studioName,
		Title:      r.title,
		URL:        r.url,
		Thumbnail:  r.thumbnail,
		Performers: r.performers,
		Duration:   r.duration,
		ScrapedAt:  time.Now().UTC(),
	}
}

// ---- helpers ----

func splitNames(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func absURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(u).String()
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

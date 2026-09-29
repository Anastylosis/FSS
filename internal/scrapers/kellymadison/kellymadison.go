// Package kellymadison scrapes the Kelly Madison Media network of studios.
//
// The network splits across two distinct CMS platforms, modelled here as two
// site families:
//
//   - Fidelity CMS (pornfidelity, teenfidelity, kellymadison): a Laravel tour
//     with a server-rendered, per-site-filtered listing at
//     /episodes?site=N&page=P and schema.org VideoObject detail pages at
//     /episodes/{id}. Each branded site has its own ?site=N filter, so the
//     listing is already correctly attributed and newest-first — perfect for
//     scraper.Paginate.
//
//   - Ultra CMS (5kporn, 5kteens, 8kmilfs, 8kteens): a single shared catalogue
//     served as an AJAX JSON listing at 5kporn.com/episodes/search?sort=newest.
//     Every episode ID is prefixed with its sub-brand (5KP/5KT/8KM/8KT), so a
//     branded site is scraped by walking the shared catalogue and keeping only
//     the rows whose prefix matches the site. Because the per-site subset is
//     still globally newest-first, KnownIDs early-stop works within it; the
//     8km/8kt /episodes/search endpoint returns a server error, so all four
//     Ultra sites read the catalogue from 5kporn.com and build per-site detail
//     URLs on their own domain.
package kellymadison

import (
	"context"
	"fmt"
	"html"
	"net/http"
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

// cms identifies which of the two platforms a site runs on.
type cms int

const (
	cmsFidelity cms = iota
	cmsUltra
)

// siteConfig is one row in the network registration table.
type siteConfig struct {
	SiteID     string // stable lowercase identifier / FSS site id
	Domain     string // bare domain, e.g. "pornfidelity.com"
	StudioName string // display name
	Platform   cms
	// FidelitySite is the ?site=N filter value for Fidelity CMS sites.
	FidelitySite int
	// UltraPrefix is the episode-id prefix (e.g. "5KP") for Ultra CMS sites.
	UltraPrefix string
}

var sites = []siteConfig{
	{SiteID: "pornfidelity", Domain: "pornfidelity.com", StudioName: "Porn Fidelity", Platform: cmsFidelity, FidelitySite: 2},
	{SiteID: "teenfidelity", Domain: "teenfidelity.com", StudioName: "Teen Fidelity", Platform: cmsFidelity, FidelitySite: 3},
	{SiteID: "kellymadison", Domain: "kellymadison.com", StudioName: "Kelly Madison", Platform: cmsFidelity, FidelitySite: 1},
	{SiteID: "5kporn", Domain: "5kporn.com", StudioName: "5KPorn", Platform: cmsUltra, UltraPrefix: "5KP"},
	{SiteID: "5kteens", Domain: "5kteens.com", StudioName: "5KTeens", Platform: cmsUltra, UltraPrefix: "5KT"},
	{SiteID: "8kmilfs", Domain: "8kmilfs.com", StudioName: "8K MILFs", Platform: cmsUltra, UltraPrefix: "8KM"},
	{SiteID: "8kteens", Domain: "8kteens.com", StudioName: "8K Teens", Platform: cmsUltra, UltraPrefix: "8KT"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newFor(cfg.SiteID))
	}
}

// ultraCatalogueBase is the host whose /episodes/search endpoint serves the
// whole Ultra-CMS catalogue (the 8k* hosts return a server error for it).
const ultraCatalogueBase = "https://www.5kporn.com"

// Scraper implements scraper.StudioScraper for one Kelly Madison network site.
type Scraper struct {
	// Client is the HTTP client; exported so tests can inject httptest.
	Client *http.Client
	cfg    siteConfig
	// base overrides the site origin in tests (defaults to https://www.<domain>).
	base string
	// catalogueBase overrides the Ultra catalogue origin in tests.
	catalogueBase string

	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// newFor builds the scraper for the given site id. Panics on an unknown id
// (only called with table-driven ids from init and tests).
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return newScraper(cfg)
		}
	}
	panic("kellymadison: unknown site id " + siteID)
}

func newScraper(cfg siteConfig) *Scraper {
	escaped := regexp.QuoteMeta(cfg.Domain)
	return &Scraper{
		Client:        httpx.NewClient(30 * time.Second),
		cfg:           cfg,
		base:          "https://www." + cfg.Domain,
		catalogueBase: ultraCatalogueBase,
		matchRe:       regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$|\?)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/episodes"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	if s.cfg.Platform == cmsFidelity {
		s.runFidelity(ctx, opts, out)
		return
	}
	s.runUltra(ctx, opts, out)
}

// ---- Fidelity CMS ----

var (
	fidelityCardRe  = regexp.MustCompile(`<a href="https?://[^"]*/episodes/(\d+)"[^>]*class="video-card`)
	fidelityCardRe2 = regexp.MustCompile(`<a href="https?://[^"]*/episodes/(\d+)"[^>]*aria-label`)
)

// runFidelity walks /episodes?site=N&page=P. The listing already carries a
// VideoObject per card, but it omits the actor list, so each new scene's detail
// page is fetched (worker pool) to fill in performers and the full description.
func (s *Scraper) runFidelity(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping Fidelity CMS (site=%d)", s.cfg.SiteID, s.cfg.FidelitySite)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/episodes?site=%d", s.base, s.cfg.FidelitySite)
		if page > 1 {
			pageURL = fmt.Sprintf("%s&page=%d", pageURL, page)
		}
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseFidelityListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := s.fidelityScenes(ctx, items, opts, now)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// fidelityItem is a listing-card scene before detail enrichment.
type fidelityItem struct {
	id string
	vo *parseutil.VideoObject
}

// parseFidelityListing returns one item per VideoObject in document (newest-first)
// order, keyed by its /episodes/{id} url.
func parseFidelityListing(body []byte) []fidelityItem {
	// Determine card order from the anchor hrefs so scenes are returned
	// newest-first (matching the page), then attach each card's VideoObject.
	ids := orderedFidelityIDs(body)
	byID := make(map[string]*parseutil.VideoObject)
	vos := parseutil.ExtractVideoObjects(body)
	for i := range vos {
		vo := vos[i]
		if id := episodeIDFromURL(vo.URL); id != "" {
			byID[id] = &vo
		}
	}
	items := make([]fidelityItem, 0, len(ids))
	for _, id := range ids {
		vo := byID[id]
		if vo == nil {
			vo = &parseutil.VideoObject{}
		}
		items = append(items, fidelityItem{id: id, vo: vo})
	}
	return items
}

func orderedFidelityIDs(body []byte) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, m := range fidelityCardRe.FindAllSubmatch(body, -1) {
		id := string(m[1])
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, m := range fidelityCardRe2.FindAllSubmatch(body, -1) {
		id := string(m[1])
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

var episodeNumRe = regexp.MustCompile(`/episodes/(\d+)`)

func episodeIDFromURL(u string) string {
	if m := episodeNumRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// fidelityScenes enriches each new listing item with its detail page (worker
// pool). Known IDs become lightweight stubs so Paginate's early-stop fires
// without spending a detail fetch.
func (s *Scraper) fidelityScenes(ctx context.Context, items []fidelityItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	results := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		if opts.KnownIDs[it.id] {
			results[i] = models.Scene{ID: it.id, SiteID: s.cfg.SiteID}
			continue
		}
		wg.Add(1)
		go func(idx int, item fidelityItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			detailURL := fmt.Sprintf("%s/episodes/%s", s.base, item.id)
			body, err := s.fetchPage(ctx, detailURL)
			if err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (skipping)", s.cfg.SiteID, item.id, err)
				return
			}
			results[idx] = s.fidelityScene(item, body, detailURL, now)
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" {
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func (s *Scraper) fidelityScene(item fidelityItem, detailBody []byte, detailURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        item.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: s.base,
		URL:       detailURL,
		Studio:    s.cfg.StudioName,
		ScrapedAt: now,
	}

	// Listing VideoObject: title, description, thumbnail, date, duration.
	if vo := item.vo; vo != nil {
		scene.Title = cleanText(vo.Name)
		scene.Description = cleanText(vo.Description)
		scene.Thumbnail = vo.ThumbnailURL
		scene.Duration = parseutil.ParseDurationISO(vo.Duration)
		scene.Date = parseISODate(firstNonEmpty(vo.UploadDate, vo.DatePublished))
	}

	// Detail VideoObject: actor list + (richer) fields.
	if dvo := parseutil.ExtractVideoObject(detailBody); dvo != nil {
		if scene.Title == "" {
			scene.Title = cleanText(dvo.Name)
		}
		if scene.Duration == 0 {
			scene.Duration = parseutil.ParseDurationISO(dvo.Duration)
		}
		if scene.Date.IsZero() {
			scene.Date = parseISODate(firstNonEmpty(dvo.UploadDate, dvo.DatePublished))
		}
		scene.Performers = cleanAll(dvo.Actors)
	}

	// og:description carries the full, untruncated synopsis on the detail page.
	if og := parseutil.OpenGraph(detailBody); og != nil {
		if d := cleanText(og["og:description"]); len(d) > len(scene.Description) {
			scene.Description = d
		}
		if scene.Thumbnail == "" {
			scene.Thumbnail = og["og:image"]
		}
	}

	return scene
}

// ---- Ultra CMS ----

type ultraSearchResponse struct {
	Total int    `json:"total"`
	HTML  string `json:"html"`
}

var (
	ultraCardRe     = regexp.MustCompile(`(?s)<!-- BEGIN an episode -->.*?<!-- END an episode -->`)
	ultraCardIDRe   = regexp.MustCompile(`/episodes/([A-Z0-9]+/\d+)"`)
	ultraCardDateRe = regexp.MustCompile(`<div class="col-4">\s*([A-Za-z]{3} \d{1,2})\s*</div>`)
	ultraTitleRe    = regexp.MustCompile(`(?s)<h3 class="ep-title">\s*<a[^>]*>([^<]+)</a>`)
	ultraModelRe    = regexp.MustCompile(`<a href="[^"]*/models/[a-z0-9-]+">([^<]+)</a>`)
	ultraThumbRe    = regexp.MustCompile(`(https://[^"]*?/episodes/\d+/thumb_1/01\.jpg)`)
	ultraRuntimeRe  = regexp.MustCompile(`Episode:\s*<a[^>]*><strong>(\d+)\s*mins?`)

	// detail-page selectors
	ultraDetailSummaryRe  = regexp.MustCompile(`(?s)Episode Summary</strong>\s*:\s*(.*?)</p>`)
	ultraDetailSummary2Re = regexp.MustCompile(`(?s)<h5 class="heavy">Episode Summary</h5>\s*<p[^>]*>(.*?)</p>`)
	ultraDetailRuntimeRe  = regexp.MustCompile(`(?s)Episode:</strong>\s*([\d:]+)\s*mins`)
)

// runUltra walks the shared Ultra catalogue (5kporn.com/episodes/search),
// keeping only the rows whose episode-id prefix matches this site, then fetches
// each new scene's detail page on this site's own domain.
func (s *Scraper) runUltra(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping Ultra CMS (prefix=%s)", s.cfg.SiteID, s.cfg.UltraPrefix)

	progressSent := false
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching catalogue page %d", s.cfg.SiteID, page)

		resp, total, err := s.fetchUltraPage(ctx, page)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}
		// The catalogue page had no rows at all → end of catalogue.
		if total == 0 {
			return
		}

		if !progressSent {
			progressSent = true
			// Total is the network total; we cannot cheaply pre-count this
			// site's subset, so report nothing rather than a misleading number.
			scraper.Debugf(1, "%s: catalogue has %d total episodes", s.cfg.SiteID, total)
		}

		items := s.fetchUltraScenes(ctx, resp, opts, now)
		for _, sc := range items {
			if opts.KnownIDs[sc.ID] {
				scraper.Debugf(1, "%s: hit known ID, stopping early", s.cfg.SiteID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- scraper.Scene(sc):
			case <-ctx.Done():
				return
			}
		}
	}
}

// ultraItem is a catalogue-card scene of this site before detail enrichment.
type ultraItem struct {
	id        string // full id, e.g. "8KM/36"
	title     string
	date      time.Time
	models    []string
	thumbnail string
	duration  int // seconds
}

// fetchUltraPage fetches one catalogue page and returns the cards for THIS
// site only (matched by id prefix), plus the catalogue's reported total so the
// caller can detect the end of the catalogue (total>0 but cards empty for a
// site whose newest content sits further in).
func (s *Scraper) fetchUltraPage(ctx context.Context, page int) (items []ultraItem, total int, err error) {
	url := fmt.Sprintf("%s/episodes/search?sort=newest&page=%d", s.catalogueBase, page)
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL: url,
		Headers: mergeHeaders(httpx.BrowserHeaders(httpx.UserAgentFirefox), map[string]string{
			"X-Requested-With": "XMLHttpRequest",
		}),
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data ultraSearchResponse
	if err := httpx.DecodeJSON(resp.Body, &data); err != nil {
		return nil, 0, err
	}

	body := []byte(data.HTML)
	cards := ultraCardRe.FindAll(body, -1)
	for _, card := range cards {
		it, ok := parseUltraCard(card, s.cfg.UltraPrefix)
		if ok {
			items = append(items, it)
		}
	}
	return items, data.Total, nil
}

func parseUltraCard(card []byte, wantPrefix string) (ultraItem, bool) {
	m := ultraCardIDRe.FindSubmatch(card)
	if m == nil {
		return ultraItem{}, false
	}
	id := string(m[1])
	if !strings.HasPrefix(id, wantPrefix+"/") {
		return ultraItem{}, false
	}

	it := ultraItem{id: id}

	if mt := ultraTitleRe.FindSubmatch(card); mt != nil {
		it.title = cleanText(string(mt[1]))
	}
	if md := ultraCardDateRe.FindSubmatch(card); md != nil {
		it.date = parseUltraDate(string(md[1]))
	}
	if mth := ultraThumbRe.FindSubmatch(card); mth != nil {
		it.thumbnail = string(mth[1])
	}
	if mr := ultraRuntimeRe.FindSubmatch(card); mr != nil {
		if n, err := strconv.Atoi(string(mr[1])); err == nil {
			it.duration = n * 60
		}
	}
	seen := make(map[string]bool)
	for _, mm := range ultraModelRe.FindAllSubmatch(card, -1) {
		name := cleanText(string(mm[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			it.models = append(it.models, name)
		}
	}
	return it, true
}

// fetchUltraScenes enriches each card item with its detail page (worker pool),
// preserving order so KnownIDs early-stop works. Known IDs become stubs.
func (s *Scraper) fetchUltraScenes(ctx context.Context, items []ultraItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	results := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		if opts.KnownIDs[it.id] {
			results[i] = models.Scene{ID: it.id, SiteID: s.cfg.SiteID}
			continue
		}
		wg.Add(1)
		go func(idx int, item ultraItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			detailURL := fmt.Sprintf("%s/episodes/%s", s.base, item.id)
			body, err := s.fetchPage(ctx, detailURL)
			if err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (using listing only)", s.cfg.SiteID, item.id, err)
				results[idx] = s.ultraScene(item, nil, detailURL, now)
				return
			}
			results[idx] = s.ultraScene(item, body, detailURL, now)
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" {
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func (s *Scraper) ultraScene(item ultraItem, detailBody []byte, detailURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         item.id,
		SiteID:     s.cfg.SiteID,
		StudioURL:  s.base,
		Title:      item.title,
		URL:        detailURL,
		Date:       item.date,
		Thumbnail:  item.thumbnail,
		Duration:   item.duration,
		Performers: item.models,
		Studio:     s.cfg.StudioName,
		ScrapedAt:  now,
	}

	if len(detailBody) > 0 {
		if d := parseUltraSummary(detailBody); d != "" {
			scene.Description = d
		}
		if scene.Duration == 0 {
			if m := ultraDetailRuntimeRe.FindSubmatch(detailBody); m != nil {
				scene.Duration = parseutil.ParseDurationColon(string(m[1]))
			}
		}
		// Prefer the detail page's models if the card had none.
		if len(scene.Performers) == 0 {
			seen := make(map[string]bool)
			for _, mm := range ultraModelRe.FindAllSubmatch(detailBody, -1) {
				name := cleanText(string(mm[1]))
				if name != "" && !seen[name] {
					seen[name] = true
					scene.Performers = append(scene.Performers, name)
				}
			}
		}
		if og := parseutil.OpenGraph(detailBody); og != nil {
			if scene.Thumbnail == "" {
				scene.Thumbnail = og["og:image"]
			}
			if scene.Title == "" {
				scene.Title = cleanText(og["og:title"])
			}
		}
	}

	return scene
}

func parseUltraSummary(body []byte) string {
	if m := ultraDetailSummary2Re.FindSubmatch(body); m != nil {
		return stripTags(string(m[1]))
	}
	if m := ultraDetailSummaryRe.FindSubmatch(body); m != nil {
		return stripTags(string(m[1]))
	}
	return ""
}

// ---- shared helpers ----

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

func mergeHeaders(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	return cleanText(tagRe.ReplaceAllString(s, ""))
}

func cleanText(s string) string {
	return strings.TrimSpace(html.UnescapeString(strings.TrimSpace(s)))
}

func cleanAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if c := cleanText(s); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseISODate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := parseutil.TryParseDate(s, time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02"); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// parseUltraDate parses a year-less "Jun 17" listing date. The catalogue is
// newest-first, so the date belongs to the most recent year that is not in the
// future: try the current year, and if that lands more than a day ahead of now,
// fall back to the previous year.
func parseUltraDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	now := time.Now().UTC()
	for _, yr := range []int{now.Year(), now.Year() - 1} {
		if t, err := time.Parse("Jan 2 2006", fmt.Sprintf("%s %d", s, yr)); err == nil {
			if t.After(now.AddDate(0, 0, 1)) {
				continue
			}
			return t.UTC()
		}
	}
	return time.Time{}
}

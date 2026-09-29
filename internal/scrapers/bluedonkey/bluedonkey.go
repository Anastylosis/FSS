// Package bluedonkey scrapes the Blue Donkey Media network of Dutch/Belgian
// amateur studios. It is an authorized open-source metadata scraper that reads
// only public listing and detail pages.
//
// The network runs two different CMSes, so the package registers two kinds of
// scraper from one table-driven config:
//
//   - Kim Holland (kimholland.com) — a custom video CMS. The listing lives at
//     /videos with ?page=N pagination, cards link to /video/{id} (the numeric
//     id is the scene ID), and each detail page carries the scene title,
//     description and a high-resolution poster in a `front-newest-item-info`
//     block (no public date/duration/performers/tags — those are behind login).
//
//   - The Sysero/Nuxt CMS — Meiden van Holland (meidenvanholland.nl), Vurig
//     Vlaanderen (vurigvlaanderen.be) and Secret Circle (secretcircle.com).
//     The listing is server-rendered at /{sexfilms|seksfilms} as `video_item`
//     cards (slug + poster, plus title/duration on the richer sites). Detail
//     pages on Meiden van Holland and Vurig Vlaanderen expose a schema.org
//     VideoObject plus /modellen/ (performers) and /genres/ (tags) links;
//     Secret Circle's detail is minimal so those sites fall back to the
//     listing card. The slug is used as the scene ID.
//
// Two network sites are intentionally not covered: ashleymore.nl redirects to
// the third-party f2f.com platform, and elisevanvlaanderen.com is a Wix site
// with no scrapeable video listing.
package bluedonkey

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

// cmsKind selects the listing/detail parsing strategy for a site.
type cmsKind int

const (
	cmsKimHolland cmsKind = iota // kimholland.com custom CMS
	cmsSysero                    // Sysero/Nuxt CMS (Meiden van Holland et al.)
)

// SiteConfig describes one Blue Donkey Media site served by this package.
type SiteConfig struct {
	SiteID     string  // stable lowercase id, e.g. "kimholland"
	Domain     string  // bare domain, e.g. "kimholland.com"
	StudioName string  // display name, e.g. "Kim Holland"
	CMS        cmsKind // which CMS the site runs
	// ListPath is the listing path for the Sysero CMS (e.g. "/sexfilms" or
	// "/seksfilms"). Unused for the Kim Holland CMS.
	ListPath string
}

var sites = []SiteConfig{
	{SiteID: "kimholland", Domain: "kimholland.com", StudioName: "Kim Holland", CMS: cmsKimHolland},
	{SiteID: "meidenvanholland", Domain: "meidenvanholland.nl", StudioName: "Meiden van Holland", CMS: cmsSysero, ListPath: "/sexfilms"},
	{SiteID: "vurigvlaanderen", Domain: "vurigvlaanderen.be", StudioName: "Vurig Vlaanderen", CMS: cmsSysero, ListPath: "/sexfilms"},
	{SiteID: "secretcircle", Domain: "secretcircle.com", StudioName: "Secret Circle", CMS: cmsSysero, ListPath: "/seksfilms"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

// newFor builds the registered scraper for a given site id. Used by tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for a single Blue Donkey site.
type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New constructs a Scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	escaped := regexp.QuoteMeta(cfg.Domain)
	base := "https://www." + cfg.Domain
	// vurigvlaanderen.be and meidenvanholland.nl serve from the bare host.
	if cfg.CMS == cmsSysero {
		base = "https://" + cfg.Domain
	}
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    base,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	if s.cfg.CMS == cmsKimHolland {
		return []string{
			s.cfg.Domain + "/videos",
			s.cfg.Domain + "/video/{id}",
		}
	}
	return []string{
		s.cfg.Domain + s.cfg.ListPath,
		s.cfg.Domain + s.cfg.ListPath + "/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, _ string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	if s.cfg.CMS == cmsKimHolland {
		s.runKimHolland(ctx, opts, out)
		return
	}
	s.runSysero(ctx, opts, out)
}

// listItem is a parsed listing card before detail enrichment.
type listItem struct {
	id         string
	url        string
	title      string
	thumbnail  string
	duration   int
	performers []string
	tags       []string
	desc       string
	date       time.Time
}

func (s *Scraper) toScene(it listItem, now time.Time) models.Scene {
	return models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Studio:      s.cfg.StudioName,
		Title:       it.title,
		URL:         it.url,
		Thumbnail:   it.thumbnail,
		Description: it.desc,
		Duration:    it.duration,
		Performers:  it.performers,
		Tags:        it.tags,
		Date:        it.date,
		ScrapedAt:   now,
	}
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

// fetchDetails enriches each listing item from its detail page using a worker
// pool, preserving order so KnownIDs early-stop works. Failed fetches keep the
// listing data already collected.
func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, enrich func(body []byte, it *listItem)) {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range items {
		if ctx.Err() != nil {
			break
		}
		if opts.KnownIDs[items[i].id] {
			continue // skip detail fetch; Paginate will stop on this ID
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetchPage(ctx, items[idx].url)
			if err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (keeping listing data)", s.cfg.SiteID, items[idx].id, err)
				return
			}
			enrich(body, &items[idx])
		}(i)
	}
	wg.Wait()
}

func cleanText(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}

// deslug turns a URL slug ("daphne-laat") into a display name ("Daphne Laat").
func deslug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// ---- Kim Holland CMS ----

var (
	khCardRe  = regexp.MustCompile(`(?s)<div class="archive-scene">.*?</a>`)
	khLinkRe  = regexp.MustCompile(`<a href="/video/(\d+)"`)
	khThumbRe = regexp.MustCompile(`<img[^>]*class="tile__img"[^>]*src="([^"]+)"[^>]*alt="([^"]*)"`)
	khTitleRe = regexp.MustCompile(`(?s)<div class="tile__title">\s*(.*?)\s*</div>`)

	khDetailTitleRe = regexp.MustCompile(`<span class="front-newest-item-info-title">\s*(.*?)\s*</span>`)
	khDetailDescRe  = regexp.MustCompile(`(?s)<p[^>]*>(.*?)</p>`)
	khDetailThumbRe = regexp.MustCompile(`<img[^>]*src="(/images/\d+/1920x1080\.jpg)"`)
)

func (s *Scraper) runKimHolland(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/videos"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/videos?page=%d", s.base, page)
		}
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := s.parseKHListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}
		s.fetchDetails(ctx, items, opts, parseKHDetail)
		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			scenes = append(scenes, s.toScene(it, now))
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) parseKHListing(body []byte) []listItem {
	cards := khCardRe.FindAll(body, -1)
	items := make([]listItem, 0, len(cards))
	seen := make(map[string]bool)
	for _, card := range cards {
		m := khLinkRe.FindSubmatch(card)
		if m == nil {
			continue
		}
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{id: id, url: s.base + "/video/" + id}
		if mt := khTitleRe.FindSubmatch(card); mt != nil {
			it.title = cleanText(string(mt[1]))
		}
		if mh := khThumbRe.FindSubmatch(card); mh != nil {
			it.thumbnail = s.absURL(string(mh[1]))
			if it.title == "" {
				it.title = cleanText(string(mh[2]))
			}
		}
		items = append(items, it)
	}
	return items
}

func parseKHDetail(body []byte, it *listItem) {
	if m := khDetailTitleRe.FindSubmatch(body); m != nil {
		if t := cleanText(string(m[1])); t != "" {
			it.title = t
		}
	}
	if m := khDetailDescRe.FindSubmatch(body); m != nil {
		it.desc = cleanText(string(m[1]))
	}
	if m := khDetailThumbRe.FindSubmatch(body); m != nil {
		it.thumbnail = string(m[1]) // higher-res poster, made absolute below
	}
}

// absURL resolves a site-relative path against the scraper base.
func (s *Scraper) absURL(p string) string {
	if p == "" || strings.HasPrefix(p, "http") {
		return p
	}
	return s.base + p
}

// ---- Sysero/Nuxt CMS ----

var (
	syseroCardRe  = regexp.MustCompile(`(?s)<article[^>]*\bvideo_item\b[^>]*>.*?</article>`)
	syseroLinkRe  = regexp.MustCompile(`href="(/[a-z]+films/[a-z0-9-]+)"`)
	syseroIDRe    = regexp.MustCompile(`line_(\d+)`)
	syseroThumbRe = regexp.MustCompile(`<img[^>]*(?:data-src|src)="(https://[^"]+)"`)
	syseroDurRe   = regexp.MustCompile(`<span class="duration">\s*([^<]*?)\s*</span>`)
	syseroH3Re    = regexp.MustCompile(`(?s)<h3 class="title">\s*(.*?)\s*</h3>`)
	// posterTitleRe pulls the scene title from the poster img title attribute,
	// e.g. title="Poster van de seksfilm: De masseuse en Lucas".
	posterTitleRe = regexp.MustCompile(`<img[^>]*\btitle="Poster van de [a-z]+film:\s*([^"]+)"`)

	syseroModelRe = regexp.MustCompile(`href="/modellen/([a-z0-9-]+)"`)
	syseroGenreRe = regexp.MustCompile(`href="/genres/([a-z0-9-]+)"`)
	// durMinRe matches the "42 min." listing duration.
	durMinRe = regexp.MustCompile(`(\d+)\s*min`)
)

func (s *Scraper) runSysero(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()
	// The Sysero listing is server-rendered as a single newest-first batch;
	// further pages load via opaque infinite-scroll XHR. We scrape the public
	// SSR batch, so the loop fetches one page and is done.
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		if page > 1 {
			return scraper.PageResult{}, nil
		}
		body, err := s.fetchPage(ctx, s.base+s.cfg.ListPath)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := s.parseSyseroListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}
		s.fetchDetails(ctx, items, opts, parseSyseroDetail)
		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			scenes = append(scenes, s.toScene(it, now))
		}
		return scraper.PageResult{Scenes: scenes, Total: len(items), Done: true}, nil
	})
}

func (s *Scraper) parseSyseroListing(body []byte) []listItem {
	cards := syseroCardRe.FindAll(body, -1)
	items := make([]listItem, 0, len(cards))
	seen := make(map[string]bool)
	for _, card := range cards {
		ml := syseroLinkRe.FindSubmatch(card)
		if ml == nil {
			continue
		}
		path := string(ml[1])
		slug := path[strings.LastIndex(path, "/")+1:]
		// Prefer the numeric line_N id when present (mvh/vv); fall back to the
		// slug (Secret Circle cards have no numeric id).
		id := slug
		if mi := syseroIDRe.FindSubmatch(card); mi != nil {
			id = string(mi[1])
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{id: id, url: s.base + path}
		if mh := syseroThumbRe.FindSubmatch(card); mh != nil {
			it.thumbnail = cleanText(string(mh[1]))
		}
		if mt := syseroH3Re.FindSubmatch(card); mt != nil {
			it.title = cleanText(string(mt[1]))
		}
		if it.title == "" {
			if mp := posterTitleRe.FindSubmatch(card); mp != nil {
				it.title = cleanText(string(mp[1]))
			}
		}
		if it.title == "" {
			it.title = deslug(slug)
		}
		if md := syseroDurRe.FindSubmatch(card); md != nil {
			if mm := durMinRe.FindSubmatch(md[1]); mm != nil {
				if n := atoiSafe(string(mm[1])); n > 0 {
					it.duration = n * 60
				}
			}
		}
		items = append(items, it)
	}
	return items
}

func parseSyseroDetail(body []byte, it *listItem) {
	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		if n := cleanText(vo.Name); n != "" {
			it.title = n
		}
		if d := cleanText(vo.Description); d != "" {
			it.desc = d
		}
		if t := cleanText(vo.ThumbnailURL); t != "" {
			it.thumbnail = t
		}
		if t, err := parseutil.TryParseDate(strings.TrimSpace(vo.UploadDate),
			time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02", "2006-01-02 15:04:05"); err == nil {
			it.date = t.UTC()
		}
	}
	it.performers = dedupSlugNames(syseroModelRe.FindAllSubmatch(body, -1))
	it.tags = dedupSlugNames(syseroGenreRe.FindAllSubmatch(body, -1))
}

// dedupSlugNames converts /modellen/{slug} or /genres/{slug} matches into
// de-duplicated display names derived from the slug (the visible anchor text on
// these sites is marketing copy, so the slug is the clean source).
func dedupSlugNames(matches [][][]byte) []string {
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, m := range matches {
		slug := string(m[1])
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, deslug(slug))
	}
	return out
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Package helixstudios scrapes the Helix Studios gay-twink network, which runs
// on the AdultEmpire / Ravalla "EWC" CMS. It is a table-driven package: one
// scraper is registered per site in init().
//
// Sites covered:
//   - helixstudios   — the main network (helixstudios.net / .com), full catalogue
//   - 8teenboy       — sub-brand listing at /8teenboy-porn-videos.html
//   - spankthis      — sub-brand listing at /spankthis-porn-videos.html
//
// The standalone domains 8teenboy.com and spankthis.com both redirect to a
// brand listing page on helixstudios.com, so they are scraped there rather than
// as separate CMS instances.
//
// Age gate: helixstudios.net unconditionally 301-redirects to
// helixstudios.com/AgeConfirmation unless the request carries the
// `ageConfirmed=true` cookie. The content host is helixstudios.com; every
// request sends the cookie. The main scraper also recognises the per-channel
// series filters (Helix Europe / Helix Latin America / Latin Studs), which the
// site exposes as `?series={id}` views of the main listing endpoint, and the
// `/videos/studios/{id}/{slug}` URLs that redirect to them.
package helixstudios

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

// contentHost is the host that actually serves content. helixstudios.net
// redirects here, and the brand domains (8teenboy.com, spankthis.com) do too.
const contentHost = "https://www.helixstudios.com"

// SiteConfig describes one listing entry point served by this package.
//
// ListPath is the path of the listing page; "%d" placeholders are not used —
// pagination is appended as `?page=N` (or `&page=N` when the path already has a
// query string). The main site's per-channel series filters are handled as URL
// modes on the main scraper, not as separate SiteConfig rows.
type SiteConfig struct {
	SiteID     string   // stable lowercase id, e.g. "helixstudios"
	Domains    []string // domains whose URLs this scraper matches
	StudioName string   // display name, e.g. "Helix Studios"
	ListPath   string   // listing page path on contentHost
}

var sites = []SiteConfig{
	{
		SiteID:     "helixstudios",
		Domains:    []string{"helixstudios.net", "helixstudios.com"},
		StudioName: "Helix Studios",
		ListPath:   "/watch-newest-helix-studios-clips-and-scenes.html",
	},
	{
		SiteID:     "8teenboy",
		Domains:    []string{"8teenboy.com"},
		StudioName: "8teenBoy",
		ListPath:   "/8teenboy-porn-videos.html",
	},
	{
		SiteID:     "spankthis",
		Domains:    []string{"spankthis.com"},
		StudioName: "Spank This",
		ListPath:   "/spankthis-porn-videos.html",
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newFor(cfg.SiteID))
	}
}

// newFor builds the registered scraper for a given site id. It is also used by
// the integration tests.
func newFor(siteID string) *Scraper {
	for _, cfg := range sites {
		if cfg.SiteID == siteID {
			return New(cfg)
		}
	}
	return nil
}

// Scraper implements scraper.StudioScraper for one Helix-network listing.
type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	base    string // content host, overridable in tests
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

// New constructs a Scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	escaped := make([]string, len(cfg.Domains))
	for i, d := range cfg.Domains {
		escaped[i] = regexp.QuoteMeta(d)
	}
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    contentHost,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?(?:` + strings.Join(escaped, "|") + `)(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	switch s.cfg.SiteID {
	case "helixstudios":
		return []string{
			"helixstudios.net",
			"helixstudios.net/watch-newest-helix-studios-clips-and-scenes.html",
			"helixstudios.net/watch-newest-helix-studios-clips-and-scenes.html?series={id}",
			"helixstudios.net/videos/studios/{id}/{slug} (channel: Helix Europe, Latin America, Latin Studs)",
		}
	default:
		return []string{s.cfg.Domains[0], "helixstudios.com" + s.cfg.ListPath}
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// channelStudioRe matches the /videos/studios/{id}/{slug} channel URLs that the
// site 301-redirects to ?series={id} listing filters.
var channelStudioRe = regexp.MustCompile(`/videos/studios/\d+/([a-z0-9-]+)`)

// resolveListing returns the listing path (with any query string) and the
// studio display name for the given input URL. For the main scraper it honours
// `?series={id}` filters and `/videos/studios/{id}/{slug}` channel URLs.
func (s *Scraper) resolveListing(studioURL string) (listPath, studioName string) {
	listPath = s.cfg.ListPath
	studioName = s.cfg.StudioName
	if s.cfg.SiteID != "helixstudios" {
		return listPath, studioName
	}

	u, err := url.Parse(studioURL)
	if err != nil {
		return listPath, studioName
	}

	if m := channelStudioRe.FindStringSubmatch(u.Path); m != nil {
		scraper.Debugf(1, "helixstudios: channel URL %q (treating as listing root)", m[1])
		return listPath, channelStudioName(m[1])
	}

	if series := u.Query().Get("series"); series != "" {
		scraper.Debugf(1, "helixstudios: series filter %q", series)
		return listPath + "?series=" + url.QueryEscape(series), seriesStudioName(series)
	}
	return listPath, studioName
}

// channelStudioName maps a channel slug to a display name.
func channelStudioName(slug string) string {
	switch slug {
	case "helix-europe":
		return "Helix Europe"
	case "helix-latin-america":
		return "Helix Latin America"
	case "latinstuds", "latin-studs":
		return "Latin Studs"
	default:
		return "Helix Studios"
	}
}

// seriesStudioName maps the known per-channel series ids to a display name.
func seriesStudioName(series string) string {
	switch series {
	case "62682":
		return "Helix Europe"
	case "62683":
		return "Helix Latin America"
	case "62483":
		return "Latin Studs"
	default:
		return "Helix Studios"
	}
}

var (
	// cardRe isolates each listing card. Cards open with a scene-widget article.
	cardRe = regexp.MustCompile(`(?s)<article class="scene-widget[^"]*"\s+data-scene-id="(\d+)".*?</article>`)
	// cardLinkRe pulls the detail href from a card.
	cardLinkRe = regexp.MustCompile(`href="(/\d+/[^"]+streaming-scene-video\.html)"`)
	// cardTitleRe pulls the title from the scene-title <h6>.
	cardTitleRe = regexp.MustCompile(`(?s)<a class="scene-title"[^>]*>.*?<h6>\s*(.*?)\s*</h6>`)
	// cardThumbRe pulls the lazy-loaded thumbnail.
	cardThumbRe = regexp.MustCompile(`data-src="([^"]+)"`)
	// cardPerfBlockRe / cardLenRe pull the secondary card details.
	cardPerfBlockRe = regexp.MustCompile(`(?s)<p class="scene-performer-names">(.*?)</p>`)
	cardLenRe       = regexp.MustCompile(`(?s)<p class="scene-length">\s*(\d+)\s*min`)

	// pageNumRe finds pagination page numbers for a total estimate.
	pageNumRe = regexp.MustCompile(`[?&]page=(\d+)`)

	// detailTitleRe pulls the page title from the video-title block.
	detailTitleRe = regexp.MustCompile(`(?s)<div class="container-fluid video-title">\s*<h1[^>]*>\s*(.*?)\s*</h1>`)
	detailDescRe  = regexp.MustCompile(`(?i)<meta\s+name="Description"\s+content="([^"]*)"`)
	detailOGImgRe = regexp.MustCompile(`(?i)<meta\s+property="og:image"\s+content="([^"]*)"`)
	// detailReleaseRe / detailLenRe parse the release-date <div> lines.
	detailReleaseRe = regexp.MustCompile(`(?s)<div class="release-date"><span[^>]*>Released:</span>\s*(.*?)\s*</div>`)
	detailLenRe     = regexp.MustCompile(`(?s)<div class="release-date"><span[^>]*>Length:</span>\s*(\d+)\s*min`)
	// detailPerfRe pulls performer names from the headshot blocks.
	detailPerfRe = regexp.MustCompile(`(?s)<a href="/scenes/\d+/[^"]+streaming-pornstar-videos\.html"[^>]*data-Label="Performer">.*?<div class="performer-name">\s*(.*?)\s*</div>`)
	// detailTagBlockRe / detailTagRe pull the scene tags scoped to the tags div.
	detailTagBlockRe = regexp.MustCompile(`(?s)<div class="tags">.*?</div>`)
	detailTagRe      = regexp.MustCompile(`data-Label="Tag"\s*>([^<]+)</a>`)
)

type listItem struct {
	id         string
	url        string
	title      string
	thumbnail  string
	duration   int
	performers []string
}

type detailData struct {
	title       string
	description string
	thumbnail   string
	duration    int
	date        time.Time
	performers  []string
	tags        []string
}

func parseListing(body []byte) []listItem {
	cards := cardRe.FindAllSubmatch(body, -1)
	items := make([]listItem, 0, len(cards))
	seen := make(map[string]bool)
	for _, c := range cards {
		it, ok := parseCard(c[1], c[0])
		if !ok || seen[it.id] {
			continue
		}
		seen[it.id] = true
		items = append(items, it)
	}
	return items
}

func parseCard(id, card []byte) (listItem, bool) {
	m := cardLinkRe.FindSubmatch(card)
	if m == nil {
		return listItem{}, false
	}
	it := listItem{
		id:  string(id),
		url: string(m[1]),
	}
	if mt := cardTitleRe.FindSubmatch(card); mt != nil {
		it.title = cleanText(string(mt[1]))
	}
	if mTh := cardThumbRe.FindSubmatch(card); mTh != nil {
		it.thumbnail = string(mTh[1])
	}
	if ml := cardLenRe.FindSubmatch(card); ml != nil {
		if n, err := strconv.Atoi(string(ml[1])); err == nil {
			it.duration = n * 60
		}
	}
	if mp := cardPerfBlockRe.FindSubmatch(card); mp != nil {
		it.performers = splitPerformers(string(mp[1]))
	}
	return it, true
}

// splitPerformers parses the comma-separated performer-name block on cards.
func splitPerformers(block string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(block, ",") {
		name := cleanText(part)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func parseDetail(body []byte) detailData {
	var d detailData
	if m := detailTitleRe.FindSubmatch(body); m != nil {
		d.title = cleanText(string(m[1]))
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		d.description = cleanText(string(m[1]))
	}
	if m := detailOGImgRe.FindSubmatch(body); m != nil {
		d.thumbnail = cleanText(string(m[1]))
	}
	if m := detailReleaseRe.FindSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(cleanText(string(m[1])), "Jan 2, 2006"); err == nil {
			d.date = t.UTC()
		}
	}
	if m := detailLenRe.FindSubmatch(body); m != nil {
		if n, err := strconv.Atoi(string(m[1])); err == nil {
			d.duration = n * 60
		}
	}
	seen := make(map[string]bool)
	for _, m := range detailPerfRe.FindAllSubmatch(body, -1) {
		name := cleanText(string(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			d.performers = append(d.performers, name)
		}
	}
	if blk := detailTagBlockRe.Find(body); blk != nil {
		tagSeen := make(map[string]bool)
		for _, m := range detailTagRe.FindAllSubmatch(blk, -1) {
			tag := cleanText(string(m[1]))
			if tag != "" && !tagSeen[tag] {
				tagSeen[tag] = true
				d.tags = append(d.tags, tag)
			}
		}
	}
	return d
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	listPath, studioName := s.resolveListing(studioURL)
	listURL := s.base + listPath
	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping listing %s", s.cfg.SiteID, listURL)

	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := listURL
		if page > 1 {
			sep := "?"
			if strings.Contains(listURL, "?") {
				sep = "&"
			}
			pageURL = fmt.Sprintf("%s%spage=%d", listURL, sep, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if firstPage {
			firstPage = false
			total = maxPageNum(body) * len(items)
		}

		scenes := s.fetchDetails(ctx, items, studioName, opts, now)
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// fetchDetails enriches each listing item from its detail page with a worker
// pool. Order is preserved so Paginate's KnownIDs early-stop fires on the right
// scene; known IDs become lightweight stubs (no detail fetch).
func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, studioName string, opts scraper.ListOpts, now time.Time) []models.Scene {
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
		go func(idx int, item listItem) {
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

			var d detailData
			if body, err := s.fetchPage(ctx, s.base+item.url); err != nil {
				scraper.Debugf(1, "%s: detail %s failed: %v (using card data)", s.cfg.SiteID, item.id, err)
			} else {
				d = parseDetail(body)
			}
			results[idx] = s.toScene(item, d, studioName, now)
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

func (s *Scraper) toScene(it listItem, d detailData, studioName string, now time.Time) models.Scene {
	title := it.title
	if d.title != "" {
		title = d.title
	}
	thumbnail := it.thumbnail
	if d.thumbnail != "" {
		thumbnail = d.thumbnail
	}
	duration := it.duration
	if d.duration > 0 {
		duration = d.duration
	}
	performers := it.performers
	if len(d.performers) > 0 {
		performers = d.performers
	}
	return models.Scene{
		ID:          it.id,
		SiteID:      s.cfg.SiteID,
		StudioURL:   s.base,
		Title:       title,
		URL:         s.base + it.url,
		Studio:      studioName,
		Description: d.description,
		Thumbnail:   thumbnail,
		Date:        d.date,
		Duration:    duration,
		Performers:  performers,
		Tags:        d.tags,
		ScrapedAt:   now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	// Age gate: without this cookie helixstudios.net 301s to the .com age wall.
	headers["Cookie"] = "ageConfirmed=true"
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: headers,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func cleanText(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}

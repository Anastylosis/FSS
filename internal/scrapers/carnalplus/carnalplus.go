// Package carnalplus scrapes the Carnal+ all-access portal and every
// sister site in its network. The network has three distinct CMSes that
// the package handles via a single table of `SiteConfig` rows dispatched
// to one of three parsers:
//
//   - VariantNATS: 13 standalone sister domains running the modern
//     `data-setid + sceneTourBase + videoBlock` NATS layout
//     (americanmusclehunks, bangbangboys, boyforsale, catholicboys,
//     funsizeboys, gaycest, jalifstudio, masonicboys, scoutboys, staghomme,
//     teensandtwinks, twinktop, twinks). Listing URL:
//     `/categories/movies_{N}_d.html`. 12 cards/page.
//
//   - VariantGrid: the Carnal+ portal layout used by the parent
//     carnalplus.com and the two sub-brands that live under it as paths
//     (`/baptistboys/`, `/carnaloriginals/`). Outer cards are
//     `<div class="grid-item-eight">` and pagination is `?page={N}`. The
//     parent already aggregates scenes from every sub-site, each card
//     carrying `<a href="{slug}/"><img …minilogo/{slug}.png></a>` which
//     identifies the source brand — that drives `Scene.Series`.
//
//   - jockpack.com portal (VariantGrid with ListPath): `/videos?site=&page=N`
//     with `grid-vid-item` cards. Hosts BetaBetaPi, StudLoads and Jason
//     Sparks Live, which have no domain of their own, so they are rows with
//     a SiteFilter.
//
//   - VariantWordPress: growlboys.com is the lone WordPress site in the
//     network. We pull `/wp-json/wp/v2/posts?per_page=100&_embed=1` and
//     map the standard REST fields onto models.Scene, same as the
//     romeromultimedia package.
//
// Detail pages are public on every NATS sister site (and on growlboys),
// so we lift the real scene URL straight from each card's `<a href>`
// instead of synthesising an anchor. The parent carnalplus.com aggregates
// each sub-site's content; users who want only one sub-brand's full
// catalogue should feed the sister domain directly.
package carnalplus

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const studioName = "Carnal+"

// Variant selects which parser + pagination URL form to use for a site.
type Variant int

const (
	VariantNATS Variant = iota
	VariantGrid
	VariantWordPress
)

// SiteConfig describes one Carnal+ network site.
type SiteConfig struct {
	ID       string
	SiteBase string // no trailing slash
	SiteName string // human-readable label → Scene.Series default
	Variant  Variant
	// SubPath is non-empty only for grid-item sub-brands hosted on the
	// parent at `/baptistboys/` etc. Pagination then targets
	// `{SiteBase}{SubPath}?page={N}` instead of `?page={N}` at the root.
	SubPath string
	// ListPath/SiteFilter select the jockpack.com listing: `{ListPath}?site={SiteFilter}&page=N`.
	// With an empty SiteFilter the `site` query of the URL passed in is used.
	ListPath   string
	SiteFilter string
	Patterns   []string
	MatchRe    *regexp.Regexp
	// ExcludeRe carves URLs out of MatchRe that a sibling row owns.
	ExcludeRe *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string         { return s.cfg.ID }
func (s *Scraper) Patterns() []string { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool {
	if s.cfg.ExcludeRe != nil && s.cfg.ExcludeRe.MatchString(u) {
		return false
	}
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- Variant NATS regexes ----

var (
	// `<div class="col-… sceneTourBase" data-setid="{id}">`
	natsCardStartRe = regexp.MustCompile(`<div class="[^"]*sceneTourBase" data-setid="(\d+)"`)
	// `<a href="https://{site}/videos/{slug}.html" class="control_thumb">`
	natsDetailURLRe = regexp.MustCompile(`<a[^>]+href="(https?://[^"]+/videos/[^"]+\.html)"[^>]*class="control_thumb"`)
	// Title from the `<h4>` inside updateDetails. The card has *two* <h4>s
	// (siteName + title); we want the one inside updateDetails. Anchoring
	// on `updateDetails` first guarantees that.
	natsTitleRe = regexp.MustCompile(`(?s)<div class="updateDetails">\s*<h4>\s*([^<]+?)\s*</h4>`)
	// Some brands nest `<span class="sceneTitle">` inside the h4 beside the series
	// and chapter, which natsTitleRe cannot match.
	natsH4Re         = regexp.MustCompile(`(?s)<div class="updateDetails">\s*<h4>(.*?)</h4>`)
	natsSceneTitleRe = regexp.MustCompile(`<span class="sceneTitle">\s*([^<]+?)\s*</span>`)
	// Performers — `<span><div>Starring Name1 and Name2</div></span>`
	natsPerformerRe = regexp.MustCompile(`(?s)<div class="updateDetails">.*?Starring\s+([^<]+?)\s*</div>`)
	// Thumbnail — the `<video … poster="…">` attribute. The webp variant is
	// served first; we accept any extension.
	natsPosterRe = regexp.MustCompile(`<video[^>]+poster="([^"]+)"`)
	// Preview MP4 — `<source src="…">` inside the video element.
	natsSourceRe = regexp.MustCompile(`<source[^>]+src="([^"]+\.mp4[^"]*)"`)
	// Pagination — highest `movies_{N}_d.html` reference.
	natsPageLinkRe = regexp.MustCompile(`movies_(\d+)_d\.html`)
)

// ---- Variant Grid (carnalplus.com portal) regexes ----

var (
	gridCardStartRe = regexp.MustCompile(`<div class="grid-(?:item-eight|vid-item)[^"]*">`)
	// ID is encoded in the thumbnail filename: `contentthumbs/{A}/{B}/{id}-1x.jpg`.
	gridIDRe = regexp.MustCompile(`contentthumbs/\d+/\d+/(\d+)-1x`)
	// Detail URL — `<a href="https://carnalplus.com/videos/{slug}.html" class="control_thumb">`.
	gridDetailURLRe = regexp.MustCompile(`<a[^>]+href="(https?://[^"]+/videos/[^"]+\.html)"[^>]+class="control_thumb"`)
	// Title — `<span class='update-title'>{title}</span>` (single-quoted attrs).
	gridTitleRe = regexp.MustCompile(`<span class='update-title'>\s*([^<]+?)\s*</span>`)
	// Series — `<span class='update-series'>{series}</span>`.
	gridSeriesRe = regexp.MustCompile(`<span class='update-series'>\s*([^<]+?)\s*</span>`)
	// Full-movie cards use neither span and put the name straight in the
	// heading, so without this fallback they arrive with an empty Title.
	gridHeadingRe  = regexp.MustCompile(`(?s)<h4 class="[^"]*titleBlockF[^"]*">(.*?)</h4>`)
	gridTagStripRe = regexp.MustCompile(`<[^>]*>`)
	// Source sub-site — `<a href="{slug}/"><img … alt="{slug} minilogo" …></a>`.
	// The href is relative (`scoutboys/`), the alt has the slug. We use alt
	// because it's a deterministic single field.
	gridSubsiteRe = regexp.MustCompile(`<img[^>]+alt="([a-z]+) minilogo"`)
	// Thumb — picture > img src.
	gridThumbRe = regexp.MustCompile(`<img[^>]+class="img-fluid"[^>]+src="([^"]+)"`)
	// Preview MP4 — `data-video-src="…"`.
	gridPreviewRe = regexp.MustCompile(`data-video-src="([^"]+\.mp4[^"]*)"`)
)

// ---- Variant WordPress (growlboys) types ----

const (
	wpPerPage    = 100
	wpPostsPathF = "/wp-json/wp/v2/posts?per_page=%d&_embed=1&page=%d"
)

type wpPost struct {
	ID       int      `json:"id"`
	DateGMT  string   `json:"date_gmt"`
	Slug     string   `json:"slug"`
	Link     string   `json:"link"`
	Title    rendered `json:"title"`
	Content  rendered `json:"content"`
	Excerpt  rendered `json:"excerpt"`
	Embedded embedded `json:"_embedded"`
}

type rendered struct {
	Rendered string `json:"rendered"`
}

type embedded struct {
	FeaturedMedia []featuredMedia `json:"wp:featuredmedia,omitempty"`
	Terms         [][]term        `json:"wp:term,omitempty"`
}

type featuredMedia struct {
	SourceURL string `json:"source_url"`
}

type term struct {
	Name     string `json:"name"`
	Taxonomy string `json:"taxonomy"`
}

// ---- Internal parsed item ----

type sceneItem struct {
	id          string
	title       string
	series      string
	url         string
	thumb       string
	preview     string
	performers  []string
	description string
	date        time.Time
}

// ---- Listing URL builder ----

func (s *Scraper) listingURL(page int, studioURL string) string {
	switch s.cfg.Variant {
	case VariantNATS:
		// Page 1 used to be the bare homepage, which cost half of it. The
		// homepage shows twelve cards, but only its first six are movies_1's
		// first six: positions 7-12 are *featured* scenes, years old
		// (boyforsale ids 146 and 7591 sitting behind 8513…8473). So the walk
		// took six of movies_1's twelve and then jumped to movies_2 — the
		// other six were never collected on any page — while incremental runs
		// hit one of those old featured cards at position 7 and stopped there,
		// capping pickup at six new scenes. Live-verified identical on
		// boyforsale, scoutboys, masonicboys, twinktop, gaycest and
		// funsizeboys. The categories listing is the real page 1.
		return fmt.Sprintf("%s/categories/movies_%d_d.html", s.cfg.SiteBase, page)
	case VariantGrid:
		if s.cfg.ListPath != "" {
			q := url.Values{}
			if site := s.siteFilter(studioURL); site != "" {
				q.Set("site", site)
			}
			if page > 1 {
				q.Set("page", strconv.Itoa(page))
			}
			u := s.cfg.SiteBase + s.cfg.ListPath
			if len(q) > 0 {
				u += "?" + q.Encode()
			}
			return u
		}
		base := s.cfg.SiteBase + s.cfg.SubPath
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		if page <= 1 {
			return base
		}
		return fmt.Sprintf("%s?page=%d", base, page)
	case VariantWordPress:
		return s.cfg.SiteBase + fmt.Sprintf(wpPostsPathF, wpPerPage, page)
	}
	return s.cfg.SiteBase + "/"
}

func (s *Scraper) siteFilter(studioURL string) string {
	if s.cfg.SiteFilter != "" {
		return s.cfg.SiteFilter
	}
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("site")
}

const (
	// gridDryPages is how many consecutive pages may add nothing new before the
	// grid walk stops. One is too few: the portal's page 7 added nothing and
	// page 8 still had two new scenes.
	gridDryPages = 3
	// gridMaxPages bounds a rotating feed that might never settle.
	gridMaxPages = 60
)

// ---- run loop ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "carnalplus/%s: scraping (variant=%d)", s.cfg.ID, s.cfg.Variant)

	now := time.Now().UTC()
	wpTotalPages := 0

	// The portal's grid listing answers `?page=N` but advertises no page links
	// and no last page: every page returns a full card set drawn from a
	// rotating pool, so pages overlap heavily and `len(items)` never falls.
	// Walked live, carnalplus.com contributed 40/10/6/6/5/2/0/2 new scenes
	// over eight pages — it never empties, so without a bound `--full` runs
	// forever. Ids are therefore tracked across pages and the walk stops once
	// gridDryPages consecutive pages add nothing new, with gridMaxPages as a
	// backstop for a feed whose rotation never settles.
	gridSeen := make(map[string]bool)
	gridDry := 0

	siteLabel := "carnalplus/" + s.cfg.ID
	scraper.Paginate(ctx, opts, siteLabel, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(page, studioURL)

		var (
			items   []sceneItem
			total   int
			headers http.Header
		)
		body, headers, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		switch s.cfg.Variant {
		case VariantNATS:
			items = parseNATSListing(body)
			total = estimateNATSTotal(body, len(items))
		case VariantGrid:
			items = parseGridListing(body)
			// Grid pages don't expose a clean last-page marker on the
			// portal; walk until empty.
			total = len(items)
		case VariantWordPress:
			items, total, wpTotalPages = parseWPListing(body, headers)
		}

		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		gridExhausted := false
		if s.cfg.Variant == VariantGrid {
			fresh := items[:0:0]
			for _, item := range items {
				if gridSeen[item.id] {
					continue
				}
				gridSeen[item.id] = true
				fresh = append(fresh, item)
			}
			if len(fresh) == 0 {
				gridDry++
			} else {
				gridDry = 0
			}
			items = fresh
			gridExhausted = gridDry >= gridDryPages || page >= gridMaxPages
			if gridExhausted {
				scraper.Debugf(1, "%s: grid listing exhausted at page %d (%d ids seen)", siteLabel, page, len(gridSeen))
			}
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = s.toScene(item, studioURL, now)
		}

		// WordPress pagination: stop once total_pages reached.
		done := gridExhausted ||
			(s.cfg.Variant == VariantWordPress && wpTotalPages > 0 && page >= wpTotalPages)
		// A grid page that filtered to nothing is not the end of the listing —
		// the rotation can still surface new scenes further in — so ask
		// Paginate to keep going until the dry counter says otherwise.
		keepGoing := len(scenes) == 0 && !done
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done, Continue: keepGoing}, nil
	})
}

// ---- NATS parser ----

func parseNATSListing(body []byte) []sceneItem {
	page := string(body)
	starts := natsCardStartRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		id := page[loc[2]:loc[3]]
		if seen[id] {
			continue
		}
		seen[id] = true

		item := sceneItem{id: id}
		if m := natsDetailURLRe.FindStringSubmatch(block); m != nil {
			item.url = m[1]
		}
		if m := natsTitleRe.FindStringSubmatch(block); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(m[1]))
		} else if m := natsH4Re.FindStringSubmatch(block); m != nil {
			if t := natsSceneTitleRe.FindStringSubmatch(m[1]); t != nil {
				item.title = html.UnescapeString(strings.TrimSpace(t[1]))
			} else {
				item.title = cleanHTML(m[1])
			}
		}
		if m := natsPerformerRe.FindStringSubmatch(block); m != nil {
			item.performers = splitPerformers(m[1])
		}
		if m := natsPosterRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		}
		if m := natsSourceRe.FindStringSubmatch(block); m != nil {
			item.preview = m[1]
		}
		items = append(items, item)
	}
	return items
}

func estimateNATSTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range natsPageLinkRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

// ---- Grid parser ----

func parseGridListing(body []byte) []sceneItem {
	page := string(body)
	starts := gridCardStartRe.FindAllStringIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		var item sceneItem
		if m := gridIDRe.FindStringSubmatch(block); m != nil {
			item.id = m[1]
		}
		if item.id == "" || seen[item.id] {
			continue
		}
		seen[item.id] = true

		if m := gridDetailURLRe.FindStringSubmatch(block); m != nil {
			item.url = m[1]
		}
		if m := gridTitleRe.FindStringSubmatch(block); m != nil {
			item.title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		if item.title == "" {
			item.title = gridHeadingTitle(block)
		}
		if m := gridSeriesRe.FindStringSubmatch(block); m != nil {
			item.series = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		// Source sub-site label takes priority over the series field for
		// `Scene.Series`. The grid-item layout's `update-series` field is
		// the within-series volume label ("Scout marcus vol. 2"), which is
		// confusing to store on Series — we instead keep it embedded in
		// the title via the `update-series | update-title` join already
		// captured below, and use the sub-site name as the canonical
		// Series.
		if m := gridSubsiteRe.FindStringSubmatch(block); m != nil {
			item.series = displaySubsite(m[1])
		}
		if m := gridThumbRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		}
		if m := gridPreviewRe.FindStringSubmatch(block); m != nil {
			item.preview = m[1]
		}

		items = append(items, item)
	}
	return items
}

// gridHeadingTitle reads the card heading as plain text. Scene cards split it
// into `update-series | update-title` spans, but full-movie cards write the
// name directly into the heading, and those arrived with no Title at all.
func gridHeadingTitle(block string) string {
	m := gridHeadingRe.FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	text := html.UnescapeString(gridTagStripRe.ReplaceAllString(m[1], " "))
	return strings.Join(strings.Fields(text), " ")
}

// ---- WordPress parser ----

func parseWPListing(body []byte, headers http.Header) ([]sceneItem, int, int) {
	var posts []wpPost
	if err := json.Unmarshal(body, &posts); err != nil {
		return nil, 0, 0
	}
	total, _ := strconv.Atoi(headers.Get("X-WP-Total"))
	totalPages, _ := strconv.Atoi(headers.Get("X-WP-TotalPages"))

	items := make([]sceneItem, 0, len(posts))
	for _, p := range posts {
		item := sceneItem{
			id:    strconv.Itoa(p.ID),
			title: cleanHTML(p.Title.Rendered),
			url:   p.Link,
		}
		desc := strings.TrimSpace(p.Content.Rendered)
		if desc == "" {
			desc = p.Excerpt.Rendered
		}
		item.description = cleanHTML(desc)
		if len(p.Embedded.FeaturedMedia) > 0 {
			item.thumb = p.Embedded.FeaturedMedia[0].SourceURL
		}
		for _, group := range p.Embedded.Terms {
			for _, t := range group {
				if t.Taxonomy == "post_tag" {
					name := strings.TrimSpace(t.Name)
					if name != "" {
						item.performers = append(item.performers, name)
					}
				}
			}
		}
		if d, err := time.Parse("2006-01-02T15:04:05", strings.TrimSpace(p.DateGMT)); err == nil {
			item.date = d.UTC()
		}
		items = append(items, item)
	}
	return items, total, totalPages
}

// ---- Helpers ----

// splitPerformers turns "Kai Neolani and Legrand Wolf" or "Alex Gonz, Guy
// Spencer, Joe Doe" into a slice. Handles "and", "&", commas as separators.
func splitPerformers(s string) []string {
	s = html.UnescapeString(strings.TrimSpace(s))
	// Normalise separators to commas then split.
	s = strings.ReplaceAll(s, " and ", ", ")
	s = strings.ReplaceAll(s, " & ", ", ")
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// displaySubsite turns the lowercased CDN slug ("scoutboys") into a
// human-readable site name ("Scout Boys"). The grid-item layout doesn't
// store the human name directly; we infer from the slug.
func displaySubsite(slug string) string {
	if name, ok := subsiteDisplay[slug]; ok {
		return name
	}
	// Fallback: title-case the slug as-is.
	return strings.Title(slug) //nolint:staticcheck // we accept English-only here
}

// subsiteDisplay maps the lowercased slug used in carnalplus.com's
// `minilogo/{slug}.png` filenames to the human-readable site name.
var subsiteDisplay = map[string]string{
	"americanmusclehunks": "American Muscle Hunks",
	"bangbangboys":        "BangBangBoys",
	"baptistboys":         "Baptist Boys",
	"betabetapi":          "BetaBetaPi",
	"boyforsale":          "Boy For Sale",
	"carnaloriginals":     "Carnal+ Originals",
	"catholicboys":        "Catholic Boys",
	"boundtwinks":         "BoundTwinks",
	"cumdumpsluts":        "Cum Dump Sluts",
	"dirtyboysociety":     "Dirty Boy Society",
	"funsizeboys":         "Fun-Size Boys",
	"gaycest":             "Gaycest",
	"growlboys":           "GrowlBoys",
	"hungfuckers":         "Hung Fuckers",
	"jalifstudio":         "Jalif Studio",
	"jasonsparkslive":     "Jason Sparks Live",
	"jockbreeders":        "JockBreeders",
	"rawfuckboys":         "Raw Fuck Boys",
	"studloads":           "StudLoads",
	"twinkloads":          "TwinkLoads",
	"masonicboys":         "Masonic Boys",
	"scoutboys":           "Scout Boys",
	"staghomme":           "Stag Homme",
	"teensandtwinks":      "TeensAndTwinks",
	"twinktop":            "Twink Top",
	"twinks":              "Twinks",
}

var (
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
	wsRe      = regexp.MustCompile(`\s+`)
)

func cleanHTML(s string) string {
	if s == "" {
		return ""
	}
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ") // U+00A0
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ---- Scene materialisation ----

func (s *Scraper) toScene(item sceneItem, studioURL string, now time.Time) models.Scene {
	series := item.series
	if series == "" {
		series = s.cfg.SiteName
	}
	sceneURL := item.url
	if sceneURL == "" {
		sceneURL = fmt.Sprintf("%s/#scene-%s", s.cfg.SiteBase, item.id)
	}
	return models.Scene{
		ID:          item.id,
		SiteID:      s.cfg.ID,
		StudioURL:   studioURL,
		Title:       item.title,
		Description: item.description,
		URL:         sceneURL,
		Thumbnail:   item.thumb,
		Preview:     item.preview,
		Date:        item.date,
		Performers:  item.performers,
		Studio:      studioName,
		Series:      series,
		ScrapedAt:   now,
	}
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, http.Header, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	if s.cfg.Variant == VariantWordPress {
		headers = map[string]string{
			"Accept":     "application/json",
			"User-Agent": httpx.UserAgentFirefox,
		}
	}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: headers,
	})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return body, resp.Header, nil
}

// Package pornstarplatinum scrapes the Pornstar Platinum network from
// the parent catalogue at `pornstarplatinum.com/tour/scenes.php?page=N`.
//
// The network has 35 sister "tour" sites (one per pornstar) but only
// `tour.alurajensonxxx.com` actually exposes scene cards on its tour;
// the other sister domains are splash redirects whose real content is
// gated behind a member login. The parent network catalogue at
// `pornstarplatinum.com/tour/scenes.php`, however, lists every public
// scene across all sites (295 pages × 16 = ~4720 scenes verified May
// 2026), with the model name in a `<div class="marker">` block so we
// can attribute scenes correctly without per-site filtering.
//
// One scraper handles the whole network. When the studio URL is a
// sister-site tour subdomain tied to a single pornstar, the scraper
// still walks the parent catalogue but filters by the expected
// performer name in-flight — so `fss scrape tour.deewilliams.xxx`
// yields just Dee Williams scenes, not the whole 4720-scene catalogue.
// When the studio URL is the parent (`pornstarplatinum.com`) or a
// themed brand without a single pornstar (Pornstar Justice, Taboo
// Stepmom), no filter is applied. `Scene.Series` always carries the
// per-card model name so a downstream consumer can filter further.
//
// **10 PSP performers don't have findable sister-site tour URLs**
// (parked, retired, or the network.php slug is typo'd):
//
//	Eva Notty, Havana Ginger, Tifanny Tyler, Rachael Cavalli,
//	Whore Today Gone Tomorrow, Nikita Von James, Claudia Valentine,
//	Emily's Playground, Raven Bay, Prince Yahshua.
//
// Their scenes still appear in the catalogue and are correctly
// attributed via `Scene.Series` when the operator scrapes the parent
// URL — just no dedicated per-pornstar URL match.
//
// **8 sister tours have a working per-site endpoint** that avoids the
// 295-page catalogue walk: Dee Williams, Kate Frost, Brooke Wylde,
// Veronica Avluv, Sexy Vanessa, Mindi Mink (sceneBlock template);
// Taboo Stepmom (tbsm template, themed); Joslyn James (Movies
// categories template). All other sister tours still walk the catalogue
// + filter by performer.
//
// Card markup:
//
//	<div class="item ...">
//	  <div class="item-header">
//	    <a href="/tour/model/{ID}/{slug}.html" class="thumbnail-link">
//	      <img src="https://c776ef2f9b.mjedge.net/pspthumbnails/{thumbID}.jpg" …>
//	    </a>
//	  </div>
//	  <div class="item-content">
//	    <div class="video-meta-title"><a href="…">{Title}</a></div>
//	    <div class="video-meta-container">
//	      <div class="marker left font-white">{Performer}</div>
//	      <div class="video-meta right font-white">
//	        {MM/DD/YYYY}
//	        <a href="…"><i class="fa fa-eye"></i>{views}</a>
//	        <span class="{ID}-likes">{likes}</span>
//	      </div>
//	    </div>
//	  </div>
//	</div>
//
// The catalogue is sorted newest-first so `KnownIDs` early-stop works.
package pornstarplatinum

import (
	"context"
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

const (
	siteID     = "pornstarplatinum"
	studioName = "Pornstar Platinum"
	baseURL    = "https://pornstarplatinum.com"
	catalogue  = "/tour/scenes.php"
	thumbCDN   = "https://c776ef2f9b.mjedge.net"
)

// siteFilter pairs a URL match regex with the performer name expected
// for scenes from that sister site. `Performer == ""` means "no
// filter" — that's the parent network URL plus the two themed brands
// (Pornstar Justice, Taboo Stepmom) which aren't tied to a single
// pornstar; for those the user gets the whole network catalogue and
// `Scene.Series` carries the per-card model name for downstream
// filtering.
//
// `PerSiteVideos` is the URL of a dedicated per-pornstar listing
// endpoint, when one exists. Scraping it directly is dramatically
// cheaper than walking 295 pages of the parent catalogue. The endpoint
// can be one of three templates, selected via `Template`:
//
//   - `templateSceneBlock` (default): `?page=N` pagination with
//     `<div class="sceneBlock">` or `<a class="sceneTitle">` cards
//     plus `pspthumbnails//{ID}.jpg` thumbnails — Dee Williams, Kate
//     Frost, Brooke Wylde, Veronica Avluv, Sexy Vanessa.
//   - `templateTbsm`: `?page=N` pagination with `<div class="scenesbgcolor">`
//     cards, `cid="{sceneID}"` on the img, `<h4>{Performer} in
//     {Title}</h4>`, and `tbsm_contentthumbs/{XX}/{YY}/{ID}-1x.jpg`
//     thumbnails — Taboo Stepmom (themed, performer parsed from title).
//   - `templateMoviesCategories`: NATS-style `categories/Movies_{N}_d.html`
//     pagination with `<div class="updateItem">` cards, `/updates/{slug}.html`
//     scene URLs, and an explicit `<span class="tour_update_models">`
//     performer block — Joslyn James.
//
// When `PerSiteVideos == ""`, the scraper falls back to the parent
// catalogue + in-flight performer filter.
//
// Performer matching is case-insensitive whitespace-collapsed string
// equality against the per-card `<div class="marker">` value, not a
// substring match — distinct performers can share name prefixes
// (`Tara Holiday` vs. `Tara Holiday Lashe`), and the catalogue
// reliably emits the canonical short name.
// endpointTemplate picks the per-site parser used by runPerSite.
type endpointTemplate int

const (
	templateSceneBlock       endpointTemplate = iota // default: /videos?page=N
	templateTbsm                                     // Taboo Stepmom: /scenes?page=N
	templateMoviesCategories                         // Joslyn James: /categories/Movies_{N}_d.html
)

type siteFilter struct {
	matchRe       *regexp.Regexp
	Performer     string
	PerSiteVideos string
	Template      endpointTemplate
}

// sites is the URL→performer mapping. Order doesn't matter — the run
// loop picks the first match. The parent network entry catches every
// `pornstarplatinum.com` URL without filtering, so it must NOT come
// before the sister-site entries (none of them overlap, but keeping
// the parent last guards against future additions that might).
var sites = []siteFilter{
	// Stashdb-tracked sister tours.
	{matchRe: regexp.MustCompile(`(?i)^https?://tour\.alurajensonxxx\.com(?:/|$)`), Performer: "Alura Jenson"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?amybrookexxx\.com(?:/|$)`), Performer: "Amy Brooke"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?angelinavalentine\.com(?:/|$)`), Performer: "Angelina Valentine"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?avadevine\.com(?:/|$)`), Performer: "Ava Devine"},
	{
		matchRe:       regexp.MustCompile(`(?i)^https?://(?:tour\.|www\.)?clubveronicaavluv\.com(?:/|$)`),
		Performer:     "Veronica Avluv",
		PerSiteVideos: "https://tour.clubveronicaavluv.com/videos",
	},
	{
		matchRe:       regexp.MustCompile(`(?i)^https?://tour\.deewilliams\.xxx(?:/|$)`),
		Performer:     "Dee Williams",
		PerSiteVideos: "https://tour.deewilliams.xxx/videos",
	},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?nickiblue\.com(?:/|$)`), Performer: "Nicki Blue"},
	// Pornstar Justice and Taboo Stepmom are themed brands rather than
	// single-pornstar sites — return the whole catalogue.
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:tour\.|www\.)?pornstarjustice\.com(?:/|$)`)},
	{
		matchRe:       regexp.MustCompile(`(?i)^https?://(?:tour\.|www\.)?sexyvanessa\.com(?:/|$)`),
		Performer:     "Sexy Vanessa",
		PerSiteVideos: "https://tour.sexyvanessa.com/videos",
	},
	{
		matchRe:       regexp.MustCompile(`(?i)^https?://(?:tour\.|www\.)?mindiminkxxx\.com(?:/|$)`),
		Performer:     "Mindi Mink",
		PerSiteVideos: "https://tour.mindiminkxxx.com/videos.php",
	},
	{
		// Themed brand (multiple performers) — Performer stays empty so
		// the parser parses the performer from each card's title.
		matchRe:       regexp.MustCompile(`(?i)^https?://(?:tour\.|www\.)?taboostepmom\.com(?:/|$)`),
		PerSiteVideos: "https://tour.taboostepmom.com/scenes",
		Template:      templateTbsm,
	},
	// Network-page sister tours not in stashdb but live.
	{matchRe: regexp.MustCompile(`(?i)^https?://tour\.kendralustxxx\.com(?:/|$)`), Performer: "Kendra Lust"},
	{
		matchRe:       regexp.MustCompile(`(?i)^https?://(?:tour\.|www\.)?joslynjames\.xxx(?:/|$)`),
		Performer:     "Joslyn James",
		PerSiteVideos: "https://tour.joslynjames.xxx/categories/Movies",
		Template:      templateMoviesCategories,
	},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?courtneytaylor\.xxx(?:/|$)`), Performer: "Courtney Taylor"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?alyssalynnxxx\.com(?:/|$)`), Performer: "Alyssa Lynn"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?taraholidayxxx\.com(?:/|$)`), Performer: "Tara Holiday"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?christiestevensxxx\.com(?:/|$)`), Performer: "Christie Stevens"},
	{matchRe: regexp.MustCompile(`(?i)^https?://tour\.clubkatiesummers\.com(?:/|$)`), Performer: "Katie Summers"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?katerinakayxxx\.com(?:/|$)`), Performer: "Katerina Kay"},
	{
		matchRe:       regexp.MustCompile(`(?i)^https?://tour\.katefrost\.com(?:/|$)`),
		Performer:     "Kate Frost",
		PerSiteVideos: "https://tour.katefrost.com/videos",
	},
	{matchRe: regexp.MustCompile(`(?i)^https?://tour\.clubrachellove\.com(?:/|$)`), Performer: "Rachel Love"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?tegansummers\.com(?:/|$)`), Performer: "Tegan Summers"},
	{
		matchRe:       regexp.MustCompile(`(?i)^https?://tour\.brookewyldexxx\.com(?:/|$)`),
		Performer:     "Brooke Wylde",
		PerSiteVideos: "https://tour.brookewyldexxx.com/videos",
	},
	{matchRe: regexp.MustCompile(`(?i)^https?://tour\.heatherstarletxxx\.com(?:/|$)`), Performer: "Heather Starlet"},
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?gigiriveraxxx\.com(?:/|$)`), Performer: "Gigi Rivera"},
	// Parent — must be last so sister-site matches win.
	{matchRe: regexp.MustCompile(`(?i)^https?://(?:www\.)?pornstarplatinum\.com(?:/|$)`)},
}

// resolveFilter returns the performer name to filter by for the passed
// studio URL. Empty string means "no filter" (whole network catalogue).
// Returns ("", false) if no site matches — the caller can then refuse
// the URL via MatchesURL.
func resolveFilter(studioURL string) (string, bool) {
	for _, s := range sites {
		if s.matchRe.MatchString(studioURL) {
			return s.Performer, true
		}
	}
	return "", false
}

// resolveSite returns the full siteFilter entry for the passed URL.
// Returns (nil, false) if no entry matches. Internal helper for run().
func resolveSite(studioURL string) (*siteFilter, bool) {
	for i := range sites {
		if sites[i].matchRe.MatchString(studioURL) {
			return &sites[i], true
		}
	}
	return nil, false
}

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }
func (s *Scraper) Patterns() []string {
	return []string{
		"pornstarplatinum.com/tour/scenes.php",
		"any-pornstarplatinum-network-sister-site",
	}
}
func (s *Scraper) MatchesURL(u string) bool {
	_, ok := resolveFilter(u)
	return ok
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- parsing ----

var (
	// cardStartRe matches the opening `<div class="item …">` of one
	// catalogue card. Card body runs to the next match or end of
	// document.
	cardStartRe = regexp.MustCompile(`<div class="item no-nth col-12[^"]*">`)
	// urlAndIDRe pulls `/tour/model/{ID}/{slug}.html` out of the card
	// header anchor.
	urlAndIDRe = regexp.MustCompile(`<a href="(/tour/model/(\d+)/[^"]+)"\s+class="thumbnail-link"`)
	// thumbRe pulls the pspthumbnails CDN URL out of the card header img.
	thumbRe = regexp.MustCompile(`<img src="(https://[^"]+/pspthumbnails/\d+\.jpg)"`)
	// titleRe pulls the anchor text from `<div class="video-meta-title">`.
	titleRe = regexp.MustCompile(`(?s)<div class="video-meta-title">\s*<a[^>]*>([^<]+)</a>`)
	// performerRe pulls the model name from `<div class="marker …">`.
	performerRe = regexp.MustCompile(`<div class="marker left font-white">\s*([^<]+?)\s*</div>`)
	// dateRe matches MM/DD/YYYY at the start of `<div class="video-meta right …">`.
	dateRe = regexp.MustCompile(`<div class="video-meta right font-white">\s*(\d{2}/\d{2}/\d{4})`)
	// viewsRe matches the inline view count. The live HTML wraps the
	// `<i>` tag across two lines (`<i\n class="…">`), so the gap
	// between `<i` and `class` must be tolerant of whitespace — the
	// fixture used in unit tests has a single space and would have
	// matched a stricter regex while production would not.
	viewsRe = regexp.MustCompile(`<i\s+class="fa fa-eye"></i>(\d+)`)
	// likesRe captures the like count from `<span class="{ID}-likes">…</span>`.
	likesRe = regexp.MustCompile(`<span\s+class="\d+-likes">\s*(\d+)\s*</span>`)
	// totalPagesRe matches `page=N` references; the highest wins.
	totalPagesRe = regexp.MustCompile(`page=(\d+)`)

	// ---- per-site /videos parsers ----
	//
	// Sister tours that expose `/videos?page=N` use one of two
	// templates (both shapes produce a hrefs to `(?:model|set)/{ID}/…`
	// and `pspthumbnails//{N}.jpg` images): the Dee Williams "sceneBlock"
	// template and the Kate Frost / Brooke Wylde "sceneTitle" template.
	// We unify on the underlying href pattern — it captures every scene
	// the page lists across either template — and treat the rest of the
	// fields as opportunistic enrichments.
	perSiteHrefRe  = regexp.MustCompile(`href="(?:/)?(?:model|set)/(\d+)/([^"?]+)\.html\?[^"]*"`)
	perSiteThumbRe = regexp.MustCompile(`<img[^>]+src="((?:https?:)?//[^"]*(?:pspthumbnails|contentthumbs)/+\d+[^"]*\.jpg)"`)

	// perSiteDateRe captures `Month D, YYYY` from `<div class="sceneDate">…</div>` (Dee template).
	perSiteDateRe = regexp.MustCompile(`<div class="sceneDate">\s*([A-Z][a-z]+ \d{1,2}, \d{4})\s*</div>`)
)

type card struct {
	id          string
	url         string // path-only, absolutised in toScene
	thumb       string
	title       string
	performer   string
	date        time.Time
	views       int
	likes       int
	description string
	duration    int // seconds
}

func parseCards(body []byte) ([]card, int) {
	page := string(body)
	starts := cardStartRe.FindAllStringIndex(page, -1)
	cards := make([]card, 0, len(starts))
	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[1]:end]

		var c card
		if m := urlAndIDRe.FindStringSubmatch(block); m != nil {
			c.url = m[1]
			c.id = m[2]
		}
		if c.id == "" {
			continue
		}
		if m := thumbRe.FindStringSubmatch(block); m != nil {
			c.thumb = m[1]
		}
		if m := titleRe.FindStringSubmatch(block); m != nil {
			c.title = cleanText(m[1])
		}
		if m := performerRe.FindStringSubmatch(block); m != nil {
			c.performer = cleanText(m[1])
		}
		if m := dateRe.FindStringSubmatch(block); m != nil {
			if t, err := time.Parse("01/02/2006", m[1]); err == nil {
				c.date = t.UTC()
			}
		}
		if m := viewsRe.FindStringSubmatch(block); m != nil {
			c.views, _ = strconv.Atoi(m[1])
		}
		if m := likesRe.FindStringSubmatch(block); m != nil {
			c.likes, _ = strconv.Atoi(m[1])
		}
		cards = append(cards, c)
	}

	// Highest page number referenced anywhere on the page is the total
	// page count (the paginator emits 1, 2, 3, …, last).
	maxPage := 1
	for _, m := range totalPagesRe.FindAllStringSubmatch(page, -1) {
		if n, _ := strconv.Atoi(m[1]); n > maxPage {
			maxPage = n
		}
	}
	return cards, maxPage
}

// siteBaseFromVideos turns the configured `PerSiteVideos` URL back into the
// bare sister-tour origin. Used to build absolute scene URLs from the
// path-only hrefs the per-site templates emit.
//
// It takes the origin off the URL rather than matching the listing suffixes
// the templates use: a suffix list has to be extended for every new template
// and silently produced a broken base when it was not. Mindi Mink's listing is
// `/videos.php`, which no entry matched, so every scene URL came out as
// `…/videos.php/set/{id}/…` and 404ed.
func siteBaseFromVideos(perSiteVideos string) string {
	u, err := url.Parse(perSiteVideos)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return perSiteVideos
	}
	return u.Scheme + "://" + u.Host
}

// perSitePageURL builds the page-N URL for a given site, honouring the
// template's pagination pattern: `?page=N` for the videos/scenes
// templates, `_{N}_d.html` for the categories/Movies template.
func perSitePageURL(site *siteFilter, page int) string {
	if site.Template == templateMoviesCategories {
		return fmt.Sprintf("%s_%d_d.html", site.PerSiteVideos, page)
	}
	return fmt.Sprintf("%s?page=%d", site.PerSiteVideos, page)
}

// parsePerSiteCards extracts scene cards from a per-pornstar `/videos`
// page. Returns (cards, maxPage) where maxPage is the highest page
// number referenced anywhere on the page. The performer name is the
// site's configured performer — we don't try to parse it from the slug
// since the per-site tours are 1:1 with a single pornstar. `siteBase`
// is the sister-tour origin used to absolutise scene URLs.
func parsePerSiteCards(body []byte, performer, siteBase string) ([]card, int) {
	page := string(body)

	// 1) Extract every (ID, slug) from the unified href pattern. The
	//    same scene may be referenced multiple times within a card
	//    (thumbnail anchor + title anchor) — dedupe by ID, keeping the
	//    first occurrence's slice index so we can pair thumb/date
	//    correctly later.
	idLocs := perSiteHrefRe.FindAllStringSubmatchIndex(page, -1)
	seen := make(map[string]int, len(idLocs))
	cards := make([]card, 0, len(idLocs))
	for _, m := range idLocs {
		id := page[m[2]:m[3]]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = len(cards)
		slug := page[m[4]:m[5]]
		cards = append(cards, card{
			id:        id,
			url:       siteBase + "/set/" + id + "/" + slug + ".html",
			title:     slugToTitle(slug),
			performer: performer,
		})
	}

	// 2) Pair each thumbnail and date with the closest href (by
	//    absolute source-position distance). Templates vary in whether
	//    the thumb/date appears before or after the card's anchor, so
	//    nearest-match handles both layouts.
	if len(cards) > 0 {
		thumbLocs := perSiteThumbRe.FindAllStringSubmatchIndex(page, -1)
		hrefLocs := perSiteHrefRe.FindAllStringSubmatchIndex(page, -1)
		type hrefMark struct {
			pos   int
			cardI int
		}
		hrefs := make([]hrefMark, 0, len(hrefLocs))
		hSeen := make(map[string]bool, len(cards))
		for _, m := range hrefLocs {
			id := page[m[2]:m[3]]
			if hSeen[id] {
				continue
			}
			hSeen[id] = true
			hrefs = append(hrefs, hrefMark{pos: m[0], cardI: seen[id]})
		}

		for _, t := range thumbLocs {
			imgURL := page[t[2]:t[3]]
			if strings.HasPrefix(imgURL, "//") {
				imgURL = "https:" + imgURL
			}
			bestDist := len(page) + 1
			bestIdx := -1
			for j := range hrefs {
				dist := hrefs[j].pos - t[0]
				if dist < 0 {
					dist = -dist
				}
				if dist < bestDist {
					bestDist = dist
					bestIdx = j
				}
			}
			if bestIdx >= 0 && cards[hrefs[bestIdx].cardI].thumb == "" {
				cards[hrefs[bestIdx].cardI].thumb = imgURL
			}
		}

		// 3) Same idea for dates — only the sceneBlock template emits
		//    them, so callers tolerate missing dates on the Kate Frost /
		//    Brooke Wylde template.
		dateLocs := perSiteDateRe.FindAllStringSubmatchIndex(page, -1)
		for _, d := range dateLocs {
			dateStr := page[d[2]:d[3]]
			bestDist := len(page) + 1
			bestIdx := -1
			for j := range hrefs {
				dist := hrefs[j].pos - d[0]
				if dist < 0 {
					dist = -dist
				}
				if dist < bestDist {
					bestDist = dist
					bestIdx = j
				}
			}
			if bestIdx >= 0 && cards[hrefs[bestIdx].cardI].date.IsZero() {
				if t, err := time.Parse("January 2, 2006", dateStr); err == nil {
					cards[hrefs[bestIdx].cardI].date = t.UTC()
				}
			}
		}
	}

	// 4) Determine the max page number — same `page=N` reference scan
	//    used by the catalogue parser.
	maxPage := 1
	for _, m := range totalPagesRe.FindAllStringSubmatch(page, -1) {
		if n, _ := strconv.Atoi(m[1]); n > maxPage {
			maxPage = n
		}
	}
	return cards, maxPage
}

// slugToTitle converts `Dee_Williams_in_Pussy_Delivery` →
// "Dee Williams in Pussy Delivery". Backticks are sometimes used in
// place of apostrophes by the URL slug (`Woman“s_BFF…`); leave them
// for the caller to decide.
func slugToTitle(slug string) string {
	return strings.ReplaceAll(slug, "_", " ")
}

// ---- Taboo Stepmom (`templateTbsm`) parser ----
//
// Card markup:
//
//	<div class="scenesbgcolor">
//	  <a href="/join">
//	    <img src=".../tbsm_contentthumbs/{XX}/{YY}/{thumbID}-1x.jpg"
//	         cid="{sceneID}" class="img-responsive scene-thumb">
//	  </a>
//	  <a href="/join"><h4>{Performer} in {Title}</h4></a>
//	  <p class="description">{Description}</p>
//	  <p class="text-left contentcount">Video: {MM:SS}</p>
//	</div>
//
// No detail-page URL exists — every link goes to `/join`. We
// synthesise the scene URL as `{base}/#scene-{sceneID}` so each scene
// has a stable anchor.
var (
	tbsmCardStartRe = regexp.MustCompile(`<div class="scenesbgcolor">`)
	tbsmImgRe       = regexp.MustCompile(`<img[^>]+src="([^"]+)"[^>]+class="[^"]*scene-thumb"`)
	tbsmThumbIDRe   = regexp.MustCompile(`tbsm_contentthumbs/\d+/\d+/(\d+)`)
	tbsmTitleRe     = regexp.MustCompile(`(?s)<a[^>]+href="/join">\s*<h4>\s*([^<]+?)\s*</h4>`)
	tbsmDescRe      = regexp.MustCompile(`<p class="description">\s*([^<]+?)\s*</p>`)
	tbsmDurationRe  = regexp.MustCompile(`Video:\s*(\d+:\d+)`)
)

func parsePerSiteCardsTbsm(body []byte, siteBase string) ([]card, int) {
	page := string(body)
	starts := tbsmCardStartRe.FindAllStringIndex(page, -1)
	cards := make([]card, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[1]:end]

		var c card
		if m := tbsmImgRe.FindStringSubmatch(block); m != nil {
			c.thumb = m[1]
			// Extract scene ID from thumb path — the cid attribute is
			// broken on taboostepmom (always the same value).
			if tid := tbsmThumbIDRe.FindStringSubmatch(m[1]); tid != nil {
				c.id = tid[1]
			}
		}
		if c.id == "" || seen[c.id] {
			continue
		}
		seen[c.id] = true

		if m := tbsmTitleRe.FindStringSubmatch(block); m != nil {
			full := cleanText(m[1])
			if idx := strings.Index(full, " in "); idx > 0 {
				c.performer = full[:idx]
				c.title = full
			} else {
				c.title = full
			}
		}
		if m := tbsmDescRe.FindStringSubmatch(block); m != nil {
			c.description = cleanText(m[1])
		}
		if m := tbsmDurationRe.FindStringSubmatch(block); m != nil {
			c.duration = parseDurationColon(m[1])
		}

		c.url = siteBase + "/#scene-" + c.id

		cards = append(cards, c)
	}

	maxPage := 1
	for _, m := range totalPagesRe.FindAllStringSubmatch(page, -1) {
		if n, _ := strconv.Atoi(m[1]); n > maxPage {
			maxPage = n
		}
	}
	return cards, maxPage
}

// ---- Joslyn James (`templateMoviesCategories`) parser ----
//
// Card markup:
//
//	<div class="updateItem">
//	  <a href="{full /updates/{slug}.html URL}">
//	    <img class="stdimage" src=".../contentthumbs/{thumbID}.jpg">
//	  </a>
//	  <div class="updateDetails">
//	    <h4><a href="{same URL}">{Performer in Title}</a></h4>
//	    <p><span class="tour_update_models">
//	      <a href=".../models/{Slug}.html">{Performer Name}</a> , …
//	    </span></p>
//	  </div>
//	</div>
//
// The scene "ID" is the URL slug — there's no numeric content ID
// exposed. Pagination is `categories/Movies_{N}_d.html`, not `?page=N`.
var (
	moviesCardStartRe = regexp.MustCompile(`<div class="updateItem"[^>]*>`)
	moviesHrefRe      = regexp.MustCompile(`<a\s+href="(https?://[^"]+/updates/([^"./]+)\.html)"`)
	moviesThumbRe     = regexp.MustCompile(`<img[^>]+class="stdimage[^"]*"[^>]+src="([^"]+)"`)
	moviesTitleRe     = regexp.MustCompile(`(?s)<h4>\s*<a[^>]*>\s*([^<]+?)\s*</a>\s*</h4>`)
	moviesPageRe      = regexp.MustCompile(`Movies_(\d+)_d\.html`)
)

func parsePerSiteCardsMovies(body []byte, performer string) ([]card, int) {
	page := string(body)
	starts := moviesCardStartRe.FindAllStringIndex(page, -1)
	cards := make([]card, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[1]:end]

		var c card
		var url, slug string
		if m := moviesHrefRe.FindStringSubmatch(block); m != nil {
			url = m[1]
			slug = m[2]
		}
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		c.id = slug
		c.url = url

		if m := moviesThumbRe.FindStringSubmatch(block); m != nil {
			c.thumb = m[1]
		}
		if m := moviesTitleRe.FindStringSubmatch(block); m != nil {
			c.title = cleanText(m[1])
		}
		// Use the configured site performer — the tour belongs to one
		// pornstar so every scene features her, even when the per-card
		// `tour_update_models` list re-orders her behind a guest
		// performer (which the site does for collab scenes).
		c.performer = performer

		cards = append(cards, c)
	}

	maxPage := 1
	for _, m := range moviesPageRe.FindAllStringSubmatch(page, -1) {
		if n, _ := strconv.Atoi(m[1]); n > maxPage {
			maxPage = n
		}
	}
	return cards, maxPage
}

func parseDurationColon(s string) int {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return 0
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		nums[i], _ = strconv.Atoi(p)
	}
	switch len(nums) {
	case 2:
		return nums[0]*60 + nums[1]
	case 3:
		return nums[0]*3600 + nums[1]*60 + nums[2]
	}
	return 0
}

var wsRe = regexp.MustCompile(`\s+`)

func cleanText(s string) string {
	if s == "" {
		return ""
	}
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ---- run loop ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	site, _ := resolveSite(studioURL)
	// Dispatch: per-site /videos endpoint (cheaper, only ~3 sites have
	// one) vs the parent network catalogue (with optional in-flight
	// filter).
	if site != nil && site.PerSiteVideos != "" {
		scraper.Debugf(1, "pornstarplatinum: scraping per-site %s (performer=%q)", site.PerSiteVideos, site.Performer)
		s.runPerSite(ctx, studioURL, site, opts, out)
		return
	}

	wantPerformer := ""
	if site != nil {
		wantPerformer = site.Performer
	}
	if wantPerformer == "" {
		scraper.Debugf(1, "pornstarplatinum: scraping whole network catalogue")
	} else {
		scraper.Debugf(1, "pornstarplatinum: scraping network catalogue, filtering to %q", wantPerformer)
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, "pornstarplatinum", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, page)
		if err != nil {
			return scraper.PageResult{}, err
		}

		cards, maxPage := parseCards(body)
		var scenes []models.Scene
		for _, c := range cards {
			if wantPerformer != "" && !strings.EqualFold(c.performer, wantPerformer) {
				continue
			}
			scenes = append(scenes, s.toScene(c, studioURL, now))
		}

		// `total` is only meaningful when we're emitting the whole
		// catalogue — for a per-performer filter we don't know upfront
		// how many cards across the 295 pages actually match.
		total := 0
		if wantPerformer == "" && maxPage > 0 {
			total = maxPage * len(cards)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			// In performer mode a page with no matching cards filters to zero
			// scenes; keep walking the catalogue until the last page.
			Continue: len(cards) > 0,
			Done:     maxPage > 0 && page >= maxPage,
		}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, page int) ([]byte, error) {
	u := fmt.Sprintf("%s%s?page=%d", baseURL, catalogue, page)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// runPerSite walks `{PerSiteVideos}?page=N` for sister tours that
// expose a per-pornstar listing. Much cheaper than walking the 295-
// page parent catalogue + filtering. The parser handles both observed
// templates (sceneBlock — Dee Williams; sceneTitle — Kate Frost,
// Brooke Wylde) via the shared href/thumb extraction in
// parsePerSiteCards.
func (s *Scraper) runPerSite(ctx context.Context, studioURL string, site *siteFilter, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

	label := site.Performer
	if label == "" {
		label = "themed"
	}
	paginateID := "pornstarplatinum/" + label

	scraper.Paginate(ctx, opts, paginateID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := perSitePageURL(site, page)
		resp, err := httpx.Do(ctx, s.client, httpx.Request{
			URL:     u,
			Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
		})
		if err != nil {
			return scraper.PageResult{}, err
		}
		body, err := httpx.ReadBody(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("per-site page %d read: %w", page, err)
		}

		var (
			cards   []card
			maxPage int
		)
		switch site.Template {
		case templateTbsm:
			cards, maxPage = parsePerSiteCardsTbsm(body, siteBaseFromVideos(site.PerSiteVideos))
		case templateMoviesCategories:
			cards, maxPage = parsePerSiteCardsMovies(body, site.Performer)
		default:
			cards, maxPage = parsePerSiteCards(body, site.Performer, siteBaseFromVideos(site.PerSiteVideos))
		}

		scenes := make([]models.Scene, len(cards))
		for i, c := range cards {
			scenes[i] = s.toScene(c, studioURL, now)
		}
		total := 0
		if maxPage > 0 {
			total = maxPage * len(cards)
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   maxPage > 0 && page >= maxPage,
		}, nil
	})
}

func (s *Scraper) toScene(c card, studioURL string, now time.Time) models.Scene {
	url := c.url
	if !strings.HasPrefix(url, "http") {
		url = baseURL + url
	}
	scene := models.Scene{
		ID:          c.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       c.title,
		URL:         url,
		Date:        c.date,
		Thumbnail:   c.thumb,
		Studio:      studioName,
		Series:      c.performer,
		Description: c.description,
		Duration:    c.duration,
		ScrapedAt:   now,
		Views:       c.views,
		Likes:       c.likes,
	}
	if c.performer != "" {
		scene.Performers = []string{c.performer}
	}
	return scene
}

// Package extrememoviepassutil scrapes sites on the Extreme Movie Pass
// affiliate network — a NATS template variant used by SexyCuckold,
// BigBreast.tv, Flexi Dolls, Voyeur Papy, and ~15 other sister sites.
//
// Detection signals:
//
//   - `tourhelper` NATS JS + `/tour/` prefix on every path.
//   - Pagination via `/tour/categories/movies/{page}/latest/`; past-end pages
//     return HTTP 200 with zero cards (clean stop signal).
//   - Card wrapper `<div class="modelfeature ... grabthis">` with thumbs
//     identified by `id="set-target-{sceneID}-{thumbID}"`.
//   - Affiliate-only links: every scene anchor goes to
//     `https://join.{site}.com/signup/signup.php?nats=...`. There is NO public
//     scene detail page, so all metadata has to come from the listing card.
//
// Card markup (one item):
//
//	<div class="modelfeature  grabthis">
//	  <div class="modelimg"><div class="wrapper">
//	    <a href="https://join.{site}.com/signup/signup.php?nats=…" title="Watch …">
//	      <img id="set-target-99331-8821891" class="update_thumb thumbs stdimage"
//	           src0_1x="https://{cdn}.mjedge.net/tour//contentthumbs/93/31/99331-1x.jpg" />
//	      <div class="description">
//	        <p><i class="fa fa-clock-o"></i> 31 min &nbsp; …
//	           <i class="fa fa-eye"></i> 14428 Views …</p>
//	      </div>
//	    </a>
//	  </div></div>
//	  <div class="modeldata">
//	    <a href="…/signup.php?nats=…" title="Sc 4k Stan005 Asya Murkovski 01">busty teen cuckold fucked</a>
//	    <p><i class="fa fa-calendar-check-o"></i> Date <font color="#48ff00">2026-05-28</font></p>
//	    <p><span class="update_models"><a href="…/tour/models/Asya-Murkovski.html">Asya Murkovski</a></span></p>
//	  </div>
//	</div>
//
// Fields lifted from the card: ID (from set-target), title (anchor text inside
// modeldata), description-title (the `title=` attribute is a short studio code
// like "Sc 4k Stan005 …" — discarded), date, duration, views, performers,
// thumbnail.
//
// Scene URL: since the network has no public detail pages, we synthesise
// `{base}/tour/#scene-{id}` so each scene gets a unique URL anchor for
// downstream matching.
package extrememoviepassutil

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one Extreme Movie Pass sister site.
type SiteConfig struct {
	ID       string
	SiteBase string // e.g. "https://www.sexycuckold.com" — no trailing slash
	Studio   string
	Patterns []string
	MatchRe  *regexp.Regexp
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

func (s *Scraper) ID() string { return s.cfg.ID }

// Patterns appends the model-page pattern to the configured ones.
//
// SiteBase carries no trailing slash, so the path needs its own leading one —
// without it every site in the table advertised "www.example.comtour/models/…"
// in `fss list-scrapers`. One SiteBase is http:// (smutbuttxxx), so both schemes
// are stripped. The result is built into a fresh slice rather than appended to
// cfg.Patterns, which would write into the shared config's backing array.
func (s *Scraper) Patterns() []string {
	host := strings.TrimSuffix(hostOf(s.cfg.SiteBase), "/")
	out := make([]string, 0, len(s.cfg.Patterns)+1)
	out = append(out, s.cfg.Patterns...)
	return append(out, host+"/tour/models/{slug}.html")
}

func hostOf(base string) string {
	return strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
}
func (s *Scraper) MatchesURL(u string) bool {
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// Card parsing.
//
// cardStartRe anchors each card by `<div class="modelfeature ... grabthis">`.
// We then slice from there to the next card start (or end of page) and extract
// each field with a focused regex.
var (
	cardStartRe = regexp.MustCompile(`<div class="modelfeature[^"]*"`)
	// set-target IDs come in the form "set-target-{sceneID}-{thumbID}". We use
	// the first number as the stable scene ID.
	sceneIDRe = regexp.MustCompile(`id="set-target-(\d+)-\d+"`)
	// modeldata block — title + date + performers area. Markup varies between
	// sister sites: most write `<div class="modeldata">` but a few (e.g.
	// pornonstage) inject a stray space `<div class="modeldata" >`. Accept
	// both, plus any extra classes mixed in.
	modeldataRe = regexp.MustCompile(`(?s)<div class="modeldata[^"]*"\s*[^>]*>(.*?)</div>`)
	// Inside modeldata: the first <a> wraps the user-facing title.
	// Title is the anchor's text content; the title= attribute holds an
	// internal short code that we ignore.
	titleRe = regexp.MustCompile(`(?s)<a [^>]*style="[^"]*font-size[^"]*"[^>]*>\s*([^<]+?)\s*</a>`)
	// Date forms seen across sister sites:
	//   - sexycuckold:    `Date <font color="#48ff00">2026-05-28</font>`
	//   - pornonstage:    `Updated 2026-05-28`
	//   - other variants: bare `<font>2026-05-28</font>` after a label
	// The bare YYYY-MM-DD with optional <font> wrapper covers all cases.
	dateRe = regexp.MustCompile(`(?:Date|Updated)\s*(?:<font[^>]*>)?\s*(\d{4}-\d{2}-\d{2})`)
	// Duration in the description block — two forms:
	//   - sexycuckold:  "31 min"      (whole-minutes)
	//   - pornonstage:  "14:12 min"   (MM:SS, then literal " min")
	// The MM:SS form is matched first; the bare-minutes form is a fallback.
	durationColonRe = regexp.MustCompile(`fa-clock-o[^>]*></i>\s*(\d+):(\d{2})(?::(\d{2}))?\s*min`)
	durationMinsRe  = regexp.MustCompile(`fa-clock-o[^>]*></i>\s*(\d+)\s*min`)
	viewsRe         = regexp.MustCompile(`fa-eye[^>]*></i>\s*(\d+)\s*Views`)
	// Thumbnail — high-res lazy-load attribute.
	thumbRe = regexp.MustCompile(`src0_1x="([^"]+)"`)
	// Performers — anchors inside <span class="update_models">.
	performerSectionRe = regexp.MustCompile(`(?s)<span class="update_models">(.*?)</span>`)
	performerAnchorRe  = regexp.MustCompile(`<a[^>]+href="[^"]*/tour/models/[^"]+"[^>]*>([^<]+)</a>`)
	// Pagination max-page lookup. Links look like
	// `/tour/categories/movies/{N}/latest/`.
	maxPageRe = regexp.MustCompile(`/tour/categories/movies/(\d+)/latest/?`)
)

type sceneItem struct {
	id         string
	title      string
	thumb      string
	date       time.Time
	duration   int // seconds
	views      int
	performers []string
}

func parseListing(body []byte) []sceneItem {
	page := string(body)
	starts := cardStartRe.FindAllStringIndex(page, -1)
	items := make([]sceneItem, 0, len(starts))
	seen := make(map[string]bool, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := page[loc[0]:end]

		var item sceneItem

		// ID — required; cards without one (e.g. promo blocks) are skipped.
		if m := sceneIDRe.FindStringSubmatch(block); m != nil {
			item.id = m[1]
		}
		if item.id == "" || seen[item.id] {
			continue
		}
		seen[item.id] = true

		// Title — inside the modeldata anchor.
		if md := modeldataRe.FindStringSubmatch(block); md != nil {
			if t := titleRe.FindStringSubmatch(md[1]); t != nil {
				item.title = html.UnescapeString(strings.TrimSpace(t[1]))
			}
			if p := performerSectionRe.FindStringSubmatch(md[1]); p != nil {
				for _, pm := range performerAnchorRe.FindAllStringSubmatch(p[1], -1) {
					name := html.UnescapeString(strings.TrimSpace(pm[1]))
					if name != "" {
						item.performers = append(item.performers, name)
					}
				}
			}
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		}
		if m := dateRe.FindStringSubmatch(block); m != nil {
			if d, err := time.Parse("2006-01-02", m[1]); err == nil {
				item.date = d.UTC()
			}
		}
		// Try the more-precise MM:SS form first; fall back to bare minutes.
		if m := durationColonRe.FindStringSubmatch(block); m != nil {
			h, _ := strconv.Atoi(m[1])
			mm, _ := strconv.Atoi(m[2])
			ss := 0
			if m[3] != "" {
				// HH:MM:SS form — m[1] is hours, m[2] is minutes, m[3] is seconds.
				ss, _ = strconv.Atoi(m[3])
				item.duration = h*3600 + mm*60 + ss
			} else {
				// MM:SS form — m[1] is minutes, m[2] is seconds.
				item.duration = h*60 + mm
			}
		} else if m := durationMinsRe.FindStringSubmatch(block); m != nil {
			mins, _ := strconv.Atoi(m[1])
			item.duration = mins * 60
		}
		if m := viewsRe.FindStringSubmatch(block); m != nil {
			item.views, _ = strconv.Atoi(m[1])
		}

		items = append(items, item)
	}
	return items
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func (s *Scraper) listingURL(page int) string {
	return fmt.Sprintf("%s/tour/categories/movies/%d/latest/", s.cfg.SiteBase, page)
}

var modelSlugRe = regexp.MustCompile(`/tour/models/([^_/.]+?)(?:\.html)?$`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if modelSlugRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: detected model page", s.cfg.ID)
		s.scrapeModelPage(ctx, studioURL, opts, out)
		return
	}

	scraper.Debugf(1, "%s: scraping full catalog", s.cfg.ID)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := estimateTotal(body, len(items))
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(s.cfg.ID, s.cfg.SiteBase, studioURL, s.cfg.Studio, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	pageURL := studioURL
	if !strings.HasPrefix(pageURL, "http") {
		pageURL = s.cfg.SiteBase + pageURL
	}

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	items := parseListing(body)
	if len(items) == 0 {
		return
	}
	scraper.Debugf(1, "%s: found %d scenes on model page", s.cfg.ID, len(items))

	now := time.Now().UTC()
	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.ID, item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(item.toScene(s.cfg.ID, s.cfg.SiteBase, studioURL, s.cfg.Studio, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (item sceneItem) toScene(siteID, siteBase, studioURL, studio string, now time.Time) models.Scene {
	// No public detail page exists; synthesise a unique URL anchor under the
	// site's /tour/ root so downstream consumers (Stash matching, etc.) still
	// have something stable.
	url := fmt.Sprintf("%s/tour/#scene-%s", siteBase, item.id)
	return models.Scene{
		ID:         item.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      item.title,
		URL:        url,
		Thumbnail:  item.thumb,
		Date:       item.date,
		Duration:   item.duration,
		Views:      item.views,
		Performers: item.performers,
		Studio:     studio,
		ScrapedAt:  now,
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

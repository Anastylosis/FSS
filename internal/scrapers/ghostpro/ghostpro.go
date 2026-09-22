// Package ghostpro scrapes the Next.js sister sites in Ghost Pro Productions'
// portfolio. Each site is a server-rendered Next.js app that ships its
// listing data in a `<script id="__NEXT_DATA__">` JSON blob — so instead of
// scraping HTML cards we just read the structured payload directly.
//
// Detection signals:
//
//   - `<script id="__NEXT_DATA__" type="application/json">{...}</script>`
//   - `props.pageProps.contents = { total, page, per_page, total_pages, data[] }`
//   - Each item has rich fields: id, title, publish_date, videos_duration,
//     short_description, description, thumb, tags, models, models_slugs,
//     trailer_url, views, site, nats_site_id.
//   - Detail links (`item.link`) all redirect through `join.{site}.com/signup/signup.php?nats=…`.
//     There is no public detail page — all metadata has to come from the
//     listing payload (which is fine: it's already complete).
//
// Sister sites not handled here:
//
//   - creampiethais.com, analjesse.com, mongerinasia.com, tailynn.com run an
//     older Elevated-X-Classic HTML template (`latestUpdateB` cards,
//     `data-setid`). They will get a separate package.
//   - tittiporn.com, hennessie.com are landing-only with no catalogue.
//
// Sort: the default `/videos` listing is `order_by=publish_date, sort_by=desc`,
// so date-sorted enumeration is the natural traversal — `KnownIDs` early-stop
// works.
package ghostpro

import (
	"context"
	"encoding/json"
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

const defaultStudioName = "Ghost Pro Productions"

// SiteConfig describes one Ghost Pro Next.js sister site. SiteBase has no
// trailing slash; SiteName is the human-readable site label shown on the
// "site" field of the JSON payload.
type SiteConfig struct {
	ID       string
	SiteBase string
	SiteName string
	Patterns []string
	MatchRe  *regexp.Regexp
	// StudioName overrides the network label stored as Scene.Studio. Empty
	// means "Ghost Pro Productions"; a site outside that network running the
	// same template (Severe Sex Films) names itself.
	StudioName string
}

func (c SiteConfig) studioName() string {
	if c.StudioName == "" {
		return defaultStudioName
	}
	return c.StudioName
}

func (s *Scraper) seriesName() string {
	if s.cfg.SiteName == s.cfg.studioName() {
		return ""
	}
	return s.cfg.SiteName
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
	return s.cfg.MatchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// nextDataBlockRe captures the JSON inside the Next.js hydration tag.
var nextDataBlockRe = regexp.MustCompile(
	`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`,
)

type nextDataPayload struct {
	Props struct {
		PageProps struct {
			Contents pageContents `json:"contents"`
		} `json:"pageProps"`
	} `json:"props"`
}

type pageContents struct {
	// Numeric pagination fields on the live API are inconsistent across
	// sites: some emit them as JSON integers, others as quoted strings.
	// flexInt accepts either form.
	Total      flexInt      `json:"total"`
	Page       flexInt      `json:"page"`
	PerPage    flexInt      `json:"per_page"`
	TotalPages flexInt      `json:"total_pages"`
	Data       []sceneEntry `json:"data"`
}

// flexInt unmarshals JSON values that may be either an integer (`42`) or a
// quoted decimal string (`"42"`). Unrecognised input zeroes the value
// silently — this is preferable to failing the whole listing parse for one
// odd field.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	// Strip surrounding quotes if it's a quoted number.
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		// Don't propagate — zero out so an oddly-typed pagination field
		// doesn't break the rest of the page.
		*f = 0
		return nil //nolint:nilerr // intentional: be lenient
	}
	*f = flexInt(n)
	return nil
}

// sceneEntry maps the fields we lift from each `data[i]`. The Ghost Pro
// payload includes a lot more (NATS codes, photo zips, extra thumbnails,
// trailer screencaps) — we keep only what feeds models.Scene.
type sceneEntry struct {
	ID               int      `json:"id"`
	Title            string   `json:"title"`
	Slug             string   `json:"slug"`
	PublishDate      string   `json:"publish_date"`    // "2006/01/02 15:04:05"
	VideosDuration   string   `json:"videos_duration"` // "MM:SS"
	ShortDescription string   `json:"short_description"`
	Description      string   `json:"description"`
	Thumb            string   `json:"thumb"`
	Tags             []string `json:"tags"`
	Models           []string `json:"models"`
	Views            flexInt  `json:"views"`
	// Link is the join.{site}.com paywall URL. Stash uses the public scene URL,
	// not the affiliate redirect, so we ignore Link and synthesize a stable
	// scene-anchor URL ourselves.
	Link string `json:"link"`
	// TrailerURL is the preview clip; surfaced as Scene.Preview when present.
	TrailerURL string `json:"trailer_url"`
	// ContentPrice is the per-scene price in USD, null on the Ghost Pro sites
	// (they sell membership only) and set on Severe Sex Films.
	ContentPrice *float64 `json:"content_price"`
}

func parseListing(body []byte) (*pageContents, error) {
	m := nextDataBlockRe.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("ghostpro: no __NEXT_DATA__ block")
	}
	var pl nextDataPayload
	if err := json.Unmarshal(m[1], &pl); err != nil {
		return nil, fmt.Errorf("ghostpro: parsing __NEXT_DATA__: %w", err)
	}
	return &pl.Props.PageProps.Contents, nil
}

func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		return s.cfg.SiteBase + "/videos"
	}
	return fmt.Sprintf("%s/videos?page=%d", s.cfg.SiteBase, page)
}

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "%s: scraping full catalog via __NEXT_DATA__", s.cfg.ID)

	now := time.Now().UTC()
	var totalPages int
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, s.listingURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		contents, err := parseListing(body)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(contents.Data) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			total = int(contents.Total)
			totalPages = int(contents.TotalPages)
		}

		scenes := make([]models.Scene, len(contents.Data))
		for i, entry := range contents.Data {
			scenes[i] = s.toScene(entry, now)
		}

		// Stop when total_pages tells us we're done. We still validate by
		// checking empty data on the next page (some sites mis-report
		// total_pages on the first page when caches are stale).
		done := totalPages > 0 && page >= totalPages
		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

// publishDateLayout matches "2026/05/24 00:00:00" as emitted in every Ghost
// Pro Next.js payload. Time-of-day is always 00:00:00 in practice but we
// parse it for completeness.
const publishDateLayout = "2006/01/02 15:04:05"

func (s *Scraper) toScene(e sceneEntry, now time.Time) models.Scene {
	id := strconv.Itoa(e.ID)

	scene := models.Scene{
		ID:        id,
		SiteID:    s.cfg.ID,
		StudioURL: s.cfg.SiteBase,
		Title:     strings.TrimSpace(e.Title),
		// No public detail page — synthesise a stable per-scene URL anchor for
		// downstream matching. Mirrors the pattern used by other listing-only
		// scrapers in FSS (e.g. extrememoviepass).
		URL:    fmt.Sprintf("%s/videos#scene-%s", s.cfg.SiteBase, id),
		Studio: s.cfg.studioName(),
		// Series names the site within the network. A site that is its own
		// studio has no series to record.
		Series:     s.seriesName(),
		ScrapedAt:  now,
		Preview:    e.TrailerURL,
		Thumbnail:  e.Thumb,
		Tags:       cleanTags(e.Tags),
		Performers: e.Models,
	}

	// Description: prefer the long form, fall back to short. Both ship with
	// HTML markup (`<strong>`, `<br>`, `&nbsp;`, `&rsquo;`); flatten it.
	desc := e.Description
	if strings.TrimSpace(desc) == "" {
		desc = e.ShortDescription
	}
	scene.Description = cleanHTML(desc)

	if d, err := time.Parse(publishDateLayout, e.PublishDate); err == nil {
		scene.Date = d.UTC()
	}
	if secs := parseDurationMMSS(e.VideosDuration); secs > 0 {
		scene.Duration = secs
	}
	if e.Views > 0 {
		scene.Views = int(e.Views)
	}
	// Only the sites that sell scenes individually quote a price; the rest
	// leave the field null, and a zero snapshot would record nothing.
	if e.ContentPrice != nil && *e.ContentPrice > 0 {
		scene.AddPrice(models.PriceSnapshot{Date: now, Regular: *e.ContentPrice})
	}

	return scene
}

var (
	// htmlTagRe matches any HTML tag (well-formed enough for these descriptions —
	// no script/style/comment edge cases on this CMS).
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
	// wsRe collapses any run of whitespace into a single space.
	wsRe = regexp.MustCompile(`\s+`)
)

// cleanHTML strips HTML tags from a string, decodes entities, and collapses
// whitespace. Returns "" for blank input.
func cleanHTML(s string) string {
	if s == "" {
		return ""
	}
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// parseDurationMMSS converts "MM:SS" or "H:MM:SS" to seconds. Returns 0 for
// the empty string or any unparseable input.
func parseDurationMMSS(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		nums[i] = n
	}
	switch len(nums) {
	case 2:
		return nums[0]*60 + nums[1]
	case 3:
		return nums[0]*3600 + nums[1]*60 + nums[2]
	}
	return 0
}

// cleanTags strips the boilerplate tags that every Ghost Pro entry carries
// regardless of content ("Photos", "Movies", "Sites", "Updates", "Tour
// Updates", "Set Updates", and the per-site domain tag). Keeps anything that
// looks like a real content tag.
func cleanTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	skip := map[string]bool{
		"photos":       true,
		"movies":       true,
		"sites":        true,
		"updates":      true,
		"tour updates": true,
		"set updates":  true,
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		clean := strings.TrimSpace(t)
		if clean == "" {
			continue
		}
		lc := strings.ToLower(clean)
		if skip[lc] || strings.HasSuffix(lc, ".com") {
			continue
		}
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

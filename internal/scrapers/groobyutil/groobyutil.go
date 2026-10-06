package groobyutil

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	TourPrefix string // "/tour" or "" for sites without /tour/
	AltDomains []string
}

type Scraper struct {
	cfg    SiteConfig
	client *http.Client
	base   string
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		client: httpx.NewClient(30 * time.Second),
		base:   "https://" + hostFor(cfg.Domain),
	}
}

// hostFor prefixes `www.` only for a bare apex. Every Grooby domain registered
// so far is two labels (`tgirls.porn`, `braziltgirls.xxx`), and all of them
// serve the tour from `www.`; a domain that already names a subdomain does not
// — `www.tour.transerotica.com` resolves to a certificate for another host
// entirely and fails the handshake.
func hostFor(domain string) string {
	if strings.Count(domain, ".") == 1 {
		return "www." + domain
	}
	return domain
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	d := s.cfg.Domain
	prefix := s.cfg.TourPrefix
	return []string{
		d + prefix + "/categories/movies.html",
		d + prefix + "/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	return scraper.HostMatches(u, append([]string{s.cfg.Domain}, s.cfg.AltDomains...)...)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// Two card shapes are in play across the network. The older one is a bare
	// `<div class="sexyvideo">`; the newer wraps it in `sexyvideo_outer`
	// together with the `modelname` block, so the performer, and on most of
	// those sites the date and duration, sit *outside* the inner div. Chunking
	// on the inner card there silently dropped all three on 26 of the 42
	// registered sites — 488 of 488 scenes on uk-tgirls had no performer, no
	// date and no duration. outerCardRe is preferred when the page has any.
	outerCardRe  = regexp.MustCompile(`<div class="sexyvideo_outer"`)
	cardRe       = regexp.MustCompile(`<div class="sexyvideo"`)
	sceneIDRe    = regexp.MustCompile(`id="set-target-(\d+)"`)
	comingSoonRe = regexp.MustCompile(`class="comingsoon"`)
	titleRe      = regexp.MustCompile(`(?s)<h4>\s*<a[^>]+title="([^"]+)"`)
	sceneURLRe   = regexp.MustCompile(`(?s)<h4>\s*<a\s+href="([^"]+)"`)
	thumbRe      = regexp.MustCompile(`<img[^>]*class="[^"]*mainThumb[^"]*"[^>]*src="([^"]+)"`)
	// `(?:&nbsp;|\s)*` because the newer template separates the icon from the
	// runtime with two non-breaking spaces rather than whitespace.
	durationRe = regexp.MustCompile(`<i class='fas fa-video'></i>(?:&nbsp;|\s)*<div[^>]*>(\d+:\d{2}(?::\d{2})?)`)
	// The older template wraps the name in a <span>; the newer writes it as the
	// anchor text and puts a site-logo <img> before the anchor. Matching the
	// model link itself covers both without a second pattern per field.
	performerRe = regexp.MustCompile(`(?s)<div class="modelname">.*?<a[^>]+href="[^"]*/models/[^"]*"[^>]*>(?:\s*<span[^>]*>)?([^<]+)`)
	descRe      = regexp.MustCompile(`<p class="photodesc">([^<]+)</p>`)
	// `fa-calendar` on the older template, `fa-calendar-check` on the newer.
	dateRe    = regexp.MustCompile(`<i class='far fa-calendar(?:-check)?'[^>]*></i>\s*(\d{1,2}(?:st|nd|rd|th)\s+\w+\s+\d{4})`)
	maxPageRe = regexp.MustCompile(`movies_(\d+)_d\.html`)

	modelSlugRe = regexp.MustCompile(`/models/([^_/.]+?)(?:\.html)?$`)
)

type sceneItem struct {
	id          string
	title       string
	url         string
	thumb       string
	date        time.Time
	duration    int
	performers  []string
	description string
}

// cardEnd returns the offset just past the card container opening at `start`, by
// matching <div>/</div> nesting depth. `limit` is the start of the next card, or the
// end of the page for the last one.
//
// Without this the last card's block ran to len(page), swallowing the footer and
// sidebar. Any `class="comingsoon"` promo element anywhere outside the grid then made
// the Coming-Soon check fire on the block remainder and silently drop the last real
// scene of *every* listing page — a steady 1-in-N loss that `--full` turns into a
// hard delete.
//
// Falls back to `limit` if the markup is unbalanced, which is the previous behaviour:
// a parse that cannot find the container end should not start dropping cards.
func cardEnd(page string, start, limit int) int {
	depth := 0
	i := start
	for i < limit {
		open := strings.Index(page[i:limit], "<div")
		closeIdx := strings.Index(page[i:limit], "</div>")
		switch {
		case closeIdx < 0:
			return limit
		case open >= 0 && open < closeIdx:
			depth++
			i += open + len("<div")
		default:
			depth--
			i += closeIdx + len("</div>")
			if depth == 0 {
				return i
			}
		}
	}
	return limit
}

func parseListingPage(body []byte) []sceneItem {
	page := string(body)
	// Prefer the outer wrapper where the template uses one: it is the whole
	// card. The inner div alone excludes the credit block that precedes it.
	starts := outerCardRe.FindAllStringIndex(page, -1)
	if len(starts) == 0 {
		starts = cardRe.FindAllStringIndex(page, -1)
	}
	items := make([]sceneItem, 0, len(starts))

	for i, loc := range starts {
		start := loc[0]
		limit := len(page)
		if i+1 < len(starts) {
			limit = starts[i+1][0]
		}
		block := page[start:cardEnd(page, start, limit)]

		// Unreleased scenes are rendered as a "Coming Soon!" card with a
		// countdown and no trailer link, so they have no title and no URL.
		// Emitting them would produce scenes with empty required fields.
		if comingSoonRe.MatchString(block) {
			continue
		}

		var item sceneItem

		if m := sceneIDRe.FindStringSubmatch(block); m != nil {
			item.id = m[1]
		}
		if item.id == "" {
			continue
		}

		if m := titleRe.FindStringSubmatch(block); m != nil {
			item.title = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := sceneURLRe.FindStringSubmatch(block); m != nil {
			item.url = strings.TrimSpace(m[1])
		}

		if m := thumbRe.FindStringSubmatch(block); m != nil {
			item.thumb = m[1]
		}

		if m := durationRe.FindStringSubmatch(block); m != nil {
			item.duration = parseutil.ParseDurationColon(m[1])
		}

		for _, m := range performerRe.FindAllStringSubmatch(block, -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name != "" {
				item.performers = append(item.performers, name)
			}
		}

		if m := descRe.FindStringSubmatch(block); m != nil {
			item.description = strings.TrimSpace(html.UnescapeString(m[1]))
		}

		if m := dateRe.FindStringSubmatch(block); m != nil {
			item.date = parseGroobyDate(m[1])
		}

		items = append(items, item)
	}
	return items
}

// parseGroobyDate parses "8th May 2026" or "5th Jun 2026" → time.Time.
func parseGroobyDate(s string) time.Time {
	cleaned := parseutil.StripOrdinalSuffix(s)
	for _, layout := range []string{"2 Jan 2006", "2 January 2006"} {
		if t, err := time.Parse(layout, cleaned); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
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

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	base := s.base

	if modelSlugRe.MatchString(studioURL) {
		scraper.Debugf(1, "%s: detected model page", s.cfg.SiteID)
		s.scrapeModelPage(ctx, studioURL, opts, out, now, base)
		return
	}

	s.scrapeListingPages(ctx, studioURL, opts, out, now, base)
}

func (s *Scraper) scrapeModelPage(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time, base string) {
	pageURL := studioURL
	if !strings.HasPrefix(pageURL, "http") {
		pageURL = base + pageURL
	}

	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	scenes := parseListingPage(body)
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: found %d scenes on model page", s.cfg.SiteID, len(scenes))

	select {
	case out <- scraper.Progress(len(scenes)):
	case <-ctx.Done():
		return
	}

	for _, item := range scenes {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(item.toScene(s.cfg.SiteID, s.cfg.StudioName, base, studioURL, now)):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) scrapeListingPages(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time, base string) {
	firstPage := true
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s/categories/movies_%d_d.html", base, s.cfg.TourPrefix, page)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListingPage(body)

		var total int
		if firstPage {
			firstPage = false
			total = estimateTotal(body, len(items))
		}

		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = item.toScene(s.cfg.SiteID, s.cfg.StudioName, base, studioURL, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

func (item sceneItem) toScene(siteID, studio, base, studioURL string, now time.Time) models.Scene {
	url := absolutize(item.url, base)
	thumb := absolutize(item.thumb, base)
	return models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         url,
		Thumbnail:   thumb,
		Date:        item.date,
		Duration:    item.duration,
		Performers:  item.performers,
		Description: item.description,
		Studio:      studio,
		ScrapedAt:   now,
	}
}

// affiliateTrailerRe matches the NATS tracking form some tours link scenes
// through: `https://join.<site>/track/<nats-code>/trailers/<slug>.html`.
var affiliateTrailerRe = regexp.MustCompile(`^https?://join\.[^/]+/track/[^/]*/(trailers/[^"?#]+\.html)$`)

// absolutize resolves a card's href against the tour's origin.
//
// Two shapes need rewriting. The newer template writes scheme-relative links
// (`//tour.example.com/trailers/x.html`), which a plain base+path join turns
// into `https://host//host/trailers/x.html`. The performer sub-tours instead
// link every card through the NATS affiliate redirect, which is a billing URL
// carrying a tracking code rather than the scene's address — the tour serves
// the same scene at its own `/trailers/{slug}.html`, so that is what is stored.
func absolutize(ref, base string) string {
	switch {
	case ref == "":
		return ""
	case strings.HasPrefix(ref, "//"):
		scheme := "https:"
		if i := strings.Index(base, "//"); i > 0 {
			scheme = base[:i]
		}
		return scheme + ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		if m := affiliateTrailerRe.FindStringSubmatch(ref); m != nil {
			return base + "/" + m[1]
		}
		return ref
	}
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

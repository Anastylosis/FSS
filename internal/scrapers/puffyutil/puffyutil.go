// Package puffyutil scrapes the Puffy + VIPissy network — eight sites that all
// run the same bespoke PHP CMS (Wet and Puffy, Wet and Pissy, We Like To Suck,
// Simply Anal, VIPissy, Fister Twister, Pee On Her, VirtualPee).
//
// Every site exposes an anonymous, paginated listing at
// {SiteBase}/{listingPath}/ (and /{listingPath}/page-{N}/) whose cards link to
// per-scene detail pages. The CMS has drifted across the network, so the
// listing card and detail markup differ from site to site. The common,
// reliable signal is the scene URL itself: the slug encodes the scene title.
// We harvest unique scene URLs from the listing (universal), then enrich each
// scene from its detail page with a small worker pool, parsing the several
// observed template dialects with cascading fall-backs.
package puffyutil

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

const defaultWorkers = 4

// SiteConfig describes one network site. Only these fields differ between the
// eight sites.
type SiteConfig struct {
	ID          string
	Studio      string
	SiteBase    string // e.g. "https://wetandpuffy.com" (no trailing slash)
	ListingPath string // "videos" or "updates"
	ScenePrefix string // "" or "video-" — the slug prefix used by this site
	Patterns    []string
	MatchRe     *regexp.Regexp
}

// Scraper implements scraper.StudioScraper for one network site.
type Scraper struct {
	cfg     SiteConfig
	sceneRe *regexp.Regexp
	Client  *http.Client
}

// New builds a Scraper for the given site config.
func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		sceneRe: regexp.MustCompile(`(?i)/` + regexp.QuoteMeta(cfg.ListingPath) + `/((?:video-)?[a-z0-9][a-z0-9-]*)/?(?:["'?#]|$)`),
		Client:  httpx.NewClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string               { return s.cfg.ID }
func (s *Scraper) Patterns() []string       { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	anchorRe    = regexp.MustCompile(`(?is)<a\b[^>]*>`)
	hrefRe      = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)
	titleAttrRe = regexp.MustCompile(`(?i)title=["']([^"']*)["']`)

	ogImageRe = regexp.MustCompile(`(?i)<meta\s+property=["']og:image["']\s+content=["']([^"']+)["']`)

	// Family A (<b>) and Fister Twister (<strong>) share these labels.
	releasedRe  = regexp.MustCompile(`(?is)Released on:\s*<(?:b|strong)>\s*([^<]+?)\s*</(?:b|strong)>`)
	durationRe  = regexp.MustCompile(`(?is)Duration:\s*<(?:b|strong)>\s*([^<]+?)\s*</(?:b|strong)>`)
	featuringRe = regexp.MustCompile(`(?is)Featuring:\s*<(?:b|strong)>(.*?)</(?:b|strong)>`)

	// VirtualPee: <h2 class="video_title"> … </h2> and <li class="vid_duration">.
	videoTitleRe  = regexp.MustCompile(`(?is)class=["']video_title["'][^>]*>(.*?)</h2>`)
	vidDurationRe = regexp.MustCompile(`(?i)class=["']vid_duration["'][^>]*>\s*([0-9:]+)`)

	girlsLinkRe = regexp.MustCompile(`(?is)/girls/[a-z0-9-]+/?["'][^>]*>\s*([^<]+?)\s*</a>`)

	descRe     = regexp.MustCompile(`(?is)class=["'][^"']*movie-description[^"']*["'][^>]*>(.*?)</`)
	metaDescRe = regexp.MustCompile(`(?i)<meta\s+name=["']description["']\s+content=["']([^"']*)["']`)

	dateSpanRe = regexp.MustCompile(`(?is)class=["'][^"']*\bdate\b[^"']*["'][^>]*>\s*([^<]+?)\s*<`)
	dateMDYRe  = regexp.MustCompile(`\b(\d{2}/\d{2}/\d{4})\b`)
	dateTxtRe  = regexp.MustCompile(`\b([A-Z][a-z]{2}\s+\d{1,2},\s+\d{4})\b`)
	thumbRe    = regexp.MustCompile(`(?i)["'](https?:)?//media\.[^"']+/cover/[^"']+\.jpg`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)

	// Listing-card duration (e.g. VIPissy "glyphicon-time … 28:10").
	durHintRe = regexp.MustCompile(`(?is)(?:glyphicon-time|fa-clock|time-counter|vid_duration).{0,80}?(\d{1,2}:\d{2}(?::\d{2})?)`)
	// Marker after which detail-page links belong to "related / more videos".
	relatedRe = regexp.MustCompile(`(?i)(related|more videos|more from|also like|you may)`)
)

// denySlug holds path segments that look like a scene slug but are navigation.
var denySlug = map[string]bool{
	"categories": true, "category": true, "tags": true, "tag": true,
	"models": true, "girls": true, "page": true, "search": true,
}

type card struct {
	slug, url, title, dateHint, thumbHint, durHint string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	now := time.Now().UTC()
	seen := make(map[string]bool)

	scraper.Debugf(1, "%s: scraping %s/%s/ with %d detail workers", s.cfg.ID, s.cfg.SiteBase, s.cfg.ListingPath, workers)

	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/%s/", s.cfg.SiteBase, s.cfg.ListingPath)
		if page > 1 {
			pageURL = fmt.Sprintf("%s/%s/page-%d/", s.cfg.SiteBase, s.cfg.ListingPath, page)
		}
		body, err := s.get(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := s.parseListing(string(body))
		fresh := cards[:0]
		for _, c := range cards {
			if !seen[c.slug] {
				seen[c.slug] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scraper.Debugf(1, "%s: page %d yielded %d new scenes", s.cfg.ID, page, len(fresh))
		scenes := s.enrich(ctx, studioURL, fresh, now, workers, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// parseListing extracts unique scene cards from a listing page. Each scene URL
// is harvested universally; per-card title/date/thumbnail are best-effort from
// the surrounding HTML window.
func (s *Scraper) parseListing(body string) []card {
	type hit struct {
		slug, url string
		idx       int
	}
	var hits []hit
	seen := make(map[string]bool)
	for _, loc := range anchorRe.FindAllStringIndex(body, -1) {
		tag := body[loc[0]:loc[1]]
		hm := hrefRe.FindStringSubmatch(tag)
		if hm == nil {
			continue
		}
		sm := s.sceneRe.FindStringSubmatch(hm[1])
		if sm == nil {
			continue
		}
		slug := strings.ToLower(sm[1])
		if denySlug[slug] || strings.HasPrefix(slug, "page-") || seen[slug] {
			continue
		}
		seen[slug] = true
		hits = append(hits, hit{slug: slug, url: s.absURL(hm[1]), idx: loc[0]})
	}

	cards := make([]card, 0, len(hits))
	for i, h := range hits {
		end := len(body)
		if i+1 < len(hits) {
			end = hits[i+1].idx
		}
		if end > h.idx+2000 {
			end = h.idx + 2000
		}
		window := body[h.idx:end]
		c := card{slug: h.slug, url: h.url}
		// Title: prefer a title="" attribute on the scene anchor in this window.
		if m := titleAttrRe.FindStringSubmatch(body[h.idx : h.idx+min(400, len(body)-h.idx)]); m != nil {
			c.title = cleanText(m[1])
		}
		// Try each date pattern and keep the first that actually parses — the
		// class="date" span is unreliable (some sites use it for a "4K" badge).
		for _, cand := range []string{
			firstSubmatch(dateSpanRe, window),
			firstSubmatch(dateTxtRe, window),
			firstSubmatch(dateMDYRe, window),
		} {
			if !parseDate(cand).IsZero() {
				c.dateHint = cand
				break
			}
		}
		if m := thumbRe.FindString(window); m != "" {
			c.thumbHint = normalizeURL(strings.Trim(m, `"'`))
		}
		if m := durHintRe.FindStringSubmatch(window); m != nil {
			c.durHint = m[1]
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) enrich(ctx context.Context, studioURL string, cards []card, now time.Time, workers int, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		wg.Add(1)
		go func(i int, c card) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, delay) {
				return
			}
			scenes[i] = s.toScene(ctx, studioURL, c, now)
		}(i, c)
	}
	wg.Wait()
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, c card, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        c.slug,
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		URL:       c.url,
		Title:     c.title,
		Thumbnail: c.thumbHint,
		Studio:    s.cfg.Studio,
		ScrapedAt: now,
	}
	if scene.Title == "" {
		scene.Title = s.titleFromSlug(c.slug)
	}
	scene.Date = parseDate(c.dateHint)

	body, err := s.get(ctx, c.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := ogImageRe.FindStringSubmatch(detail); m != nil {
		scene.Thumbnail = normalizeURL(m[1])
	}
	if d := parseDate(firstSubmatch(releasedRe, detail)); !d.IsZero() {
		scene.Date = d
	}
	scene.Duration = parseDuration(detail)
	if scene.Duration == 0 {
		scene.Duration = parseDurationValue(c.durHint)
	}
	scene.Performers = parsePerformers(detail)
	if desc := parseDescription(detail); desc != "" {
		scene.Description = desc
	}
	return scene
}

// titleFromSlug derives a human title from a URL slug, e.g.
// "video-wet-yoga" -> "Wet Yoga".
func (s *Scraper) titleFromSlug(slug string) string {
	t := strings.TrimPrefix(slug, s.cfg.ScenePrefix)
	t = strings.TrimPrefix(t, "video-")
	parts := strings.Split(t, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (s *Scraper) absURL(u string) string {
	u = strings.TrimSpace(u)
	switch {
	case strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return s.cfg.SiteBase + u
	default:
		return s.cfg.SiteBase + "/" + u
	}
}

func parseDuration(detail string) int {
	if m := durationRe.FindStringSubmatch(detail); m != nil {
		return parseDurationValue(m[1])
	}
	if m := vidDurationRe.FindStringSubmatch(detail); m != nil {
		return parseDurationValue(m[1])
	}
	return 0
}

// parseDurationValue handles "27' 45”" (MM' SS”), "33:33" (MM:SS / HH:MM:SS)
// and "n/a".
func parseDurationValue(v string) int {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "'") {
		nums := regexp.MustCompile(`\d+`).FindAllString(v, -1)
		if len(nums) >= 2 {
			return atoi(nums[0])*60 + atoi(nums[1])
		}
		if len(nums) == 1 {
			return atoi(nums[0]) * 60
		}
		return 0
	}
	if strings.Contains(v, ":") {
		return parseutil.ParseDurationColon(v)
	}
	return 0
}

func parsePerformers(detail string) []string {
	block := firstSubmatch(featuringRe, detail)
	if block == "" {
		block = firstSubmatch(videoTitleRe, detail)
	}
	if block == "" {
		// VIPissy/Pee On Her variant: no labelled block. Take /girls/ links from
		// the main content only — truncate at the "related videos" sidebar so we
		// don't pick up every model in the recommendations.
		block = detail
		if loc := relatedRe.FindStringIndex(detail); loc != nil {
			block = detail[:loc[0]]
		}
	}
	if block == "" {
		return nil
	}
	var names []string
	seen := make(map[string]bool)
	for _, m := range girlsLinkRe.FindAllStringSubmatch(block, -1) {
		name := cleanText(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func parseDescription(detail string) string {
	if m := descRe.FindStringSubmatch(detail); m != nil {
		if d := cleanText(m[1]); d != "" {
			return d
		}
	}
	if m := metaDescRe.FindStringSubmatch(detail); m != nil {
		return cleanText(m[1])
	}
	return ""
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	d, _ := parseutil.TryParseDate(s, "Jan 2, 2006", "Jan. 2, 2006", "01/02/2006", "1/2/2006")
	return d
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

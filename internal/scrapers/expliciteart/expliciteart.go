// Package expliciteart scrapes explicite-art.com's public visitor tour.
// See docs/scrapers.md.
package expliciteart

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
	siteID     = "expliciteart"
	studioName = "Explicite Art"
	// detailWorkers caps the per-page detail pool; the listing serves 50 cards
	// a page and every field but the title is on the scene's own page.
	detailWorkers = 4
	// addedCapDays is the value the tour prints for anything older than a
	// year. It is a cap, not a date, so such scenes are stored undated.
	addedCapDays = 365
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?explicite-art\.com(?:/|$)`)
	pageRe  = regexp.MustCompile(`page\d+\.html$`)

	cardRe = regexp.MustCompile(`(?s)<a href="([^"]*/visitor/video/[^"?#]+-(\d+)\.html)"><img[^>]+src="([^"]+)"[^>]*alt="([^"]*)"`)

	titleRe   = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
	descRe    = regexp.MustCompile(`(?s)<div class="player-info-desc">(.*?)</div>`)
	runtimeRe = regexp.MustCompile(`RUNTIME</span>\s*([0-9:]+)`)
	viewsRe   = regexp.MustCompile(`VIEWS</span>\s*(\d+)`)
	addedRe   = regexp.MustCompile(`ADDED</span>\s*(\d+)\s*(day|days|hour|hours|month|months)\s*ago`)
	catsRe    = regexp.MustCompile(`(?s)CATEGORIES</span>\s*<span class="tags">(.*?)</span>`)
	tagsRe    = regexp.MustCompile(`(?s)TAGS</span>\s*<span class="tags">(.*?)</span>`)
	starsRe   = regexp.MustCompile(`(?s)PORN ACTRESS IN THE VIDEO</span>(.*?)</div>`)
	playerRe  = regexp.MustCompile(`file:\s*"([^"]+)"`)
	posterRe  = regexp.MustCompile(`image:\s*"([^"]+)"`)

	anchorTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for explicite-art.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Explicite Art scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.explicite-art.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"explicite-art.com",
		"explicite-art.com/visitor/channel/{id}/{slug}/",
		"explicite-art.com/visitor/search/{tag}/",
		"explicite-art.com/visitor/pornstars/{slug}-{id}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry. The card's `alt` is the scene's clean title; the
// neighbouring `vtitle` prepends the performer's name.
type card struct {
	id    string
	path  string
	title string
	thumb string
}

// listing describes what an operator URL selects: a directory that paginates
// with pageN.html, or a single page (a performer's own page does not paginate).
type listing struct {
	prefix string
	single bool
}

// resolveListing maps an operator URL onto its listing. Anything that is not a
// channel, search or performer page walks the whole video index.
func resolveListing(studioURL string) listing {
	u, err := url.Parse(studioURL)
	if err != nil {
		return listing{prefix: "/visitor/videos/"}
	}
	p := u.Path
	switch {
	case strings.HasPrefix(p, "/visitor/channel/"), strings.HasPrefix(p, "/visitor/search/"):
		return listing{prefix: dirOf(p)}
	case strings.HasPrefix(p, "/visitor/pornstars/") && strings.HasSuffix(p, ".html"):
		return listing{prefix: p, single: true}
	default:
		return listing{prefix: "/visitor/videos/"}
	}
}

// dirOf trims a trailing pageN.html and guarantees a trailing slash.
func dirOf(p string) string {
	if pageRe.MatchString(p) {
		p = p[:strings.LastIndex(p, "/")+1]
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	l := resolveListing(studioURL)
	scraper.Debugf(1, "%s: listing %s (single page: %v)", siteID, l.prefix, l.single)

	// The tour orders by neither date nor id, so a known scene says nothing
	// about what follows it: the early-stop hint is dropped rather than
	// truncating the walk at an arbitrary point.
	opts.KnownIDs = nil

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + l.prefix
		if !l.single {
			pageURL = fmt.Sprintf("%s%spage%d.html", s.base, l.prefix, page)
		}
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			fresh = append(fresh, c)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes, opts, out, now)
		return scraper.PageResult{Scenes: scenes, Done: l.single}, nil
	})
}

// parseListing reads the listing's scene cards.
func parseListing(body string) []card {
	ms := cardRe.FindAllStringSubmatch(body, -1)
	cards := make([]card, 0, len(ms))
	for _, m := range ms {
		u, err := url.Parse(html.UnescapeString(m[1]))
		if err != nil {
			continue
		}
		cards = append(cards, card{
			id:    m[2],
			path:  u.Path,
			thumb: html.UnescapeString(m[3]),
			title: cleanText(m[4]),
		})
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     c.title,
		URL:       s.base + c.path,
		Thumbnail: resolveURL(s.base, c.thumb),
		ScrapedAt: now,
	}
}

// enrich fetches each scene's page, which carries everything but the title.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body, s.base, now)
		}(i)
	}
	wg.Wait()
}

// applyDetail folds the scene's own page into the record.
func applyDetail(sc *models.Scene, body, base string, now time.Time) {
	if t := titleRe.FindStringSubmatch(body); t != nil {
		if title := cleanText(t[1]); title != "" {
			sc.Title = title
		}
	}
	if d := descRe.FindStringSubmatch(body); d != nil {
		sc.Description = cleanText(d[1])
	}
	if r := runtimeRe.FindStringSubmatch(body); r != nil {
		sc.Duration = parseRuntime(r[1])
	}
	if v := viewsRe.FindStringSubmatch(body); v != nil {
		sc.Views, _ = strconv.Atoi(v[1])
	}
	if c := catsRe.FindStringSubmatch(body); c != nil {
		sc.Categories = anchorTexts(c[1])
	}
	if t := tagsRe.FindStringSubmatch(body); t != nil {
		sc.Tags = anchorTexts(t[1])
	}
	if p := starsRe.FindStringSubmatch(body); p != nil {
		sc.Performers = anchorTexts(p[1])
	}
	if d, ok := parseAdded(body, now); ok {
		sc.Date = d
	}
	if p := playerRe.FindStringSubmatch(body); p != nil {
		sc.Preview = resolveURL(base, html.UnescapeString(p[1]))
	}
	if p := posterRe.FindStringSubmatch(body); p != nil {
		sc.Thumbnail = resolveURL(base, html.UnescapeString(p[1]))
	}
}

// parseRuntime reads the tour's RUNTIME field, which is H:MM:SS, MM:SS, or a
// bare count of minutes.
func parseRuntime(v string) int {
	if strings.Contains(v, ":") {
		return parseutil.ParseDurationColon(v)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n * 60
}

// parseAdded turns the tour's relative ADDED value into a date. The tour caps
// it at "365 days ago" for everything older than a year, so that value is a
// cap rather than a date and yields none.
func parseAdded(body string, now time.Time) (time.Time, bool) {
	m := addedRe.FindStringSubmatch(body)
	if m == nil {
		return time.Time{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	var d time.Duration
	switch strings.TrimSuffix(m[2], "s") {
	case "hour":
		d = time.Duration(n) * time.Hour
	case "day":
		if n >= addedCapDays {
			return time.Time{}, false
		}
		d = time.Duration(n) * 24 * time.Hour
	case "month":
		d = time.Duration(n) * 30 * 24 * time.Hour
	default:
		return time.Time{}, false
	}
	t := now.Add(-d)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
}

func anchorTexts(span string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range anchorTextRe.FindAllStringSubmatch(span, -1) {
		t := cleanText(m[1])
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func resolveURL(base, ref string) string {
	switch {
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return base + "/" + ref
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

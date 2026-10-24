// Package concoxxxion scrapes Conor Coxxx (conorcoxxx.com) and Horny Household
// (hornyhousehold.com), Concoxxxion's two AdultEmpire "EWC" white-label sites.
//
// Both sites share one scene catalogue (same IDs on either domain) and the
// same listing skin, which differs from spankmonster and helixstudios. Besides
// the clip listings it scrapes the full-DVD movie list and performer pages.
package concoxxxion

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "concoxxxion"
	defaultStudio = "Concoxxxion"
	detailWorkers = 4
	dateLayout    = "Jan 2, 2006"
	newestPath    = "/watch-newest-clips-and-scenes.html"
	moviesPath    = "/watch-newest-concoxxxion-movies.html"
	moviesStudio  = "95763"
	moviesOrigin  = "https://www.conorcoxxx.com"
	movieIDPrefix = "movie-"
)

// siteBase overrides the origin taken from the studio URL (tests only).
var siteBase string

// Scraper implements scraper.StudioScraper for Concoxxxion.
type Scraper struct {
	Client *http.Client
}

// New constructs a Concoxxxion scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"hornyhousehold.com",
		"hornyhousehold.com/watch-newest-clips-and-scenes.html",
		"conorcoxxx.com",
		"conorcoxxx.com/conor-coxxx-clips.html",
		"conorcoxxx.com/horny-household-clips.html",
		"conorcoxxx.com/watch-newest-clips-and-scenes.html",
		"conorcoxxx.com/watch-newest-concoxxxion-movies.html",
		"conorcoxxx.com/{id}/{slug}-streaming-pornstar-videos.html",
		"conorcoxxx.com/scenes/{id}/{slug}-streaming-pornstar-videos.html",
		"hornyhousehold.com/watch-newest-concoxxxion-movies.html",
		"hornyhousehold.com/{id}/{slug}-streaming-pornstar-videos.html",
		"hornyhousehold.com/scenes/{id}/{slug}-streaming-pornstar-videos.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:hornyhousehold|conorcoxxx)\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

type mode int

const (
	modeClips mode = iota
	modeMovies
	modePerformer
)

// target is what a studio URL resolves to: an origin, a listing path and how
// to read the cards on it.
type target struct {
	base, path string
	mode       mode
}

var performerPathRe = regexp.MustCompile(`^/(?:scenes/)?(\d+)/([^/]+)-streaming-pornstar-videos\.html$`)

func resolveTarget(studioURL string) target {
	u, err := url.Parse(studioURL)
	if err != nil || u.Host == "" {
		return target{base: "https://hornyhousehold.com", path: newestPath}
	}
	base := u.Scheme + "://" + u.Host
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if u.Path == moviesPath && host != "conorcoxxx.com" {
		// Horny Household serves an error page here; the list lives on Conor Coxxx.
		base = moviesOrigin
	}
	if siteBase != "" {
		base = siteBase
	}
	switch {
	case u.Path == moviesPath:
		return target{base: base, path: moviesPath, mode: modeMovies}
	case performerPathRe.MatchString(u.Path):
		// The bare performer page does not paginate its scenes; the /scenes/ tab does.
		m := performerPathRe.FindStringSubmatch(u.Path)
		return target{base: base, path: "/scenes/" + m[1] + "/" + m[2] + "-streaming-pornstar-videos.html", mode: modePerformer}
	case u.Path == newestPath,
		host == "conorcoxxx.com" && (u.Path == "/conor-coxxx-clips.html" || u.Path == "/horny-household-clips.html"):
		return target{base: base, path: u.Path}
	case host == "conorcoxxx.com":
		return target{base: base, path: "/conor-coxxx-clips.html"}
	}
	return target{base: base, path: newestPath}
}

func (t target) pageURL(page int) string {
	if t.mode == modeMovies {
		// Unfiltered, this path lists the whole AdultEmpire marketplace.
		return fmt.Sprintf("%s%s?studio=%s&page=%d", t.base, t.path, moviesStudio, page)
	}
	return fmt.Sprintf("%s%s?page=%d", t.base, t.path, page)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	t := resolveTarget(studioURL)
	switch t.mode {
	case modeMovies:
		scraper.Debugf(1, "%s: scraping movie list", siteID)
	case modePerformer:
		scraper.Debugf(1, "%s: scraping performer page %s", siteID, t.path)
	}
	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, t.pageURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		var items []listItem
		if t.mode == modeMovies {
			items = parseMovies(body, t.base)
		} else {
			items = parseListing(body, t.base)
		}
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// ---- listing ----

var (
	// The CMS wraps attributes across lines, so pairs tolerate any whitespace.
	cardRe      = regexp.MustCompile(`<div class="grid-item"\s+id="ascene_(\d+)"`)
	linkRe      = regexp.MustCompile(`<a class="scene-img"\s+href="(/[^"]+)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<a class="scene-title".*?<h6>\s*(.*?)\s*</h6>`)
	cardPerfRe  = regexp.MustCompile(`(?s)<p class="scene-performer-names">\s*(.*?)\s*</p>`)
	cardLenRe   = regexp.MustCompile(`(?s)<p class="scene-length">\s*([^<]+)`)
	thumbRe     = regexp.MustCompile(`data-srcset="(https://[^"\s]+)\s+700w`)
)

type listItem struct {
	id, url, title, thumb string
	performers            []string
	minutes               int
}

func parseListing(body []byte, base string) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := page[loc[0]:end]

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{id: page[loc[2]:loc[3]], url: base + m[1]}

		if t := cardTitleRe.FindStringSubmatch(card); t != nil {
			it.title = cleanText(t[1])
		}
		if p := cardPerfRe.FindStringSubmatch(card); p != nil {
			for _, name := range strings.Split(cleanText(markupRe.ReplaceAllString(p[1], "")), ",") {
				if name = strings.TrimSpace(name); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}
		if l := cardLenRe.FindStringSubmatch(card); l != nil {
			it.minutes = parseMinutes(l[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = html.UnescapeString(th[1])
		}
		items = append(items, it)
	}
	return items
}

var (
	movieCardRe  = regexp.MustCompile(`<div id="item_(\d+)"><div class="grid-item">`)
	movieLinkRe  = regexp.MustCompile(`<a class="boxcover"\s+href="/shop/(\d+)/([^"]+)-porn-videos\.html"`)
	movieTitleRe = regexp.MustCompile(`alt="[^"]*"\s+title="([^"]*)"`)
	movieThumbRe = regexp.MustCompile(`data-src="(https://[^"\s]+/movie/\d+\?w=500)"`)
)

// parseMovies reads the boxcover grid of the movie list. Movies get the
// streaming URL (the /shop/ link is the DVD store page) and a prefixed ID, as
// they are products rather than scenes.
func parseMovies(body []byte, base string) []listItem {
	page := string(body)
	locs := movieCardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := page[loc[0]:end]
		m := movieLinkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			id:  movieIDPrefix + m[1],
			url: base + "/" + m[1] + "/" + m[2] + "-streaming-porn-videos.html",
		}
		if t := movieTitleRe.FindStringSubmatch(card); t != nil {
			it.title = cleanText(t[1])
		}
		if th := movieThumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = html.UnescapeString(th[1])
		}
		items = append(items, it)
	}
	return items
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
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
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

var (
	titleRe     = regexp.MustCompile(`(?s)<h1 class="description">\s*(.*?)\s*</h1>`)
	synopsisRe  = regexp.MustCompile(`(?s)<div class="synopsis">\s*(.*?)\s*</div>`)
	releasedRe  = regexp.MustCompile(`(?s)Released:</span\s*>\s*([A-Z][a-z]{2} \d{1,2}, \d{4})`)
	lengthRe    = regexp.MustCompile(`(?s)Length:</span>\s*([^<]+)`)
	directorRe  = regexp.MustCompile(`(?s)Director:</span>\s*(?:<a[^>]*>)?\s*([^<]+?)\s*<`)
	studioRe    = regexp.MustCompile(`(?s)Studio:</span>\s*<(?:span|a[^>]*)>\s*([^<]+?)\s*</`)
	perfRe      = regexp.MustCompile(`(?s)<div class="performer-name">\s*(.*?)\s*</div>`)
	scenePerfRe = regexp.MustCompile(`<p class="scene-performer-names">(?s:(.*?))</p>`)
	perfLinkRe  = regexp.MustCompile(`-streaming-pornstar-videos\.html">\s*([^<]+?)\s*</a>`)
	tagRe       = regexp.MustCompile(`data-Label="Tag"[^>]*>\s*([^<]+?)\s*</a>`)
	categoryRe  = regexp.MustCompile(`data-Label="Category - [^"]*">\s*([^<]+?)\s*</a>`)
	markupRe    = regexp.MustCompile(`<[^>]+>`)
	hoursRe     = regexp.MustCompile(`(\d+)\s*h`)
	minutesRe   = regexp.MustCompile(`(\d+)\s*min`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Duration:   it.minutes * 60,
		Studio:     defaultStudio,
		ScrapedAt:  now,
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		scraper.Debugf(1, "%s: detail %s: %v", siteID, it.url, err)
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := titleRe.FindStringSubmatch(detail); m != nil {
		if t := cleanText(m[1]); t != "" {
			scene.Title = t
		}
	}
	if m := synopsisRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(markupRe.ReplaceAllString(m[1], " "))
	}
	if m := releasedRe.FindStringSubmatch(detail); m != nil {
		if ts, err := time.Parse(dateLayout, m[1]); err == nil {
			scene.Date = ts.UTC()
		}
	}
	if m := lengthRe.FindStringSubmatch(detail); m != nil {
		if n := parseMinutes(m[1]); n > 0 {
			scene.Duration = n * 60
		}
	}
	if m := directorRe.FindStringSubmatch(detail); m != nil {
		scene.Director = cleanText(m[1])
	}
	if m := studioRe.FindStringSubmatch(detail); m != nil {
		if n := strings.TrimSpace(strings.TrimSuffix(cleanText(m[1]), " Clips")); n != "" {
			scene.Studio = n
		}
	}
	// A movie's headshot grid stops at eight and skips the men; its scene
	// list credits the rest. A scene cut from a movie lists its siblings too,
	// so only movies read it.
	names := collect(perfRe, detail)
	if strings.HasPrefix(scene.ID, movieIDPrefix) {
		for _, block := range scenePerfRe.FindAllStringSubmatch(detail, -1) {
			names = append(names, collect(perfLinkRe, block[1])...)
		}
	}
	if names = dedupe(names); len(names) > 0 {
		scene.Performers = names
	}
	scene.Tags = dedupe(append(collect(tagRe, detail), collect(categoryRe, detail)...))
}

// parseMinutes reads the CMS's length strings: "44 min", "1 h 3 min" and
// "2 hrs. 18 mins.".
func parseMinutes(s string) int {
	n := 0
	if m := hoursRe.FindStringSubmatch(s); m != nil {
		n = atoi(m[1]) * 60
	}
	if m := minutesRe.FindStringSubmatch(s); m != nil {
		n += atoi(m[1])
	}
	return n
}

func dedupe(in []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func collect(re *regexp.Regexp, s string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		v := cleanText(m[1])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	// Without this the site 302s every request to /AgeConfirmation.
	headers["Cookie"] = "ageConfirmed=true"

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: rawURL, Headers: headers})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

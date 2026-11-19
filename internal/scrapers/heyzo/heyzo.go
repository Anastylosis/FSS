// Package heyzo scrapes heyzo.com, a Japanese uncensored-video site.
//
// Despite the D2Pass billing link on every page it is not a D2Pass "bifrost"
// site: the JSON endpoints d2passutil drives (`/dyn/phpauto/movie_lists/…`)
// answer 404 here. The site was rebuilt in 2026-11 as a server-rendered
// Next.js app: `/listpages/all_{N}` pages of 30 cards and a `/movie/{id}` page
// per movie (the old `.html` and `/moviepages/` URLs redirect or 404).
//
// Discovery walks the listing; the cards already carry id, title, thumbnail,
// performer and release date. The detail page is fetched for what they do not
// carry: the synopsis, the category chips and the tag list. It publishes no
// duration.
//
// Filtered views work as listings too — `/listpages/actor_{id}_{N}` for one
// performer and `/listpages/category_{id}_{N}` for a category — so a URL
// naming either scrapes that slice with the same walk.
package heyzo

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
	siteID     = "heyzo"
	domain     = "heyzo.com"
	studioName = "HEYZO"
)

type Scraper struct {
	Client       *http.Client
	baseOverride string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.|en\.)?heyzo\.com(?:/|$)`)
	// A filtered listing names its own stem: `actor_293_1` walks one
	// performer, `category_22_1.html` one category. The trailing number is the
	// page, so it is dropped and rebuilt.
	filterRe = regexp.MustCompile(`/listpages/((?:actor|category|series)_\d+)_\d+`)
)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		domain,
		domain + "/listpages/all_{N}",
		domain + "/listpages/actor_{id}_{N}",
		domain + "/listpages/category_{id}_{N}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// base resolves the origin to fetch from. The operator's own host wins so the
// en. mirror stays addressable; baseOverride is the test server.
func (s *Scraper) base(studioURL string) string {
	if s.baseOverride != "" {
		return strings.TrimSuffix(s.baseOverride, "/")
	}
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return "https://www." + domain
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := s.base(studioURL)
	stem := "all"
	if m := filterRe.FindStringSubmatch(studioURL); m != nil {
		stem = m[1]
		scraper.Debugf(1, "%s: scraping filtered listing %s", siteID, stem)
	} else {
		scraper.Debugf(1, "%s: scraping the full catalogue", siteID)
	}

	// Discovery and detail fetching run together: the catalogue is ~115
	// listing pages, and holding every scene back until the last one is read
	// leaves a scrape looking hung for half a minute before the first result.
	// The consequence is that the true count is only known at the end, so the
	// progress total is sent then — the cmd layer shows a running count until
	// it arrives.
	work := make(chan listEntry)
	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.detailWorker(ctx, studioURL, base, work, opts, out)
		}()
	}

	found := s.discover(ctx, base, stem, opts, out, work)
	close(work)
	wg.Wait()

	if found > 0 {
		send(ctx, out, scraper.Progress(found))
	}
}

// maxListingPages bounds the walk. The catalogue is ~115 pages; the cap is a
// runaway guard, since a page past the end answers 200 with no cards rather
// than 404ing and an unbounded loop would depend entirely on that parse.
const maxListingPages = 2000

// discover walks the listing, handing each new movie to the detail pool as it
// is seen. It returns how many it sent.
func (s *Scraper) discover(ctx context.Context, base, stem string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listEntry) int {
	seen := make(map[string]bool)
	sent := 0

	for page := 1; page <= maxListingPages; page++ {
		if ctx.Err() != nil {
			return sent
		}
		if page > 1 && !sleep(ctx, opts.Delay) {
			return sent
		}

		pageURL := fmt.Sprintf("%s/listpages/%s_%d", base, stem, page)
		scraper.Debugf(1, "%s: fetching listing page %d", siteID, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			send(ctx, out, scraper.Error(fmt.Errorf("listing page %d: %w", page, err)))
			return sent
		}

		cards := parseCards(string(body))
		if len(cards) == 0 {
			if page == 1 {
				// Past the last page the site answers 200 with an empty grid,
				// so an empty page is the ordinary end signal — except on the
				// first one, where it means the template changed or the filter
				// does not exist. That has to be loud, or an authoritative
				// --full save reads it as a catalogue that vanished.
				send(ctx, out, scraper.Error(scraper.ParseError(pageURL, fmt.Errorf("no movie cards on the first listing page"))))
			}
			return sent
		}

		added := 0
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			added++
			if opts.KnownIDs[c.id] {
				// The listing is newest-first, so the first stored id means
				// everything after it is stored too.
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, c.id)
				send(ctx, out, scraper.StoppedEarly())
				return sent
			}
			select {
			case work <- c:
				sent++
			case <-ctx.Done():
				return sent
			}
		}
		if added == 0 {
			return sent
		}
	}
	return sent
}

func (s *Scraper) detailWorker(ctx context.Context, studioURL, base string, work <-chan listEntry, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	for e := range work {
		if !sleep(ctx, opts.Delay) {
			return
		}
		if !send(ctx, out, scraper.Scene(s.buildScene(ctx, studioURL, base, e, out))) {
			return
		}
	}
}

// buildScene fetches the detail page for the fields the card does not carry. A
// failed detail costs the description, duration and tags, not the scene: the
// card already holds id, title, date, performer and thumbnail.
func (s *Scraper) buildScene(ctx context.Context, studioURL, base string, e listEntry, out chan<- scraper.SceneResult) models.Scene {
	sceneURL := base + e.path
	scene := models.Scene{
		ID:        e.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     e.title,
		URL:       sceneURL,
		Studio:    studioName,
		Thumbnail: absolutize(e.thumb, base),
		Date:      e.date,
		ScrapedAt: time.Now().UTC(),
	}
	if e.performer != "" {
		scene.Performers = []string{e.performer}
	}

	body, err := s.fetch(ctx, sceneURL)
	if err != nil {
		send(ctx, out, scraper.Error(fmt.Errorf("detail %s: %w", e.id, err)))
		return scene
	}
	d := parseDetail(string(body))
	if d.empty() {
		send(ctx, out, scraper.Error(scraper.ParseError(sceneURL, fmt.Errorf("no movie block on detail page"))))
		return scene
	}
	if d.title != "" {
		scene.Title = d.title
	}
	if d.description != "" {
		scene.Description = d.description
	}
	if !d.date.IsZero() {
		scene.Date = d.date
	}
	if len(d.performers) > 0 {
		scene.Performers = d.performers
	}
	scene.Tags = d.tags
	scene.Categories = d.categories
	scene.Series = d.series
	return scene
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- listing cards ----

type listEntry struct {
	id        string
	path      string
	title     string
	thumb     string
	performer string
	date      time.Time
}

var (
	cardSplitRe = regexp.MustCompile(`MuiCard-root`)
	cardPathRe  = regexp.MustCompile(`href="(/movie/(\d+))"`)
	cardTitleRe = regexp.MustCompile(`(?s)<a class="title"[^>]*>.*?<div[^>]*>(.*?)</div>`)
	cardThumbRe = regexp.MustCompile(`<img alt="Thumbnail"[^>]* src="([^"]+)"`)
	cardActorRe = regexp.MustCompile(`(?s)href="/listpages/actor_\d+_\d+"[^>]*>(.*?)</a>`)
	jaDateRe    = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
)

func parseCards(body string) []listEntry {
	chunks := cardSplitRe.Split(body, -1)
	var out []listEntry
	for i, chunk := range chunks {
		if i == 0 {
			continue // preamble before the first card
		}
		m := cardPathRe.FindStringSubmatch(chunk)
		if m == nil {
			continue
		}
		e := listEntry{
			id:        m[2],
			path:      m[1],
			title:     cleanText(firstSubmatch(cardTitleRe, chunk)),
			thumb:     imageSource(html.UnescapeString(firstSubmatch(cardThumbRe, chunk))),
			performer: cleanText(firstSubmatch(cardActorRe, chunk)),
		}
		if d, ok := parseJapaneseDate(chunk); ok {
			e.date = d
		}
		out = append(out, e)
	}
	return out
}

// imageSource unwraps a `/_next/image?url=…` optimiser URL to the original
// asset, which is what a stored thumbnail should point at.
func imageSource(src string) string {
	if u, err := url.Parse(src); err == nil && strings.HasPrefix(u.Path, "/_next/image") {
		if orig := u.Query().Get("url"); orig != "" {
			return orig
		}
	}
	return src
}

// ---- detail page ----

type detail struct {
	title       string
	description string
	date        time.Time
	performers  []string
	tags        []string
	categories  []string
	series      string
	found       bool
}

func (d detail) empty() bool { return !d.found }

var (
	h1Re         = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	memoRe       = regexp.MustCompile(`(?s)<p class="Movie_memo[^"]*">(.*?)</p>`)
	itemSplitRe  = regexp.MustCompile(`<div class="Movie_item[^"]*">`)
	itemLabelRe  = regexp.MustCompile(`(?s)^<span>(.*?)</span>(.*)$`)
	headEndRe    = regexp.MustCompile(`Movie_memo__|Movie_disclaimer__|Movie_item__`)
	categoryRe   = regexp.MustCompile(`(?s)<a [^>]*href="/listpages/category_\d+_\d+"[^>]*>(.*?)</a>`)
	anchorTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
)

func parseDetail(body string) detail {
	var d detail
	loc := h1Re.FindStringSubmatchIndex(body)
	if loc == nil {
		return d
	}
	d.found = true
	d.title = cleanText(body[loc[2]:loc[3]])
	rest := body[loc[1]:]

	// The category chips sit between the heading and the synopsis.
	head := rest
	if m := headEndRe.FindStringIndex(rest); m != nil {
		head = rest[:m[0]]
	}
	d.categories = dedupe(texts(categoryRe, head))
	d.description = cleanText(firstSubmatch(memoRe, rest))

	// Each labelled row runs to the next one; the value keeps any trailing
	// markup, which the extractors below ignore.
	for _, chunk := range itemSplitRe.Split(rest, -1)[1:] {
		it := itemLabelRe.FindStringSubmatch(chunk)
		if it == nil {
			continue
		}
		label, value := cleanText(it[1]), it[2]
		switch label {
		case "出演":
			d.performers = anchorTexts(value)
		case "配信日":
			if t, ok := parseJapaneseDate(value); ok {
				d.date = t
			}
		case "タグ":
			d.tags = anchorTexts(value)
		case "シリーズ":
			if v := cleanText(value); v != "" {
				d.series = v
			}
		}
	}
	return d
}

func texts(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if t := cleanText(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ---- helpers ----

var tagStripRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return tagStripRe.ReplaceAllString(s, " ") }

func cleanText(s string) string {
	s = stripTags(s)
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.Join(strings.Fields(s), " ")
}

func anchorTexts(s string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range anchorTextRe.FindAllStringSubmatch(s, -1) {
		t := cleanText(m[1])
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func parseJapaneseDate(s string) (time.Time, bool) {
	m := jaDateRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-1-2", m[1]+"-"+m[2]+"-"+m[3])
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// absolutize resolves the site's protocol-relative and rooted asset paths.
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
		return ref
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

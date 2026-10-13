// Package aromaplanning scrapes aroma-p.com, the Japanese fetish AV label's
// own catalogue. See docs/scrapers.md.
package aromaplanning

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "aromaplanning"
	studioName = "Aroma Planning"
	// detailWorkers caps the per-page detail pool; a date page lists a handful
	// of titles and every field but the title lives on the detail page.
	detailWorkers = 4
	// datesPerPage batches the day pages of a whole-site walk: most days list
	// one to three titles, so fetching them one at a time leaves the detail
	// pool idle.
	datesPerPage = 12
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?aroma-p\.com(?:/|$)`)
	// filterRe selects a paginated label or genre listing.
	filterRe = regexp.MustCompile(`/works/list/(label|genre)/(\d+)(?:/|$)`)
	dateRe   = regexp.MustCompile(`/works/list/date/(\d{8})/`)

	searchDateRe = regexp.MustCompile(`href="/works/list/date/(\d{8})/"`)
	itemSplitRe  = regexp.MustCompile(`<li class="c-list-works-item`)
	itemLinkRe   = regexp.MustCompile(`class="ttl-works" href="/works/detail/([^/"]+)/">(.*?)</a>`)
	itemThumbRe  = regexp.MustCompile(`class="img-works-package" src="([^"]+)"`)
	nextRe       = regexp.MustCompile(`<link rel="next"`)

	dlRe        = regexp.MustCompile(`(?s)<dl class="lst-works-data">\s*<dt class="ttl-works-data">(.*?)</dt>\s*<dd class="item-works-data">(.*?)</dd>\s*</dl>`)
	castRe      = regexp.MustCompile(`(?s)<li class="item-works-cast">(.*?)</li>`)
	tagRe       = regexp.MustCompile(`(?s)<a class="btn-inner"[^>]*>(.*?)</a>`)
	minutesRe   = regexp.MustCompile(`(\d+)\s*分`)
	titleRe     = regexp.MustCompile(`(?s)<p class="ttl-main-works-package">(.*?)</p>`)
	synopsisRe  = regexp.MustCompile(`(?s)<p class="tx-works-info">(.*?)</p>`)
	posterRe    = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
	sampleRe    = regexp.MustCompile(`data-movie-path="([^"]+)"`)
	codeSplitRe = regexp.MustCompile(`^([a-z]+)(\d+)$`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

// Scraper implements scraper.StudioScraper for aroma-p.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Aroma Planning scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.aroma-p.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"aroma-p.com",
		"aroma-p.com/works/list/label/{id}/",
		"aroma-p.com/works/list/genre/{id}/",
		"aroma-p.com/works/list/date/{YYYYMMDD}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// item is one listing entry; it carries no cast, runtime or synopsis.
type item struct {
	slug  string
	title string
	thumb string
	date  time.Time
}

// target is what an operator URL selects: a single day, a paginated label or
// genre, or (the zero value) the whole catalogue walked day by day.
type target struct {
	kind string
	id   string
	date string
}

func resolveTarget(studioURL string) target {
	u, err := url.Parse(studioURL)
	if err != nil {
		return target{}
	}
	if m := dateRe.FindStringSubmatch(u.Path); m != nil {
		return target{kind: "date", date: m[1]}
	}
	if m := filterRe.FindStringSubmatch(u.Path); m != nil {
		return target{kind: m[1], id: m[2]}
	}
	return target{}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	t := resolveTarget(studioURL)
	scraper.Debugf(1, "%s: target %+v", siteID, t)

	now := time.Now().UTC()
	seen := map[string]bool{}
	var dates []string

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var (
			items []item
			last  bool
			err   error
		)
		switch t.kind {
		case "date":
			items, _, err = s.fetchListing(ctx, fmt.Sprintf("%s/works/list/date/%s/", s.base, t.date), parseDay(t.date))
			last = true
		case "label", "genre":
			pageURL := fmt.Sprintf("%s/works/list/%s/%s/", s.base, t.kind, t.id)
			if page > 1 {
				pageURL = fmt.Sprintf("%s%d/", pageURL, page)
			}
			var body string
			items, body, err = s.fetchListing(ctx, pageURL, time.Time{})
			last = !nextRe.MatchString(body)
			if err == nil && page == 1 && len(items) == 0 {
				err = scraper.ParseError(pageURL, fmt.Errorf("no titles on the first page"))
			}
		default:
			if page == 1 {
				dates, err = s.fetchDates(ctx)
				if err != nil {
					return scraper.PageResult{}, err
				}
			}
			lo := (page - 1) * datesPerPage
			if lo >= len(dates) {
				return scraper.PageResult{Done: true}, nil
			}
			hi := min(lo+datesPerPage, len(dates))
			items, err = s.fetchDays(ctx, dates[lo:hi], opts)
			last = hi == len(dates)
		}
		if err != nil {
			return scraper.PageResult{}, err
		}

		var scenes []models.Scene
		for _, it := range items {
			id := catalogueID(it.slug)
			if seen[id] {
				continue
			}
			seen[id] = true
			scenes = append(scenes, s.baseScene(it, id, studioURL, now))
		}
		var fresh []*models.Scene
		for i := range scenes {
			if !opts.KnownIDs[scenes[i].ID] {
				fresh = append(fresh, &scenes[i])
			}
		}
		s.enrich(ctx, fresh, opts, out)
		return scraper.PageResult{Scenes: scenes, Done: last, Continue: t.kind == ""}, nil
	})
}

func (s *Scraper) fetchDates(ctx context.Context) ([]string, error) {
	body, err := s.fetch(ctx, s.base+"/search/")
	if err != nil {
		return nil, err
	}
	dates := parseDates(body)
	if len(dates) == 0 {
		return nil, scraper.ParseError(s.base+"/search/", fmt.Errorf("no release dates found"))
	}
	scraper.Debugf(1, "%s: %d release dates", siteID, len(dates))
	return dates, nil
}

// fetchListing reads one listing page; date is the release date every title
// on it shares, when the page is a single day.
func (s *Scraper) fetchListing(ctx context.Context, pageURL string, date time.Time) ([]item, string, error) {
	body, err := s.fetch(ctx, pageURL)
	if err != nil {
		return nil, "", err
	}
	items := parseListing(body)
	for i := range items {
		items[i].date = date
	}
	return items, body, nil
}

// fetchDays reads several day pages concurrently and returns their titles in
// the order the days were given, so the walk stays newest-first.
func (s *Scraper) fetchDays(ctx context.Context, days []string, opts scraper.ListOpts) ([]item, error) {
	results := make([][]item, len(days))
	errs := make([]error, len(days))
	sem := make(chan struct{}, scraper.WorkerCount(opts, detailWorkers))
	var wg sync.WaitGroup
	for i, d := range days {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				errs[i] = ctx.Err()
				return
			}
			results[i], _, errs[i] = s.fetchListing(ctx, fmt.Sprintf("%s/works/list/date/%s/", s.base, d), parseDay(d))
		}()
	}
	wg.Wait()
	var all []item
	for i := range days {
		if errs[i] != nil {
			return nil, errs[i]
		}
		all = append(all, results[i]...)
	}
	return all, nil
}

// parseDates lists the release dates linked from /search/, newest first.
func parseDates(body string) []string {
	seen := map[string]bool{}
	var dates []string
	for _, m := range searchDateRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			dates = append(dates, m[1])
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

// parseListing reads a listing page's title cards.
func parseListing(body string) []item {
	parts := itemSplitRe.Split(body, -1)
	var items []item
	for _, blk := range parts[1:] {
		link := itemLinkRe.FindStringSubmatch(blk)
		if link == nil {
			continue
		}
		it := item{slug: link[1], title: cleanText(link[2])}
		if th := itemThumbRe.FindStringSubmatch(blk); th != nil {
			it.thumb = html.UnescapeString(th[1])
		}
		items = append(items, it)
	}
	return items
}

// catalogueID turns a URL slug (aarm367) into the catalogue number the label
// prints on the package (AARM-367).
func catalogueID(slug string) string {
	if m := codeSplitRe.FindStringSubmatch(slug); m != nil {
		return strings.ToUpper(m[1]) + "-" + m[2]
	}
	return strings.ToUpper(slug)
}

func parseDay(d string) time.Time {
	t, err := time.Parse("20060102", d)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *Scraper) baseScene(it item, id, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     it.title,
		URL:       s.base + "/works/detail/" + it.slug + "/",
		Date:      it.date,
		Thumbnail: s.abs(it.thumb),
		ScrapedAt: now,
	}
}

func (s *Scraper) abs(ref string) string {
	if strings.HasPrefix(ref, "/") {
		return s.base + ref
	}
	return ref
}

// enrich fetches each scene's detail page for cast, runtime, tags and synopsis.
func (s *Scraper) enrich(ctx context.Context, scenes []*models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	workers := scraper.WorkerCount(opts, detailWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, sc := range scenes {
		wg.Add(1)
		go func(sc *models.Scene) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, sc.URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(sc, body)
		}(sc)
	}
	wg.Wait()
}

// applyDetail folds a detail page into a scene built from its listing card.
// Rows are matched by their Japanese label, not by position.
func applyDetail(sc *models.Scene, body string) {
	if m := titleRe.FindStringSubmatch(body); m != nil {
		if t := cleanText(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := synopsisRe.FindStringSubmatch(body); m != nil {
		sc.Description = cleanText(m[1])
	}
	if m := posterRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(m[1])
	}
	if m := sampleRe.FindStringSubmatch(body); m != nil {
		sc.Preview = html.UnescapeString(m[1])
	}
	// The related-titles rail repeats the row markup further down, so only
	// the first row of each label counts.
	got := map[string]bool{}
	for _, row := range dlRe.FindAllStringSubmatch(body, -1) {
		label := cleanText(row[1])
		if got[label] {
			continue
		}
		got[label] = true
		val := row[2]
		switch label {
		case "出演者":
			for _, m := range castRe.FindAllStringSubmatch(val, -1) {
				if n := cleanText(m[1]); n != "" {
					sc.Performers = append(sc.Performers, n)
				}
			}
		case "監督":
			sc.Director = cleanText(val)
		case "ジャンル":
			for _, m := range tagRe.FindAllStringSubmatch(val, -1) {
				if n := cleanText(m[1]); n != "" {
					sc.Tags = append(sc.Tags, n)
				}
			}
		case "レーベル":
			for _, m := range tagRe.FindAllStringSubmatch(val, -1) {
				if n := cleanText(m[1]); n != "" {
					sc.Categories = append(sc.Categories, n)
				}
			}
		case "発売日":
			if m := dateRe.FindStringSubmatch(val); m != nil {
				if d := parseDay(m[1]); !d.IsZero() {
					sc.Date = d
				}
			}
		case "収録時間":
			if m := minutesRe.FindStringSubmatch(val); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil {
					sc.Duration = n * 60
				}
			}
		}
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

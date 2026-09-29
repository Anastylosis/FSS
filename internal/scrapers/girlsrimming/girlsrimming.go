// Package girlsrimming scrapes Girls Rimming (girlsrimming.com), an Elevated X
// classic tour.
//
// It uses the same `update_details` card skin as auntjudys, differing only in
// the scene link shape: `/tour/trailers/{Slug}.html` rather than
// `..._vids.html`.
//
// The listing carries id, title, URL, date, duration, performers and
// thumbnail — everything except the description and tags, which come from a
// detail worker pool.
//
// Two limits worth knowing:
//
//   - The detail page has no description body. og:description is the only
//     source and the CMS truncates it (~150 chars, trailing "..."). The RSS
//     feed has full text but only for the newest 9 scenes, which is too few to
//     be worth a second transport.
//   - `<meta name="keywords">` mixes genre tags with performer names, so the
//     performers read off the listing card are subtracted from it.
package girlsrimming

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

const (
	siteID        = "girlsrimming"
	studioName    = "Girls Rimming"
	detailWorkers = 4
)

var siteBase = "https://girlsrimming.com"

// Scraper implements scraper.StudioScraper for Girls Rimming.
type Scraper struct {
	Client *http.Client
}

// New constructs a Girls Rimming scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"girlsrimming.com",
		"girlsrimming.com/tour/categories/movies/{N}/latest/",
		"girlsrimming.com/tour/trailers/{slug}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?girlsrimming\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/tour/categories/movies/%d/latest/", siteBase, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
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
	// Cards are delimited by the Elevated X data-setid marker.
	cardRe = regexp.MustCompile(`class="update_details" data-setid="(\d+)"`)
	// Only the trailer filename is captured so the URL is rebuilt against
	// siteBase — cards link absolutely.
	trailerRe = regexp.MustCompile(`href="[^"]*/tour/trailers/([^"/]+\.html)"[^>]*>([^<]*)</a>`)
	thumbRe   = regexp.MustCompile(`src0_(?:4x|3x|1x)="([^"]+)"`)
	// The date cell holds an HTML comment before the value
	// (`<div class="cell update_date"> <!-- Date --> 07/11/2026 </div>`), so
	// the cell is captured first and the date picked out of it.
	dateCellRe  = regexp.MustCompile(`(?s)class="cell update_date">(.*?)</div>`)
	dateValueRe = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	countsRe    = regexp.MustCompile(`(?s)class="update_counts">(.*?)</div>`)
	durationRe  = regexp.MustCompile(`(\d+)\s*(?:&nbsp;)*\s*min`)
	modelsRe    = regexp.MustCompile(`(?s)class="update_models">(.*?)</span>`)
	modelLinkRe = regexp.MustCompile(`href="[^"]*/models/[^"]*">([^<]+)</a>`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
	performers            []string
}

func parseListing(body []byte) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		card := page[loc[0]:end]

		it := listItem{id: page[loc[2]:loc[3]]}

		m := trailerRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it.url = siteBase + "/tour/trailers/" + m[1]
		// The first trailer anchor wraps the thumbnail and has no text; the
		// second is the title link. Take the first non-empty one.
		for _, tm := range trailerRe.FindAllStringSubmatch(card, -1) {
			if t := html.UnescapeString(strings.TrimSpace(tm[2])); t != "" {
				it.title = t
				break
			}
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}
		if cell := dateCellRe.FindStringSubmatch(card); cell != nil {
			if d := dateValueRe.FindStringSubmatch(cell[1]); d != nil {
				// US-format date.
				if ts, err := time.Parse("01/02/2006", d[1]); err == nil {
					it.date = ts.UTC()
				}
			}
		}
		// "73&nbsp;Photos, 35&nbsp;min&nbsp;of video" — the runtime is given in
		// whole minutes, and the photo count must not be mistaken for it.
		if c := countsRe.FindStringSubmatch(card); c != nil {
			if d := durationRe.FindStringSubmatch(c[1]); d != nil {
				it.duration = atoi(d[1]) * 60
			}
		}
		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			for _, pm := range modelLinkRe.FindAllStringSubmatch(mb[1], -1) {
				if name := html.UnescapeString(strings.TrimSpace(pm[1])); name != "" {
					it.performers = append(it.performers, name)
				}
			}
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

var keywordsRe = regexp.MustCompile(`name="keywords" content="([^"]*)"`)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Date:       it.date,
		Duration:   it.duration,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     studioName,
		ScrapedAt:  now,
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		return scene
	}
	applyDetail(&scene, body)
	return scene
}

func applyDetail(scene *models.Scene, body []byte) {
	og := parseutil.OpenGraph(body)
	// The detail page has no description body; og:description is the only
	// source and the CMS truncates it.
	if v := og["og:description"]; v != "" {
		scene.Description = html.UnescapeString(strings.TrimSpace(v))
	}
	if scene.Title == "" {
		if v := og["og:title"]; v != "" {
			scene.Title = html.UnescapeString(strings.TrimSpace(v))
		}
	}

	// The keywords list mixes genre tags and performer names; drop the names
	// already known from the listing card so tags stay descriptive.
	m := keywordsRe.FindSubmatch(body)
	if m == nil {
		return
	}
	isPerformer := make(map[string]bool, len(scene.Performers))
	for _, p := range scene.Performers {
		isPerformer[strings.ToLower(p)] = true
	}
	for _, kw := range strings.Split(string(m[1]), ",") {
		kw = html.UnescapeString(strings.TrimSpace(kw))
		if kw == "" || isPerformer[strings.ToLower(kw)] {
			continue
		}
		scene.Tags = append(scene.Tags, kw)
	}
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
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

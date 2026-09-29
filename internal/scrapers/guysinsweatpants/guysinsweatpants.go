// Package guysinsweatpants scrapes Guys In Sweatpants (guysinsweatpants.com),
// an Elevated X tour on the modern Bootstrap 5 skin — `item item-video` cards
// rather than the classic `update_details`/`data-setid` markup.
//
// The listing card carries the scene id, title, URL, performers, date, duration
// and thumbnail, so the detail worker pool only adds the description.
//
// The site has no tag taxonomy: models double as the only classification.
package guysinsweatpants

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
	siteID        = "guysinsweatpants"
	studioName    = "Guys In Sweatpants"
	detailWorkers = 4
	dateLayout    = "January 2, 2006"
)

var siteBase = "https://guysinsweatpants.com"

// Scraper implements scraper.StudioScraper for Guys In Sweatpants.
type Scraper struct {
	Client *http.Client
}

// New constructs a Guys In Sweatpants scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"guysinsweatpants.com",
		"guysinsweatpants.com/tour/categories/movies_{N}.html",
		"guysinsweatpants.com/tour/trailers/{slug}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?guysinsweatpants\.com(?:/|$)`)

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
		body, err := s.fetchPage(ctx, listingURL(page))
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

// listingURL builds the movies-category page. Page 1 has no suffix.
func listingURL(page int) string {
	if page <= 1 {
		return siteBase + "/tour/categories/movies.html"
	}
	return fmt.Sprintf("%s/tour/categories/movies_%d.html", siteBase, page)
}

// ---- listing ----

var (
	cardRe = regexp.MustCompile(`<div class="item item-video`)
	// set-target carries the scene id; the same number appears in the
	// item-video-thumb class and data-videoid.
	idRe = regexp.MustCompile(`id="set-target-(\d+)"`)
	// Only the trailer filename is captured so the URL is rebuilt against
	// siteBase — cards link absolutely.
	trailerRe = regexp.MustCompile(`href="[^"]*/tour/trailers/([^"/]+\.html)"[^>]*title="([^"]*)"`)
	modelsRe  = regexp.MustCompile(`(?s)class="item-models-list[^"]*">(.*?)</div>`)
	modelRe   = regexp.MustCompile(`/tour/models/[^"]*\.html">([^<]+)</a>`)
	dateRe    = regexp.MustCompile(`class="item-meta-date">.*?</i>\s*([A-Z][a-z]+ \d{1,2}, \d{4})`)
	// The duration block also carries a photo count, so only the clock value
	// is taken.
	durationRe = regexp.MustCompile(`(?s)class="item-meta-duration">.*?(\d{1,2}:\d{2}(?::\d{2})?)`)
	thumbRe    = regexp.MustCompile(`class="video_placeholder stdimage"\s+src="([^"]+)"`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
	performers            []string
}

func parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := trailerRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			url:   siteBase + "/tour/trailers/" + m[1],
			title: cleanText(m[2]),
		}
		if idm := idRe.FindStringSubmatch(card); idm != nil {
			it.id = idm[1]
		} else {
			it.id = strings.TrimSuffix(m[1], ".html")
		}

		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			for _, pm := range modelRe.FindAllStringSubmatch(mb[1], -1) {
				if name := cleanText(pm[1]); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if ts, err := time.Parse(dateLayout, d[1]); err == nil {
				it.date = ts.UTC()
			}
		}
		if du := durationRe.FindStringSubmatch(card); du != nil {
			it.duration = parseutil.ParseDurationColon(du[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = normalizeURL(th[1])
		}

		items = append(items, it)
	}
	return items
}

func normalizeURL(u string) string {
	if strings.HasPrefix(u, "/") {
		return siteBase + u
	}
	return u
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
	descRe     = regexp.MustCompile(`(?s)<div class="update-info-block text-larger">\s*<p>(.*?)</p>`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

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

	// Only the description needs the detail page; a failure there still leaves
	// a complete scene.
	if body, err := s.fetchPage(ctx, it.url); err == nil {
		if m := descRe.FindSubmatch(body); m != nil {
			scene.Description = cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
		}
	}
	return scene
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
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

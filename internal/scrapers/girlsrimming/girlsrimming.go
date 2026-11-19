// Package girlsrimming scrapes Girls Rimming (girlsrimming.com).
//
// The site was rebuilt in 2026-11 on a Bootstrap template: the old Elevated X
// `/tour/` pages now redirect to `/all/video`, which lists 48 cards a page
// newest first with title, publish date, runtime, performers, categories and
// tags. Only the full description needs the detail page (`/v/{id}-{slug}`),
// fetched by a small worker pool.
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
		"girlsrimming.com/all/video?page={N}",
		"girlsrimming.com/v/{slug}",
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
		pageURL := fmt.Sprintf("%s/all/video?page=%d", siteBase, page)
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
	cardRe     = regexp.MustCompile(`<article class="wlgr-update-card`)
	linkRe     = regexp.MustCompile(`href="[^"]*/v/((\d+)-[^"/?#]+)"`)
	titleRe    = regexp.MustCompile(`(?s)<h2 class="h5[^"]*">(.*?)</h2>`)
	thumbRe    = regexp.MustCompile(`<img class="thumb[^"]*" src="([^"]+)"`)
	dateRe     = regexp.MustCompile(`datetime="(\d{4}-\d{2}-\d{2})`)
	durationRe = regexp.MustCompile(`(?s)class="duration[^"]*">.*?(\d{1,2}(?::\d{2}){1,2})\s*</small>`)
	modelsRe   = regexp.MustCompile(`(?s)class="wlgr-card-models[^"]*">(.*?)</div>`)
	tagsBoxRe  = regexp.MustCompile(`(?s)class="wlgr-card-tags[^"]*">(.*?)</div>`)
	actorRe    = regexp.MustCompile(`href="[^"]*/actor/[^"]*">([^<]+)</a>`)
	categoryRe = regexp.MustCompile(`href="[^"]*/category/[^"]*">([^<]+)</a>`)
	tagRe      = regexp.MustCompile(`href="[^"]*/tag/[^"]*">#([^<]+)</a>`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
	performers            []string
	categories, tags      []string
}

func names(re *regexp.Regexp, block string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		if n := html.UnescapeString(strings.TrimSpace(m[1])); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func parseListing(body []byte) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringIndex(page, -1)
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
		it := listItem{id: m[2], url: siteBase + "/v/" + m[1]}
		if t := titleRe.FindStringSubmatch(card); t != nil {
			it.title = html.UnescapeString(strings.TrimSpace(t[1]))
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if ts, err := time.Parse("2006-01-02", d[1]); err == nil {
				it.date = ts.UTC()
			}
		}
		if d := durationRe.FindStringSubmatch(card); d != nil {
			it.duration = parseutil.ParseDurationColon(d[1])
		}
		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			it.performers = names(actorRe, mb[1])
		}
		if tb := tagsBoxRe.FindStringSubmatch(card); tb != nil {
			it.categories = names(categoryRe, tb[1])
			it.tags = names(tagRe, tb[1])
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
		Categories: it.categories,
		Tags:       it.tags,
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

var (
	descRe = regexp.MustCompile(`(?s)<div class="wlgr-description[^"]*">(.*?)</div>`)
	htmlRe = regexp.MustCompile(`<[^>]+>`)
)

func applyDetail(scene *models.Scene, body []byte) {
	if m := descRe.FindSubmatch(body); m != nil {
		text := htmlRe.ReplaceAllString(strings.ReplaceAll(string(m[1]), "</p>", "\n"), "")
		scene.Description = strings.TrimSpace(html.UnescapeString(text))
	}
	if scene.Title == "" {
		if v := parseutil.OpenGraph(body)["og:title"]; v != "" {
			scene.Title = html.UnescapeString(strings.TrimSpace(v))
		}
	}
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

// Package japanboyz scrapes Japanboyz (japanboyz.com), an Elevated X tour with
// the Bootstrap "v-item" card theme (part of the Gay Asian Network).
//
// The listing (/categories/scenes_{N}_d.html) carries id, title, date, duration
// and thumbnail; the detail page adds performers, tags and the full
// description. Out-of-range pages clamp to the last page, which the dedup in
// run() and Paginate's repeat-page guard turn into a stop.
package japanboyz

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
	siteID        = "japanboyz"
	studioName    = "Japanboyz"
	detailWorkers = 4
	dateLayout    = "2 Jan 06"
)

var siteBase = "https://www.japanboyz.com"

// Scraper implements scraper.StudioScraper for Japanboyz.
type Scraper struct {
	Client *http.Client
}

// New constructs a Japanboyz scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"japanboyz.com",
		"japanboyz.com/categories/scenes.html",
		"japanboyz.com/scenes/{slug}_vids.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?japanboyz\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) pageURL(page int) string {
	if page <= 1 {
		return siteBase + "/categories/scenes.html"
	}
	return fmt.Sprintf("%s/categories/scenes_%d_d.html", siteBase, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, s.pageURL(page))
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

var (
	cardRe  = regexp.MustCompile(`class="[^"]*\bv-item\b[^"]*"`)
	titleRe = regexp.MustCompile(`<div class="title[^"]*"><a href="([^"]+/scenes/[^"]+_vids\.html)">([^<]+)</a>`)
	idRe    = regexp.MustCompile(`data-type="set" data-id="(\d+)"`)
	dateRe  = regexp.MustCompile(`fa-calendar"></i>\s*(?:<span[^>]*>)?\s*(\d{1,2} [A-Za-z]{3} \d{2})`)
	durRe   = regexp.MustCompile(`(?s)fa-clock[^>]*></i>\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	thumbRe = regexp.MustCompile(`<img src="([^"]+contentthumbs[^"]+)"`)
)

type listItem struct {
	id, title, url, thumb string
	date                  time.Time
	duration              int
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
		m := titleRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			url:   m[1],
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		}
		if im := idRe.FindStringSubmatch(card); im != nil {
			it.id = im[1]
		} else {
			continue
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if ts, err := time.Parse(dateLayout, d[1]); err == nil {
				it.date = ts.UTC()
			}
		}
		if du := durRe.FindStringSubmatch(card); du != nil {
			it.duration = parseutil.ParseDurationColon(du[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}
		items = append(items, it)
	}
	return items
}

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
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Date:      it.date,
		Duration:  it.duration,
		Thumbnail: it.thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}
	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

var (
	descRe     = regexp.MustCompile(`(?s)<div class="description[^"]*">(.*?)</div>`)
	tagStripRe = regexp.MustCompile(`(?s)<[^>]+>`)
	tagRe      = regexp.MustCompile(`<a class="mr-2" href="[^"]*/categories/[^"]+">#([^<]+)</a>`)
	modelRe    = regexp.MustCompile(`<div class="m-name[^"]*"><a href="[^"]*/models/[^"]+">([^<]+)</a>`)
)

func applyDetail(scene *models.Scene, detail string) {
	if m := descRe.FindStringSubmatch(detail); m != nil {
		text := html.UnescapeString(tagStripRe.ReplaceAllString(m[1], " "))
		scene.Description = strings.Join(strings.Fields(text), " ")
	}
	for _, tm := range tagRe.FindAllStringSubmatch(detail, -1) {
		tag := html.UnescapeString(strings.TrimSpace(tm[1]))
		if tag != "" && !contains(scene.Tags, tag) {
			scene.Tags = append(scene.Tags, tag)
		}
	}
	for _, pm := range modelRe.FindAllStringSubmatch(detail, -1) {
		name := html.UnescapeString(strings.TrimSpace(pm[1]))
		if name != "" && !contains(scene.Performers, name) {
			scene.Performers = append(scene.Performers, name)
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

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

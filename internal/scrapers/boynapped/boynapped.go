// Package boynapped scrapes BoyNapped (boynapped.com).
//
// BoyNapped sits on the same YPP/"Journey" backend as the sibling sites in the
// indiebucks package, but runs a newer "pasig" theme: its listing lives at
// /ultimatekink/movies/newest?page=N (8/page) and carries no JSON-LD, so the
// cards are parsed from HTML instead.
//
// The card is unusually complete — id, title, URL, performers, date, duration
// and thumbnail all come from the listing. Only the description and tags need a
// detail fetch, which a worker pool handles.
//
// The catalogue runs back to 2008 and is far larger than StashDB suggests
// (~1738 scenes, not ~348), so a full crawl is ~218 listing pages plus one
// detail fetch per scene. Listings are newest-first, so incremental runs stop
// early.
package boynapped

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
	siteID        = "boynapped"
	studioName    = "BoyNapped"
	detailWorkers = 4
	// The card date is unpadded, e.g. "Jul 8, 2026".
	dateLayout = "Jan 2, 2006"
)

var siteBase = "https://www.boynapped.com"

// Scraper implements scraper.StudioScraper for BoyNapped.
type Scraper struct {
	Client *http.Client
}

// New constructs a BoyNapped scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"boynapped.com",
		"boynapped.com/ultimatekink/movies/newest",
		"boynapped.com/ultimatekink/movie/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?boynapped\.com(?:/|$)`)

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
		pageURL := fmt.Sprintf("%s/ultimatekink/movies/newest?page=%d", siteBase, page)
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
	cardRe = regexp.MustCompile(`<div class="content-item`)
	// The slug is the scene id; it is also the movie URL's last segment.
	titleRe  = regexp.MustCompile(`(?s)<h3 class="title">\s*<a href="[^"]*/ultimatekink/movie/([^"]+)"\s*>\s*(.*?)\s*</a>`)
	modelsRe = regexp.MustCompile(`(?s)<h4 class="models">(.*?)</h4>`)
	modelRe  = regexp.MustCompile(`/ultimatekink/model/\d+/[^"]*"\s*>\s*([^<]+?)\s*</a>`)
	dateRe   = regexp.MustCompile(`(?s)class="pub-date">.*?&nbsp;\s*([A-Z][a-z]{2} \d{1,2}, \d{4})`)
	durRe    = regexp.MustCompile(`(?s)class="video-duration">.*?&nbsp;\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	// The big thumbnail is protocol-relative.
	thumbRe = regexp.MustCompile(`class="thumb-big" data-image="([^"]+)"`)
)

type listItem struct {
	id, title, thumb string
	date             time.Time
	duration         int
	performers       []string
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
			id:    m[1],
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		}
		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			for _, pm := range modelRe.FindAllStringSubmatch(mb[1], -1) {
				if name := html.UnescapeString(strings.TrimSpace(pm[1])); name != "" {
					it.performers = append(it.performers, name)
				}
			}
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
			it.thumb = normalizeURL(th[1])
		}

		items = append(items, it)
	}
	return items
}

// normalizeURL upgrades the CDN's protocol-relative URLs to https.
func normalizeURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
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
	descRe     = regexp.MustCompile(`(?s)<div class="description">.*?<p>(.*?)</p>`)
	tagsBlkRe  = regexp.MustCompile(`(?s)<div class="tags">(.*?)</div>`)
	tagRe      = regexp.MustCompile(`/ultimatekink/movies/category/\d+"\s*>\s*([^<]+?)\s*</a>`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	sceneURL := siteBase + "/ultimatekink/movie/" + it.id

	scene := models.Scene{
		ID:         it.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        sceneURL,
		Date:       it.date,
		Duration:   it.duration,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     studioName,
		ScrapedAt:  now,
	}

	// Only the description and tags need the detail page; a failure there
	// still leaves a complete-enough scene.
	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := descRe.FindStringSubmatch(detail); m != nil {
		text := html.UnescapeString(tagStripRe.ReplaceAllString(m[1], " "))
		scene.Description = strings.Join(strings.Fields(text), " ")
	}
	if tb := tagsBlkRe.FindStringSubmatch(detail); tb != nil {
		for _, tm := range tagRe.FindAllStringSubmatch(tb[1], -1) {
			tag := html.UnescapeString(strings.TrimSpace(tm[1]))
			if tag != "" && !contains(scene.Tags, tag) {
				scene.Tags = append(scene.Tags, tag)
			}
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

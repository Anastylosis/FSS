// Package lewood scrapes LeWood (https://www.lewood.com/), an Adult Empire /
// Ravana LLC "hybrid-core" ASP.NET VOD platform (Evil Angel - LeWood studio).
//
// The full catalog is reachable through the paginated browse listing at
// /watch-newest-lewood-clips-and-scenes.html?page=N (52 scenes per page, ~31
// pages). The listing grid is authoritative for the scene title, performers and
// thumbnail. A concurrent detail worker pool then fetches each scene page to add
// the release date and description. Every request carries an AgeConfirmed=True
// cookie to bypass the /AgeConfirmation age gate.
package lewood

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "lewood"
	studioName    = "LeWood"
	browsePath    = "/watch-newest-lewood-clips-and-scenes.html"
	detailWorkers = 4
)

// siteBase is a var (not const) only so offline tests can point it at an
// httptest server; the default is the production host.
var siteBase = "https://www.lewood.com"

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"lewood.com/",
		"lewood.com/watch-newest-lewood-clips-and-scenes.html",
		"lewood.com/watch-newest-lewood-clips-and-scenes.html?studio={id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?lewood\.com(?:/|$)`)

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
	// Carry a ?studio=NNN filter from the studio URL into the browse listing.
	studioFilter := ""
	if u, err := url.Parse(studioURL); err == nil {
		studioFilter = u.Query().Get("studio")
	}
	scraper.Debugf(1, "%s: scraping catalog (studio filter=%q)", siteID, studioFilter)

	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := browseURL(studioFilter, page)
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		// Defensive: stop if the listing repeats or runs dry.
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
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func browseURL(studioFilter string, page int) string {
	v := url.Values{}
	v.Set("page", fmt.Sprintf("%d", page))
	if studioFilter != "" {
		v.Set("studio", studioFilter)
	}
	return siteBase + browsePath + "?" + v.Encode()
}

// ---- listing parse ----

type listItem struct {
	id         string
	url        string
	title      string
	performers []string
	thumb      string
}

var (
	cardSplitRe  = regexp.MustCompile(`<div class="grid-item" id="ascene_\d+">`)
	cardIDRe     = regexp.MustCompile(`id="ascene_(\d+)"`)
	cardTitleRe  = regexp.MustCompile(`(?s)<h6[^>]*>(.*?)</h6>`)
	cardLinkRe   = regexp.MustCompile(`<a class="scene-title"\s+href="([^"]+)"`)
	cardPerfRe   = regexp.MustCompile(`(?s)<p class="scene-performer-names">(.*?)</p>`)
	cardThumbRe  = regexp.MustCompile(`data-srcset="([^ "]+)`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
	perfSplitRe  = regexp.MustCompile(`\s*(?:&amp;|&|,)\s*`)
	anchorTextRe = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
)

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	text := string(body)
	blocks := cardSplitRe.Split(text, -1)
	items := make([]listItem, 0, len(blocks))
	for i := 1; i < len(blocks); i++ {
		// Only real scene cards carry a scene-title anchor — recommendation
		// widgets on empty pages do not, so this filters out the tail.
		b := blocks[i]
		if !strings.Contains(b, `class="scene-title"`) {
			continue
		}
		it, ok := parseCard(b)
		if ok {
			items = append(items, it)
		}
	}
	return items, nil
}

func parseCard(b string) (listItem, bool) {
	link := cardLinkRe.FindStringSubmatch(b)
	if link == nil {
		return listItem{}, false
	}
	href := link[1]
	// id is the leading numeric path segment: /1788734/...-streaming-scene-video.html
	id := ""
	if p := strings.TrimPrefix(href, "/"); len(p) > 0 {
		if slash := strings.IndexByte(p, '/'); slash > 0 {
			id = p[:slash]
		}
	}
	if id == "" {
		if m := cardIDRe.FindStringSubmatch(b); m != nil {
			id = m[1]
		}
	}
	if id == "" {
		return listItem{}, false
	}

	it := listItem{
		id:  id,
		url: absURL(href),
	}
	if m := cardTitleRe.FindStringSubmatch(b); m != nil {
		it.title = cleanText(m[1])
	}
	if m := cardThumbRe.FindStringSubmatch(b); m != nil {
		it.thumb = m[1]
	}
	if m := cardPerfRe.FindStringSubmatch(b); m != nil {
		it.performers = parsePerformers(m[1])
	}
	return it, true
}

// parsePerformers extracts performer names from a scene-performer-names block.
// Names appear either as <a> links or as plain text, optionally joined by "&"
// or ",".
func parsePerformers(block string) []string {
	var raw []string
	if links := anchorTextRe.FindAllStringSubmatch(block, -1); len(links) > 0 {
		for _, m := range links {
			raw = append(raw, m[1])
		}
	} else {
		raw = perfSplitRe.Split(block, -1)
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raw {
		name := cleanText(r)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "%s: fetching %d scene details with %d workers", siteID, len(items), workers)
	scenes := make([]models.Scene, len(items))
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
	// Drop any scenes left zero-valued by a cancelled context.
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

var (
	releasedRe = regexp.MustCompile(`Released:\s*</span>\s*([A-Za-z]{3,9}\.?\s+\d{1,2},?\s+\d{4})`)
	metaDescRe = regexp.MustCompile(`(?i)<meta\s+name="og:description"\s+content="([^"]*)"`)
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
		Studio:     studioName,
		ScrapedAt:  now,
	}

	if body, err := s.get(ctx, it.url); err == nil {
		detail := string(body)
		if m := releasedRe.FindStringSubmatch(detail); m != nil {
			if d, err := parseutil.TryParseDate(strings.TrimSpace(m[1]),
				"Jan 02, 2006", "Jan 2, 2006", "January 2, 2006"); err == nil {
				scene.Date = d
			}
		}
		if m := metaDescRe.FindStringSubmatch(detail); m != nil {
			scene.Description = cleanText(m[1])
		}
	}
	return scene
}

// ---- helpers ----

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["Cookie"] = "AgeConfirmed=True"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: headers})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func absURL(href string) string {
	if strings.HasPrefix(href, "http") {
		return href
	}
	return siteBase + "/" + strings.TrimPrefix(href, "/")
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

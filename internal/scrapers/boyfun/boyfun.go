// Package boyfun scrapes BoyFun (boyfun.com), a gay twink studio tour. The
// /videos/ listing is page-numbered (/videos/page2.html, /videos/page3.html,
// …) and each card carries the scene URL, title, thumbnail and date. The
// detail page adds the performers, a full description and a larger poster.
// Every request must carry the warningHidden=hide cookie, otherwise the site
// serves an age-warning interstitial instead of the content.
package boyfun

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
	siteID        = "boyfun"
	studioName    = "BoyFun"
	detailWorkers = 4
)

var siteBase = "https://www.boyfun.com"

type Scraper struct {
	Client *http.Client
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"boyfun.com/videos/",
		"boyfun.com/video/{slug}-{id}.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?boyfun\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="item">`)
	cardHrefRe  = regexp.MustCompile(`href="([^"]*/video/[^"]+-(\d+)\.html)"`)
	cardTitleRe = regexp.MustCompile(`<span class="title">([^<]*)</span>`)
	cardDateRe  = regexp.MustCompile(`<span class="date">([^<]*)</span>`)
	cardThumbRe = regexp.MustCompile(`data-src="([^"]+)"`)

	modelsRe     = regexp.MustCompile(`(?s)<span class="models">.*?<span class="content">(.*?)</span>`)
	modelLinkRe  = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)
	detailDateRe = regexp.MustCompile(`(?s)<span class="date">.*?<span class="content">([^<]+)</span>`)
	descRe       = regexp.MustCompile(`(?s)<div class="content-information-description">.*?<p>(.*?)</p>`)
	posterRe     = regexp.MustCompile(`(?s)<div class="video-poster">\s*<img src="([^"]+)"`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := siteBase + "/videos/"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/videos/page%d.html", siteBase, page)
		}
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
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
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

type listItem struct {
	id        string
	url       string
	title     string
	date      string
	thumbnail string
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	parts := cardSplitRe.Split(string(body), -1)
	items := make([]listItem, 0, len(parts))
	seen := make(map[string]bool)
	for _, card := range parts[1:] {
		m := cardHrefRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{id: id, url: m[1]}
		if t := cardTitleRe.FindStringSubmatch(card); t != nil {
			it.title = html.UnescapeString(strings.TrimSpace(t[1]))
		}
		if d := cardDateRe.FindStringSubmatch(card); d != nil {
			it.date = strings.TrimSpace(d[1])
		}
		if th := cardThumbRe.FindStringSubmatch(card); th != nil {
			it.thumbnail = th[1]
		}
		items = append(items, it)
	}
	scraper.Debugf(1, "boyfun: listing %s -> %d cards", pageURL, len(items))
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "boyfun: fetching %d details with %d workers", len(items), workers)
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
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Thumbnail: it.thumbnail,
		Studio:    studioName,
		ScrapedAt: now,
	}
	// Listing date (e.g. "26 Jun 2026") as a baseline.
	if d, err := parseutil.TryParseDate(it.date, "2 Jan 2006"); err == nil {
		scene.Date = d.UTC()
	}

	body, err := s.get(ctx, it.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := modelsRe.FindStringSubmatch(detail); m != nil {
		var performers []string
		seen := make(map[string]bool)
		for _, p := range modelLinkRe.FindAllStringSubmatch(m[1], -1) {
			name := html.UnescapeString(strings.TrimSpace(p[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				performers = append(performers, name)
			}
		}
		scene.Performers = performers
	}

	if m := detailDateRe.FindStringSubmatch(detail); m != nil {
		// e.g. "Jun 26th, 2026" -> strip ordinal -> "Jun 2, 2006".
		raw := parseutil.StripOrdinalSuffix(strings.TrimSpace(m[1]))
		if d, derr := parseutil.TryParseDate(raw, "Jan 2, 2006"); derr == nil {
			scene.Date = d.UTC()
		}
	}

	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}

	if m := posterRe.FindStringSubmatch(detail); m != nil {
		scene.Thumbnail = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	return scene
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["Cookie"] = "warningHidden=hide"
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: headers})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "<br>", " ")
	s = strings.ReplaceAll(s, "<br/>", " ")
	s = strings.ReplaceAll(s, "<br />", " ")
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

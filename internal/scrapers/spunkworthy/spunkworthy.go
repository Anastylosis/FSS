// Package spunkworthy scrapes SpunkWorthy (spunkworthy.com), a legacy gay
// amateur studio. The /preview/videos?page=N listing yields cards with the
// scene id, title and poster; the publish date is hidden inside an HTML
// comment on each card. The detail page (/preview/view_video/{id}) adds the
// performers (rendered as "More of {Name}") and a multi-paragraph description.
package spunkworthy

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
	siteID        = "spunkworthy"
	studioName    = "SpunkWorthy"
	detailWorkers = 4
)

var siteBase = "https://www.spunkworthy.com"

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
		"spunkworthy.com/preview/videos",
		"spunkworthy.com/preview/view_video/{id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?spunkworthy\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	// Each listing card is a <div class="vid"> block.
	cardSplitRe  = regexp.MustCompile(`<div class="vid">`)
	cardLinkRe   = regexp.MustCompile(`<p><a href="/preview/view_video/(\d+)[^"]*">([^<]+)</a></p>`)
	cardPosterRe = regexp.MustCompile(`<img src="(/preview/videos/[^"]+/poster_tn\.jpg)"`)
	cardDateRe   = regexp.MustCompile(`<!--\s*<span class="date">([^<]+)</span>\s*-->`)

	modelLinkRe = regexp.MustCompile(`<a href="/preview/view_guy/\d+">More of ([^<]+)</a>`)
	vidTextRe   = regexp.MustCompile(`(?s)<div class="vid_text">(.*)`)
	pRe         = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/preview/videos?page=%d", siteBase, page)
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
	id     string
	title  string
	poster string
	date   string
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
		m := cardLinkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		it := listItem{id: id, title: html.UnescapeString(strings.TrimSpace(m[2]))}
		if p := cardPosterRe.FindStringSubmatch(card); p != nil {
			it.poster = siteBase + p[1]
		}
		if d := cardDateRe.FindStringSubmatch(card); d != nil {
			it.date = strings.TrimSpace(d[1])
		}
		items = append(items, it)
	}
	scraper.Debugf(1, "spunkworthy: listing %s -> %d cards", pageURL, len(items))
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "spunkworthy: fetching %d details with %d workers", len(items), workers)
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
		URL:       fmt.Sprintf("%s/preview/view_video/%s", siteBase, it.id),
		Thumbnail: it.poster,
		Studio:    studioName,
		ScrapedAt: now,
	}
	// Date lives in the listing card comment, e.g. "19 Jun 26".
	if d, err := parseutil.TryParseDate(it.date, "2 Jan 06"); err == nil {
		scene.Date = d.UTC()
	}

	body, err := s.get(ctx, scene.URL)
	if err != nil {
		return scene
	}
	detail := string(body)

	var performers []string
	seen := make(map[string]bool)
	for _, m := range modelLinkRe.FindAllStringSubmatch(detail, -1) {
		name := html.UnescapeString(strings.TrimSpace(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			performers = append(performers, name)
		}
	}
	scene.Performers = performers

	// Description paragraphs live in vid_text. Skip any <p> that carries a
	// link (the "More of {Name}" model, nav, related-photos) or the Tags
	// line — the real synopsis paragraphs are plain text.
	if m := vidTextRe.FindStringSubmatch(detail); m != nil {
		var paras []string
		for _, p := range pRe.FindAllStringSubmatch(m[1], -1) {
			inner := p[1]
			if strings.Contains(inner, "<a ") {
				continue
			}
			text := cleanText(inner)
			if text == "" || text == " " || strings.HasPrefix(text, "Tags:") {
				continue
			}
			paras = append(paras, text)
		}
		scene.Description = strings.Join(paras, "\n\n")
	}

	return scene
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

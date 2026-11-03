// Package plushies scrapes Plushies (plushies.tv), an Elevated X "Classic"
// tour with latestUpdateB cards served from /tour.
//
// The listing carries id, title, performers, duration and thumbnail; the detail
// page adds the description and tags. The CMS exposes no release date, so the
// date is taken from the upload folder in the trailer path
// (upload/YYYY_MM_DD_Title/). Out-of-range pages clamp to the last page, which
// the dedup in run() and Paginate's repeat-page guard turn into a stop.
package plushies

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "plushies"
	studioName    = "Plushies"
	detailWorkers = 4
)

var siteBase = "https://plushies.tv"

// Scraper implements scraper.StudioScraper for Plushies.
type Scraper struct {
	Client *http.Client
}

// New constructs a Plushies scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"plushies.tv",
		"plushies.tv/tour/categories/movies.html",
		"plushies.tv/tour/scenes/{slug}_vids.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?plushies\.tv(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) pageURL(page int) string {
	if page <= 1 {
		return siteBase + "/tour/categories/movies.html"
	}
	return fmt.Sprintf("%s/tour/categories/movies_%d.html", siteBase, page)
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
	cardRe  = regexp.MustCompile(`<div class="latestUpdateB" data-setid="(\d+)"`)
	titleRe = regexp.MustCompile(`(?s)<h4 class="link_bright">\s*<a\s+href="([^"]+/scenes/[^"]+_vids\.html)"[^>]*>\s*(.*?)\s*</a>`)
	perfRe  = regexp.MustCompile(`href="[^"]*/models/[^"]+\.html">([^<]+)</a>`)
	durRe   = regexp.MustCompile(`fa-video"></i>\s*(\d+)\s*min`)
	thumbRe = regexp.MustCompile(`src0_2x="([^"]+)"`)
)

type listItem struct {
	id, title, url, thumb string
	duration              int
	performers            []string
}

func absURL(u string) string {
	if strings.HasPrefix(u, "/") {
		return siteBase + u
	}
	return u
}

func parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringSubmatchIndex(page, -1)
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
			id:    page[loc[2]:loc[3]],
			url:   absURL(m[1]),
			title: html.UnescapeString(strings.TrimSpace(m[2])),
		}
		for _, pm := range perfRe.FindAllStringSubmatch(card, -1) {
			if name := html.UnescapeString(strings.TrimSpace(pm[1])); name != "" {
				it.performers = append(it.performers, name)
			}
		}
		if d := durRe.FindStringSubmatch(card); d != nil {
			n, _ := strconv.Atoi(d[1])
			it.duration = n * 60
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = absURL(th[1])
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
		ID:         it.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
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
	applyDetail(&scene, string(body))
	return scene
}

var (
	uploadDateRe = regexp.MustCompile(`/upload/(\d{4})_(\d{2})_(\d{2})_`)
	descBlockRe  = regexp.MustCompile(`(?s)<div class="vidImgContent[^"]*">(.*?)<!-- List Of Keyword Tags -->`)
	tagStripRe   = regexp.MustCompile(`(?s)<[^>]+>`)
	tagRe        = regexp.MustCompile(`href="[^"]*/categories/[^"]+\.html"><i class="[^"]*"></i>([^<]+)</a>`)
)

func applyDetail(scene *models.Scene, detail string) {
	if m := uploadDateRe.FindStringSubmatch(detail); m != nil {
		if t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3]); err == nil {
			scene.Date = t.UTC()
		}
	}
	if m := descBlockRe.FindStringSubmatch(detail); m != nil {
		text := html.UnescapeString(tagStripRe.ReplaceAllString(m[1], " "))
		text = strings.NewReplacer("```html", " ", "```", " ").Replace(text)
		scene.Description = strings.Join(strings.Fields(text), " ")
	}
	for _, tm := range tagRe.FindAllStringSubmatch(detail, -1) {
		tag := html.UnescapeString(strings.TrimSpace(tm[1]))
		if tag != "" && !contains(scene.Tags, tag) {
			scene.Tags = append(scene.Tags, tag)
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

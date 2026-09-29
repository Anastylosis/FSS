// Package pissplay scrapes Piss Play (pissplay.com), a WordPress couple-site
// (Bruce and Morgan). The WP REST API is locked, so the scene list is taken
// from the sitemap (every /videos/{slug} <loc> is a scene) and each detail
// page is parsed for its OpenGraph tags plus the JSON-LD WebPage
// "datePublished" field (with the on-page video_date div as a date fallback).
package pissplay

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
	siteID     = "pissplay"
	studioName = "Piss Play"
	siteBase   = "https://pissplay.com"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?pissplay\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// drive the whole walk instead of only the parsers.
	base string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"pissplay.com",
		"pissplay.com/videos/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) sitemapURL() string { return s.base + "/sitemap.xml" }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "pissplay: fetching sitemap %s", s.sitemapURL())
	body, err := s.fetchPage(ctx, s.sitemapURL())
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	slugs := parseSitemap(body)
	scraper.Debugf(1, "pissplay: sitemap lists %d scene slugs", len(slugs))

	select {
	case out <- scraper.Progress(len(slugs)):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan string)
	var wg sync.WaitGroup
	scraper.Debugf(1, "pissplay: fetching detail pages with %d workers", workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for slug := range work {
				scene, err := s.fetchDetail(ctx, slug, studioURL, opts.Delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(work)
		for _, slug := range slugs {
			if opts.KnownIDs[slug] {
				scraper.Debugf(1, "pissplay: hit known ID %s, stopping early", slug)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- slug:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
}

var sitemapLocRe = regexp.MustCompile(`<loc>\s*https?://(?:www\.)?pissplay\.com/videos/([A-Za-z0-9_-]+)\s*</loc>`)

// parseSitemap extracts scene slugs from the sitemap XML. Only /videos/{slug}
// entries are scenes; all other paths (blog, models, loops, etc.) are skipped.
func parseSitemap(body []byte) []string {
	page := string(body)
	matches := sitemapLocRe.FindAllStringSubmatch(page, -1)
	slugs := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		slug := m[1]
		if seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs
}

var (
	jsonLDDateRe = regexp.MustCompile(`"datePublished"\s*:\s*"([0-9]{4}-[0-9]{2}-[0-9]{2})`)
	videoDateRe  = regexp.MustCompile(`(?s)<div class="video_date">.*?</svg>\s*([0-9]{1,2}\s+[A-Za-z]{3,}\s+[0-9]{4})\s*</div>`)
)

type detailData struct {
	title       string
	description string
	thumbnail   string
	url         string
	date        time.Time
}

func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)

	og := parseutil.OpenGraph(body)
	d.title = strings.TrimSpace(html.UnescapeString(og["og:title"]))
	d.description = strings.TrimSpace(html.UnescapeString(og["og:description"]))
	d.thumbnail = strings.TrimSpace(html.UnescapeString(og["og:image"]))
	d.url = strings.TrimSpace(html.UnescapeString(og["og:url"]))

	if m := jsonLDDateRe.FindStringSubmatch(page); m != nil {
		if t, err := parseutil.TryParseDate(m[1], "2006-01-02"); err == nil {
			d.date = t.UTC()
		}
	}
	if d.date.IsZero() {
		if m := videoDateRe.FindStringSubmatch(page); m != nil {
			if t, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "2 Jan 2006"); err == nil {
				d.date = t.UTC()
			}
		}
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, slug, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	url := s.base + "/videos/" + slug
	scene := models.Scene{
		ID:        slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       url,
		Studio:    studioName,
		ScrapedAt: time.Now().UTC(),
	}

	body, err := s.fetchPage(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", slug, err)
	}
	d := parseDetail(body)
	scene.Title = d.title
	scene.Description = d.description
	scene.Thumbnail = d.thumbnail
	scene.Date = d.date
	if d.url != "" {
		scene.URL = d.url
	}
	if scene.Title == "" {
		scene.Title = slugToTitle(slug)
	}
	return scene, nil
}

func slugToTitle(slug string) string {
	return strings.ReplaceAll(slug, "-", " ")
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

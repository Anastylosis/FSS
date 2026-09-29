// Package cearalynch scrapes Ceara Lynch (cearalynch.com), a solo-creator site
// on Drupal 7. The site has no listing API or rich OpenGraph metadata, so the
// scene list is taken from /sitemap.xml (filtered to /video/{slug} entries,
// each carrying a <lastmod> crawl date) and each detail page is fetched by a
// worker pool to recover the title, description and thumbnail.
package cearalynch

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
	siteID     = "cearalynch"
	studioName = "Ceara Lynch"
	performer  = "Ceara Lynch"
	siteBase   = "https://www.cearalynch.com"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?cearalynch\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// drive the sitemap -> detail walk.
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
		"cearalynch.com",
		"cearalynch.com/video/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// sitemapEntry is one /video/{slug} record from the sitemap.
type sitemapEntry struct {
	slug    string
	url     string
	lastmod time.Time
}

func (s *Scraper) sitemapURL() string { return s.base + "/sitemap.xml" }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "cearalynch: fetching sitemap %s", s.sitemapURL())
	body, err := s.fetchPage(ctx, s.sitemapURL())
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	entries := parseSitemap(s.base, body)
	scraper.Debugf(1, "cearalynch: sitemap listed %d video entries", len(entries))

	select {
	case out <- scraper.Progress(len(entries)):
	case <-ctx.Done():
		return
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan sitemapEntry)
	var wg sync.WaitGroup
	scraper.Debugf(1, "cearalynch: fetching %d detail pages with %d workers", len(entries), workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range work {
				scene, err := s.fetchDetail(ctx, e, studioURL, opts.Delay)
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
		for _, e := range entries {
			if ctx.Err() != nil {
				return
			}
			if opts.KnownIDs[e.slug] {
				scraper.Debugf(1, "cearalynch: skipping known ID %s", e.slug)
				continue
			}
			select {
			case work <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
}

var (
	urlBlockRe  = regexp.MustCompile(`(?s)<url>(.*?)</url>`)
	locRe       = regexp.MustCompile(`<loc>\s*([^<]+?)\s*</loc>`)
	lastmodRe   = regexp.MustCompile(`<lastmod>\s*([^<]+?)\s*</lastmod>`)
	videoPathRe = regexp.MustCompile(`^https?://[^/]*cearalynch\.com/video/([^/?#]+)$`)
)

// parseSitemap returns one entry per /video/{slug} <url> block in the sitemap,
// capturing the slug and <lastmod>. Non-video paths (/content, /gallery,
// /links, the home page) are skipped.
func parseSitemap(base string, body []byte) []sitemapEntry {
	page := string(body)
	var entries []sitemapEntry
	seen := map[string]bool{}
	for _, block := range urlBlockRe.FindAllStringSubmatch(page, -1) {
		lm := locRe.FindStringSubmatch(block[1])
		if lm == nil {
			continue
		}
		loc := strings.TrimSpace(lm[1])
		vm := videoPathRe.FindStringSubmatch(loc)
		if vm == nil {
			continue
		}
		slug := vm[1]
		if seen[slug] {
			continue
		}
		seen[slug] = true

		e := sitemapEntry{slug: slug, url: base + "/video/" + slug}
		if mm := lastmodRe.FindStringSubmatch(block[1]); mm != nil {
			if t, err := parseutil.TryParseDate(strings.TrimSpace(mm[1]),
				"2006-01-02T15:04Z", "2006-01-02T15:04:05Z07:00", time.RFC3339, "2006-01-02"); err == nil {
				e.lastmod = t.UTC()
			}
		}
		entries = append(entries, e)
	}
	return entries
}

var (
	titleRe = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	descRe  = regexp.MustCompile(`<meta\s+name="description"\s+content="([^"]*)"`)
	thumbRe = regexp.MustCompile(`src="([^"]*/video_image/[^"]+)"`)
)

type detailData struct {
	title       string
	description string
	thumbnail   string
}

func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)

	if m := titleRe.FindStringSubmatch(page); m != nil {
		title := strings.TrimSpace(html.UnescapeString(m[1]))
		// Strip the trailing " | Ceara Lynch" site-name suffix.
		if i := strings.LastIndex(title, "|"); i >= 0 {
			title = strings.TrimSpace(title[:i])
		}
		d.title = title
	}
	if m := descRe.FindStringSubmatch(page); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := thumbRe.FindStringSubmatch(page); m != nil {
		d.thumbnail = m[1]
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, e sitemapEntry, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	scene := models.Scene{
		ID:         e.slug,
		SiteID:     siteID,
		StudioURL:  studioURL,
		URL:        e.url,
		Date:       e.lastmod,
		Studio:     studioName,
		Performers: []string{performer},
		ScrapedAt:  time.Now().UTC(),
	}

	body, err := s.fetchPage(ctx, e.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", e.slug, err)
	}
	d := parseDetail(body)
	scene.Title = d.title
	scene.Description = d.description
	scene.Thumbnail = d.thumbnail
	if scene.Title == "" {
		scene.Title = slugToTitle(e.slug)
	}
	return scene, nil
}

func slugToTitle(slug string) string {
	return strings.ReplaceAll(slug, "-", " ")
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

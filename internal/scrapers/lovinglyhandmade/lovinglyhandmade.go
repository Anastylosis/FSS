// Package lovinglyhandmade scrapes Lovingly Handmade Pornography
// (lovinglyhandmadepornography.com), a custom Ruby on Rails + Hotwire/Turbo
// site. The flat /sitemap.xml lists every /detail/{slug} scene; each detail
// page carries the title, date (in the twitter:title suffix), description,
// thumbnail, mixed performer/content tags, and an optional duration.
package lovinglyhandmade

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "lovinglyhandmade"
	studioName = "Lovingly Handmade Pornography"
	siteBase   = "https://lovinglyhandmadepornography.com"
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?lovinglyhandmadepornography\.com(?:/|$)`)

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
		"lovinglyhandmadepornography.com",
		"lovinglyhandmadepornography.com/detail/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "lovinglyhandmade: fetching sitemap")
	body, err := s.fetchPage(ctx, siteBase+"/sitemap.xml")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	slugs := parseSitemap(body)
	scraper.Debugf(1, "lovinglyhandmade: sitemap listed %d scenes", len(slugs))
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
	scraper.Debugf(1, "lovinglyhandmade: fetching detail pages with %d workers", workers)
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
			if ctx.Err() != nil {
				return
			}
			if opts.KnownIDs[slug] {
				continue
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

var locRe = regexp.MustCompile(`<loc>\s*` + regexp.QuoteMeta(siteBase) + `/detail/([^<\s]+)\s*</loc>`)

// parseSitemap extracts the scene slug from every /detail/{slug} <loc> entry.
func parseSitemap(body []byte) []string {
	matches := locRe.FindAllSubmatch(body, -1)
	slugs := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		slug := string(m[1])
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs
}

var (
	titleRe        = regexp.MustCompile(`<h1 class="text-2xl font-bold mb-2">([^<]*)</h1>`)
	twitterTitleRe = regexp.MustCompile(`<meta name="twitter:title" content="([^"]*)"`)
	descRe         = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	twitterDescRe  = regexp.MustCompile(`<meta name="twitter:description" content="([^"]*)"`)
	thumbRe        = regexp.MustCompile(`<meta name="twitter:image:src" content="([^"]*)"`)
	tagRe          = regexp.MustCompile(`<a[^>]*\brel="tag"[^>]*\bhref="/tagged/[^"]*"[^>]*>([^<]*)</a>`)
	durationRe     = regexp.MustCompile(`data-full-duration="(\d+)"`)
	dateSuffixRe   = regexp.MustCompile(`\|\s*(\d{4}-\d{2}-\d{2})\s*$`)
	updatedRe      = regexp.MustCompile(`Updated:\s*(\d{4}-\d{2}-\d{2})`)
)

type detailData struct {
	title       string
	date        time.Time
	description string
	thumbnail   string
	tags        []string
	duration    int
}

func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)

	twTitle := ""
	if m := twitterTitleRe.FindStringSubmatch(page); m != nil {
		twTitle = html.UnescapeString(m[1])
	}

	if m := titleRe.FindStringSubmatch(page); m != nil {
		d.title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if d.title == "" && twTitle != "" {
		// Fall back to the twitter:title with the " | YYYY-MM-DD" suffix removed.
		d.title = strings.TrimSpace(dateSuffixRe.ReplaceAllString(twTitle, ""))
	}

	if m := dateSuffixRe.FindStringSubmatch(twTitle); m != nil {
		if t, err := time.Parse("2006-01-02", m[1]); err == nil {
			d.date = t.UTC()
		}
	}
	if d.date.IsZero() {
		if m := updatedRe.FindStringSubmatch(page); m != nil {
			if t, err := time.Parse("2006-01-02", m[1]); err == nil {
				d.date = t.UTC()
			}
		}
	}

	if m := descRe.FindStringSubmatch(page); m != nil {
		d.description = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if d.description == "" {
		if m := twitterDescRe.FindStringSubmatch(page); m != nil {
			d.description = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}

	if m := thumbRe.FindStringSubmatch(page); m != nil {
		d.thumbnail = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	seen := map[string]bool{}
	for _, m := range tagRe.FindAllStringSubmatch(page, -1) {
		tag := strings.TrimSpace(html.UnescapeString(m[1]))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		d.tags = append(d.tags, tag)
	}

	if m := durationRe.FindStringSubmatch(page); m != nil {
		if secs, err := parseInt(m[1]); err == nil && secs > 0 && secs <= 604800 {
			d.duration = secs
		}
	}

	return d
}

func parseInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func (s *Scraper) fetchDetail(ctx context.Context, slug, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	detailURL := siteBase + "/detail/" + slug
	body, err := s.fetchPage(ctx, detailURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", slug, err)
	}
	d := parseDetail(body)

	scene := models.Scene{
		ID:          slug,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       d.title,
		URL:         detailURL,
		Date:        d.date,
		Description: d.description,
		Thumbnail:   d.thumbnail,
		Tags:        d.tags,
		Duration:    d.duration,
		Studio:      studioName,
		ScrapedAt:   time.Now().UTC(),
	}
	if scene.Title == "" {
		scene.Title = strings.ReplaceAll(slug, "-", " ")
	}
	return scene, nil
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

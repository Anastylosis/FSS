// Package venuslux scrapes Venus Lux Entertainment (venus-lux.com), an NATS
// tour whose listing cards are `div.product-item` blocks paged as
// `/categories/movies_{N}_d.html`. The detail page adds the description only;
// the site publishes no dates or performer lists. See docs/scrapers.md.
package venuslux

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "venuslux"
	studioName = "Venus Lux Entertainment"
)

var siteBase = "https://venus-lux.com"

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?venus-lux\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)

	cardRe    = regexp.MustCompile(`<div class="product-item">`)
	hrefRe    = regexp.MustCompile(`href="([^"]*/trailers/([^"/]+)\.html)"`)
	titleRe   = regexp.MustCompile(`(?s)<h3 class="vid[^"]*">\s*<a[^>]*>(.*?)</a>`)
	thumbRe   = regexp.MustCompile(`<img[^>]*\ssrc="([^"]+)"`)
	thumbIDRe = regexp.MustCompile(`/(\d+)-\d+x\.[a-z]+$`)
	previewRe = regexp.MustCompile(`<source src="([^"#]+)`)
	descRe    = regexp.MustCompile(`(?s)<h4>(.*?)(?:</h4>|</div>)`)
	tagRe     = regexp.MustCompile(`<[^>]*>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"venus-lux.com", "venus-lux.com/categories/{category}.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func category(u string) string {
	if m := categoryRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return "movies"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	cat := category(studioURL)
	scraper.Debugf(1, "%s: scraping category %q", siteID, cat)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetch(ctx, fmt.Sprintf("%s/categories/%s_%d_d.html", siteBase, cat, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseListing(string(body), studioURL, now)
		s.enrich(ctx, scenes, opts)
		return scraper.PageResult{Scenes: scenes}, ctx.Err()
	})
}

// enrich adds descriptions. The listing already names the scene, so a detail
// page that will not load only costs the description.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts) {
	workers := scraper.WorkerCount(opts, 4)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var wg sync.WaitGroup
	idx := make(chan int)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				body, err := s.fetch(ctx, scenes[i].URL)
				if err != nil {
					scraper.Debugf(1, "%s: detail %s: %v", siteID, scenes[i].URL, err)
					continue
				}
				scenes[i].Description = parseDescription(string(body))
			}
		}()
	}
loop:
	for i := range scenes {
		if opts.KnownIDs[scenes[i].ID] {
			continue
		}
		select {
		case idx <- i:
		case <-ctx.Done():
			break loop
		}
	}
	close(idx)
	wg.Wait()
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseListing(body, studioURL string, now time.Time) []models.Scene {
	locs := cardRe.FindAllStringIndex(body, -1)
	var scenes []models.Scene
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]
		href := hrefRe.FindStringSubmatch(block)
		title := titleRe.FindStringSubmatch(block)
		if href == nil || title == nil {
			continue
		}
		sc := models.Scene{
			ID:        href[2],
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     clean(title[1]),
			URL:       absolute(html.UnescapeString(href[1])),
			Studio:    studioName,
			ScrapedAt: now,
		}
		if t := thumbRe.FindStringSubmatch(block); t != nil {
			sc.Thumbnail = absolute(html.UnescapeString(t[1]))
			if m := thumbIDRe.FindStringSubmatch(sc.Thumbnail); m != nil {
				sc.ID = m[1]
			}
		}
		if p := previewRe.FindStringSubmatch(block); p != nil {
			sc.Preview = absolute(html.UnescapeString(p[1]))
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func parseDescription(body string) string {
	if m := descRe.FindStringSubmatch(body); m != nil {
		return clean(m[1])
	}
	return ""
}

func absolute(u string) string {
	base, err := url.Parse(siteBase + "/")
	if err != nil {
		return u
	}
	ref, err := url.Parse(u)
	if err != nil {
		return u
	}
	return base.ResolveReference(ref).String()
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}

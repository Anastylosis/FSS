// Package rawroadnation scrapes Raw Road Nation, an Elevated X tour whose
// listing cards are `div.video-thumb[data-setid]`. The detail page carries
// performers, date and description inside HTML comments, so the regexes are
// not anchored on the comment markers. See docs/scrapers.md.
package rawroadnation

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
	siteID     = "rawroadnation"
	studioName = "Raw Road Nation"
)

var siteBase = "https://www.rawroadnation.com"

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?rawroadnation\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)

	cardStartRe = regexp.MustCompile(`<div[^>]*class="[^"]*video-thumb[^"]*"[^>]*data-setid="(\d+)"`)
	hrefRe      = regexp.MustCompile(`<a[^>]*href="([^"]+/updates/[^"]+\.html)"`)
	titleRe     = regexp.MustCompile(`(?s)<h3 class="scene-title">(.*?)</h3>`)
	thumbRe     = regexp.MustCompile(`<img[^>]*\ssrc="([^"]+)"`)

	modelsRe = regexp.MustCompile(`(?s)class="model_name_title"[^>]*>(.*?)</a>`)
	dateRe   = regexp.MustCompile(`class="update_date">\s*(\d{2}/\d{2}/\d{4})`)
	descRe   = regexp.MustCompile(`(?s)class="latest_update_description">(.*?)</span>`)
	tagRe    = regexp.MustCompile(`<[^>]*>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"rawroadnation.com", "rawroadnation.com/tour/categories/{category}.html"}
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
		body, err := s.fetch(ctx, fmt.Sprintf("%s/tour/categories/%s_%d_d.html", siteBase, cat, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := parseListing(string(body), studioURL, now)
		if err := s.enrich(ctx, scenes, opts); err != nil {
			return scraper.PageResult{}, err
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts) error {
	workers := scraper.WorkerCount(opts, 4)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), workers)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		idx      = make(chan int)
	)
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
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					continue
				}
				applyDetail(string(body), &scenes[i])
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
	if err := ctx.Err(); err != nil {
		return err
	}
	return firstErr
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
	locs := cardStartRe.FindAllStringSubmatchIndex(body, -1)
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
			ID:        body[loc[2]:loc[3]],
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     clean(title[1]),
			URL:       html.UnescapeString(href[1]),
			Studio:    studioName,
			ScrapedAt: now,
		}
		if t := thumbRe.FindStringSubmatch(block); t != nil {
			sc.Thumbnail = html.UnescapeString(t[1])
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func applyDetail(body string, sc *models.Scene) {
	for _, m := range modelsRe.FindAllStringSubmatch(body, -1) {
		if n := clean(m[1]); n != "" {
			sc.Performers = append(sc.Performers, n)
		}
	}
	if m := dateRe.FindStringSubmatch(body); m != nil {
		if t, err := time.Parse("01/02/2006", m[1]); err == nil {
			sc.Date = t.UTC()
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}

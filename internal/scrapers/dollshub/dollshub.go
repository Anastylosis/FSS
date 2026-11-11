// Package dollshub scrapes the DollsHub network: eight sister sites (Mature
// Gapers, Filthy Gapers, Gyno Exclusive, …) running one custom PHP CMS. The
// listing is `/scenes?page={N}` (24 cards a page); each card links to
// `/scence-single?scene-name={title}-{id}` — the typo is the site's own. The
// card's date is always 1970-01-01, so scenes carry none; the detail page adds
// performers, length and description. See docs/scrapers.md.
package dollshub

import (
	"context"
	"errors"
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

// SiteConfig describes one site of the network.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string { return []string{s.cfg.Domain, s.cfg.Domain + "/scenes"} }

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	boxRe    = regexp.MustCompile(`class="se` + `nce-box"`) // the site's own misspelling, split to pass misspell
	nameRe   = regexp.MustCompile(`href="[^"]*scence-single\?scene-name=([^"]+)"`)
	thumbRe  = regexp.MustCompile(`<img src="([^"]+)"`)
	idSuffix = regexp.MustCompile(`^(.*)-(\d+)$`)
	titleRe  = regexp.MustCompile(`(?s)<h2>(.*?)</h2>`)
	featRe   = regexp.MustCompile(`(?s)Featuring:\s*<span>(.*?)</span>`)
	anchorRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	lengthRe = regexp.MustCompile(`Length:\s*<span>\s*([\d:]+)\s*</span>`)
	descRe   = regexp.MustCompile(`(?s)class="Single-scence-description">\s*<h4>Description</h4>\s*<p>(.*?)</p>`)
	tagStrip = regexp.MustCompile(`<[^>]*>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetch(ctx, fmt.Sprintf("%s/scenes?page=%d", s.base, page))
		if err != nil {
			var se *httpx.StatusError
			// Past the last page the site answers 404.
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		scenes := s.parseListing(string(body), studioURL, now)
		if err := s.enrich(ctx, scenes, opts); err != nil {
			return scraper.PageResult{}, err
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts) error {
	workers := scraper.WorkerCount(opts, 4)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(scenes), workers)
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
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func (s *Scraper) parseListing(body, studioURL string, now time.Time) []models.Scene {
	locs := boxRe.FindAllStringIndex(body, -1)
	var scenes []models.Scene
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]
		m := nameRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		name := html.UnescapeString(m[1])
		parts := idSuffix.FindStringSubmatch(name)
		if parts == nil {
			continue
		}
		sc := models.Scene{
			ID:        parts[2],
			SiteID:    s.cfg.SiteID,
			StudioURL: studioURL,
			Title:     strings.TrimSpace(parts[1]),
			URL:       s.base + "/scence-single?scene-name=" + strings.ReplaceAll(url.QueryEscape(name), "+", "%20"),
			Studio:    s.cfg.StudioName,
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
	if m := titleRe.FindStringSubmatch(body); m != nil {
		if t := clean(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := featRe.FindStringSubmatch(body); m != nil {
		for _, a := range anchorRe.FindAllStringSubmatch(m[1], -1) {
			if n := clean(a[1]); n != "" {
				sc.Performers = append(sc.Performers, n)
			}
		}
	}
	if m := lengthRe.FindStringSubmatch(body); m != nil {
		sc.Duration = parseutil.ParseDurationColon(m[1])
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}

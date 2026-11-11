// Package elxplaylistutil scrapes Elevated X tours whose listing cards are
// `div.mpp-playlist-item` blocks (Only 3x VR, Only 3x Lost):
//
//	<div class="mpp-playlist-item" data-thumb='<img alt="only3xlost_bella_modele_0035" src="…/4-large.jpg" />'
//	     data-hover-path='/trailers/mh/68.mp4' data-link="/updates/slug.html"
//	     data-title="…" data-models='<div class="starring">Starring</div><a href="/models/x.html">Name</a>'>
//
// Pages are `/categories/movies_{N}_d.html`; the walk ends on an empty page.
// The card's `alt` is the only stable id — the slug follows the title.
// A site either has a usable detail page (date, duration, description, tags)
// or is ListingOnly (a card whose detail page 404s keeps its card data): Only 3x VR's detail pages die in a PHP fatal error.
// See docs/scrapers.md.
package elxplaylistutil

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// SiteConfig describes one site served by this template.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
	// ListingOnly skips the detail fetch; the card alone makes the scene.
	ListingOnly bool
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

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/categories/{category}.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)

	cardRe    = regexp.MustCompile(`<div class="mpp-playlist-item"`)
	linkRe    = regexp.MustCompile(`data-link="([^"]+)"`)
	titleRe   = regexp.MustCompile(`data-title="([^"]*)"`)
	thumbRe   = regexp.MustCompile(`(?s)data-thumb='(.*?)'\s`)
	imgSrcRe  = regexp.MustCompile(`\ssrc="([^"]+)"`)
	imgAltRe  = regexp.MustCompile(`\salt="([^"]+)"`)
	hoverRe   = regexp.MustCompile(`data-hover-path='([^']+)'`)
	modelsRe  = regexp.MustCompile(`(?s)data-models='(.*?)'>`)
	anchorRe  = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	spanRe    = regexp.MustCompile(`(?s)<span>(.*?)</span>`)
	dateRe    = regexp.MustCompile(`<span>since</span>\s*(\d{2}/\d{2}/\d{4})`)
	minutesRe = regexp.MustCompile(`icon-time"></i>\s*(\d+)(?:&nbsp;|\s)*min`)
	descRe    = regexp.MustCompile(`(?s)<div class="set-desc">(.*?)</div>`)
	tagsRe    = regexp.MustCompile(`(?s)<div class="content tag">(.*?)</div>`)
	tagStrip  = regexp.MustCompile(`<[^>]*>`)
	slugRe    = regexp.MustCompile(`/([^/]+)\.html`)
)

func category(u string) string {
	if m := categoryRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return "movies"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	cat := category(studioURL)
	scraper.Debugf(1, "%s: scraping category %q", s.cfg.SiteID, cat)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetch(ctx, fmt.Sprintf("%s/categories/%s_%d_d.html", s.base, cat, page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := s.parseListing(string(body), studioURL, now)
		if !s.cfg.ListingOnly {
			if err := s.enrich(ctx, scenes, opts); err != nil {
				return scraper.PageResult{}, err
			}
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
				var se *httpx.StatusError
				if errors.As(err, &se) && (se.StatusCode == http.StatusNotFound || se.StatusCode == http.StatusGone) {
					scraper.Debugf(1, "%s: card links to a missing page %s, keeping the listing data", s.cfg.SiteID, scenes[i].URL)
					continue
				}
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

func (s *Scraper) abs(u string) string {
	base, err := url.Parse(s.base + "/")
	if err != nil {
		return u
	}
	ref, err := url.Parse(u)
	if err != nil {
		return u
	}
	return base.ResolveReference(ref).String()
}

func (s *Scraper) parseListing(body, studioURL string, now time.Time) []models.Scene {
	locs := cardRe.FindAllStringIndex(body, -1)
	var scenes []models.Scene
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]
		link := linkRe.FindStringSubmatch(block)
		title := titleRe.FindStringSubmatch(block)
		if link == nil || title == nil {
			continue
		}
		sc := models.Scene{
			SiteID:    s.cfg.SiteID,
			StudioURL: studioURL,
			Title:     clean(title[1]),
			URL:       s.abs(html.UnescapeString(link[1])),
			Studio:    s.cfg.StudioName,
			ScrapedAt: now,
		}
		if m := slugRe.FindStringSubmatch(link[1]); m != nil {
			sc.ID = m[1]
		}
		if t := thumbRe.FindStringSubmatch(block); t != nil {
			if m := imgSrcRe.FindStringSubmatch(t[1]); m != nil {
				sc.Thumbnail = s.abs(html.UnescapeString(m[1]))
			}
			if m := imgAltRe.FindStringSubmatch(t[1]); m != nil {
				sc.ID = html.UnescapeString(m[1])
			}
		}
		if m := hoverRe.FindStringSubmatch(block); m != nil {
			sc.Preview = s.abs(html.UnescapeString(m[1]))
		}
		if m := modelsRe.FindStringSubmatch(block); m != nil {
			sc.Performers = performers(m[1])
		}
		if sc.ID == "" || sc.Title == "" {
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func performers(block string) []string {
	var names []string
	for _, m := range anchorRe.FindAllStringSubmatch(block, -1) {
		if n := clean(m[1]); n != "" {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		return names
	}
	for _, m := range spanRe.FindAllStringSubmatch(block, -1) {
		for _, n := range strings.Split(clean(m[1]), " & ") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

func applyDetail(body string, sc *models.Scene) {
	if m := dateRe.FindStringSubmatch(body); m != nil {
		if t, err := time.Parse("01/02/2006", m[1]); err == nil {
			sc.Date = t.UTC()
		}
	}
	if m := minutesRe.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(m[1])
		sc.Duration = n * 60
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := tagsRe.FindStringSubmatch(body); m != nil {
		for _, a := range anchorRe.FindAllStringSubmatch(m[1], -1) {
			if n := clean(a[1]); n != "" {
				sc.Tags = append(sc.Tags, n)
			}
		}
	}
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}

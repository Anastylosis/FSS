// Package dreamboyplus scrapes the Dream Boy Plus network (dreamboyplus.com):
// Dream Boy Bondage, Roped Studs and Bound Up Boys, whose own domains all
// redirect into the network tour. One row per brand plus the whole network.
// See docs/scrapers.md.
package dreamboyplus

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

const (
	networkHost    = "dreamboyplus.com"
	defaultWorkers = 4
)

var siteBase = "https://dreamboyplus.com"

// SiteConfig is one brand of the network; an empty Site is the whole network.
type SiteConfig struct {
	SiteID     string
	StudioName string
	Site       string // the tour's ?site= value
	Domain     string // the brand's own domain, which redirects into the tour
}

var sites = []SiteConfig{
	{SiteID: "dreamboyplus", StudioName: "Dream Boy Plus"},
	{SiteID: "dreamboybondage", StudioName: "Dream Boy Bondage", Site: "dreamboybondage", Domain: "dreamboybondage.com"},
	{SiteID: "ropedstuds", StudioName: "Roped Studs", Site: "ropedstuds", Domain: "ropedstuds.com"},
	{SiteID: "boundupboys", StudioName: "Bound Up Boys", Site: "boundupboys", Domain: "boundupboys.com"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	if s.cfg.Site == "" {
		return []string{networkHost, networkHost + "/categories/movies.html"}
	}
	return []string{
		s.cfg.Domain,
		networkHost + "/" + s.cfg.Site + "/",
		networkHost + "/categories/movies.html?site=" + s.cfg.Site,
	}
}

// brandOf names the brand a network URL is filtered to: ?site= or the
// /{brand}/ landing path the brand domains redirect to.
func brandOf(u *url.URL) string {
	if v := u.Query().Get("site"); v != "" {
		return v
	}
	first, _, _ := strings.Cut(strings.Trim(u.Path, "/"), "/")
	for _, c := range sites {
		if c.Site != "" && first == c.Site {
			return c.Site
		}
	}
	return ""
}

func (s *Scraper) MatchesURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host == networkHost {
		return brandOf(u) == s.cfg.Site
	}
	return s.cfg.Domain != "" && host == s.cfg.Domain
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe = regexp.MustCompile(`<div class="item item-video`)
	idRe        = regexp.MustCompile(`id="set-target-(\d+)"`)
	linkRe      = regexp.MustCompile(`<h3><a href="([^"]+/trailers/[^"]+)" title="([^"]*)"`)
	modelsRe    = regexp.MustCompile(`(?s)<div class="item-models-list">(.*?)</div>`)
	thumbRe     = regexp.MustCompile(`class="video_placeholder stdimage" src="([^"]+)"`)

	infoRe    = regexp.MustCompile(`(?s)<span class="update-info-title">([^<]+)</span>\s*<span class="update-info-value">([^<]*)</span>`)
	descRe    = regexp.MustCompile(`(?s)<span class="flex-full">\s*(.*?)</span>`)
	channelRe = regexp.MustCompile(`(?s)video-page-channel">\s*<a [^>]*><img [^>]*alt="([^"]+)"`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type card struct {
	id, url, title, thumb string
	performers            []string
}

func (s *Scraper) listURL(page int) string {
	u := fmt.Sprintf("%s/categories/movies_%d.html", siteBase, page)
	if s.cfg.Site != "" {
		u += "?site=" + s.cfg.Site
	}
	return u
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := s.listURL(page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(u, errors.New("no video cards"))
		}
		if len(cards) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.details(ctx, cards, studioURL, opts, now, out)}, nil
	})
}

func parseListing(body string) []card {
	var cards []card
	for _, block := range cardSplitRe.Split(body, -1)[1:] {
		id := idRe.FindStringSubmatch(block)
		link := linkRe.FindStringSubmatch(block)
		if id == nil || link == nil {
			continue
		}
		c := card{id: id[1], url: html.UnescapeString(link[1]), title: clean(link[2])}
		if m := thumbRe.FindStringSubmatch(block); m != nil {
			c.thumb = absolute(html.UnescapeString(m[1]))
		}
		if m := modelsRe.FindStringSubmatch(block); m != nil {
			for _, n := range strings.Split(clean(m[1]), ",") {
				if n = strings.TrimSpace(n); n != "" {
					c.performers = append(c.performers, n)
				}
			}
		}
		cards = append(cards, c)
	}
	return cards
}

// absolute resolves the tour's root-relative image paths (and collapses the
// doubled slash it writes: /content//contentthumbs/…).
func absolute(p string) string {
	if strings.HasPrefix(p, "http") {
		return p
	}
	return siteBase + "/" + strings.TrimLeft(strings.ReplaceAll(p, "//", "/"), "/")
}

// details fetches each card's trailer page for date, runtime, description and
// brand. Known IDs keep a stub so Paginate stops at the end of the page; a
// failed fetch is reported and the card's fields kept.
func (s *Scraper) details(ctx context.Context, cards []card, studioURL string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(cards), workers)
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		scenes[i] = models.Scene{
			ID:         c.id,
			SiteID:     s.cfg.SiteID,
			StudioURL:  studioURL,
			Title:      c.title,
			URL:        c.url,
			Thumbnail:  c.thumb,
			Performers: c.performers,
			Studio:     s.cfg.StudioName,
			ScrapedAt:  now,
		}
		if opts.KnownIDs[c.id] {
			continue
		}
		wg.Add(1)
		go func(i int, c card) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.get(ctx, c.url)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body, s.cfg.Site == "")
		}(i, c)
	}
	wg.Wait()
	return scenes
}

func applyDetail(sc *models.Scene, body string, network bool) {
	for _, m := range infoRe.FindAllStringSubmatch(body, -1) {
		v := clean(m[2])
		switch strings.TrimSpace(m[1]) {
		case "ADDED:":
			if t, err := parseutil.TryParseDate(v, "January 2, 2006"); err == nil {
				sc.Date = t
			}
		case "RUNTIME:":
			sc.Duration = parseutil.ParseDurationColon(v)
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	// On the network row the brand logo names which site a scene is from.
	if m := channelRe.FindStringSubmatch(body); m != nil && network {
		sc.Series = clean(m[1])
	}
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	return string(b), err
}

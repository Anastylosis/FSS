// Package xsiteability scrapes clip sites hosted on xsiteability.com (the
// "/x-new/new-preview-grid.php?user=" tour). One config row per site. See
// docs/scrapers.md.
package xsiteability

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const defaultWorkers = 3

// SiteConfig is one xsiteability site.
type SiteConfig struct {
	SiteID     string
	Domain     string // e.g. "gkaproductions.com"
	User       string // the platform's account name, the grid's ?user=
	StudioName string
}

var sites = []SiteConfig{
	{SiteID: "gkaproductions", Domain: "gkaproductions.com", User: "gkaproductions", StudioName: "Ginary's Kinky Adventures"},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://" + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/x-new/" + s.cfg.User + "/preview/"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardRe    = regexp.MustCompile(`(?s)<li class="span4 prevgrid".*?</li>`)
	setIDRe   = regexp.MustCompile(`new-set\.php\?setid=(\d+)`)
	titleRe   = regexp.MustCompile(`(?s)<h4>(.*?)</h4>`)
	kindRe    = regexp.MustCompile(`(?s)<p>\s*(?:(\d{1,2}:\d{2}(?::\d{2})?)\s+)?(video|photos?)\s*</p>`)
	thumbRe   = regexp.MustCompile(`<img src="([^"]+)" class="aimg"`)
	setDescRe = regexp.MustCompile(`(?s)<p style="border:0px solid red;">(.*?)Free Downloads:`)
	priceRe   = regexp.MustCompile(`Individually for \$([\d.]+)`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type card struct {
	id, title, thumb string
	duration         int
}

func (s *Scraper) gridURL(page int) string {
	return fmt.Sprintf("%s/x-new/new-preview-grid.php?page=%d&user=%s", s.base, page, url.QueryEscape(s.cfg.User))
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	seen := map[string]bool{}
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := s.gridURL(page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards, total := parseGrid(body, s.base)
		if page == 1 && total == 0 {
			return scraper.PageResult{}, scraper.ParseError(u, errors.New("no sets on the grid"))
		}
		fresh := cards[:0]
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		next := strings.Contains(body, fmt.Sprintf("page=%d&", page+1))
		res := scraper.PageResult{
			Scenes: s.details(ctx, fresh, studioURL, opts, now, out),
			Done:   !next || total == 0,
		}
		// A page of photo sets yields nothing but is not the end.
		res.Continue = len(res.Scenes) == 0 && !res.Done
		return res, nil
	})
}

// parseGrid returns the video sets on a grid page and how many sets of any
// kind it carried.
func parseGrid(body, base string) ([]card, int) {
	var cards []card
	blocks := cardRe.FindAllString(body, -1)
	for _, b := range blocks {
		id := setIDRe.FindStringSubmatch(b)
		k := kindRe.FindStringSubmatch(b)
		if id == nil || k == nil || k[2] != "video" {
			continue
		}
		c := card{id: id[1], duration: parseutil.ParseDurationColon(k[1])}
		if m := titleRe.FindStringSubmatch(b); m != nil {
			c.title = clean(m[1])
		}
		if m := thumbRe.FindStringSubmatch(b); m != nil {
			c.thumb = base + strings.ReplaceAll(html.UnescapeString(m[1]), " ", "%20")
		}
		cards = append(cards, c)
	}
	return cards, len(blocks)
}

// details fetches each set page for the full description and price. Known
// IDs keep a stub so Paginate stops at the end of the page; a failed fetch is
// reported and the grid fields kept.
func (s *Scraper) details(ctx context.Context, cards []card, studioURL string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(cards), workers)
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		scenes[i] = models.Scene{
			ID:        c.id,
			SiteID:    s.cfg.SiteID,
			StudioURL: studioURL,
			Title:     c.title,
			URL:       s.base + "/x-new/new-set.php?setid=" + c.id,
			Thumbnail: c.thumb,
			Duration:  c.duration,
			Studio:    s.cfg.StudioName,
			ScrapedAt: now,
		}
		if opts.KnownIDs[c.id] {
			continue
		}
		wg.Add(1)
		go func(i int) {
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
			body, err := s.get(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applySet(&scenes[i], body, now)
		}(i)
	}
	wg.Wait()
	return scenes
}

func applySet(sc *models.Scene, body string, now time.Time) {
	if m := setDescRe.FindStringSubmatch(body); m != nil {
		d := clean(strings.NewReplacer("<br>", " ", "</p>", " ").Replace(m[1]))
		// The description repeats the title as its first line ("Title -").
		d = strings.TrimSpace(strings.TrimPrefix(d, sc.Title+" -"))
		sc.Description = d
	}
	if m := priceRe.FindStringSubmatch(body); m != nil {
		if p, err := strconv.ParseFloat(m[1], 64); err == nil && p > 0 {
			sc.AddPrice(models.PriceSnapshot{Date: now, Regular: p})
		}
	}
}

func clean(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
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

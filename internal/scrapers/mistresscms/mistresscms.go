// Package mistresscms scrapes clip stores running MistressCMS (the
// "Powered by MistressCMS" footer; /{lang}/store/videos/... paths). One
// config row per site. See docs/scrapers.md.
package mistresscms

import (
	"context"
	"errors"
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

const defaultWorkers = 3

// SiteConfig is one MistressCMS store.
type SiteConfig struct {
	SiteID     string
	Domain     string // bare domain, e.g. "mistress-iside.com"
	StudioName string
	Performer  string // the store's own domme, credited on every clip
}

var sites = []SiteConfig{
	{SiteID: "mistressiside", Domain: "mistress-iside.com", StudioName: "Mistress Iside", Performer: "Mistress Iside"},
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
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	d := s.cfg.Domain
	return []string{d, d + "/en/store/videos/new/p/{n}/"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardSplitRe  = regexp.MustCompile(`<div class="col-md-4 text-center video">`)
	cardLinkRe   = regexp.MustCompile(`<a href="([^"]+/store/videos/v/[^"]+)" class="videotitle">([^<]*)</a>`)
	cardDateRe   = regexp.MustCompile(`<div class="videodate">(\d{4}-\d{2}-\d{2})</div>`)
	cardPriceRe  = regexp.MustCompile(`<div class="videoprice"><strong>([\d.,]+)€</strong>`)
	cardPosterRe = regexp.MustCompile(`poster="([^"]+)"`)
	productIDRe  = regexp.MustCompile(`product_id=(\d+)`)
	totalRe      = regexp.MustCompile(`(\d+)\s+products`)

	lengthRe   = regexp.MustCompile(`Video-Length:\s*(?:(\d+)h\s*)?(?:(\d+)m\s*)?(?:(\d+)s)?`)
	descRe     = regexp.MustCompile(`(?s)<div class="col-md-12 videodescription">(.*?)</div>`)
	tagBlockRe = regexp.MustCompile(`(?s)<div class="col-md-12 videotags">(.*?)</div>`)
	tagRe      = regexp.MustCompile(`<a [^>]*>([^<]+)</a>`)
	ogImageRe  = regexp.MustCompile(`<meta name="og:image" content="([^"]+)"`)
	stripRe    = regexp.MustCompile(`<[^>]+>`)
)

type card struct {
	id, url, title, thumb string
	date                  time.Time
	price                 float64
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/en/store/videos/new/p/%d/", s.base, page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(u, errors.New("no video cards"))
		}
		fresh := cards[:0]
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		total := 0
		if m := totalRe.FindStringSubmatch(body); m != nil && page == 1 {
			total, _ = strconv.Atoi(m[1])
		}
		next := strings.Contains(body, fmt.Sprintf("/store/videos/new/p/%d/", page+1))
		scenes := s.details(ctx, fresh, studioURL, opts, now, out)
		return scraper.PageResult{Scenes: scenes, Total: total, Done: !next || len(fresh) == 0}, nil
	})
}

func parseListing(body string) []card {
	var cards []card
	for _, block := range cardSplitRe.Split(body, -1)[1:] {
		m := cardLinkRe.FindStringSubmatch(block)
		id := productIDRe.FindStringSubmatch(block)
		if m == nil || id == nil {
			continue
		}
		c := card{id: id[1], url: html.UnescapeString(m[1]), title: clean(m[2])}
		if d := cardDateRe.FindStringSubmatch(block); d != nil {
			c.date, _ = time.Parse("2006-01-02", d[1])
		}
		if p := cardPriceRe.FindStringSubmatch(block); p != nil {
			c.price = euros(p[1])
		}
		if p := cardPosterRe.FindStringSubmatch(block); p != nil {
			c.thumb = html.UnescapeString(p[1])
		}
		cards = append(cards, c)
	}
	return cards
}

// euros reads a European price ("1.021,99") as a float.
func euros(s string) float64 {
	s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// details builds each card's scene, fetching the product page for runtime,
// description and tags. Known IDs keep a stub so Paginate can stop early;
// a failed detail is reported and the card's own fields are kept.
func (s *Scraper) details(ctx context.Context, cards []card, studioURL string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(cards), workers)
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		scenes[i] = s.baseScene(c, studioURL, now)
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
			applyDetail(&scenes[i], body)
		}(i, c)
	}
	wg.Wait()
	return scenes
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        c.id,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       c.url,
		Date:      c.date,
		Thumbnail: c.thumb,
		Studio:    s.cfg.StudioName,
		ScrapedAt: now,
	}
	if s.cfg.Performer != "" {
		sc.Performers = []string{s.cfg.Performer}
	}
	if c.price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: c.price})
	}
	return sc
}

func applyDetail(sc *models.Scene, body string) {
	if m := lengthRe.FindStringSubmatch(body); m != nil && m[0] != "Video-Length:" {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		se, _ := strconv.Atoi(m[3])
		sc.Duration = h*3600 + mi*60 + se
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := tagBlockRe.FindStringSubmatch(body); m != nil {
		for _, t := range tagRe.FindAllStringSubmatch(m[1], -1) {
			if tag := clean(t[1]); tag != "" {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(m[1])
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

// Package flavaworks scrapes the FlavaWorks network — Thug Boy, Papi Cock, Raw
// Rods, Mix It Up Boy and Raw Rio — which share one Laravel tour behind a
// POST age gate. See docs/scrapers.md.
package flavaworks

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
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

// SiteConfig is one site on the network.
type SiteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []SiteConfig{
	{"thugboy", "thugboy.com", "Thug Boy"},
	{"papicock", "papicock.com", "Papi Cock"},
	{"rawrods", "rawrods.com", "Raw Rods"},
	{"mixitupboy", "mixitupboy.com", "Mix It Up Boy"},
	{"rawrio", "rawrio.com", "Raw Rio"},
}

// detailWorkers caps the per-page detail pool. The listing serves 24 cards a
// page and the detail page is only needed for the synopsis.
const detailWorkers = 4

var (
	modelURLRe = regexp.MustCompile(`/model/(\d+)`)
	tokenRe    = regexp.MustCompile(`name="_token" value="([^"]+)"`)

	// Cards are sliced between their opening anchors. The href must be
	// relative: the same markup is used for cross-promo cards pointing at
	// flavaflix.com, which are another site's scenes.
	cardSplitRe = regexp.MustCompile(`<a href="/scene/(\d+)" class="card"`)
	cardImgRe   = regexp.MustCompile(`<img class="card-img" src="([^"]+)"`)
	cardDurRe   = regexp.MustCompile(`<span class="card-duration">([^<]+)</span>`)
	cardTitleRe = regexp.MustCompile(`(?s)<div class="card-title">(.*?)</div>`)
	cardModelRe = regexp.MustCompile(`(?s)<div class="card-models">(.*?)</div>`)
	cardDateRe  = regexp.MustCompile(`(?s)<div class="card-meta[^"]*">.*?<span>([^<]+)</span>`)

	detailDescRe = regexp.MustCompile(`(?s)<div class="scene-description">(.*?)</div>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

// cardDateLayouts cover the listing's "Sep 15, 2026", padded or not.
var cardDateLayouts = []string{"Jan 2, 2006", "Jan 02, 2006"}

// Scraper implements scraper.StudioScraper for one FlavaWorks site.
type Scraper struct {
	cfg     SiteConfig
	client  *http.Client
	base    string
	matchRe *regexp.Regexp

	gateMu sync.Mutex
	gateOK bool
}

// New builds a scraper for one network site.
func New(cfg SiteConfig) *Scraper {
	jar, _ := cookiejar.New(nil)
	c := httpx.NewClient(45 * time.Second)
	c.Jar = jar
	return &Scraper{
		cfg:     cfg,
		client:  c,
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`),
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain, s.cfg.Domain + "/model/{id}"}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry. It carries everything but the synopsis.
type card struct {
	id       string
	thumb    string
	title    string
	models   []string
	date     string
	duration string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if err := s.passAgeGate(ctx); err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}

	path := "/scenes"
	if m := modelURLRe.FindStringSubmatch(studioURL); m != nil {
		path = "/model/" + m[1]
		scraper.Debugf(1, "%s: scraping model %s", s.cfg.SiteID, m[1])
	}

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", s.base, path, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			fresh = append(fresh, c)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		enrichTo := len(fresh)
		for i, c := range fresh {
			if opts.KnownIDs[c.id] {
				enrichTo = i
				break
			}
		}
		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// passAgeGate posts the site's age confirmation, without which every page is
// the gate itself. The token is a Laravel CSRF field on the gate form and the
// session it belongs to lives in this scraper's cookie jar.
func (s *Scraper) passAgeGate(ctx context.Context) error {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	if s.gateOK {
		return nil
	}
	body, err := s.fetch(ctx, s.base+"/")
	if err != nil {
		return fmt.Errorf("age gate: %w", err)
	}
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		// Already past it, or the gate is gone: either way nothing to post.
		s.gateOK = true
		return nil
	}
	form := url.Values{"_token": {html.UnescapeString(m[1])}, "intended": {s.base}}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method:  http.MethodPost,
		URL:     s.base + "/age-gate/confirm",
		Body:    []byte(form.Encode()),
		Headers: gateHeaders(s.base),
	})
	if err != nil {
		return fmt.Errorf("age gate: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = httpx.ReadBody(resp.Body)

	scraper.Debugf(1, "%s: passed the age gate", s.cfg.SiteID)
	s.gateOK = true
	return nil
}

func gateHeaders(base string) map[string]string {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Content-Type"] = "application/x-www-form-urlencoded"
	h["Origin"] = base
	h["Referer"] = base + "/"
	return h
}

// parseListing reads the scene cards on a listing page.
func parseListing(body string) []card {
	marks := cardSplitRe.FindAllStringSubmatchIndex(body, -1)
	cards := make([]card, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[1]:end]

		c := card{id: body[m[2]:m[3]]}
		if im := cardImgRe.FindStringSubmatch(block); im != nil {
			c.thumb = html.UnescapeString(im[1])
		}
		if d := cardDurRe.FindStringSubmatch(block); d != nil {
			c.duration = strings.TrimSpace(d[1])
		}
		if t := cardTitleRe.FindStringSubmatch(block); t != nil {
			c.title = cleanText(t[1])
		}
		if md := cardModelRe.FindStringSubmatch(block); md != nil {
			for _, name := range strings.Split(cleanText(md[1]), ",") {
				if name = strings.TrimSpace(name); name != "" {
					c.models = append(c.models, name)
				}
			}
		}
		if d := cardDateRe.FindStringSubmatch(block); d != nil {
			c.date = cleanText(d[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         c.id,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Studio:     s.cfg.StudioName,
		Title:      c.title,
		URL:        s.base + "/scene/" + c.id,
		Thumbnail:  c.thumb,
		Performers: c.models,
		Duration:   parseutil.ParseDurationColon(c.duration),
		ScrapedAt:  now,
	}
	if t, err := parseutil.TryParseDate(c.date, cardDateLayouts...); err == nil {
		sc.Date = t
	}
	return sc
}

// enrich fetches each scene's detail page for the synopsis, which is the one
// field the card does not carry.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(scenes), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range scenes {
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
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				send(ctx, out, scraper.Error(err))
				return
			}
			if d := detailDescRe.FindStringSubmatch(body); d != nil {
				scenes[i].Description = cleanText(d[1])
			}
		}(i)
	}
	wg.Wait()
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

// Package jakecruise scrapes the Jake Cruise network — CocksureMen, Jake Cruise,
// Straight Guys For Gay Eyes and Hot Dads Hot Lads — four Elevated X tours
// sharing one card skin (`sexycock` blocks with a `cockdetails`/`timing`
// footer). The network's fifth brand, The Cock Squad, no longer accepts
// connections.
//
// The listing card is complete: scene id, title, URL, performers, duration,
// date and thumbnail all come from it. Only the description needs the detail
// page, which a worker pool fetches.
//
// Two details the markup forces:
//
//   - The date lives in the same `<p class="timing">` as the runtime, after a
//     `<br/>`. A document-wide date regex instead picks up thumbnail paths like
//     `content//contentthumbs/74/07/7407.jpg`, so both values are read from
//     inside that one element.
//   - Card hrefs and thumbnails are protocol-relative or site-relative, so both
//     are rebuilt against the site's base.
//
// It stays a standalone scraper rather than joining a shared Elevated X util
// for the reason set out in the goldwinpass package doc: every site in that
// family wraps its cards in a different class, so a util would just be several
// regex sets sharing a name. Within this network the skin *is* shared, which is
// why the three sites are table-driven here.
package jakecruise

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
	detailWorkers = 4
	// Card dates are US-format.
	dateLayout = "01/02/2006"
)

type siteConfig struct {
	SiteID     string
	Domain     string
	StudioName string
}

var sites = []siteConfig{
	{"cocksuremen", "cocksuremen.com", "CocksureMen"},
	{"jakecruise", "jakecruise.com", "Jake Cruise"},
	{"straightguysforgayeyes", "straightguysforgayeyes.com", "Straight Guys For Gay Eyes"},
	{"hotdadshotlads", "hotdadshotlads.com", "Hot Dads Hot Lads"},
}

// Scraper implements scraper.StudioScraper for one Jake Cruise network site.
type Scraper struct {
	cfg     siteConfig
	Client  *http.Client
	base    string
	matchRe *regexp.Regexp
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func newScraper(cfg siteConfig) *Scraper {
	escaped := strings.ReplaceAll(cfg.Domain, ".", `\.`)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		base:    "https://www." + cfg.Domain,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?` + escaped + `(?:/|$)`),
	}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/tour/categories/movies/{N}/latest/",
		s.cfg.Domain + "/tour/trailers/{slug}.html",
		s.cfg.Domain + "/tour/models/{slug}.html",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/tour/categories/movies/%d/latest/", s.base, page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := s.parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// ---- listing ----

var (
	cardRe = regexp.MustCompile(`<div class="sexycock\s*"`)
	// The attribute is "set-target-{setid}-{nonce}". Only the set id is stable:
	// the trailing number is regenerated on every request, so keying scenes on
	// the pair would make each incremental run treat the whole catalogue as new.
	idRe = regexp.MustCompile(`id="set-target-(\d+)-\d+"`)
	// Only the trailer filename is captured so the URL is rebuilt against the
	// site base — cards link protocol-relative.
	linkRe   = regexp.MustCompile(`<a href='[^']*/tour/trailers/([^'/]+\.html)' title="([^"]*)"`)
	modelsRe = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	modelRe  = regexp.MustCompile(`/tour/models/[^"]*\.html">([^<]+)</a>`)
	// Runtime and date share one element, the date after a <br/>. Matching them
	// document-wide instead would pick up thumbnail paths such as
	// content//contentthumbs/74/07/7407.jpg.
	timingRe   = regexp.MustCompile(`(?s)<p class="timing">(.*?)</p>`)
	durationRe = regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?)`)
	dateRe     = regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`)
	thumbRe    = regexp.MustCompile(`class="update_thumb thumbs stdimage" src="([^"]+)"`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
	performers            []string
}

func (s *Scraper) parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			url:   s.base + "/tour/trailers/" + m[1],
			title: cleanText(m[2]),
		}
		if idm := idRe.FindStringSubmatch(card); idm != nil {
			it.id = idm[1]
		} else {
			it.id = strings.TrimSuffix(m[1], ".html")
		}

		if mb := modelsRe.FindStringSubmatch(card); mb != nil {
			for _, pm := range modelRe.FindAllStringSubmatch(mb[1], -1) {
				if name := cleanText(pm[1]); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}
		// Some sites in the network render two timing elements per card — one
		// with the runtime alone and one with runtime plus date — so both
		// values are taken from the first block that carries each.
		for _, tm := range timingRe.FindAllStringSubmatch(card, -1) {
			if it.duration == 0 {
				if d := durationRe.FindStringSubmatch(tm[1]); d != nil {
					it.duration = parseutil.ParseDurationColon(d[1])
				}
			}
			if it.date.IsZero() {
				if d := dateRe.FindStringSubmatch(tm[1]); d != nil {
					if ts, err := time.Parse(dateLayout, d[1]); err == nil {
						it.date = ts.UTC()
					}
				}
			}
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = s.normalizeURL(th[1])
		}

		items = append(items, it)
	}
	return items
}

// normalizeURL absolutises the card's protocol-relative and site-relative
// references.
func (s *Scraper) normalizeURL(u string) string {
	switch {
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return s.base + u
	case strings.HasPrefix(u, "http"):
		return u
	default:
		return s.base + "/tour/" + u
	}
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.SiteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, delay) {
				return
			}
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

var (
	descRe     = regexp.MustCompile(`(?s)<div class="aboutvideo">(.*?)</div>`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:         it.id,
		SiteID:     s.cfg.SiteID,
		StudioURL:  studioURL,
		Title:      it.title,
		URL:        it.url,
		Date:       it.date,
		Duration:   it.duration,
		Thumbnail:  it.thumb,
		Performers: it.performers,
		Studio:     s.cfg.StudioName,
		ScrapedAt:  now,
	}

	// Only the description needs the detail page; a failure there still leaves
	// a complete scene.
	if body, err := s.fetchPage(ctx, it.url); err == nil {
		if m := descRe.FindSubmatch(body); m != nil {
			scene.Description = cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
		}
	}
	return scene
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// ---- HTTP ----

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

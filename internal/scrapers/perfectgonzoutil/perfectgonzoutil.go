// Package perfectgonzoutil scrapes the Perfect Gonzo network (All Internal,
// Ass Traffic, Sperm Swap, Prime Cups, Tamed Teens, Cum For Cover, Milf Thing,
// Pure POV). All eight sites run the same custom PHP CMS: a /movies listing of
// 24 cards per page (paginated as /movies/page-{N}/) and per-movie detail pages
// at /movies/{slug}/. The listing card carries the id, slug, title, thumbnail,
// publish date (MM/DD/YYYY) and runtime; the detail page adds the description,
// featured performers and tags.
package perfectgonzoutil

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
	cardsPerPage  = 24
)

type SiteConfig struct {
	ID       string
	SiteBase string // e.g. "http://www.allinternal.com"
	Studio   string
	Patterns []string
	MatchRe  *regexp.Regexp
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string               { return s.cfg.ID }
func (s *Scraper) Patterns() []string       { return s.cfg.Patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.MatchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	itemmRe   = regexp.MustCompile(`<div class="itemm"`)
	idRe      = regexp.MustCompile(`data-id="(\d+)"`)
	linkRe    = regexp.MustCompile(`class="bloc-link[^"]*"\s+href="/movies/([^/"]+)/"\s+title='([^']*)'`)
	coverRe   = regexp.MustCompile(`alt="0%"\s+src="([^"]+)"`)
	dateRe    = regexp.MustCompile(`nm-date">([^<]+)<`)
	nameRe    = regexp.MustCompile(`nm-name[^>]*>([^<]+)<`)
	lengthRe  = regexp.MustCompile(`<p>Length:\s*([0-9:]+)`)
	titleRe   = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	descRe    = regexp.MustCompile(`(?s)<p class="mg-md">(.*?)</p>`)
	modelsRe  = regexp.MustCompile(`(?s)Featured model\(s\):</h4>(.*?)</div>`)
	modelRe   = regexp.MustCompile(`href='/models/[^']+/'>([^<]+)</a>`)
	tagRe     = regexp.MustCompile(`href='/movies\?tag\[\]=[^']*'>([^<]+)</a>`)
	tagStripR = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.ID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		body, err := s.get(ctx, s.listingURL(page))
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		scraper.Debugf(1, "%s: page %d yielded %d cards", s.cfg.ID, page, len(cards))

		// Pages past the end repeat the last page — stop when nothing is new.
		fresh := cards[:0]
		for _, c := range cards {
			if c.id != "" && !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		// Fewer than a full page of cards means this is the last page.
		done := len(cards) < cardsPerPage
		return scraper.PageResult{Scenes: scenes, Done: done}, nil
	})
}

func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		return s.cfg.SiteBase + "/movies"
	}
	return fmt.Sprintf("%s/movies/page-%d/", s.cfg.SiteBase, page)
}

type card struct {
	id, slug, title, thumb, date, length string
}

// parseListing splits the page into itemm cards and extracts each card's
// fields. Cards missing an id or slug are skipped.
func parseListing(body []byte) []card {
	doc := string(body)
	starts := itemmRe.FindAllStringIndex(doc, -1)
	cards := make([]card, 0, len(starts))
	for i, loc := range starts {
		end := len(doc)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := doc[loc[0]:end]
		c := parseCard(block)
		if c.id == "" || c.slug == "" {
			continue
		}
		cards = append(cards, c)
	}
	return cards
}

func parseCard(block string) card {
	var c card
	if m := idRe.FindStringSubmatch(block); m != nil {
		c.id = m[1]
	}
	if m := linkRe.FindStringSubmatch(block); m != nil {
		c.slug = m[1]
		c.title = decode(m[2])
	}
	if m := coverRe.FindStringSubmatch(block); m != nil {
		c.thumb = m[1]
	}
	if m := dateRe.FindStringSubmatch(block); m != nil {
		c.date = strings.TrimSpace(m[1])
	}
	if c.title == "" {
		if m := nameRe.FindStringSubmatch(block); m != nil {
			c.title = decode(m[1])
		}
	}
	if m := lengthRe.FindStringSubmatch(block); m != nil {
		c.length = m[1]
	}
	return c
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, cards []card, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(cards), workers)
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		wg.Add(1)
		go func(i int, c card) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scenes[i] = s.toScene(ctx, studioURL, c, now)
		}(i, c)
	}
	wg.Wait()
	// Drop any scenes left zero-valued by a cancelled context.
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, c card, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        c.slug,
		SiteID:    s.cfg.ID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       fmt.Sprintf("%s/movies/%s/", s.cfg.SiteBase, c.slug),
		Thumbnail: c.thumb,
		Studio:    s.cfg.Studio,
		ScrapedAt: now,
	}
	if d, err := parseutil.TryParseDate(c.date, "01/02/2006"); err == nil {
		scene.Date = d
	}
	if c.length != "" {
		scene.Duration = parseutil.ParseDurationColon(c.length)
	}

	if body, err := s.get(ctx, scene.URL); err == nil {
		s.applyDetail(&scene, string(body))
	}
	return scene
}

// applyDetail enriches scene with the description, performers and tags from the
// movie detail page.
func (s *Scraper) applyDetail(scene *models.Scene, detail string) {
	if scene.Title == "" {
		if m := titleRe.FindStringSubmatch(detail); m != nil {
			title := strings.TrimSpace(m[1])
			if idx := strings.LastIndex(title, " - "); idx > 0 {
				title = title[:idx]
			}
			scene.Title = decode(strings.TrimSpace(title))
		}
	}
	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(m[1])
	}
	if m := modelsRe.FindStringSubmatch(detail); m != nil {
		seen := map[string]bool{}
		for _, pm := range modelRe.FindAllStringSubmatch(m[1], -1) {
			name := decode(strings.TrimSpace(pm[1]))
			if name != "" && !seen[name] {
				seen[name] = true
				scene.Performers = append(scene.Performers, name)
			}
		}
	}
	tseen := map[string]bool{}
	for _, tm := range tagRe.FindAllStringSubmatch(detail, -1) {
		tag := decode(strings.TrimSpace(tm[1]))
		if tag != "" && !tseen[tag] {
			tseen[tag] = true
			scene.Tags = append(scene.Tags, tag)
		}
	}
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func decode(s string) string { return html.UnescapeString(strings.TrimSpace(s)) }

func cleanText(s string) string {
	s = tagStripR.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

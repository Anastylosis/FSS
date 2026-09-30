package karups

import (
	"context"
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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type siteConfig struct {
	id      string
	base    string
	studio  string
	pattern string
	matchRe *regexp.Regexp
}

var sites = []siteConfig{
	{
		id:      "karupsow",
		base:    "https://www.karupsow.com",
		studio:  "Karups Older Women",
		pattern: "karupsow.com",
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?karupsow\.com(?:/|$)`),
	},
	{
		id:      "karupspc",
		base:    "https://www.karupspc.com",
		studio:  "Karups Private Collection",
		pattern: "karupspc.com",
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?karupspc\.com(?:/|$)`),
	},
	{
		id:      "karupsha",
		base:    "https://www.karupsha.com",
		studio:  "Karups Hometown Amateurs",
		pattern: "karupsha.com",
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?karupsha\.com(?:/|$)`),
	},
}

type Scraper struct {
	client *http.Client
	cfg    siteConfig
}

func newScraper(cfg siteConfig) *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), cfg: cfg}
}

func init() {
	for _, cfg := range sites {
		scraper.Register(newScraper(cfg))
	}
}

func (s *Scraper) ID() string { return s.cfg.id }
func (s *Scraper) Patterns() []string {
	return []string{s.cfg.pattern, s.cfg.pattern + "/model/{slug}.html"}
}
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	cardRe    = regexp.MustCompile(`(?s)<div class="item-inside">\s*<a href="([^"]+)">\s*<div class="thumb">\s*<img src="([^"]+)"[^>]*>\s*<div class="meta">\s*<span class="title">([^<]+)</span>\s*<span class="date">([^<]+)</span>`)
	sceneIDRe = regexp.MustCompile(`-(\d+)\.html`)
	maxPageRe = regexp.MustCompile(`href="page(\d+)\.html"`)
	// Individual model pages are canonically /model/{slug}-{id}.html, but the
	// site also serves the plural /models/{slug}-{id}.html and 302s it to the
	// singular form, and links in the wild use both. Requiring the {slug}.html
	// component is what keeps the bare /models/ index from matching: that page
	// lists models rather than videos, so it correctly falls through to the
	// full /videos/ walk instead of being parsed as one model's scenes.
	modelURLRe = regexp.MustCompile(`/models?/[^/]+\.html`)

	videoSectionRe = regexp.MustCompile(`(?s)<div class="listing-videos[^"]*"[^>]*>(.*)</div>`)

	modelsRe    = regexp.MustCompile(`(?s)<span class="models">(.*?)</span>\s*</span>`)
	modelNameRe = regexp.MustCompile(`<a[^>]*>([^<]+)</a>`)

	baseURLRe = regexp.MustCompile(`^(https?://[^/]+)`)
)

type listEntry struct {
	id        string
	url       string
	title     string
	thumbnail string
	date      time.Time
}

func parseListingPage(body []byte) []listEntry {
	matches := cardRe.FindAllSubmatch(body, -1)
	entries := make([]listEntry, 0, len(matches))
	for _, m := range matches {
		url := string(m[1])
		idMatch := sceneIDRe.FindStringSubmatch(url)
		if idMatch == nil {
			continue
		}
		entries = append(entries, listEntry{
			id:        idMatch[1],
			url:       url,
			title:     strings.TrimSpace(html.UnescapeString(string(m[3]))),
			thumbnail: string(m[2]),
			date:      parseDate(string(m[4])),
		})
	}
	return entries
}

func parseDate(s string) time.Time {
	t, _ := parseutil.TryParseDate(parseutil.StripOrdinalSuffix(strings.TrimSpace(s)), "January 2, 2006", "Jan 2, 2006")
	return t
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

func listingURL(base string, page int) string {
	if page <= 1 {
		return base + "/videos/"
	}
	return fmt.Sprintf("%s/videos/page%d.html", base, page)
}

func resolveBase(studioURL string, cfg siteConfig) string {
	if m := baseURLRe.FindString(studioURL); m != "" {
		if !strings.Contains(m, "karups") {
			return m
		}
	}
	return cfg.base
}

func isModelURL(u string) bool { return modelURLRe.MatchString(u) }

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	if isModelURL(studioURL) {
		scraper.Debugf(1, "%s: scraping model page", s.cfg.id)
		s.runModel(ctx, studioURL, opts, out)
	} else {
		s.runPaginated(ctx, studioURL, opts, out)
	}
}

func (s *Scraper) runModel(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	now := time.Now().UTC()

	body, err := s.fetchPage(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	section := body
	if m := videoSectionRe.Find(body); m != nil {
		section = m
	}

	entries := parseListingPage(section)
	scraper.Debugf(1, "%s: %d total scenes", s.cfg.id, len(entries))
	select {
	case out <- scraper.Progress(len(entries)):
	case <-ctx.Done():
		return
	}

	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if opts.KnownIDs[e.id] {
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scene, err := s.processEntry(ctx, studioURL, now, e)
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
				return
			}
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) runPaginated(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	base := resolveBase(studioURL, s.cfg)
	now := time.Now().UTC()

	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.processEntry(ctx, studioURL, now, entry)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	seen := make(map[string]bool)
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			break
		}

		scraper.Debugf(1, "%s: fetching page %d", s.cfg.id, page)
		body, err := s.fetchPage(ctx, listingURL(base, page))
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		entries := parseListingPage(body)
		if len(entries) == 0 {
			break
		}

		if page == 1 {
			total := estimateTotal(body, len(entries))
			if total > 0 {
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
				}
			}
		}

		cancelled := false
		hitKnown := false
		for _, e := range entries {
			if seen[e.id] {
				continue
			}
			seen[e.id] = true
			if opts.KnownIDs[e.id] {
				hitKnown = true
				break
			}
			select {
			case work <- e:
			case <-ctx.Done():
				cancelled = true
			}
			if cancelled {
				break
			}
		}
		if cancelled || hitKnown {
			if hitKnown {
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
			}
			break
		}
	}

	close(work)
	wg.Wait()
}

// processEntry builds a scene from its listing card and enriches it from the
// detail page. A dead detail page costs the cast, not the scene, so the scene
// is returned either way — with the error, which the caller reports so an
// authoritative --full Save cannot treat a half-read catalogue as complete.
func (s *Scraper) processEntry(ctx context.Context, studioURL string, now time.Time, entry listEntry) (models.Scene, error) {
	scene := models.Scene{
		ID:        entry.id,
		SiteID:    s.cfg.id,
		StudioURL: studioURL,
		Title:     entry.title,
		Thumbnail: entry.thumbnail,
		URL:       entry.url,
		Date:      entry.date,
		Studio:    s.cfg.studio,
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, entry.url)
	if err != nil {
		return scene, fmt.Errorf("detail %s: %w", entry.url, err)
	}

	if m := modelsRe.FindSubmatch(body); m != nil {
		for _, name := range modelNameRe.FindAllSubmatch(m[1], -1) {
			performer := strings.TrimSpace(html.UnescapeString(string(name[1])))
			if performer != "" {
				scene.Performers = append(scene.Performers, performer)
			}
		}
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: url,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentChrome)
			h["Cookie"] = "warningHidden=hide"
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

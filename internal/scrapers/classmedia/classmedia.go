// Package classmedia scrapes the Class Media network sites that expose a
// public listing: Oldje (oldje.com), Oldje-3some (oldje-3some.com) and
// Subspaceland (subspaceland.com). The three sites run three different HTML
// templates, so each has its own parser, but they share one package and one
// Scraper type selected by a per-site template enum.
//
//   - Subspaceland — richest metadata. Scenes are enumerated from sitemap.xml
//     (~207 /video/{model}/{slug} URLs) and each detail page yields a real
//     title, release date, model and tag list. Uses a worker pool.
//   - Oldje — listing-only. /gallery/{n} pages carry set thumbnails of the form
//     /sets/{id}/{slug}.webp; the set id and de-slugged title are all that is
//     publicly available (the real scene detail lives behind /join). The newest
//     handful of sets ship obfuscated slugs, so their titles are gibberish.
//   - Oldje-3some — listing-only. /gallery/{n} embeds the whole catalogue as a
//     window.sslSearchItems JSON array (title, actors, duration, cover, URL),
//     so one fetch yields every scene.
package classmedia

import (
	"context"
	"encoding/json"
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

type template int

const (
	tplSubspaceland template = iota
	tplOldje
	tplOldje3some
)

const subspacelandWorkers = 4

type siteConfig struct {
	id       string
	studio   string
	base     string // e.g. "https://www.oldje.com"
	tpl      template
	patterns []string
	matchRe  *regexp.Regexp
}

// Scraper implements scraper.StudioScraper for one Class Media site.
type Scraper struct {
	cfg    siteConfig
	Client *http.Client
}

func newScraper(cfg siteConfig) *Scraper {
	return &Scraper{cfg: cfg, Client: httpx.NewClient(30 * time.Second)}
}

// NewSubspaceland returns the Subspaceland scraper.
func NewSubspaceland() *Scraper {
	return newScraper(siteConfig{
		id:      "subspaceland",
		studio:  "Subspaceland",
		base:    "https://www.subspaceland.com",
		tpl:     tplSubspaceland,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?subspaceland\.com(?:/|$)`),
		patterns: []string{
			"subspaceland.com",
			"subspaceland.com/video/{model}/{slug}",
		},
	})
}

// NewOldje returns the Oldje scraper.
func NewOldje() *Scraper {
	return newScraper(siteConfig{
		id:      "oldje",
		studio:  "Oldje",
		base:    "https://www.oldje.com",
		tpl:     tplOldje,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?oldje\.com(?:/|$)`),
		patterns: []string{
			"oldje.com",
			"oldje.com/gallery/{n}",
		},
	})
}

// NewOldje3some returns the Oldje-3some scraper.
func NewOldje3some() *Scraper {
	return newScraper(siteConfig{
		id:      "oldje3some",
		studio:  "Oldje-3some",
		base:    "https://www.oldje-3some.com",
		tpl:     tplOldje3some,
		matchRe: regexp.MustCompile(`^https?://(?:www\.)?oldje-3some\.com(?:/|$)`),
		patterns: []string{
			"oldje-3some.com",
			"oldje-3some.com/gallery/{n}",
		},
	})
}

var (
	_ scraper.StudioScraper = (*Scraper)(nil)
)

func init() {
	scraper.Register(NewSubspaceland())
	scraper.Register(NewOldje())
	scraper.Register(NewOldje3some())
}

func (s *Scraper) ID() string               { return s.cfg.id }
func (s *Scraper) Patterns() []string       { return s.cfg.patterns }
func (s *Scraper) MatchesURL(u string) bool { return s.cfg.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	switch s.cfg.tpl {
	case tplSubspaceland:
		s.runSubspaceland(ctx, studioURL, opts, out)
	case tplOldje:
		s.runOldje(ctx, studioURL, opts, out)
	case tplOldje3some:
		s.runOldje3some(ctx, studioURL, opts, out)
	default:
		close(out)
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

// ---- Subspaceland (sitemap + worker pool) ----

var (
	sslSitemapRe = regexp.MustCompile(`<loc>(https?://(?:www\.)?subspaceland\.com/video/[a-z0-9-]+/[a-z0-9-]+)</loc>`)
	sslTitleRe   = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	sslDateRe    = regexp.MustCompile(`Released on\s+(\d{1,2}\s+[A-Za-z]+\s+\d{4})`)
	sslModelRe   = regexp.MustCompile(`href="https?://(?:www\.)?subspaceland\.com/model/[a-z0-9-]+"[^>]*>([^<]+)</a>`)
	sslTagRe     = regexp.MustCompile(`href="https?://(?:www\.)?subspaceland\.com/tag/[^"]+"[^>]*>([^<]+)</a>`)
	sslSetRe     = regexp.MustCompile(`/sets/(\d+)/`)
	sslDescRe    = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
)

func (s *Scraper) runSubspaceland(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	urls, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("sitemap: %w", err)):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "subspaceland: %d video URLs in sitemap", len(urls))

	select {
	case out <- scraper.Progress(len(urls)):
	case <-ctx.Done():
		return
	}

	// Skip URLs whose ID is already stored (incremental runs).
	jobs := make([]string, 0, len(urls))
	for _, u := range urls {
		if !opts.KnownIDs[sslID(u)] {
			jobs = append(jobs, u)
		}
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = subspacelandWorkers
	}
	scraper.Debugf(1, "subspaceland: fetching %d details with %d workers", len(jobs), workers)

	jobCh := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobCh {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				sc, ok := s.scrapeSubspacelandDetail(ctx, studioURL, u, now)
				if !ok {
					continue
				}
				select {
				case out <- scraper.Scene(sc):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, u := range jobs {
		select {
		case jobCh <- u:
		case <-ctx.Done():
			close(jobCh)
			wg.Wait()
			return
		}
	}
	close(jobCh)
	wg.Wait()
}

func (s *Scraper) fetchSitemap(ctx context.Context) ([]string, error) {
	body, err := s.get(ctx, s.cfg.base+"/sitemap.xml")
	if err != nil {
		return nil, err
	}
	matches := sslSitemapRe.FindAllStringSubmatch(string(body), -1)
	seen := make(map[string]bool, len(matches))
	urls := make([]string, 0, len(matches))
	for _, sm := range matches {
		m := strings.Replace(sm[1], "http://", "https://", 1)
		if !seen[m] {
			seen[m] = true
			urls = append(urls, m)
		}
	}
	return urls, nil
}

// sslID derives a stable scene id from a /video/{model}/{slug} URL.
func sslID(u string) string {
	u = strings.TrimRight(u, "/")
	parts := strings.Split(u, "/video/")
	if len(parts) == 2 {
		return parts[1]
	}
	return u
}

func (s *Scraper) scrapeSubspacelandDetail(ctx context.Context, studioURL, u string, now time.Time) (models.Scene, bool) {
	body, err := s.get(ctx, u)
	if err != nil {
		return models.Scene{}, false
	}
	page := string(body)

	scene := models.Scene{
		ID:        sslID(u),
		SiteID:    s.cfg.id,
		StudioURL: studioURL,
		URL:       u,
		Studio:    s.cfg.studio,
		ScrapedAt: now,
	}

	if m := sslTitleRe.FindStringSubmatch(page); m != nil {
		scene.Title = cleanText(m[1])
	}
	if scene.Title == "" {
		return models.Scene{}, false
	}

	if m := sslDateRe.FindStringSubmatch(page); m != nil {
		if d, err := parseutil.TryParseDate(strings.TrimSpace(m[1]), "02 Jan 2006", "2 Jan 2006"); err == nil {
			scene.Date = d
		}
	}
	if m := sslModelRe.FindStringSubmatch(page); m != nil {
		if name := cleanText(m[1]); name != "" {
			scene.Performers = []string{name}
		}
	}
	if m := sslSetRe.FindStringSubmatch(page); m != nil {
		scene.Thumbnail = s.cfg.base + "/sets/" + m[1] + "/mov_img/movie_preview.jpg"
	}
	if m := sslDescRe.FindStringSubmatch(page); m != nil {
		scene.Description = cleanText(m[1])
	}

	tagSeen := make(map[string]bool)
	for _, tm := range sslTagRe.FindAllStringSubmatch(page, -1) {
		t := cleanText(tm[1])
		if t != "" && !tagSeen[t] {
			tagSeen[t] = true
			scene.Tags = append(scene.Tags, t)
		}
	}

	return scene, true
}

// ---- Oldje (/gallery/{n} listing, listing-only) ----

var oldjeSetRe = regexp.MustCompile(`/sets/(\d+)/([a-z0-9-]+)\.webp`)

func (s *Scraper) runOldje(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.id, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/gallery/%d", s.cfg.base, page)
		body, err := s.get(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		matches := oldjeSetRe.FindAllStringSubmatch(string(body), -1)
		scenes := make([]models.Scene, 0, len(matches))
		for _, m := range matches {
			id, slug := m[1], m[2]
			if seen[id] {
				continue
			}
			seen[id] = true
			scenes = append(scenes, models.Scene{
				ID:        id,
				SiteID:    s.cfg.id,
				StudioURL: studioURL,
				Title:     deslug(slug),
				URL:       pageURL,
				Thumbnail: s.cfg.base + m[0],
				Studio:    s.cfg.studio,
				ScrapedAt: now,
			})
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// ---- Oldje-3some (/gallery/{n} listing, listing-only) ----

var sslSearchItemsRe = regexp.MustCompile(`(?s)window\.sslSearchItems\s*=\s*(\[.*?\]);`)

var oldje3someSetIDRe = regexp.MustCompile(`/sets/(\d+)/`)

type sslSearchItem struct {
	Title    string `json:"title"`
	Actors   string `json:"actors"`
	Duration string `json:"duration"`
	Thumb    string `json:"thumb"`
	URL      string `json:"url"`
}

func (s *Scraper) runOldje3some(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	body, err := s.get(ctx, s.cfg.base+"/gallery/1")
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	m := sslSearchItemsRe.FindSubmatch(body)
	if m == nil {
		select {
		case out <- scraper.Error(scraper.ParseError(s.cfg.base+"/gallery/1", fmt.Errorf("sslSearchItems block not found"))):
		case <-ctx.Done():
		}
		return
	}
	var items []sslSearchItem
	if err := json.Unmarshal(m[1], &items); err != nil {
		select {
		case out <- scraper.Error(scraper.ParseError(s.cfg.base+"/gallery/1", err)):
		case <-ctx.Done():
		}
		return
	}

	now := time.Now().UTC()
	scenes := make([]models.Scene, 0, len(items))
	seen := make(map[string]bool)
	for _, it := range items {
		idm := oldje3someSetIDRe.FindStringSubmatch(it.Thumb)
		if idm == nil || seen[idm[1]] {
			continue
		}
		seen[idm[1]] = true
		scenes = append(scenes, models.Scene{
			ID:         idm[1],
			SiteID:     s.cfg.id,
			StudioURL:  studioURL,
			Title:      strings.TrimSpace(html.UnescapeString(it.Title)),
			URL:        s.cfg.base + it.URL,
			Thumbnail:  s.cfg.base + it.Thumb,
			Studio:     s.cfg.studio,
			Performers: oldje3somePerformers(it.Actors, s.cfg.studio),
			Duration:   parseutil.ParseDurationColon(it.Duration),
			ScrapedAt:  now,
		})
	}

	scraper.Paginate(ctx, opts, s.cfg.id, out, func(_ context.Context, page int) (scraper.PageResult, error) {
		if page > 1 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: scenes, Total: len(scenes), Done: true}, nil
	})
}

// oldje3somePerformers splits the comma-separated actors field, dropping the
// studio name the site uses when it lists no real cast.
func oldje3somePerformers(actors, studio string) []string {
	var out []string
	for _, a := range strings.Split(actors, ",") {
		name := strings.TrimSpace(html.UnescapeString(a))
		if name == "" || strings.EqualFold(name, studio) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// ---- helpers ----

var wsRe = regexp.MustCompile(`<[^>]+>`)

func cleanText(s string) string {
	s = wsRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// deslug turns a URL slug ("cosplay-love") into a display title
// ("Cosplay Love"). Obfuscated slugs produce non-meaningful but non-empty
// titles, which is the best the site exposes.
func deslug(slug string) string {
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.TrimSpace(strings.Join(words, " "))
}

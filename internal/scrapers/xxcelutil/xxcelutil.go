// Package xxcelutil is the shared scraper for the XX-Cel network — a Euro
// big-bust glamour CMS shared by xx-cel.com and heavyonhotties.com. Both sites
// serve a `/movies/page-{N}/` listing of scene cards (each carrying the scene
// URL and cover thumbnail) and per-scene `/movies/{slug}` detail pages that
// expose the release date, runtime and performer(s). The only structural
// difference is that xx-cel.com prefixes its scene slugs with "video-"; that is
// captured in SiteConfig.DetailPrefix.
package xxcelutil

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

type SiteConfig struct {
	SiteID     string
	Domain     string // bare domain, e.g. "xx-cel.com"
	Host       string // full base, e.g. "https://xx-cel.com" or "https://www.heavyonhotties.com"
	StudioName string
}

type Scraper struct {
	Client *http.Client
	cfg    SiteConfig
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second), cfg: cfg}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.Domain,
		s.cfg.Domain + "/movies/page-{N}/",
		s.cfg.Domain + "/movies/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool {
	d := regexp.QuoteMeta(s.cfg.Domain)
	return regexp.MustCompile(`^https?://(?:www\.)?` + d + `(?:/|$)`).MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listingScene struct {
	slug  string // path segment under /movies/, e.g. "video-megara-steele-video-8"
	url   string
	thumb string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	work := make(chan listingScene)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching detail pages with %d workers", s.cfg.SiteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, ls, studioURL, opts.Delay)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		s.enqueueListing(ctx, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueueListing(ctx context.Context, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, page)
		pageURL := fmt.Sprintf("%s/movies/page-%d/?sort=recent", s.cfg.Host, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}
		scenes := s.parseListing(body)
		if len(scenes) == 0 {
			return
		}
		if page == 1 {
			if total := estimateTotal(body, len(scenes)); total > 0 {
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}
		}
		for _, ls := range scenes {
			if opts.KnownIDs[ls.slug] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", s.cfg.SiteID, ls.slug)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- ls:
			case <-ctx.Done():
				return
			}
		}
	}
}

var (
	cardLinkRe = regexp.MustCompile(`href="/movies/([a-z0-9][a-z0-9-]*)"`)
	posterRe   = regexp.MustCompile(`poster="(//media\.[^"]+\.(?:jpg|jpeg|png|webp))"`)
	maxPageRe  = regexp.MustCompile(`/movies/page-(\d+)/`)

	releasedRe = regexp.MustCompile(`released on:\s*<strong>\s*([A-Za-z]+ \d{1,2}, \d{4})\s*</strong>`)
	durationRe = regexp.MustCompile(`duration:\s*<strong>\s*(\d{1,2}:\d{2}(?::\d{2})?)\s*</strong>`)
	// Performers live in a "starring:" run (xx-cel) or a "feature title" span
	// (heavyonhotties). Scoping to these avoids the page's filter-menu /models
	// links (e.g. "All Girls", "Pregnant").
	starringRe = regexp.MustCompile(`(?s)(?:starring:|class="feature title">)(.*?)</span>`)
	modelRe    = regexp.MustCompile(`href=['"]/models/[^'"]+['"][^>]*>\s*([^<]+?)\s*</a>`)
)

// parseListing extracts each scene card's slug, URL and cover thumbnail.
func (s *Scraper) parseListing(body []byte) []listingScene {
	page := string(body)
	seen := map[string]bool{}
	var scenes []listingScene

	// Each card is a `href="/movies/{slug}"` followed shortly by its poster.
	locs := cardLinkRe.FindAllStringSubmatchIndex(page, -1)
	for i, loc := range locs {
		slug := page[loc[2]:loc[3]]
		if slug == "" || strings.HasPrefix(slug, "page-") || seen[slug] {
			continue
		}
		seen[slug] = true

		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := page[loc[0]:end]

		ls := listingScene{slug: slug, url: fmt.Sprintf("%s/movies/%s", s.cfg.Host, slug)}
		if m := posterRe.FindStringSubmatch(block); m != nil {
			ls.thumb = "https:" + m[1]
		}
		scenes = append(scenes, ls)
	}
	return scenes
}

func estimateTotal(body []byte, perPage int) int {
	maxPage := 1
	for _, m := range maxPageRe.FindAllSubmatch(body, -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > maxPage {
			maxPage = n
		}
	}
	return maxPage * perPage
}

type detailData struct {
	date       time.Time
	duration   int
	performers []string
}

func parseDetail(body []byte) detailData {
	var d detailData
	page := string(body)

	if m := releasedRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("Jan 2, 2006", strings.TrimSpace(m[1])); err == nil {
			d.date = t.UTC()
		}
	}
	if m := durationRe.FindStringSubmatch(page); m != nil {
		d.duration = parseutil.ParseDurationColon(m[1])
	}
	seen := map[string]bool{}
	for _, block := range starringRe.FindAllStringSubmatch(page, -1) {
		for _, m := range modelRe.FindAllStringSubmatch(block[1], -1) {
			name := strings.TrimSpace(html.UnescapeString(m[1]))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			d.performers = append(d.performers, name)
		}
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	scene := models.Scene{
		ID:        ls.slug,
		SiteID:    s.cfg.SiteID,
		StudioURL: studioURL,
		URL:       ls.url,
		Title:     slugToTitle(ls.slug),
		Thumbnail: ls.thumb,
		Studio:    s.cfg.StudioName,
		ScrapedAt: time.Now().UTC(),
	}

	body, err := s.fetchPage(ctx, ls.url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", ls.slug, err)
	}
	d := parseDetail(body)
	scene.Date = d.date
	scene.Duration = d.duration
	scene.Performers = d.performers
	return scene, nil
}

// slugToTitle turns a scene slug into a display title, dropping the leading
// "video-" prefix that xx-cel.com uses and title-casing the words.
func slugToTitle(slug string) string {
	slug = strings.TrimPrefix(slug, "video-")
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

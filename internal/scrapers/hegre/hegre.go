// Package hegre scrapes Hegre (hegre.com), the art-erotica studio of
// photographer Petter Hegre. The site is server-rendered but the bare
// /movies feed only exposes the latest ~60 films and ignores ?page, so the
// full catalogue is enumerated through the models index instead:
//
//	/models                 -> ~384 /models/{slug} links
//	/models/{slug}          -> that model's /films/{slug} links
//	/films/{slug}           -> per-film detail (title, description, cast, ...)
//
// A film can be featured by several models, so the same film slug turns up on
// multiple model pages; slugs are de-duplicated into one set before the detail
// fetch. The performers come from the film's own "record-models" cast block,
// which lists exactly the models in that film.
//
// The two stages run as a pipeline of bounded worker pools: model pages are
// fetched concurrently and stream newly-seen film slugs into a channel that a
// second pool of detail fetchers drains. Streaming (rather than enumerating
// every model page up front) means scenes start flowing after the first model
// page lands, so an early ctx cancellation — e.g. the integration test's
// limit — stops all workers promptly instead of waiting on ~384 fetches.
//
// IMPORTANT: Hegre geolocates by IP and defaults Polish visitors to Polish
// copy. A "country=US" cookie (alongside "locale=en") forces the English
// metadata on every request.
package hegre

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

// siteBase is a var (not const) so tests can point it at an httptest server.
var siteBase = "https://www.hegre.com"

const defaultWorker = 6

// Scraper implements scraper.StudioScraper for hegre.com.
type Scraper struct {
	client *http.Client
}

// New constructs a Hegre scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "hegre" }

func (s *Scraper) Patterns() []string {
	return []string{
		"hegre.com",
		"hegre.com/models/{slug}",
		"hegre.com/films/{slug}",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?hegre\.com(?:/|$)`)
	filmRe  = regexp.MustCompile(`/films/([a-zA-Z0-9_-]+)`)
	modelRe = regexp.MustCompile(`/models/([a-zA-Z0-9_-]+)`)

	filmLinkRe  = regexp.MustCompile(`href="/films/([a-zA-Z0-9_-]+)"`)
	modelLinkRe = regexp.MustCompile(`href="/models/([a-zA-Z0-9_-]+)"`)
	castRe      = regexp.MustCompile(`<a href="/models/[a-zA-Z0-9_-]+" class="record-model" title="([^"]+)"`)
	runtimeRe   = regexp.MustCompile(`Runtime:</span>\s*<strong>\s*([0-9:]+)`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	// Single-film URL: scrape just that film.
	if m := filmRe.FindStringSubmatch(studioURL); m != nil {
		if scene, ok := s.fetchFilm(ctx, studioURL, m[1], now); ok {
			s.send(ctx, out, scraper.Scene(scene))
		}
		return
	}

	// Single-model URL: scrape only that model's films.
	if m := modelRe.FindStringSubmatch(studioURL); m != nil {
		slug := m[1]
		films := s.fetchModelFilms(ctx, slug)
		scraper.Debugf(1, "hegre: model %s has %d films", slug, len(films))
		slugCh := make(chan string, len(films)+1)
		go func() {
			defer close(slugCh)
			for _, f := range films {
				select {
				case slugCh <- f:
				case <-ctx.Done():
					return
				}
			}
		}()
		s.emitFromChan(ctx, studioURL, slugCh, now, opts, out)
		return
	}

	// Base/site URL: enumerate the full catalogue via the models index,
	// streaming discovered film slugs straight into the detail pool.
	modelSlugs := s.fetchModelSlugs(ctx)
	scraper.Debugf(1, "hegre: %d models fetched", len(modelSlugs))
	if len(modelSlugs) == 0 {
		return
	}
	slugCh := make(chan string, 128)
	go s.produceFilmSlugs(ctx, modelSlugs, slugCh, opts.Delay)
	s.emitFromChan(ctx, studioURL, slugCh, now, opts, out)
}

// fetchModelSlugs reads the /models index and returns every unique model slug.
func (s *Scraper) fetchModelSlugs(ctx context.Context) []string {
	body, err := s.get(ctx, siteBase+"/models")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var slugs []string
	for _, m := range modelLinkRe.FindAllSubmatch(body, -1) {
		slug := string(m[1])
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	return slugs
}

// produceFilmSlugs fetches every model page concurrently and streams the film
// slugs it finds into slugCh, closing the channel when enumeration completes.
// De-duplication is handled downstream in emitFromChan.
func (s *Scraper) produceFilmSlugs(ctx context.Context, modelSlugs []string, slugCh chan<- string, delay time.Duration) {
	defer close(slugCh)
	scraper.Debugf(1, "hegre: enumerating films from %d models with %d workers", len(modelSlugs), defaultWorker)

	var wg sync.WaitGroup
	sem := make(chan struct{}, defaultWorker)
	for _, ms := range modelSlugs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(ms string) {
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
			for _, f := range s.fetchModelFilms(ctx, ms) {
				select {
				case slugCh <- f:
				case <-ctx.Done():
					return
				}
			}
		}(ms)
	}
	wg.Wait()
}

// fetchModelFilms returns the unique film slugs linked on a model page.
func (s *Scraper) fetchModelFilms(ctx context.Context, slug string) []string {
	body, err := s.get(ctx, siteBase+"/models/"+slug)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var films []string
	for _, m := range filmLinkRe.FindAllSubmatch(body, -1) {
		f := string(m[1])
		if !seen[f] {
			seen[f] = true
			films = append(films, f)
		}
	}
	return films
}

// emitFromChan drains film slugs with a worker pool, de-duplicating across the
// stream, fetching each film's detail page and sending the resulting scene.
func (s *Scraper) emitFromChan(ctx context.Context, studioURL string, slugCh <-chan string, now time.Time, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorker
	}
	scraper.Debugf(1, "hegre: fetching film details with %d workers", workers)

	var seenMu sync.Mutex
	seen := map[string]bool{}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				var slug string
				var ok bool
				select {
				case slug, ok = <-slugCh:
					if !ok {
						return
					}
				case <-ctx.Done():
					return
				}
				seenMu.Lock()
				dup := seen[slug]
				seen[slug] = true
				seenMu.Unlock()
				if dup {
					continue
				}
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ok := s.fetchFilm(ctx, studioURL, slug, now)
				if !ok {
					continue
				}
				if !s.send(ctx, out, scraper.Scene(scene)) {
					return
				}
			}
		}()
	}
	wg.Wait()
}

// fetchFilm fetches and parses one film detail page.
func (s *Scraper) fetchFilm(ctx context.Context, studioURL, slug string, now time.Time) (models.Scene, bool) {
	body, err := s.get(ctx, siteBase+"/films/"+slug)
	if err != nil {
		return models.Scene{}, false
	}
	og := parseutil.OpenGraph(body)

	var performers []string
	pseen := map[string]bool{}
	for _, m := range castRe.FindAllSubmatch(body, -1) {
		name := html.UnescapeString(strings.TrimSpace(string(m[1])))
		if name != "" && !pseen[name] {
			pseen[name] = true
			performers = append(performers, name)
		}
	}

	scene := models.Scene{
		ID:          slug,
		SiteID:      "hegre",
		StudioURL:   studioURL,
		URL:         siteBase + "/films/" + slug,
		Title:       html.UnescapeString(strings.TrimSpace(og["og:title"])),
		Description: html.UnescapeString(strings.TrimSpace(og["og:description"])),
		Thumbnail:   strings.TrimSpace(og["og:image"]),
		Performers:  performers,
		Studio:      "Hegre",
		ScrapedAt:   now,
	}
	if m := runtimeRe.FindSubmatch(body); m != nil {
		scene.Duration = parseutil.ParseDurationColon(string(m[1]))
	}
	return scene, true
}

// send delivers a result, respecting ctx cancellation. Returns false if the
// context was cancelled before the send completed.
func (s *Scraper) send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	headers := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	headers["Cookie"] = "locale=en; country=US"
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: headers})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

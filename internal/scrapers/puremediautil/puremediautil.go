// Package puremediautil scrapes the Pure Media Enterprises trans/BBW network
// (PureTS, Pure BBW, TSPOV, PureXXX, Becoming Femme, Sissy POV). All six run the
// same static "tour" CMS with no sitemap and no numbered listing — enumeration
// happens through model pages:
//
//	/tour/models/models.html      -> 25 model links
//	/tour/models/{Model}.html     -> that model's scenes (trailer links + dates)
//	/tour/trailers/{slug}.html    -> scene detail (title, description, thumb id)
//
// Metadata is fetched anonymously; no duration is available without a login.
package puremediautil

import (
	"context"
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

const detailWorkers = 4

type SiteConfig struct {
	ID       string
	Studio   string
	SiteBase string // e.g. "https://pure-ts.com" (no trailing slash, no /tour)
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
	modelLinkRe = regexp.MustCompile(`href="([^"]*/tour/models/([^"/]+)\.html)"`)
	trailerRe   = regexp.MustCompile(`href="([^"]*/tour/trailers/([^"/]+)\.html)"`)
	updatedRe   = regexp.MustCompile(`(?is)class="updatedScenes"[^>]*>\s*([A-Za-z]{3,}\.?\s+\d{1,2},\s*\d{4})`)
	vpTitleRe   = regexp.MustCompile(`(?is)class="vpTitle"[^>]*>\s*<h1>(.*?)</h1>`)
	descRe      = regexp.MustCompile(`(?is)class="descriptionR".*?<p>(.*?)</p>`)
	thumbIDRe   = regexp.MustCompile(`src0_1x="([^"]*?/(\d+)-1x\.jpg)"`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
	looseDateRe = regexp.MustCompile(`[A-Za-z]{3,}\.?\s+\d{1,2},\s*\d{4}`)
)

var dateLayouts = []string{"Jan 2, 2006", "Jan. 2, 2006", "January 2, 2006"}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()

	modelURLs, err := s.fetchModelList(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: found %d model pages", s.cfg.ID, len(modelURLs))

	workers := scraper.WorkerCount(opts, detailWorkers)
	stubs := s.collectScenes(ctx, workers, modelURLs, opts.Delay)
	if ctx.Err() != nil {
		return
	}
	scraper.Debugf(1, "%s: collected %d unique scenes", s.cfg.ID, len(stubs))

	select {
	case out <- scraper.Progress(len(stubs)):
	case <-ctx.Done():
		return
	}

	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.ID, len(stubs), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, st := range stubs {
		wg.Add(1)
		go func(st *sceneStub) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			scene := s.toScene(ctx, studioURL, st, now)
			if scene.ID == "" {
				return
			}
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
			}
		}(st)
	}
	wg.Wait()
}

type sceneStub struct {
	url        string
	slug       string
	date       time.Time
	performers []string
}

// fetchModelList returns the deduped list of model-page URLs.
func (s *Scraper) fetchModelList(ctx context.Context) ([]string, error) {
	body, err := s.get(ctx, s.cfg.SiteBase+"/tour/models/models.html")
	if err != nil {
		return nil, err
	}
	var urls []string
	seen := map[string]bool{}
	for _, m := range modelLinkRe.FindAllSubmatch(body, -1) {
		u := string(m[1])
		name := string(m[2])
		if name == "models" || seen[u] {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	return urls, nil
}

// collectScenes walks every model page (worker pool) and aggregates scene stubs,
// deduping by trailer URL and unioning the performers that listed each scene.
func (s *Scraper) collectScenes(ctx context.Context, workers int, modelURLs []string, delay time.Duration) []*sceneStub {
	var mu sync.Mutex
	byURL := map[string]*sceneStub{}
	var order []*sceneStub

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, mu0 := range modelURLs {
		wg.Add(1)
		go func(modelURL string) {
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
			body, err := s.get(ctx, modelURL)
			if err != nil {
				return
			}
			performer := humanizeModel(modelURL)
			trailers := trailerRe.FindAllSubmatch(body, -1)
			dates := updatedRe.FindAllSubmatch(body, -1)
			mu.Lock()
			defer mu.Unlock()
			for i, t := range trailers {
				u := string(t[1])
				slug := string(t[2])
				st, ok := byURL[u]
				if !ok {
					st = &sceneStub{url: u, slug: slug}
					byURL[u] = st
					order = append(order, st)
				}
				if i < len(dates) && st.date.IsZero() {
					if d, err := parseutil.TryParseDate(strings.TrimSpace(string(dates[i][1])), dateLayouts...); err == nil {
						st.date = d
					}
				}
				if performer != "" && !contains(st.performers, performer) {
					st.performers = append(st.performers, performer)
				}
			}
		}(mu0)
	}
	wg.Wait()
	return order
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, st *sceneStub, now time.Time) models.Scene {
	body, err := s.get(ctx, st.url)
	if err != nil {
		return models.Scene{}
	}
	detail := string(body)
	og := parseutil.OpenGraph(body)

	id := ""
	thumb := ""
	if m := thumbIDRe.FindStringSubmatch(detail); m != nil {
		id = m[2]
		thumb = m[1]
		if !strings.HasPrefix(thumb, "http") {
			thumb = s.cfg.SiteBase + "/" + strings.TrimPrefix(thumb, "/")
		}
	}
	if id == "" {
		// Without a content id we cannot key the scene reliably.
		return models.Scene{}
	}

	title := ""
	if m := vpTitleRe.FindStringSubmatch(detail); m != nil {
		title = cleanText(m[1])
	}
	if title == "" {
		title = strings.TrimSpace(html.UnescapeString(og["og:title"]))
	}

	description := ""
	if m := descRe.FindStringSubmatch(detail); m != nil {
		description = cleanText(m[1])
	}
	if description == "" {
		description = strings.TrimSpace(html.UnescapeString(og["og:description"]))
	}

	scene := models.Scene{
		ID:          id,
		SiteID:      s.cfg.ID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         st.url,
		Description: description,
		Performers:  st.performers,
		Thumbnail:   thumb,
		Studio:      s.cfg.Studio,
		ScrapedAt:   now,
	}
	scene.Date = st.date
	if scene.Date.IsZero() {
		if d := findDetailDate(detail); !d.IsZero() {
			scene.Date = d
		}
	}
	return scene
}

func findDetailDate(detail string) time.Time {
	if m := looseDateRe.FindString(detail); m != "" {
		if d, err := parseutil.TryParseDate(strings.TrimSpace(m), dateLayouts...); err == nil {
			return d
		}
	}
	return time.Time{}
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// humanizeModel turns a model-page URL into a display name:
// ".../tour/models/Jane-Doe.html" -> "Jane Doe".
func humanizeModel(modelURL string) string {
	name := modelURL
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, ".html")
	name = strings.NewReplacer("-", " ", "_", " ", "%20", " ").Replace(name)
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

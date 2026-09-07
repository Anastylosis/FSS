// Package brasileirinhas scrapes brasileirinhas.com.
package brasileirinhas

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
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

const (
	siteID     = "brasileirinhas"
	studioName = "Brasileirinhas"
	defaultURL = "https://www.brasileirinhas.com"
	// Scene stills are served off the CDN under the scene's own numeric id;
	// the page's og:image is the site logo on every scene.
	thumbBase      = "https://static1.brasileirinhas.com.br/Brasileirinhas/images/conteudo/cenas/player/"
	chunkSize      = 20
	defaultWorkers = 6
)

type Scraper struct {
	Client *http.Client
	base   string
	// thumbBase is overridable so tests can serve stills locally.
	thumbBase string
}

func New() *Scraper {
	return &Scraper{
		Client:    httpx.NewClient(45 * time.Second),
		base:      defaultURL,
		thumbBase: thumbBase,
	}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"brasileirinhas.com",
		"brasileirinhas.com/videos.html",
		"brasileirinhas.com/pornstar/{slug}.html",
		"brasileirinhas.com/videos/{category}.html",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?brasileirinhas\.com(?:\.br)?(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- discovery ----

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

var (
	videoPathRe  = regexp.MustCompile(`(?i)/video/[^/"?]*?-(\d+)\.html`)
	hrefRe       = regexp.MustCompile(`href="([^"]+)"`)
	pornstarPath = regexp.MustCompile(`(?i)^/pornstar/[^/]+\.html$`)
	categoryPath = regexp.MustCompile(`(?i)^/videos/[^/]+\.html$`)
)

// sceneURLs reads the sitemap. The site has no paged scene index — `/videos.html`
// is a single page of highlights and `?page=` on it 404s — so the sitemap's
// 5,400 `/video/` entries are the only complete listing.
func (s *Scraper) sceneURLs(ctx context.Context) ([]string, error) {
	u := s.base + "/sitemap.xml"
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var set urlset
	if err := xml.Unmarshal(body, &set); err != nil {
		return nil, scraper.ParseError(u, err)
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range set.URLs {
		if !videoPathRe.MatchString(e.Loc) || seen[e.Loc] {
			continue
		}
		seen[e.Loc] = true
		out = append(out, e.Loc)
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(u, fmt.Errorf("sitemap names no scene pages"))
	}
	return out, nil
}

// pageSceneURLs reads the scene links off a performer or category page. Both
// list their whole set on one page — a performer with 268 videos has all 268
// there, with no pager.
func (s *Scraper) pageSceneURLs(ctx context.Context, pageURL string) ([]string, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range hrefRe.FindAllSubmatch(body, -1) {
		ref := html.UnescapeString(string(m[1]))
		if !videoPathRe.MatchString(ref) {
			continue
		}
		abs := absURL(pageURL, ref)
		if abs == "" || seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no scenes linked"))
	}
	return out, nil
}

// ---- run ----

var h1Re = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var (
		urls      []string
		err       error
		performer string
	)
	switch u, perr := url.Parse(studioURL); {
	case perr == nil && pornstarPath.MatchString(u.Path):
		scraper.Debugf(1, "%s: scraping performer page %s", siteID, u.Path)
		var body []byte
		body, err = s.get(ctx, studioURL)
		if err == nil {
			// The performer's name is only stated here. Scene pages credit
			// nobody, which is why the full walk stores no cast at all.
			if m := h1Re.FindSubmatch(body); m != nil {
				performer = cleanText(string(m[1]))
			}
			urls, err = s.pageSceneURLs(ctx, studioURL)
		}
	case perr == nil && categoryPath.MatchString(u.Path):
		scraper.Debugf(1, "%s: scraping category page %s", siteID, u.Path)
		urls, err = s.pageSceneURLs(ctx, studioURL)
	default:
		scraper.Debugf(1, "%s: scraping full catalogue from the sitemap", siteID)
		urls, err = s.sceneURLs(ctx)
	}
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("%s: %w", siteID, err)):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scene pages to fetch", siteID, len(urls))

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}

	// The sitemap is not in date order and the site publishes no dated feed, so
	// the KnownIDs early-stop cannot apply and is not used.
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		start := (page - 1) * chunkSize
		if start >= len(urls) {
			return scraper.PageResult{Done: true}, nil
		}
		end := min(start+chunkSize, len(urls))
		scenes := s.fetchChunk(ctx, urls[start:end], studioURL, performer, workers, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Total:    len(urls),
			Done:     end >= len(urls),
			Continue: true,
		}, nil
	})
}

func (s *Scraper) fetchChunk(ctx context.Context, urls []string, studioURL, performer string, workers int, out chan<- scraper.SceneResult) []models.Scene {
	results := make([]*models.Scene, len(urls))
	errs := make([]error, len(urls))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(urls)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				body, err := s.get(ctx, urls[i])
				if err != nil {
					errs[i] = err
					continue
				}
				sc, err := s.parseScene(body, urls[i], studioURL)
				if err != nil {
					errs[i] = err
					continue
				}
				if performer != "" {
					sc.Performers = []string{performer}
				}
				results[i] = sc
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range urls {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	var scenes []models.Scene
	for i, sc := range results {
		if errs[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("%s: %s: %w", siteID, urls[i], errs[i])):
			case <-ctx.Done():
				return scenes
			}
			continue
		}
		if sc != nil {
			scenes = append(scenes, *sc)
		}
	}
	return scenes
}

// ---- parsing ----

var (
	titleRe      = regexp.MustCompile(`(?is)<h1[^>]*class="titleVideo"[^>]*>(.*?)</h1>`)
	synopsisRe   = regexp.MustCompile(`(?is)<div class="sinopseVideo"[^>]*>(.*?)</div>`)
	durationRe   = regexp.MustCompile(`(?is)class="tempoCena"[^>]*>\s*(\d{1,2}:\d{2}(?::\d{2})?)`)
	breadcrumbRe = regexp.MustCompile(`(?is)<li class="breadcrumb-item active"[^>]*>(.*?)</li>`)
	tagRe        = regexp.MustCompile(`(?is)<li class="liTaguer"><a[^>]*>(.*?)</a></li>`)
	tagStripRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe         = regexp.MustCompile(`\s+`)
)

func (s *Scraper) parseScene(body []byte, sceneURL, studioURL string) (*models.Scene, error) {
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no titleVideo heading"))
	}
	title := cleanText(string(m[1]))
	if title == "" {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("empty title"))
	}
	id := sceneIDOf(sceneURL)
	if id == "" {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no numeric id in the URL"))
	}

	sc := &models.Scene{
		ID:        id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       sceneURL,
		Thumbnail: s.thumbBase + id + ".jpg",
		ScrapedAt: time.Now().UTC(),
	}

	// The synopsis block opens with the title heading, so the heading is cut
	// off rather than repeated into the description.
	if d := synopsisRe.FindSubmatch(body); d != nil {
		sc.Description = cleanText(string(titleRe.ReplaceAll(d[1], nil)))
	}
	if d := durationRe.FindSubmatch(body); d != nil {
		sc.Duration = parseutil.ParseDurationColon(string(d[1]))
	}
	// The breadcrumb's active item names the release the scene came from.
	if b := breadcrumbRe.FindSubmatch(body); b != nil {
		if series := cleanText(string(b[1])); series != "" && series != title {
			sc.Series = series
		}
	}
	seen := map[string]bool{}
	for _, t := range tagRe.FindAllSubmatch(body, -1) {
		name := cleanText(string(t[1]))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		sc.Categories = append(sc.Categories, name)
	}
	return sc, nil
}

// sceneIDOf takes the numeric id off the end of a scene URL. It is the site's
// own content id — the slug in front of it is the localised title and differs
// between the .com and .com.br spellings of the same scene.
func sceneIDOf(sceneURL string) string {
	if m := videoPathRe.FindStringSubmatch(sceneURL); m != nil {
		return m[1]
	}
	return ""
}

func absURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return b.ResolveReference(u).String()
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

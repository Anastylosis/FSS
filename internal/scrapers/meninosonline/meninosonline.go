// Package meninosonline scrapes meninosonline.net.
package meninosonline

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "meninosonline"
	studioName = "Meninos Online"
	defaultURL = "https://www.meninosonline.net"
	listPath   = "/en/movies"
	// The tour renders 15 cards a page and the pager names the last page.
	defaultWorkers = 6
	maxListPages   = 500
)

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), base: defaultURL}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"meninosonline.net",
		"meninosonline.net/en/movies",
		"meninosonline.net/en/player/{slug}",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?meninosonline\.net(?:/.*)?$`)

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

// ---- parsing ----

var (
	cardRe   = regexp.MustCompile(`(?is)<div class="pnl-mini">\s*<a[^>]+href="(/en/player/[^"]+)"[^>]*>\s*<figure>(.*?)</figure>`)
	figCapRe = regexp.MustCompile(`(?is)<figcaption>(.*?)</figcaption>`)
	figImgRe = regexp.MustCompile(`(?is)<img[^>]+src="([^"]+)"`)
	pagerRe  = regexp.MustCompile(`/en/movies\?page=(\d+)`)
	// The scene's cast is the set of performers the page groups its "related
	// scenes" under; the count in brackets is that performer's catalogue size.
	castRe     = regexp.MustCompile(`(?is)<h4 class="d-1">\s*(.*?)\s*(?:\(\d+\))?\s*</h4>`)
	coverRe    = regexp.MustCompile(`(?is)<img[^>]+src="(https://[^"]*/videos/images/[^"]+)"`)
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

type listItem struct {
	slug      string
	title     string
	thumbnail string
}

func parseListing(body []byte, base string) []listItem {
	var items []listItem
	seen := map[string]bool{}
	for _, m := range cardRe.FindAllSubmatch(body, -1) {
		href := html.UnescapeString(string(m[1]))
		slug := strings.TrimPrefix(href, "/en/player/")
		if slug == "" || seen[slug] {
			continue
		}
		fig := string(m[2])
		it := listItem{slug: slug}
		if c := figCapRe.FindStringSubmatch(fig); c != nil {
			it.title = cleanText(c[1])
		}
		if i := figImgRe.FindStringSubmatch(fig); i != nil {
			it.thumbnail = absURL(base, html.UnescapeString(i[1]))
		}
		if it.title == "" {
			continue
		}
		seen[slug] = true
		items = append(items, it)
	}
	return items
}

func lastPage(body []byte) int {
	last := 1
	for _, m := range pagerRe.FindAllSubmatch(body, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		if n > last {
			last = n
		}
	}
	return min(last, maxListPages)
}

// parseCast reads the performers off the page's "related scenes" section, which
// is grouped one heading per performer of this scene. It is the only place the
// cast is named as data — the title runs them together with an ampersand, and
// splitting that would file "Aquele Ton" and "Luiz Felipe" correctly but break
// on any title that is not two names and a subtitle.
func parseCast(body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range castRe.FindAllSubmatch(body, -1) {
		name := cleanText(string(m[1]))
		name = strings.TrimSpace(regexp.MustCompile(`\s*\(\d+\)\s*$`).ReplaceAllString(name, ""))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// ---- run ----

func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		return s.base + listPath
	}
	return fmt.Sprintf("%s%s?page=%d", s.base, listPath, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.Debugf(1, "%s: scraping the scene listing", siteID)

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	last := 0
	perPage := 0

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := s.listingURL(page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if page == 1 {
			// A pager naming only page 1 is indistinguishable from no pager,
			// so it is treated as unknown and the empty-page stop takes over.
			if n := lastPage(body); n > 1 {
				last = n
			}
			scraper.Debugf(1, "%s: listing pager names %d pages", siteID, last)
		}

		items := parseListing(body, s.base)
		if len(items) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("no scene cards on the first listing page"))
			}
			return scraper.PageResult{}, nil
		}
		if page == 1 {
			perPage = len(items)
		}

		scenes := s.fetchDetails(ctx, items, studioURL, workers, out)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  perPage * last,
			Done:   last > 0 && page >= last,
		}, nil
	})
}

func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, studioURL string, workers int, out chan<- scraper.SceneResult) []models.Scene {
	scenes := make([]models.Scene, len(items))
	failed := make([]error, len(items))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(items)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				sc := s.baseScene(items[i], studioURL)
				body, err := s.get(ctx, sc.URL)
				if err != nil {
					failed[i] = err
				} else {
					sc.Performers = parseCast(body)
					if m := coverRe.FindSubmatch(body); m != nil {
						sc.Thumbnail = html.UnescapeString(string(m[1]))
					}
				}
				scenes[i] = sc
			}
		}()
	}
	func() {
		defer close(jobs)
		for i := range items {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()

	// A detail page that did not arrive costs the cast, not the scene — the
	// listing card already carries the title, thumbnail and URL. The failure is
	// still reported so the run counts as incomplete.
	for i := range items {
		if failed[i] != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("%s: %s: %w", siteID, items[i].slug, failed[i])):
			case <-ctx.Done():
				return scenes
			}
		}
	}
	return scenes
}

// baseScene builds what the listing alone knows. The site publishes **no
// per-scene date anywhere**: a scene's own page carries dates only for the
// *other* scenes of its performers, so Scene.Date is left zero rather than
// taken from a thumbnail's upload timestamp.
func (s *Scraper) baseScene(it listItem, studioURL string) models.Scene {
	return models.Scene{
		ID:        it.slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     it.title,
		URL:       s.base + "/en/player/" + it.slug,
		Thumbnail: it.thumbnail,
		ScrapedAt: time.Now().UTC(),
	}
}

func absURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(u).String()
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

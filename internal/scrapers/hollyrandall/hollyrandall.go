// Package hollyrandall scrapes Holly Randall (hollyrandall.com), an Elevated X
// glamour studio tour. The categories/updates_{N}_p.html listing yields each
// scene's _vids.html URL plus a release date (from the embedded package JSON);
// the detail page adds the title, models, description and a content thumbnail
// whose numeric id is used as the stable scene ID. Listing pages past the end
// return no scene blocks, so pagination stops on an empty page.
package hollyrandall

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
	siteID        = "hollyrandall"
	studioName    = "Holly Randall"
	detailWorkers = 4
)

var siteBase = "https://hollyrandall.com"

// Scraper implements scraper.StudioScraper for Holly Randall.
type Scraper struct {
	Client *http.Client
}

// New constructs a Holly Randall scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"hollyrandall.com/categories/updates_{n}_p.html",
		"hollyrandall.com/scenes/{slug}_vids.html",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?hollyrandall\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

// blockSep splits the listing into one segment per scene card. Each card opens
// with `latestUpdateB" data-setid=` and contains the _vids.html link plus the
// package JSON whose TSU timestamp is the release date.
const blockSep = `latestUpdateB" data-setid=`

var (
	vidsURLRe = regexp.MustCompile(`href="[^"]*?(/scenes/[^"]+?_vids\.html)"`)
	tsuRe     = regexp.MustCompile(`"TSU":"(\d{4}-\d{2}-\d{2})`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/categories/updates_%d_p.html", siteBase, page)
		items, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.url] {
				seen[it.url] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

type listItem struct {
	url  string
	date time.Time
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listItem, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	var items []listItem
	seen := make(map[string]bool)
	segments := strings.Split(string(body), blockSep)
	for _, block := range segments[1:] {
		m := vidsURLRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		u := siteBase + m[1]
		if seen[u] {
			continue
		}
		seen[u] = true
		it := listItem{url: u}
		if d := tsuRe.FindStringSubmatch(block); d != nil {
			if t, derr := parseutil.TryParseDate(d[1], "2006-01-02"); derr == nil {
				it.date = t
			}
		}
		items = append(items, it)
	}
	scraper.Debugf(1, "hollyrandall: listing %s -> %d scenes", pageURL, len(items))
	return items, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "hollyrandall: fetching %d details with %d workers", len(items), workers)
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
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

// ---- detail parsing ----

var (
	ogTitleRe   = regexp.MustCompile(`<meta property="og:title" content="([^"]+)"`)
	ogImageRe   = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
	ogDescRe    = regexp.MustCompile(`<meta property="og:description" content="([^"]*)"`)
	contentIDRe = regexp.MustCompile(`contentthumbs/\d+/\d+/(\d+)-`)
	modelLinkRe = regexp.MustCompile(`/models/([A-Za-z0-9_.-]+)\.html"[^>]*>\s*([^<]+?)\s*</a>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       it.url,
		Date:      it.date,
		Studio:    studioName,
		ScrapedAt: now,
	}

	body, err := s.get(ctx, it.url)
	if err != nil {
		return scene
	}
	detail := string(body)

	if m := ogTitleRe.FindStringSubmatch(detail); m != nil {
		scene.Title = cleanTitle(m[1])
	}

	if m := ogImageRe.FindStringSubmatch(detail); m != nil {
		scene.Thumbnail = html.UnescapeString(strings.TrimSpace(m[1]))
		if id := contentIDRe.FindStringSubmatch(scene.Thumbnail); id != nil {
			scene.ID = id[1]
		}
	}

	if m := ogDescRe.FindStringSubmatch(detail); m != nil {
		scene.Description = html.UnescapeString(strings.TrimSpace(m[1]))
	}

	seen := make(map[string]bool)
	for _, m := range modelLinkRe.FindAllStringSubmatch(detail, -1) {
		slug := strings.ToLower(m[1])
		if slug == "models" {
			continue
		}
		name := html.UnescapeString(strings.TrimSpace(m[2]))
		if name == "" || name == "Models" || seen[name] {
			continue
		}
		seen[name] = true
		scene.Performers = append(scene.Performers, name)
	}

	return scene
}

// cleanTitle strips the "Holly Randall - " prefix and " - Movies" suffix from
// the og:title, e.g. "Holly Randall - Built for Speed - Movies" -> "Built for Speed".
func cleanTitle(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, studioName+" - ")
	s = strings.TrimSuffix(s, " - Movies")
	return strings.TrimSpace(s)
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

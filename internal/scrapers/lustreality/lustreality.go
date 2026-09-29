// Package lustreality scrapes LustReality (lustreality.com), a VR site on a
// bespoke Nette/PHP stack (ndevs.eu) with no JSON API.
//
// Enumeration runs off the video sitemap rather than the 6-page listing: it is
// one request for the complete set of ~150 scenes, and the listing cards carry
// neither date nor performers anyway.
//
// Detail pages expose a full schema.org VideoObject, which is the sole
// extraction target — the visible HTML shows only a relative date ("1 week
// ago"), so the JSON-LD uploadDate is the only precise one. Pages carry several
// JSON-LD blocks, so the VideoObject is selected by @type.
//
// The site has no real tag taxonomy: detail pages repeat a fixed global keyword
// strip, so tags are deliberately not collected.
//
// robots.txt asks for Crawl-delay: 10, which the operator's --delay setting
// should honour; RecommendedDelay surfaces that.
package lustreality

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
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
	siteID        = "lustreality"
	studioName    = "LustReality"
	detailWorkers = 3
)

// RecommendedDelay mirrors the site's robots.txt Crawl-delay of 10s.
const RecommendedDelay = 10 * time.Second

var siteBase = "https://www.lustreality.com"

// Scraper implements scraper.StudioScraper for LustReality.
type Scraper struct {
	Client *http.Client
}

// New constructs a LustReality scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"lustreality.com",
		"lustreality.com/en/videos",
		"lustreality.com/en/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?lustreality\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- sitemap ----

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func (s *Scraper) fetchSitemap(ctx context.Context) ([]string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     siteBase + "/sitemaps/lustreality/sitemap_video.xml",
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading sitemap: %w", err)
	}

	var us urlset
	if err := xml.Unmarshal(body, &us); err != nil {
		return nil, fmt.Errorf("parsing sitemap: %w", err)
	}

	seen := make(map[string]bool)
	urls := make([]string, 0, len(us.URLs))
	for _, u := range us.URLs {
		loc := strings.TrimSpace(u.Loc)
		if loc == "" || seen[loc] {
			continue
		}
		seen[loc] = true
		urls = append(urls, loc)
	}
	return urls, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	defer close(out)

	scraper.WarnDelayBelow(siteID, opts.Delay, RecommendedDelay)

	urls, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes in sitemap", siteID, len(urls))

	select {
	case out <- scraper.Progress(len(urls)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	work := make(chan string)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching details with %d workers", siteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pageURL := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.toScene(ctx, studioURL, pageURL, now)
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

	for _, u := range urls {
		select {
		case work <- u:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return
		}
	}
	close(work)
	wg.Wait()
}

// ---- detail ----

// errNoVideoObject covers both a redesign and the Anubis challenge page the
// site now serves in place of every HTML page.
var errNoVideoObject = errors.New("no VideoObject JSON-LD on the page")

var (
	ldRe = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*?)</script>`)
	// The scene's stable id is the stream UUID on the listing/detail markup.
	uuidRe = regexp.MustCompile(`stream-lr\.ndevs\.eu/(?:trailer|thumbnail)/([0-9a-f-]{36})`)
)

type person struct {
	Name string `json:"name"`
}

type videoObject struct {
	Type        string   `json:"@type"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Thumbnail   string   `json:"thumbnailUrl"`
	UploadDate  string   `json:"uploadDate"`
	Duration    string   `json:"duration"`
	Actors      []person `json:"actor"`
}

func (s *Scraper) toScene(ctx context.Context, studioURL, pageURL string, now time.Time) (models.Scene, error) {
	body, err := s.fetchPage(ctx, pageURL)
	if err != nil {
		return models.Scene{}, err
	}
	detail := string(body)

	vo := parseVideoObject(detail)
	if vo == nil {
		return models.Scene{}, scraper.ParseError(pageURL, errNoVideoObject)
	}

	scene := models.Scene{
		ID:          sceneID(detail, pageURL),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       html.UnescapeString(strings.TrimSpace(vo.Name)),
		URL:         pageURL,
		Description: html.UnescapeString(strings.TrimSpace(vo.Description)),
		Thumbnail:   vo.Thumbnail,
		Studio:      studioName,
		ScrapedAt:   now,
	}

	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(vo.UploadDate)); err == nil {
		scene.Date = t.UTC()
	}
	// ISO 8601, e.g. "PT46M10S".
	scene.Duration = parseutil.ParseDurationISO(vo.Duration)

	for _, a := range vo.Actors {
		if name := html.UnescapeString(strings.TrimSpace(a.Name)); name != "" {
			scene.Performers = append(scene.Performers, name)
		}
	}

	return scene, nil
}

// sceneID prefers the stream UUID, which is stable across renames; the URL
// slug is the fallback.
func sceneID(detail, pageURL string) string {
	if m := uuidRe.FindStringSubmatch(detail); m != nil {
		return m[1]
	}
	trimmed := strings.TrimRight(pageURL, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// parseVideoObject returns the page's VideoObject. Detail pages carry several
// JSON-LD blocks, so it is selected by @type rather than position.
func parseVideoObject(detail string) *videoObject {
	for _, m := range ldRe.FindAllStringSubmatch(detail, -1) {
		var vo videoObject
		if err := json.Unmarshal([]byte(m[1]), &vo); err != nil {
			continue
		}
		if vo.Type == "VideoObject" {
			return &vo
		}
	}
	return nil
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

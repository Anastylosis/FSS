// Package frenchtwinks scrapes French Twinks (french-twinks.com), a bespoke
// in-house PHP site.
//
// Enumeration runs off the site's video sitemap rather than the paginated
// listing: one 1.1 MB request yields every scene's URL, id, release date and
// tags, which is cheaper and more complete than walking 26 listing pages.
//
// Two things shape the design:
//
//   - The sitemap is bilingual. Each scene appears once with a French <loc>
//     and an English <xhtml:link hreflang="en">, and the video:title /
//     video:description / video:category fields are French on both. So the
//     sitemap supplies the URL, date, id and tags, and the English detail page
//     supplies the title, description and performers.
//   - Detail pages carry two JSON-LD blocks, an Organization and a VideoObject.
//     They must be selected by @type, not by position.
//
// Duration is not in the JSON-LD — it exists only as prose ("15 minutes"), and
// `actors` is a comma-joined string rather than an array.
package frenchtwinks

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "frenchtwinks"
	studioName    = "French Twinks"
	detailWorkers = 4
)

var siteBase = "https://www.french-twinks.com"

// Scraper implements scraper.StudioScraper for French Twinks.
type Scraper struct {
	Client *http.Client
}

// New constructs a French Twinks scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(60 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"french-twinks.com",
		"french-twinks.com/en/gay-porn-videos/{slug}",
		"french-twinks.com/videos-porno-gay/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?french-twinks\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- sitemap ----

type urlset struct {
	URLs []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc   string `xml:"loc"`
	Links []struct {
		HrefLang string `xml:"hreflang,attr"`
		Href     string `xml:"href,attr"`
	} `xml:"link"`
	Video *struct {
		Title       string `xml:"title"`
		ThumbnailV  string `xml:"thumbnail_loc"`
		PublishDate string `xml:"publication_date"`
		Category    string `xml:"category"`
		ContentLoc  string `xml:"content_loc"`
	} `xml:"video"`
}

type sitemapItem struct {
	id, url, thumbnail string
	date               time.Time
	tags               []string
}

// sceneIDRe pulls the numeric scene id out of the thumbnail path
// (".../video-gay_946-1.jpg"), which is the only place it appears in the
// sitemap.
var sceneIDRe = regexp.MustCompile(`video-gay_(\d+)-\d+\.jpg`)

func (s *Scraper) fetchSitemap(ctx context.Context) ([]sitemapItem, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     siteBase + "/sitemap.xml",
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

	items := make([]sitemapItem, 0, len(us.URLs))
	seen := make(map[string]bool)
	for _, u := range us.URLs {
		if u.Video == nil {
			continue
		}
		m := sceneIDRe.FindStringSubmatch(u.Video.ThumbnailV)
		if m == nil || seen[m[1]] {
			continue
		}

		// Prefer the English URL; the <loc> may be either language.
		pageURL := u.Loc
		if !strings.Contains(pageURL, "/en/") {
			for _, l := range u.Links {
				if l.HrefLang == "en" && l.Href != "" {
					pageURL = l.Href
					break
				}
			}
		}
		if !strings.Contains(pageURL, "/en/") {
			continue
		}

		seen[m[1]] = true
		it := sitemapItem{id: m[1], url: pageURL, thumbnail: u.Video.ThumbnailV}
		if d, err := time.Parse("2006-01-02", strings.TrimSpace(u.Video.PublishDate)); err == nil {
			it.date = d.UTC()
		}
		for _, c := range strings.Split(u.Video.Category, ",") {
			if c = strings.TrimSpace(html.UnescapeString(c)); c != "" {
				it.tags = append(it.tags, c)
			}
		}
		items = append(items, it)
	}
	return items, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	defer close(out)

	items, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes in sitemap", siteID, len(items))

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	work := make(chan sitemapItem)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching details with %d workers", siteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range work {
				if opts.Delay > 0 {
					select {
					case <-time.After(opts.Delay):
					case <-ctx.Done():
						return
					}
				}
				select {
				case out <- scraper.Scene(s.toScene(ctx, studioURL, it, now)):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, it := range items {
		select {
		case work <- it:
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

var (
	ldRe       = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*?)</script>`)
	durationRe = regexp.MustCompile(`<strong>(\d+)\s*minutes?</strong>`)
)

type videoObject struct {
	Type        string `json:"@type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	UploadDate  string `json:"uploadDate"`
	Actors      string `json:"actors"`
	Directors   string `json:"directors"`
	Thumbnail   string `json:"thumbnailUrl"`
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, it sitemapItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       it.url,
		Date:      it.date,
		Thumbnail: it.thumbnail,
		Tags:      it.tags,
		Studio:    studioName,
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, it.url)
	if err != nil {
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if vo := parseVideoObject(detail); vo != nil {
		if v := strings.TrimSpace(vo.Name); v != "" {
			scene.Title = html.UnescapeString(v)
		}
		if v := strings.TrimSpace(vo.Description); v != "" {
			scene.Description = html.UnescapeString(v)
		}
		if vo.Thumbnail != "" {
			scene.Thumbnail = vo.Thumbnail
		}
		scene.Director = html.UnescapeString(strings.TrimSpace(vo.Directors))
		// actors is a comma-joined string, not an array.
		for _, a := range strings.Split(vo.Actors, ",") {
			if a = html.UnescapeString(strings.TrimSpace(a)); a != "" {
				scene.Performers = append(scene.Performers, a)
			}
		}
		if scene.Date.IsZero() {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(vo.UploadDate)); err == nil {
				scene.Date = t.UTC()
			}
		}
	}

	// Duration is prose, not a JSON-LD field.
	if m := durationRe.FindStringSubmatch(detail); m != nil {
		scene.Duration = atoi(m[1]) * 60
	}
}

// parseVideoObject returns the page's VideoObject block. Pages carry an
// Organization block too, so selection is by @type rather than position.
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

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
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

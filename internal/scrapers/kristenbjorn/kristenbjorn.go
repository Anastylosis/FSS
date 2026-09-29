// Package kristenbjorn scrapes Kristen Bjorn (kristenbjorn.com), a bespoke PHP
// site.
//
// There is no working pagination: the homepage's "More Scenes" button only
// reveals rows already present in the DOM, and no `?page=`/offset parameter
// exists. Enumeration therefore runs off the sitemap, which lists all ~2100
// `/video-{id}/{slug}` pages in one request. (The sitemap's `<lastmod>` is a
// single global timestamp on every entry, so it is useless for change detection
// and is ignored.)
//
// Detail pages carry a schema.org VideoObject, which supplies title,
// description, thumbnail and date — note `uploadDate` is
// "YYYY-MM-DD HH:MM:SS", not RFC 3339. Pages also carry a second Product
// JSON-LD block, so the VideoObject is selected by @type.
//
// Performers and categories are not in the JSON-LD. They are read from the
// `title="Gay Porn Star: …"` / `title="Categorie: …"` attributes, which is what
// separates the scene's own credits from the site-wide nav menu listing all 876
// stars and 599 categories.
//
// Duration is not published anywhere.
package kristenbjorn

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
	siteID        = "kristenbjorn"
	studioName    = "Kristen Bjorn"
	detailWorkers = 4
	// The JSON-LD date is a space-separated timestamp, not RFC 3339.
	dateLayout = "2006-01-02 15:04:05"
	// maxSitemapBytes covers the ~1.4 MB sitemap with headroom.
	maxSitemapBytes = 32 * 1024 * 1024
)

var siteBase = "https://www.kristenbjorn.com"

// Scraper implements scraper.StudioScraper for Kristen Bjorn.
type Scraper struct {
	Client *http.Client
}

// New constructs a Kristen Bjorn scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(60 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"kristenbjorn.com",
		"kristenbjorn.com/video-{id}/{slug}",
		"kristenbjorn.com/pornvideo-tags/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?kristenbjorn\.com(?:/|$)`)

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

// videoURLRe matches scene pages. The sitemap also lists models, tags, DVD
// titles and theatre pages, which are not scenes.
var videoURLRe = regexp.MustCompile(`/video-(\d+)/`)

type sceneRef struct {
	id, url string
}

func (s *Scraper) fetchSitemap(ctx context.Context) ([]sceneRef, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     siteBase + "/sitemap.xml",
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBodyN(resp.Body, maxSitemapBytes)
	if err != nil {
		return nil, fmt.Errorf("reading sitemap: %w", err)
	}

	var us urlset
	if err := xml.Unmarshal(body, &us); err != nil {
		return nil, fmt.Errorf("parsing sitemap: %w", err)
	}

	refs := make([]sceneRef, 0, len(us.URLs))
	seen := make(map[string]bool)
	for _, u := range us.URLs {
		m := videoURLRe.FindStringSubmatch(u.Loc)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		refs = append(refs, sceneRef{id: m[1], url: u.Loc})
	}
	return refs, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	defer close(out)

	refs, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes in sitemap", siteID, len(refs))

	select {
	case out <- scraper.Progress(len(refs)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	work := make(chan sceneRef)
	var wg sync.WaitGroup
	scraper.Debugf(1, "%s: fetching details with %d workers", siteID, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ok := s.toScene(ctx, studioURL, ref, now)
				if !ok {
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

	for _, ref := range refs {
		select {
		case work <- ref:
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
	ldRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	// The title attributes are what separate the scene's own credits from the
	// site-wide nav menu, which links every star and category on the site.
	starRe     = regexp.MustCompile(`title="Gay Porn Star: ([^"]+)"`)
	catRe      = regexp.MustCompile(`title="Categorie: ([^"]+)"`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type videoObject struct {
	Type        string `json:"@type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Thumbnail   string `json:"thumbnailUrl"`
	UploadDate  string `json:"uploadDate"`
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, ref sceneRef, now time.Time) (models.Scene, bool) {
	body, err := s.fetchPage(ctx, ref.url)
	if err != nil {
		return models.Scene{}, false
	}
	detail := string(body)

	vo := parseVideoObject(detail)
	if vo == nil {
		return models.Scene{}, false
	}

	scene := models.Scene{
		ID:        ref.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     cleanText(vo.Name),
		URL:       ref.url,
		Thumbnail: vo.Thumbnail,
		Studio:    studioName,
		ScrapedAt: now,
	}
	// The description is HTML.
	scene.Description = cleanText(tagStripRe.ReplaceAllString(vo.Description, " "))

	if t, err := time.Parse(dateLayout, strings.TrimSpace(vo.UploadDate)); err == nil {
		scene.Date = t.UTC()
	}

	scene.Performers = titleCased(starRe, detail)
	scene.Categories = titleCased(catRe, detail)

	return scene, true
}

// parseVideoObject returns the page's VideoObject. Pages also carry a Product
// block, so selection is by @type rather than position.
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

// titleCased collects the regex's first group, deduped and title-cased — the
// title attributes render names in lower case.
func titleCased(re *regexp.Regexp, detail string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range re.FindAllStringSubmatch(detail, -1) {
		v := titleCase(cleanText(m[1]))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func titleCase(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
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

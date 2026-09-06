// Package nucosplay scrapes nucosplay.com.
package nucosplay

import (
	"bytes"
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
	siteID     = "nucosplay"
	studioName = "Nu Cosplay"
	defaultURL = "https://nucosplay.com"
	// The site is WordPress but its REST API answers 401 to an anonymous
	// client, so the scene list comes from the sitemap the same install
	// publishes. Scenes are the `vms_videos` post type.
	sitemapIndex   = "/wp-sitemap.xml"
	chunkSize      = 20
	defaultWorkers = 6
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
		"nucosplay.com",
		"nucosplay.com/pornstar/{slug}",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?nucosplay\.com(?:/.*)?$`)

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

type sitemapIdx struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// videoSitemapRe names the sub-sitemaps holding scenes. WordPress splits a post
// type across numbered files once it grows past a few thousand entries, so
// every matching part is read rather than only the first.
var videoSitemapRe = regexp.MustCompile(`(?i)wp-sitemap-posts-vms_videos-\d+\.xml`)

func (s *Scraper) sceneURLs(ctx context.Context) ([]string, error) {
	idxURL := s.base + sitemapIndex
	body, err := s.get(ctx, idxURL)
	if err != nil {
		return nil, err
	}
	var idx sitemapIdx
	if err := xml.Unmarshal(body, &idx); err != nil {
		return nil, scraper.ParseError(idxURL, err)
	}

	var parts []string
	for _, sm := range idx.Sitemaps {
		if videoSitemapRe.MatchString(sm.Loc) {
			parts = append(parts, sm.Loc)
		}
	}
	if len(parts) == 0 {
		return nil, scraper.ParseError(idxURL, fmt.Errorf("sitemap index names no vms_videos part"))
	}

	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		body, err := s.get(ctx, part)
		if err != nil {
			return nil, fmt.Errorf("sitemap %s: %w", part, err)
		}
		var set urlset
		if err := xml.Unmarshal(body, &set); err != nil {
			return nil, scraper.ParseError(part, err)
		}
		for _, u := range set.URLs {
			if u.Loc == "" || seen[u.Loc] {
				continue
			}
			seen[u.Loc] = true
			out = append(out, u.Loc)
		}
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(parts[0], fmt.Errorf("sitemap lists no scenes"))
	}
	return out, nil
}

// performerSceneURLs reads one `/pornstar/{slug}/` page.
func (s *Scraper) performerSceneURLs(ctx context.Context, pageURL string) ([]string, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range hrefRe.FindAllSubmatch(body, -1) {
		u := absSceneURL(pageURL, html.UnescapeString(string(m[1])))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, scraper.ParseError(pageURL, fmt.Errorf("no scenes linked"))
	}
	return out, nil
}

// absSceneURL returns the absolute form of a scene link, or "" for any other
// href. Scene permalinks are `/{slug}-{n}/` at the site root, which is also
// where the site's ordinary pages live, so the shape of the path is the only
// thing distinguishing them.
func absSceneURL(pageURL, ref string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(u)
	if abs.Host != base.Host || !scenePathRe.MatchString(abs.Path) {
		return ""
	}
	abs.RawQuery = ""
	abs.Fragment = ""
	return abs.String()
}

// ---- run ----

var performerPathRe = regexp.MustCompile(`(?i)^/pornstar/[^/]+/?$`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var (
		urls []string
		err  error
	)
	if u, perr := url.Parse(studioURL); perr == nil && performerPathRe.MatchString(u.Path) {
		scraper.Debugf(1, "%s: scraping performer page %s", siteID, u.Path)
		urls, err = s.performerSceneURLs(ctx, studioURL)
	} else {
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

	// The sitemap is not in date order, so a stored id never means the rest of
	// the walk is stored; the KnownIDs early-stop is deliberately not used.
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		start := (page - 1) * chunkSize
		if start >= len(urls) {
			return scraper.PageResult{Done: true}, nil
		}
		end := min(start+chunkSize, len(urls))
		scenes := s.fetchChunk(ctx, urls[start:end], studioURL, workers, out)
		return scraper.PageResult{
			Scenes:   scenes,
			Total:    len(urls),
			Done:     end >= len(urls),
			Continue: true,
		}, nil
	})
}

func (s *Scraper) fetchChunk(ctx context.Context, urls []string, studioURL string, workers int, out chan<- scraper.SceneResult) []models.Scene {
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
				sc, err := parseScene(body, urls[i], studioURL)
				if err != nil {
					errs[i] = err
					continue
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
	hrefRe             = regexp.MustCompile(`href="([^"]+)"`)
	scenePathRe        = regexp.MustCompile(`(?i)^/[a-z0-9][a-z0-9-]*-\d+/?$`)
	h1Re               = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	durationMarkerRe   = regexp.MustCompile(`class="video-duration"`)
	readMoreToggleRe   = regexp.MustCompile(`(?is)<span[^>]*class="dots"[^>]*>.*?</span>`)
	openTagTailRe      = regexp.MustCompile(`(?s)<[^>]*$`)
	durationRe         = regexp.MustCompile(`(?is)class="video-duration"[^>]*>.*?(\d{1,2}:\d{2}(?::\d{2})?)`)
	dateRe             = regexp.MustCompile(`(?is)class="video-date"[^>]*>.*?</svg>\s*([A-Z][a-z]+\s+\d{1,2}(?:st|nd|rd|th)?,\s*\d{4})`)
	ogImageRe          = regexp.MustCompile(`(?i)<meta property="og:image" content="([^"]+)"`)
	performerRe        = regexp.MustCompile(`(?is)<a[^>]+href="[^"]*/pornstar/[^"]+"[^>]*>(.*?)</a>`)
	categoryRe         = regexp.MustCompile(`(?is)<a[^>]+href="[^"]*/category/[^"]+"[^>]*>(.*?)</a>`)
	tagStripRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe               = regexp.MustCompile(`\s+`)
	spaceBeforePunctRe = regexp.MustCompile(` +([.,;:!?])`)
)

func parseScene(body []byte, sceneURL, studioURL string) (*models.Scene, error) {
	m := h1Re.FindSubmatch(body)
	if m == nil {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("no <h1> on the page"))
	}
	title := cleanText(string(m[1]))
	if title == "" {
		return nil, scraper.ParseError(sceneURL, fmt.Errorf("empty title"))
	}

	sc := &models.Scene{
		ID:        slugOf(sceneURL),
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     title,
		URL:       sceneURL,
		ScrapedAt: time.Now().UTC(),
	}

	if d := durationRe.FindSubmatch(body); d != nil {
		sc.Duration = parseutil.ParseDurationColon(string(d[1]))
	}
	// Dates are written with an ordinal day ("November 26th, 2023"), which no
	// Go layout parses.
	if d := dateRe.FindSubmatch(body); d != nil {
		if t, err := parseutil.TryParseDate(parseutil.StripOrdinalSuffix(cleanText(string(d[1]))),
			"January 2, 2006"); err == nil {
			sc.Date = t.UTC()
		}
	}
	if i := ogImageRe.FindSubmatch(body); i != nil {
		sc.Thumbnail = html.UnescapeString(string(i[1]))
	}
	sc.Performers = names(performerRe, body)
	sc.Categories = names(categoryRe, body)
	sc.Description = description(body)
	return sc, nil
}

func names(re *regexp.Regexp, body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range re.FindAllSubmatch(body, -1) {
		n := cleanText(string(m[1]))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// description reads the scene copy. It has no class of its own and the page's
// own meta description is truncated mid-sentence, but its position is fixed: it
// is the last paragraph before the metadata strip that opens with the runtime.
// Anchoring there matters — the breadcrumb and the age warning are both longer
// paragraphs on every page — and the slice runs from the opening <p> rather
// than a matched pair, because the markup leaves paragraphs unclosed and a
// non-greedy <p>…</p> match swallows most of the page.
func description(body []byte) string {
	loc := durationMarkerRe.FindIndex(body)
	if loc == nil {
		return ""
	}
	head := body[:loc[0]]
	i := bytes.LastIndex(head, []byte("<p"))
	if i < 0 {
		return ""
	}
	// The copy is split by a "... Read More" toggle whose visible half sits
	// inside the paragraph; dropping that span rejoins the sentence it cut.
	para := readMoreToggleRe.ReplaceAll(head[i:], nil)
	// The slice ends inside whatever markup opened the metadata strip, so the
	// tag stripper needs to drop that unterminated fragment too.
	para = openTagTailRe.ReplaceAll(para, nil)
	return cleanText(string(para))
}

func slugOf(sceneURL string) string {
	u, err := url.Parse(sceneURL)
	if err != nil {
		return sceneURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if last := parts[len(parts)-1]; last != "" {
		return last
	}
	return sceneURL
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	// Stripping the tags between a sentence and its own full stop — which the
	// "Read More" toggle splits apart — leaves a space in front of it.
	s = spaceBeforePunctRe.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

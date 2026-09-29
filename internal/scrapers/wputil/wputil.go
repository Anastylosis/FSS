package wputil

import (
	"context"
	"encoding/xml"
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

// Meta holds metadata extracted from a WordPress page's HTML.
type Meta struct {
	Title       string
	Date        time.Time
	Description string
	Thumbnail   string
	PostID      string
	Tags        []string // from <meta property="article:tag">
	Categories  []string // from articleSection in JSON-LD
	Width       int      // from VideoObject JSON-LD
	Height      int      // from VideoObject JSON-LD
	HasVideo    bool     // true if a VideoObject JSON-LD block was found
}

// ---- sitemap ----

type urlset struct {
	URLs []SitemapURL `xml:"url"`
}

type SitemapURL struct {
	Loc string `xml:"loc"`
	// LastMod is the sitemap's <lastmod>, when present. It is a modification
	// time, not a publish date, so treat it as approximate — but on sites that
	// expose no date at all in their page markup it is the only date source.
	LastMod string `xml:"lastmod"`
}

func FetchSitemap(ctx context.Context, client *http.Client, sitemapURL string, headers map[string]string) ([]SitemapURL, error) {
	resp, err := httpx.Do(ctx, client, httpx.Request{
		URL:     sitemapURL,
		Headers: headers,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching sitemap %s: %w", sitemapURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading sitemap: %w", err)
	}

	var us urlset
	if err := xml.Unmarshal(body, &us); err != nil {
		return nil, fmt.Errorf("parsing sitemap XML: %w", err)
	}
	return us.URLs, nil
}

type sitemapindex struct {
	Sitemaps []SitemapURL `xml:"sitemap"`
}

// FetchSitemapIndex returns the <loc> of every sitemap listed in a sitemap
// index, so a site that adds a sitemap file is picked up without a code change.
// Callers filter the result to the sitemaps they want.
func FetchSitemapIndex(ctx context.Context, client *http.Client, indexURL string, headers map[string]string) ([]string, error) {
	resp, err := httpx.Do(ctx, client, httpx.Request{
		URL:     indexURL,
		Headers: headers,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching sitemap index %s: %w", indexURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading sitemap index: %w", err)
	}

	var idx sitemapindex
	if err := xml.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("parsing sitemap index XML: %w", err)
	}
	locs := make([]string, 0, len(idx.Sitemaps))
	for _, sm := range idx.Sitemaps {
		if loc := strings.TrimSpace(sm.Loc); loc != "" {
			locs = append(locs, loc)
		}
	}
	return locs, nil
}

// FetchAllSitemaps fetches multiple sitemaps and returns the combined URL list.
func FetchAllSitemaps(ctx context.Context, client *http.Client, urls []string, headers map[string]string) ([]SitemapURL, error) {
	var all []SitemapURL
	for _, u := range urls {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		entries, err := FetchSitemap(ctx, client, u, headers)
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}
	return all, nil
}

// ---- page fetching ----

func FetchPage(ctx context.Context, client *http.Client, pageURL string, headers map[string]string) ([]byte, error) {
	resp, err := httpx.Do(ctx, client, httpx.Request{
		URL:     pageURL,
		Headers: headers,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", pageURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", pageURL, err)
	}
	return body, nil
}

// ---- metadata extraction ----

var (
	titleRe          = regexp.MustCompile(`<title>([^<]+)</title>`)
	publishedRe      = regexp.MustCompile(`article:published_time"\s+content="([^"]+)"`)
	shortlinkRe      = regexp.MustCompile(`rel='shortlink'\s+href='[^?]*\?p=(\d+)'`)
	articleTagRe     = regexp.MustCompile(`article:tag"\s+content="([^"]*)"`)
	articleSectionRe = regexp.MustCompile(`"articleSection"\s*:\s*"([^"]*)"`)
	videoWidthRe     = regexp.MustCompile(`"@type"\s*:\s*"VideoObject"[^}]*"width"\s*:\s*"(\d+)"`)
	videoHeightRe    = regexp.MustCompile(`"@type"\s*:\s*"VideoObject"[^}]*"height"\s*:\s*"(\d+)"`)
	hasVideoRe       = regexp.MustCompile(`"@type"\s*:\s*"VideoObject"`)
	ldDescRe         = regexp.MustCompile(`"@type"\s*:\s*"VideoObject"[^}]*"description"\s*:\s*"([^"]*)"`)
	ldThumbnailRe    = regexp.MustCompile(`"@type"\s*:\s*"VideoObject"[^}]*"thumbnailUrl"\s*:\s*"([^"]*)"`)
	ldUploadDateRe   = regexp.MustCompile(`"@type"\s*:\s*"VideoObject"[^}]*"uploadDate"\s*:\s*"([^"]*)"`)
)

// ParseMeta extracts common WordPress metadata from raw HTML.
// titleSuffix is stripped from the <title> tag (e.g. " - Mom Comes First").
func ParseMeta(body []byte, titleSuffix string) Meta {
	var m Meta

	if match := titleRe.FindSubmatch(body); match != nil {
		m.Title = html.UnescapeString(string(match[1]))
		m.Title = strings.TrimSuffix(m.Title, titleSuffix)
		m.Title = strings.TrimSpace(m.Title)
	}

	if match := publishedRe.FindSubmatch(body); match != nil {
		if t, err := time.Parse(time.RFC3339, string(match[1])); err == nil {
			m.Date = t.UTC()
		}
	}

	og := parseutil.OpenGraph(body)
	if v := og["og:description"]; v != "" {
		m.Description = html.UnescapeString(v)
	}
	if v := og["og:image"]; v != "" {
		m.Thumbnail = v
	}

	if match := shortlinkRe.FindSubmatch(body); match != nil {
		m.PostID = string(match[1])
	}

	seen := make(map[string]bool)
	for _, match := range articleTagRe.FindAllSubmatch(body, -1) {
		tag := html.UnescapeString(strings.TrimSpace(string(match[1])))
		if tag != "" && !seen[tag] {
			seen[tag] = true
			m.Tags = append(m.Tags, tag)
		}
	}

	if match := articleSectionRe.FindSubmatch(body); match != nil {
		for _, cat := range strings.Split(string(match[1]), ",") {
			cat = html.UnescapeString(strings.TrimSpace(cat))
			if cat != "" {
				m.Categories = append(m.Categories, cat)
			}
		}
	}

	if match := videoWidthRe.FindSubmatch(body); match != nil {
		m.Width, _ = strconv.Atoi(string(match[1]))
	}
	if match := videoHeightRe.FindSubmatch(body); match != nil {
		m.Height, _ = strconv.Atoi(string(match[1]))
	}

	m.HasVideo = hasVideoRe.Match(body)

	// JSON-LD VideoObject fallbacks for sites without og: meta tags.
	if m.Description == "" {
		if match := ldDescRe.FindSubmatch(body); match != nil {
			m.Description = html.UnescapeString(string(match[1]))
		}
	}
	if m.Thumbnail == "" {
		if match := ldThumbnailRe.FindSubmatch(body); match != nil {
			m.Thumbnail = string(match[1])
		}
	}
	if m.Date.IsZero() {
		if match := ldUploadDateRe.FindSubmatch(body); match != nil {
			if t, err := time.Parse(time.RFC3339, string(match[1])); err == nil {
				m.Date = t.UTC()
			}
		}
	}

	return m
}

// SlugFromURL extracts the last path segment, stripping .html extension.
// Trailing slashes are removed before .html stripping so inputs like
// "https://x/post.html/" still resolve to "post".
func SlugFromURL(pageURL string) string {
	pageURL = strings.TrimRight(pageURL, "/")
	pageURL = strings.TrimSuffix(pageURL, ".html")
	parts := strings.Split(pageURL, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return pageURL
}

// ParseDuration converts "MM:SS" or "H:MM:SS" to seconds.
func ParseDuration(s string) int {
	parts := strings.Split(s, ":")
	total := 0
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		total = total*60 + n
	}
	return total
}

// VideoWidth returns the standard width for a given height.
func VideoWidth(height int) int {
	switch {
	case height >= 2160:
		return 3840
	case height >= 1080:
		return 1920
	case height >= 720:
		return 1280
	case height >= 480:
		return 854
	default:
		return 0
	}
}

// ---- worker pool ----

// PageParser is called for each page. Returns a scene, whether to skip, and any error.
type PageParser func(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, bool, error)

// RunWorkerPool fetches sitemaps, then dispatches pages to workers that call parse.
func RunWorkerPool(ctx context.Context, client *http.Client, headers map[string]string,
	sitemapURLs []string, studioURL string, opts scraper.ListOpts,
	parse PageParser, out chan<- scraper.SceneResult) {

	scraper.Debugf(1, "wputil: fetching %d sitemaps", len(sitemapURLs))
	allURLs, err := FetchAllSitemaps(ctx, client, sitemapURLs, headers)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}

	if len(allURLs) > 0 {
		scraper.Debugf(1, "wputil: %d total pages from sitemaps", len(allURLs))
		select {
		case out <- scraper.Progress(len(allURLs)):
		case <-ctx.Done():
			return
		}
	}

	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	scraper.Debugf(1, "wputil: fetching %d detail pages with %d workers", len(allURLs), opts.Workers)

	work := make(chan SitemapURL, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if ctx.Err() != nil {
					return
				}
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}

				body, fetchErr := FetchPage(ctx, client, entry.Loc, headers)
				if fetchErr != nil {
					select {
					case out <- scraper.Error(fetchErr):
					case <-ctx.Done():
						return
					}
					continue
				}

				scene, skip, parseErr := parse(studioURL, entry.Loc, body, time.Now().UTC())
				if parseErr != nil {
					select {
					case out <- scraper.Error(parseErr):
					case <-ctx.Done():
						return
					}
					continue
				}
				if skip {
					continue
				}
				if opts.KnownIDs[scene.ID] {
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

	for _, u := range allURLs {
		select {
		case work <- u:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}

	close(work)
	wg.Wait()
}

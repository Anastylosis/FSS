// Package producersfun scrapes Producers Fun (producersfun.com), a custom
// Laravel app (`nebula_front_session` cookie, CloudFront assets).
//
// Enumeration runs off `/sitemapxml/1.xml` rather than the listing. The listing
// pages work (`/videos?page=N`, 10 per page) but render no pagination markup at
// all, so the only way to find the end is to keep probing until a page comes
// back empty; the sitemap gives all 329 scene URLs in one request.
//
// The sitemap also carries the site's 165 `/performer/{slug}` URLs, which
// matters because **the detail page marks up no cast at all**. Scene slugs are
// built as `{performer-slug}-{category-slug}`, so the performer is recovered by
// matching each scene slug against the known performer slugs — longest match
// first, since some performer slugs are prefixes of others.
//
// Date, duration and tags all live on the detail page, inside a single
// `video-date` paragraph ("July 17th, 2026 … 42:16 … 73 Photos") whose date
// carries an English ordinal suffix and whose photo count must not be mistaken
// for the runtime.
package producersfun

import (
	"context"
	"encoding/xml"
	"fmt"
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

const (
	siteID        = "producersfun"
	studioName    = "Producers Fun"
	detailWorkers = 4
	// Dates render as "July 17th, 2026" — the ordinal suffix is stripped first.
	dateLayout = "January 2, 2006"
)

var siteBase = "https://producersfun.com"

// Scraper implements scraper.StudioScraper for Producers Fun.
type Scraper struct {
	Client *http.Client
}

// New constructs a Producers Fun scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"producersfun.com",
		"producersfun.com/videos",
		"producersfun.com/video/{slug}",
		"producersfun.com/performer/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?producersfun\.com(?:/|$)`)

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

var (
	videoURLRe = regexp.MustCompile(`/video/([^/?#]+)$`)
	// The performers index (/performers) is not a performer.
	performerURLRe = regexp.MustCompile(`/performer/([^/?#]+)$`)
)

type catalogue struct {
	slugs      []string
	performers []string
}

func (s *Scraper) fetchSitemap(ctx context.Context) (catalogue, error) {
	body, err := s.fetchPage(ctx, siteBase+"/sitemapxml/1.xml")
	if err != nil {
		return catalogue{}, err
	}

	var us urlset
	if err := xml.Unmarshal(body, &us); err != nil {
		return catalogue{}, fmt.Errorf("parsing sitemap: %w", err)
	}

	var c catalogue
	seen := make(map[string]bool)
	for _, u := range us.URLs {
		if m := videoURLRe.FindStringSubmatch(u.Loc); m != nil {
			if !seen[m[1]] {
				seen[m[1]] = true
				c.slugs = append(c.slugs, m[1])
			}
			continue
		}
		if m := performerURLRe.FindStringSubmatch(u.Loc); m != nil {
			c.performers = append(c.performers, m[1])
		}
	}
	// Longest first: some performer slugs are prefixes of others, and the
	// longest match is the correct one.
	sort.Slice(c.performers, func(i, j int) bool {
		return len(c.performers[i]) > len(c.performers[j])
	})
	return c, nil
}

// performerFor recovers the cast from the scene slug, which is built as
// "{performer-slug}-{category-slug}". The detail page marks up no cast.
func performerFor(slug string, performers []string) string {
	for _, p := range performers {
		if strings.HasPrefix(slug, p+"-") || slug == p {
			return titleCaseSlug(p)
		}
	}
	return ""
}

func titleCaseSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, detailWorkers)
	defer close(out)

	cat, err := s.fetchSitemap(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes and %d performers in sitemap", siteID, len(cat.slugs), len(cat.performers))

	select {
	case out <- scraper.Progress(len(cat.slugs)):
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
			for slug := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, ok := s.toScene(ctx, studioURL, slug, cat.performers, now)
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

	for _, slug := range cat.slugs {
		select {
		case work <- slug:
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
	h1Re = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	// The description is the paragraph straight after the heading.
	descRe = regexp.MustCompile(`(?s)</h1>\s*<p>(.*?)</p>`)
	// One paragraph holds date, runtime and a photo count.
	metaRe     = regexp.MustCompile(`(?s)<p class="video-date">(.*?)</p>`)
	dateRe     = regexp.MustCompile(`([A-Z][a-z]+ \d{1,2}(?:st|nd|rd|th), \d{4})`)
	durationRe = regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?)`)
	tagsRe     = regexp.MustCompile(`(?s)<p class=['"]video-tags['"]>(.*?)</p>`)
	tagRe      = regexp.MustCompile(`<a href=['"][^'"]*tags[^'"]*['"]>([^<]+)</a>`)
	// The player poster is the highest-resolution frame the page exposes.
	thumbRe    = regexp.MustCompile(`(https://[a-z0-9]+\.cloudfront\.net/videos/[^"'\s]+\.jpg)`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL, slug string, performers []string, now time.Time) (models.Scene, bool) {
	sceneURL := siteBase + "/video/" + slug

	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return models.Scene{}, false
	}
	detail := string(body)

	m := h1Re.FindStringSubmatch(detail)
	if m == nil {
		return models.Scene{}, false
	}
	title := cleanText(m[1])
	if title == "" {
		return models.Scene{}, false
	}

	scene := models.Scene{
		// The site exposes no numeric id; the slug is the stable key.
		ID:        slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     title,
		URL:       sceneURL,
		Studio:    studioName,
		ScrapedAt: now,
	}

	if d := descRe.FindStringSubmatch(detail); d != nil {
		scene.Description = cleanText(tagStripRe.ReplaceAllString(d[1], " "))
	}
	if meta := metaRe.FindStringSubmatch(detail); meta != nil {
		block := meta[1]
		if d := dateRe.FindStringSubmatch(block); d != nil {
			if t, err := time.Parse(dateLayout, parseutil.StripOrdinalSuffix(d[1])); err == nil {
				scene.Date = t.UTC()
			}
		}
		// The same block ends with a photo count, so only a clock value counts.
		if du := durationRe.FindStringSubmatch(block); du != nil {
			scene.Duration = parseutil.ParseDurationColon(du[1])
		}
	}
	if tb := tagsRe.FindStringSubmatch(detail); tb != nil {
		seen := make(map[string]bool)
		for _, tm := range tagRe.FindAllStringSubmatch(tb[1], -1) {
			tag := cleanText(tm[1])
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			scene.Tags = append(scene.Tags, tag)
		}
	}
	if th := thumbRe.FindStringSubmatch(detail); th != nil {
		scene.Thumbnail = th[1]
	}
	if p := performerFor(slug, performers); p != "" {
		scene.Performers = []string{p}
	}

	return scene, true
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

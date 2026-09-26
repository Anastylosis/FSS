// Package tokyohot scrapes Tokyo-Hot (tokyo-hot.com), a Japanese JAV catalog.
// The /product/ listing is page-numbered (?page=N) and yields product detail
// links (/product/{code}/). Each server-rendered detail page carries a
// <dl class="info"> spec table (Model, Play, Tags, Theme, Label, Release Date,
// Duration, Product ID) plus the <title>/og:title and a CDN package image.
// Tokyo-Hot content is largely anonymous, so the Model field is frequently
// "Unknown" and is skipped when so.
package tokyohot

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

var siteBase = "https://www.tokyo-hot.com"

const detailWorkers = 4

type Scraper struct{ client *http.Client }

func New() *Scraper {
	// Tokyo-Hot sets a sessionid cookie via redirect on first hit; a jar
	// carries it across hops so paginated listing requests don't loop.
	c := httpx.NewClient(30 * time.Second)
	if jar, err := cookiejar.New(nil); err == nil {
		c.Jar = jar
	}
	return &Scraper{client: c}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "tokyohot" }

func (s *Scraper) Patterns() []string {
	return []string{
		"tokyo-hot.com/product/",
		"tokyo-hot.com/product/{code}/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?tokyo-hot\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	productLinkRe = regexp.MustCompile(`href="/product/([A-Za-z0-9]+)/"`)
	titleRe       = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	ogTitleRe     = regexp.MustCompile(`<meta property="og:title" content="([^"]*)"`)
	posterRe      = regexp.MustCompile(`<video poster="(https://my\.cdn\.tokyo-hot\.com[^"]+)"`)
	listImageRe   = regexp.MustCompile(`https://my\.cdn\.tokyo-hot\.com/media/([A-Za-z0-9]+)/list_image/[^"]*820x462[^"]*\.jpg`)
	infoRe        = regexp.MustCompile(`(?s)<dl class="info".*?</dl>`)
	rowRe         = regexp.MustCompile(`(?s)<dt>(.*?)</dt>\s*<dd>(.*?)</dd>`)
	anchorRe      = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe    = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, "tokyohot", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/product/?page=%d", siteBase, page)
		codes, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		// Dedupe within and across pages; an empty page (no product links)
		// stops the loop.
		fresh := codes[:0]
		for _, code := range codes {
			if !seen[code] {
				seen[code] = true
				fresh = append(fresh, code)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]string, error) {
	body, err := s.get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	matches := productLinkRe.FindAllStringSubmatch(string(body), -1)
	codes := make([]string, 0, len(matches))
	for _, m := range matches {
		codes = append(codes, m[1])
	}
	scraper.Debugf(1, "tokyohot: listing page has %d product links", len(codes))
	return codes, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, codes []string, now time.Time, delay time.Duration) []models.Scene {
	scraper.Debugf(1, "tokyohot: fetching %d details with %d workers", len(codes), workers)
	scenes := make([]models.Scene, len(codes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, code := range codes {
		wg.Add(1)
		go func(i int, code string) {
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
			scenes[i] = s.toScene(ctx, studioURL, code, now)
		}(i, code)
	}
	wg.Wait()
	// Drop any scenes left zero-valued by a cancelled context or fetch error.
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Scraper) toScene(ctx context.Context, studioURL, code string, now time.Time) models.Scene {
	body, err := s.get(ctx, fmt.Sprintf("%s/product/%s/", siteBase, code))
	if err != nil {
		return models.Scene{}
	}
	detail := string(body)

	scene := models.Scene{
		ID:        code,
		SiteID:    "tokyohot",
		StudioURL: studioURL,
		URL:       fmt.Sprintf("%s/product/%s/", siteBase, code),
		Studio:    "Tokyo-Hot",
		ScrapedAt: now,
	}

	scene.Title = parseTitle(detail)
	if scene.Title == "" {
		// Pre-release/placeholder products carry no title; fall back to the code.
		scene.Title = code
	}

	scene.Thumbnail = parseThumbnail(detail, code)

	tagSeen := map[string]bool{}
	addTags := func(vals []string) {
		for _, v := range vals {
			if v != "" && !tagSeen[v] {
				tagSeen[v] = true
				scene.Tags = append(scene.Tags, v)
			}
		}
	}

	if info := infoRe.FindString(detail); info != "" {
		for _, row := range rowRe.FindAllStringSubmatch(info, -1) {
			label := strings.TrimSpace(stripTags(row[1]))
			ddHTML := row[2]
			switch label {
			case "Model", "Models":
				for _, p := range anchorTexts(ddHTML) {
					if !strings.EqualFold(p, "Unknown") {
						scene.Performers = append(scene.Performers, p)
					}
				}
			case "Play", "Tags", "Theme", "Themes", "Ethnicities":
				addTags(anchorTexts(ddHTML))
			case "Series":
				scene.Series = cleanText(ddHTML)
			case "Label":
				if scene.Series == "" {
					scene.Series = cleanText(ddHTML)
				}
			case "Resolution":
				scene.Resolution = cleanText(ddHTML)
			case "Release Date":
				if d, err := time.Parse("2006/01/02", cleanText(ddHTML)); err == nil {
					scene.Date = d.UTC()
				}
			case "Duration":
				scene.Duration = parseutil.ParseDurationColon(cleanText(ddHTML))
			}
		}
	}

	return scene
}

// parseTitle pulls the scene title from <title> (or og:title as fallback) and
// strips the trailing " | Tokyo-Hot …" site suffix.
func parseTitle(detail string) string {
	raw := ""
	if m := titleRe.FindStringSubmatch(detail); m != nil {
		raw = m[1]
	}
	if strings.TrimSpace(stripSuffix(raw)) == "" {
		if m := ogTitleRe.FindStringSubmatch(detail); m != nil {
			raw = m[1]
		}
	}
	return strings.TrimSpace(html.UnescapeString(stripSuffix(raw)))
}

// parseThumbnail prefers the main <video> poster, falling back to the product's
// own list_image (filtered by code, since the page also embeds neighbouring
// products' thumbnails).
func parseThumbnail(detail, code string) string {
	if m := posterRe.FindStringSubmatch(detail); m != nil {
		return m[1]
	}
	for _, m := range listImageRe.FindAllStringSubmatch(detail, -1) {
		if strings.EqualFold(m[1], code) {
			return m[0]
		}
	}
	return ""
}

func stripSuffix(s string) string {
	if i := strings.Index(s, " | Tokyo-Hot"); i >= 0 {
		return s[:i]
	}
	return s
}

// anchorTexts returns the cleaned text of each <a> in the fragment; if there
// are no anchors it returns the fragment's plain text (single value).
func anchorTexts(frag string) []string {
	matches := anchorRe.FindAllStringSubmatch(frag, -1)
	if len(matches) == 0 {
		if t := cleanText(frag); t != "" {
			return []string{t}
		}
		return nil
	}
	var out []string
	for _, m := range matches {
		if t := cleanText(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func stripTags(s string) string { return tagStripRe.ReplaceAllString(s, "") }

func cleanText(s string) string {
	s = stripTags(s)
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

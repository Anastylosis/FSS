// Package xxxjobinterviews scrapes xxxjobinterviews.com, a MechBunny tour
// (`mjedge.net` CDN, `/templates/{site}/` assets).
//
// Listing at `/videos/` and `/videos/page/{N}`, six cards a page:
//
//	<div class="thumbnail-card">
//	  <a href="https://xxxjobinterviews.com/video/brandi-swan-…-427.html">
//	    <img src="//c7467abd8e.mjedge.net/thumbs/…-7.jpg" alt="Brandi Swan returns…"></a>
//	  <div class="description"><a href="…">Brandi Swan returns…</a></div>
//	  <span> 1:16:03 </span> … <span> August 22nd, 2026 </span>
//	</div>
//
// The card gives the title, runtime, date and thumbnail; the detail page adds
// the cast (`/pornstars/{slug}-{id}.html` links), the trailer mp4 and — only
// in its meta tags — the description and keyword list. **The date carries an
// English ordinal** ("August 22nd, 2026"), which `parseutil.StripOrdinalSuffix`
// removes before parsing.
package xxxjobinterviews

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
	siteID     = "xxxjobinterviews"
	studioName = "XXX Job Interviews"
	defaultURL = "https://xxxjobinterviews.com"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"xxxjobinterviews.com", "xxxjobinterviews.com/videos/"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?xxxjobinterviews\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	seen := make(map[string]bool)
	lastPage := 0
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/videos/"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/videos/page/%d", s.base, page)
		}
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		parsed := parseListing(body)
		if len(parsed) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			lastPage = maxPage(body)
			total = lastPage * len(parsed)
		}

		fresh := parsed[:0]
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			fresh = append(fresh, item)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := s.enrichPage(ctx, fresh, studioURL, opts, out, now)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   lastPage > 0 && page >= lastPage,
		}, nil
	})
}

// enrichPage fetches one page's detail pages concurrently, so scenes stream
// out per page rather than after the whole walk.
func (s *Scraper) enrichPage(ctx context.Context, items []listItem, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	errs := make([]error, len(items))

	for i := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			body, err := s.fetchPage(ctx, items[idx].url)
			if err != nil {
				errs[idx] = err
				return
			}
			enrichFromDetail(body, &items[idx])
		}(i)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(items))
	for i, item := range items {
		if errs[i] != nil {
			// The card already names the scene, its runtime and its date; a
			// detail page that will not load costs the cast, tags and
			// description.
			select {
			case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.url, errs[i])):
			case <-ctx.Done():
				return scenes
			}
		}
		scenes = append(scenes, toScene(item, studioURL, now))
	}
	return scenes
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- parsing ----

type listItem struct {
	id          string
	url         string
	title       string
	thumbnail   string
	preview     string
	duration    int
	date        string
	description string
	performers  []string
	tags        []string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="thumbnail-card">`)
	cardLinkRe  = regexp.MustCompile(`href="([^"]*/video/[a-z0-9-]+-(\d+)\.html)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<div class="description"><a[^>]*>(.*?)</a>`)
	cardThumbRe = regexp.MustCompile(`<img src="([^"]+)"`)
	durationRe  = regexp.MustCompile(`<span>\s*(\d{1,2}:\d{2}(?::\d{2})?)\s*</span>`)
	// The date carries an English ordinal: "August 22nd, 2026".
	dateRe    = regexp.MustCompile(`<span>\s*([A-Z][a-z]+ \d{1,2}(?:st|nd|rd|th), \d{4})\s*</span>`)
	pageNumRe = regexp.MustCompile(`href="page/(\d+)"`)

	detailPerfRe = regexp.MustCompile(`href="[^"]*/pornstars/([a-z0-9-]+)-\d+\.html"`)
	detailPrevRe = regexp.MustCompile(`<source src="([^"]+\.mp4)"`)
	metaDescRe   = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	metaKeysRe   = regexp.MustCompile(`<meta name="keywords" content="([^"]*)"`)
	tagStripRe   = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		m := cardLinkRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item := listItem{url: html.UnescapeString(string(m[1])), id: string(m[2])}
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if t := cardThumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = absURL(html.UnescapeString(string(t[1])))
		}
		if t := durationRe.FindSubmatch(block); t != nil {
			item.duration = parseutil.ParseDurationColon(string(t[1]))
		}
		if t := dateRe.FindSubmatch(block); t != nil {
			item.date = cleanText(string(t[1]))
		}
		if item.title == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

func enrichFromDetail(body []byte, item *listItem) {
	// The description and keyword list live only in the meta tags; the body
	// repeats the title where a description would go.
	if m := metaDescRe.FindSubmatch(body); m != nil {
		item.description = cleanText(string(m[1]))
	}
	if m := detailPrevRe.FindSubmatch(body); m != nil {
		item.preview = absURL(html.UnescapeString(string(m[1])))
	}
	seen := make(map[string]bool)
	for _, m := range detailPerfRe.FindAllSubmatch(body, -1) {
		name := titleCaseSlug(string(m[1]))
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		item.performers = append(item.performers, name)
	}
	if m := metaKeysRe.FindSubmatch(body); m != nil {
		item.tags = splitKeywords(string(m[1]), item.performers)
	}
}

// splitKeywords turns the meta keyword list into tags, dropping the cast names
// the site repeats there.
func splitKeywords(raw string, performers []string) []string {
	isPerformer := make(map[string]bool, len(performers))
	for _, p := range performers {
		isPerformer[strings.ToLower(p)] = true
	}
	var tags []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		t := cleanText(part)
		key := strings.ToLower(t)
		if t == "" || seen[key] || isPerformer[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, t)
	}
	return tags
}

// titleCaseSlug turns a performer slug into a display name. The site's model
// slugs sometimes carry a parenthetical the URL flattens
// ("brandi-swan-previously-confetti"), which is kept as written.
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

func maxPage(body []byte) int {
	last := 0
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > last {
			last = n
		}
	}
	return last
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// absURL fixes the protocol-relative CDN URLs the tour writes.
func absURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

func toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         item.url,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Preview:     item.preview,
		Performers:  item.performers,
		Tags:        item.tags,
		Studio:      studioName,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(parseutil.StripOrdinalSuffix(item.date), "January 2, 2006"); err == nil {
		sc.Date = d
	}
	return sc
}

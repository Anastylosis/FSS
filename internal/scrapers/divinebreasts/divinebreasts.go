// Package divinebreasts scrapes divinebreasts.com.
//
// The tour lives under `/tour1/` and paginates at
// `/tour1/categories/movies_{N}_d.html`, 56 cards a page over ~51 pages.
// Everything is on the card:
//
//	<div class="videoBlock">
//	  <div class="videoPic hover">
//	    <a href="…/tour1/trailers/Kitty-Doggy-Style-Jigglers-Outside.html" title="…">
//	      <h5>Kitty Doggy Style Jigglers Outside</h5>
//	      <div class="videoPreview"><img id="set-target-6033" src0_4x="/tour1/content//contentthumbs/55/69/115569-4x.jpg" …/></div></a></div>
//	  <p>Featuring: <a href="…/tour1/models/Kitty.html">Kitty</a>
//	     <i class="fa fa-calendar"></i> 26 August, 2026</p>
//	</div>
//
// There is no detail fetch: `/tour1/trailers/{slug}.html` repeats the card's
// title and cast behind a join overlay and adds no description or runtime.
//
// **The site gates every page behind a cookie**: the first request answers 302
// to the same URL with a `region_allowed` cookie, so the client carries a jar
// and the second hop of the redirect succeeds. Without one every page is a
// redirect loop and the scrape returns nothing.
package divinebreasts

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "divinebreasts"
	studioName = "Divine Breasts"
	defaultURL = "https://divinebreasts.com"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	c := httpx.NewClient(30 * time.Second)
	// The region gate sets a cookie and redirects to the same URL; without a
	// jar the redirect repeats until the client gives up.
	if jar, err := cookiejar.New(nil); err == nil {
		c.Jar = jar
	}
	return &Scraper{client: c, base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"divinebreasts.com",
		"divinebreasts.com/tour1/categories/{category}.html",
		"divinebreasts.com/tour1/models/{Name}.html",
	}
}

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?divinebreasts\.com(?:/|$)`)
	// The tour appends `_{page}_{sort}` to a listing slug; the slug identifies
	// the view.
	categoryRe = regexp.MustCompile(`/categories/([^/?#]+?)(?:_\d+_[a-z])?\.html`)
	modelRe    = regexp.MustCompile(`/models/([^/?#]+?)(?:_\d+_[a-z])?\.html`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

type urlKind int

const (
	kindAll urlKind = iota
	kindCategory
	kindModel
)

// classifyURL decides which listing a studio URL selects. "movies" is the
// tour's own name for the whole catalogue.
func classifyURL(u string) (urlKind, string) {
	if m := modelRe.FindStringSubmatch(u); m != nil && !strings.EqualFold(m[1], "models") {
		return kindModel, m[1]
	}
	if m := categoryRe.FindStringSubmatch(u); m != nil && !strings.EqualFold(m[1], "movies") {
		return kindCategory, m[1]
	}
	return kindAll, ""
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	kind, slug := classifyURL(studioURL)
	switch kind {
	case kindModel:
		scraper.Debugf(1, "%s: scraping model %q", siteID, slug)
	case kindCategory:
		scraper.Debugf(1, "%s: scraping category %q", siteID, slug)
	default:
		scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	}

	seen := make(map[string]bool)
	lastPage := 0
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(kind, slug, page)
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

		scenes := make([]models.Scene, 0, len(parsed))
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			scenes = append(scenes, s.toScene(item, studioURL, now))
		}
		// A page whose cards were all seen already is the listing clamping —
		// the tour re-serves its last page past the end — so it ends the walk.
		// Cards do not otherwise repeat wholesale between pages.
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(scenes) == 0 || (lastPage > 0 && page >= lastPage),
		}, nil
	})
}

func (s *Scraper) listingURL(kind urlKind, slug string, page int) string {
	switch kind {
	case kindModel:
		return fmt.Sprintf("%s/tour1/models/%s_%d_d.html", s.base, slug, page)
	case kindCategory:
		return fmt.Sprintf("%s/tour1/categories/%s_%d_d.html", s.base, slug, page)
	default:
		return fmt.Sprintf("%s/tour1/categories/movies_%d_d.html", s.base, page)
	}
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
	id         string
	title      string
	url        string
	thumbnail  string
	performers []string
	date       string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="videoBlock">`)
	cardURLRe   = regexp.MustCompile(`<a\s+href="([^"]*/trailers/[^"]+\.html)"`)
	cardTitleRe = regexp.MustCompile(`(?s)<h5>(.*?)</h5>`)
	setIDRe     = regexp.MustCompile(`id="set-target-(\d+)"`)
	// The card offers the same still at four widths; 4x first, then down.
	thumbRes = []*regexp.Regexp{
		regexp.MustCompile(`src0_4x="([^"]+)"`),
		regexp.MustCompile(`src0_3x="([^"]+)"`),
		regexp.MustCompile(`src0_2x="([^"]+)"`),
		regexp.MustCompile(`src0_1x="([^"]+)"`),
	}
	featuringRe = regexp.MustCompile(`(?s)Featuring:(.*?)(?:<br|<i class="fa fa-calendar")`)
	dateRe      = regexp.MustCompile(`fa-calendar"></i>\s*(\d{1,2} [A-Z][a-z]+, \d{4})`)
	pagerNumRe  = regexp.MustCompile(`/(?:categories|models)/[^"/]*_(\d+)_[a-z]\.html`)
	anchorRe    = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
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

		var item listItem
		if m := setIDRe.FindSubmatch(block); m != nil {
			item.id = string(m[1])
		}
		if m := cardURLRe.FindSubmatch(block); m != nil {
			item.url = html.UnescapeString(string(m[1]))
		}
		if m := cardTitleRe.FindSubmatch(block); m != nil {
			item.title = cleanText(string(m[1]))
		}
		if item.id == "" || item.title == "" || item.url == "" {
			continue
		}
		for _, re := range thumbRes {
			if m := re.FindSubmatch(block); m != nil {
				item.thumbnail = html.UnescapeString(string(m[1]))
				break
			}
		}
		if m := featuringRe.FindSubmatch(block); m != nil {
			item.performers = anchorNames(m[1])
		}
		if m := dateRe.FindSubmatch(block); m != nil {
			item.date = cleanText(string(m[1]))
		}
		items = append(items, item)
	}
	return items
}

// maxPage reads the highest page number any listing link on the page names.
func maxPage(body []byte) int {
	last := 0
	for _, m := range pagerNumRe.FindAllSubmatch(body, -1) {
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

func anchorNames(block []byte) []string {
	var names []string
	seen := make(map[string]bool)
	for _, m := range anchorRe.FindAllSubmatch(block, -1) {
		n := cleanText(tagStripRe.ReplaceAllString(string(m[1]), " "))
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		names = append(names, n)
	}
	return names
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         item.id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      item.title,
		URL:        absURL(s.base, item.url),
		Thumbnail:  absURL(s.base, item.thumbnail),
		Performers: item.performers,
		Studio:     studioName,
		ScrapedAt:  now,
	}
	if d, err := parseutil.TryParseDate(item.date, "2 January, 2006"); err == nil {
		sc.Date = d
	}
	return sc
}

func absURL(base, u string) string {
	switch {
	case u == "", strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "/"):
		return base + u
	default:
		return base + "/tour1/" + u
	}
}

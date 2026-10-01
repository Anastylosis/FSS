// Package littlemutt scrapes Little Mutt (littlemutt.com), a 2000s-era
// hand-rolled PHP tour built from nested tables.
//
// Content lives on the plain-HTTP tour host: the apex is a warning splash and
// www.littlemutt.com/guests4_1.php is a CCBill join page. Detail pages exist
// but are a generic Flash-era join splash with no scene-specific content, so
// they are never fetched — everything comes from the listing.
//
// Pagination is by row offset in steps of 7, not by page number:
// /videos/, /videos/7/, /videos/14/ … through /videos/798/ (~800 videos).
//
// Two quirks:
//
//   - Dates come in two shapes. Newer scenes read "Released Dec 30th 2023";
//     the oldest few hundred are a bulk-import placeholder reading
//     "Released Jan 2020", i.e. month precision only.
//   - Scene ids are not contiguous, so the offsets are walked rather than the
//     ids enumerated.
//
// Duration, performers and tags are not exposed anywhere public. The performer
// is only inferable from the title, which this package does not attempt.
package littlemutt

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "littlemutt"
	studioName = "Little Mutt"
	// perPage is the listing's row-offset step.
	perPage = 7
)

// siteBase is plain HTTP: the tour host does not serve HTTPS.
var siteBase = "http://tour.littlemutt.com"

// Scraper implements scraper.StudioScraper for Little Mutt.
type Scraper struct {
	Client *http.Client
}

// New constructs a Little Mutt scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"littlemutt.com",
		"tour.littlemutt.com/videos/",
		"tour.littlemutt.com/videos/{offset}/",
		"tour.littlemutt.com/video/{id}/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.|tour\.)?littlemutt\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		// The listing keys off a row offset, not a page number.
		body, err := s.fetchPage(ctx, listingURL((page-1)*perPage))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			if seen[it.id] {
				continue
			}
			seen[it.id] = true
			scenes = append(scenes, it.toScene(studioURL, now))
		}
		if len(scenes) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func listingURL(offset int) string {
	if offset <= 0 {
		return siteBase + "/videos/"
	}
	return fmt.Sprintf("%s/videos/%d/", siteBase, offset)
}

// ---- listing ----

var (
	// The title anchor is the only reliable per-scene marker in this
	// table-soup markup, so cards are delimited by it.
	cardRe = regexp.MustCompile(`<a href="/video/(\d+)/([^"]+)" class="videoNameText">([^<]*)</a>`)
	// "Released Dec 30th 2023" or the bulk-import "Released Jan 2020".
	dateRe  = regexp.MustCompile(`Released\s+([A-Z][a-z]{2}\s+\d{1,2}(?:st|nd|rd|th)\s+\d{4}|[A-Z][a-z]{2}\s+\d{4})`)
	thumbRe = regexp.MustCompile(`<img src="(/WebVideos/[^"]+)"`)
	descRe  = regexp.MustCompile(`class="videoDescShort">([^<]*)`)
)

type listItem struct {
	id, slug, title, description, thumb string
	date                                time.Time
}

func parseListing(body []byte) []listItem {
	page := string(body)
	locs := cardRe.FindAllStringSubmatchIndex(page, -1)
	items := make([]listItem, 0, len(locs))

	for i, loc := range locs {
		// The date and thumbnail sit in the table cell *before* the title
		// anchor, so each card spans from the previous match to this one's end.
		start := 0
		if i > 0 {
			start = locs[i-1][1]
		}
		end := len(page)
		if i+1 < len(locs) {
			end = locs[i+1][1]
		}
		card := page[start:end]

		it := listItem{
			id:    page[loc[2]:loc[3]],
			slug:  page[loc[4]:loc[5]],
			title: cleanText(page[loc[6]:loc[7]]),
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			it.date = parseDate(d[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = siteBase + th[1]
		}
		if de := descRe.FindStringSubmatch(card); de != nil {
			it.description = cleanText(de[1])
		}

		items = append(items, it)
	}
	return items
}

// parseDate handles both shapes the tour emits: "Dec 30th 2023" (ordinal day)
// and the month-only bulk-import placeholder "Jan 2020", which yields the
// first of that month.
func parseDate(s string) time.Time {
	cleaned := parseutil.StripOrdinalSuffix(strings.TrimSpace(s))
	if t, err := parseutil.TryParseDate(cleaned, "Jan 2 2006", "Jan 2006"); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func (it listItem) toScene(studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       fmt.Sprintf("%s/video/%s/%s", siteBase, it.id, it.slug),
		Date:      it.date,
		Thumbnail: it.thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}
	// The short description is very often just the title repeated; only keep
	// it when it adds something.
	if it.description != "" && it.description != it.title {
		scene.Description = it.description
	}
	return scene
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

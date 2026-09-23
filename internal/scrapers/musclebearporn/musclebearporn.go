// Package musclebearporn scrapes musclebearporn.com, an Elevated X tour on a
// custom skin whose scene metadata is rendered inside HTML comments.
// See docs/scrapers.md.
package musclebearporn

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
	siteID     = "musclebearporn"
	studioName = "Muscle Bear Porn"
	// detailWorkers caps the per-page detail pool; the listing serves 12 cards
	// a page over ~25 pages.
	detailWorkers = 4
)

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?musclebearporn\.com(?:/|$)`)
	// categoryRe reads the slug out of a category listing URL, with or without
	// the page suffix the pager appends.
	categoryRe = regexp.MustCompile(`/tour/categories/([a-zA-Z0-9-]+?)(?:_(\d+)_d)?\.html`)

	// Cards are sliced between their opening markers; the grid shares its
	// closing tags.
	cardSplitRe = regexp.MustCompile(`data-setid="(\d+)"`)
	cardLinkRe  = regexp.MustCompile(`<a title="([^"]*)" href="[^"]*(/tour/updates/[^"?#]+\.html)"`)
	cardImgRe   = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)

	// The theme hides the scene's metadata block in HTML comments rather than
	// dropping it, so these spans are read out of the comment.
	titleRe  = regexp.MustCompile(`(?s)<span class="update_title">(.*?)</span>`)
	dateRe   = regexp.MustCompile(`<span class="update_date">\s*(\d{2}/\d{2}/\d{4})`)
	descRe   = regexp.MustCompile(`(?s)<span class="latest_update_description">(.*?)</span>`)
	modelsRe = regexp.MustCompile(`(?s)<span class="tour_update_models">(.*?)</span>`)
	tagsRe   = regexp.MustCompile(`(?s)<span class="tour_update_tags">(.*?)</span>`)

	posterRe     = regexp.MustCompile(`<video[^>]+poster="([^"]+)"`)
	trailerRe    = regexp.MustCompile(`<source src="([^"]+\.mp4)"`)
	anchorTextRe = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

// dateLayout matches the tour's MM/DD/YYYY.
const dateLayout = "01/02/2006"

// Scraper implements scraper.StudioScraper for musclebearporn.com.
type Scraper struct {
	client *http.Client
	base   string
}

// New builds the Muscle Bear Porn scraper.
func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second), base: "https://www.musclebearporn.com"}
}

func init() { scraper.Register(New()) }

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"musclebearporn.com", "musclebearporn.com/tour/categories/{slug}.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// card is one listing entry. The card carries no metadata beyond the title, so
// every scene needs its detail page.
type card struct {
	id    string
	path  string
	title string
	thumb string
}

// categorySlug picks the category an operator URL selects. The tour has no
// per-model pages, so "movies" — the whole catalogue — is the only other mode.
func categorySlug(studioURL string) string {
	if m := categoryRe.FindStringSubmatch(studioURL); m != nil && m[1] != "" {
		return m[1]
	}
	return "movies"
}

// listingURL builds a category page. Page 1 has no suffix.
func (s *Scraper) listingURL(slug string, page int) string {
	if page <= 1 {
		return fmt.Sprintf("%s/tour/categories/%s.html", s.base, slug)
	}
	return fmt.Sprintf("%s/tour/categories/%s_%d_d.html", s.base, slug, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	slug := categorySlug(studioURL)
	scraper.Debugf(1, "%s: scraping category %q", siteID, slug)

	now := time.Now().UTC()
	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.listingURL(slug, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no scene cards on the first page"))
		}

		fresh := cards[:0]
		for _, c := range cards {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			fresh = append(fresh, c)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		enrichTo := len(fresh)
		for i, c := range fresh {
			if opts.KnownIDs[c.id] {
				enrichTo = i
				break
			}
		}
		scenes := make([]models.Scene, len(fresh))
		for i, c := range fresh {
			scenes[i] = s.baseScene(c, studioURL, now)
		}
		s.enrich(ctx, scenes[:enrichTo], opts, out)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

// parseListing reads the listing's scene cards.
func parseListing(body string) []card {
	marks := cardSplitRe.FindAllStringSubmatchIndex(body, -1)
	cards := make([]card, 0, len(marks))
	for i, m := range marks {
		end := len(body)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		block := body[m[1]:end]

		link := cardLinkRe.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		c := card{id: body[m[2]:m[3]], path: link[2], title: cleanText(link[1])}
		if im := cardImgRe.FindStringSubmatch(block); im != nil {
			c.thumb = html.UnescapeString(im[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (s *Scraper) baseScene(c card, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Studio:    studioName,
		Title:     c.title,
		URL:       s.base + c.path,
		Thumbnail: c.thumb,
		ScrapedAt: now,
	}
}

// enrich fetches each scene's detail page, which is where everything but the
// title lives.
func (s *Scraper) enrich(ctx context.Context, scenes []models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if len(scenes) == 0 {
		return
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(scenes), detailWorkers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, detailWorkers)
	for i := range scenes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body, s.base)
		}(i)
	}
	wg.Wait()
}

// applyDetail folds the detail page into a scene built from its listing card.
func applyDetail(sc *models.Scene, body, base string) {
	if t := titleRe.FindStringSubmatch(body); t != nil {
		if title := cleanText(t[1]); title != "" {
			sc.Title = title
		}
	}
	if d := descRe.FindStringSubmatch(body); d != nil {
		sc.Description = cleanText(d[1])
	}
	if m := modelsRe.FindStringSubmatch(body); m != nil {
		sc.Performers = anchorTexts(m[1])
	}
	if tg := tagsRe.FindStringSubmatch(body); tg != nil {
		sc.Tags = anchorTexts(tg[1])
	}
	if d := dateRe.FindStringSubmatch(body); d != nil {
		if parsed, err := parseutil.TryParseDate(d[1], dateLayout); err == nil {
			sc.Date = parsed
		}
	}
	if p := posterRe.FindStringSubmatch(body); p != nil {
		sc.Thumbnail = resolveURL(base, html.UnescapeString(p[1]))
	}
	if tr := trailerRe.FindStringSubmatch(body); tr != nil {
		sc.Preview = resolveURL(base, html.UnescapeString(tr[1]))
	}
}

func anchorTexts(span string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range anchorTextRe.FindAllStringSubmatch(span, -1) {
		t := cleanText(m[1])
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func resolveURL(base, ref string) string {
	switch {
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return ref
	case strings.HasPrefix(ref, "/"):
		return base + ref
	default:
		return base + "/" + ref
	}
}

func (s *Scraper) fetch(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "<!--", " ")
	s = strings.ReplaceAll(s, "-->", " ")
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

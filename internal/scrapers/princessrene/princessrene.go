// Package princessrene scrapes Princess Rene (worshiprene.com), a solo-creator
// site on a custom WordPress theme (Yoast sitemaps). The /videos/ listing (and
// its /videos/page/{N}/ pages) carries cards linking to /videos/{slug}/ detail
// pages; each detail page exposes full OpenGraph tags (og:title, og:description,
// og:image) plus a Yoast JSON-LD datePublished. Every scene is solo Princess
// Rene, so Studio and Performers are set implicitly.
package princessrene

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
	siteID     = "princessrene"
	studioName = "Princess Rene"
	performer  = "Princess Rene"
	siteBase   = "https://worshiprene.com"
	perPage    = 10
)

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?worshiprene\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// drive the whole walk instead of only the parsers.
	base string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"worshiprene.com",
		"worshiprene.com/videos/",
		"worshiprene.com/videos/page/{N}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/videos/page/%d/", s.base, page)
		if page == 1 {
			pageURL = s.base + "/videos/"
		}
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		slugs := parseListing(body)
		if len(slugs) == 0 {
			return scraper.PageResult{}, nil
		}

		scenes := make([]models.Scene, 0, len(slugs))
		for i, slug := range slugs {
			// Listing is date-sorted (newest first): a known slug means we have
			// reached already-scraped content. Emit a minimal scene so Paginate
			// triggers its KnownIDs early-stop, and skip the detail fetch.
			if opts.KnownIDs[slug] {
				scenes = append(scenes, models.Scene{ID: slug})
				break
			}
			if i > 0 && opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return scraper.PageResult{}, ctx.Err()
				}
			}
			scene, err := s.fetchDetail(ctx, slug, studioURL)
			if err != nil {
				return scraper.PageResult{}, err
			}
			scenes = append(scenes, scene)
		}

		return scraper.PageResult{
			Scenes: scenes,
			Done:   len(slugs) < perPage,
		}, nil
	})
}

// sceneLinkRe matches detail-page links `/videos/{slug}/` while excluding the
// `/videos/category/...` and `/videos/page/...` listing variants (those slugs
// contain no nested path beyond a single segment ending in a trailing slash).
var sceneLinkRe = regexp.MustCompile(`href="https?://(?:www\.)?worshiprene\.com/videos/([a-z0-9-]+)/?"`)

// parseListing extracts ordered, deduped scene slugs from a listing page.
func parseListing(body []byte) []string {
	page := string(body)
	var slugs []string
	seen := map[string]bool{}
	for _, m := range sceneLinkRe.FindAllStringSubmatch(page, -1) {
		slug := m[1]
		switch slug {
		case "category", "page", "":
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs
}

type detailData struct {
	title       string
	description string
	thumbnail   string
	date        time.Time
}

var datePublishedRe = regexp.MustCompile(`"datePublished":"([^"]+)"`)

// parseDetail extracts the scene metadata from a detail page's OpenGraph tags
// and Yoast JSON-LD datePublished field.
func parseDetail(body []byte) detailData {
	var d detailData
	og := parseutil.OpenGraph(body)

	title := strings.TrimSpace(html.UnescapeString(og["og:title"]))
	// og:title is "<Title> - Princess Rene"; drop the trailing site name.
	title = strings.TrimSuffix(title, " - "+studioName)
	d.title = strings.TrimSpace(title)

	d.description = strings.TrimSpace(html.UnescapeString(og["og:description"]))
	d.thumbnail = strings.TrimSpace(html.UnescapeString(og["og:image"]))

	if m := datePublishedRe.FindSubmatch(body); m != nil {
		raw := strings.TrimSpace(string(m[1]))
		// Yoast emits RFC3339, occasionally without the timezone colon.
		if t, err := parseutil.TryParseDate(raw, time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02"); err == nil {
			d.date = t.UTC()
		}
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, slug, studioURL string) (models.Scene, error) {
	url := fmt.Sprintf("%s/videos/%s/", s.base, slug)
	scraper.Debugf(1, "princessrene: fetching detail %s", slug)
	body, err := s.fetchPage(ctx, url)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", slug, err)
	}
	d := parseDetail(body)

	title := d.title
	if title == "" {
		title = slugToTitle(slug)
	}

	return models.Scene{
		ID:          slug,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       title,
		URL:         url,
		Date:        d.date,
		Description: d.description,
		Thumbnail:   d.thumbnail,
		Performers:  []string{performer},
		Studio:      studioName,
		ScrapedAt:   time.Now().UTC(),
	}, nil
}

func slugToTitle(slug string) string {
	return strings.TrimSpace(strings.ReplaceAll(slug, "-", " "))
}

func (s *Scraper) fetchPage(ctx context.Context, url string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

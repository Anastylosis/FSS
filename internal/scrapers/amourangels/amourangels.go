// Package amourangels scrapes Amour Angels (amourangels.com), an old-school
// static-HTML glamour/teen site served as ISO-8859-1. The /videos2.html listing
// (and its /videos2_{N}.html follow-on pages) enumerate per-set cover links of
// the form /z_cover_{id}.html. Each detail page carries the set title, the
// "Added YYYY-MM-DD" publish date, a "MM:SS min. VIDEO" runtime, the big cover
// thumbnail and a "BY <photographer>" credit. Listing pages are walked with the
// Paginate helper; each page's detail pages are fetched by a small worker pool.
package amourangels

import (
	"context"
	"errors"
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
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

const detailWorkers = 4

// siteBase is a var (not const) so tests can point it at a local httptest server.
var siteBase = "http://amourangels.com"

type Scraper struct {
	Client *http.Client
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "amourangels" }

func (s *Scraper) Patterns() []string {
	return []string{"amourangels.com", "amourangels.com/videos2.html"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?amourangels\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	coverIDRe      = regexp.MustCompile(`z_cover_(\d+)\.html`)
	dateRe         = regexp.MustCompile(`Added\s+(\d{4}-\d{2}-\d{2})`)
	durationRe     = regexp.MustCompile(`(\d{1,2}:\d{2})\s*min`)
	titleBRe       = regexp.MustCompile(`<b>([^<]+)</b>`)
	coverImgRe     = regexp.MustCompile(`<img\s+src="(/cm_cvl/[^"]+)"`)
	photographerRe = regexp.MustCompile(`<A href="/photographer_\d+\.html"[^>]*>([^<]+)</A>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)

	scraper.Paginate(ctx, opts, "amourangels", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		ids, err := s.fetchListing(ctx, page)
		if err != nil {
			// A 404 past the last listing page is the normal end of pagination.
			var se *httpx.StatusError
			if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		// Pages near the end overlap — drop already-seen IDs and stop when a
		// page contributes nothing new.
		fresh := ids[:0]
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				fresh = append(fresh, id)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		scenes := s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)
		return scraper.PageResult{Scenes: scenes}, nil
	})
}

func (s *Scraper) listingURL(page int) string {
	if page <= 1 {
		return siteBase + "/videos2.html"
	}
	return fmt.Sprintf("%s/videos2_%d.html", siteBase, page)
}

func (s *Scraper) fetchListing(ctx context.Context, page int) ([]string, error) {
	body, err := s.get(ctx, s.listingURL(page))
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}
	for _, m := range coverIDRe.FindAllStringSubmatch(string(body), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids, nil
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, ids []string, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, delay) {
				return
			}
			scenes[i] = s.toScene(ctx, studioURL, id, now)
		}(i, id)
	}
	wg.Wait()
	out := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Scraper) toScene(ctx context.Context, studioURL, id string, now time.Time) models.Scene {
	pageURL := fmt.Sprintf("%s/z_cover_%s.html", siteBase, id)
	scene := models.Scene{
		ID:        id,
		SiteID:    "amourangels",
		StudioURL: studioURL,
		URL:       pageURL,
		Studio:    "Amour Angels",
		ScrapedAt: now,
	}

	body, err := s.get(ctx, pageURL)
	if err != nil {
		return models.Scene{} // dropped by enrich
	}
	page := string(body)

	// Title: the last pure-text <b>...</b> before the "Added" date (the prev/next
	// nav <b> tags wrap an inner <u>, so [^<]+ never matches them).
	if loc := dateRe.FindStringIndex(page); loc != nil {
		if ms := titleBRe.FindAllStringSubmatch(page[:loc[0]], -1); len(ms) > 0 {
			scene.Title = html.UnescapeString(strings.TrimSpace(ms[len(ms)-1][1]))
		}
	}
	if scene.Title == "" {
		// Fallback so the scene still validates if the layout shifts.
		scene.Title = "Amour Angels " + id
	}

	if m := dateRe.FindStringSubmatch(page); m != nil {
		if d, err := parseutil.TryParseDate(m[1], "2006-01-02"); err == nil {
			scene.Date = d
		}
	}
	if m := durationRe.FindStringSubmatch(page); m != nil {
		scene.Duration = parseutil.ParseDurationColon(m[1])
	}
	if m := coverImgRe.FindStringSubmatch(page); m != nil {
		scene.Thumbnail = siteBase + html.UnescapeString(strings.TrimSpace(m[1]))
	}
	if m := photographerRe.FindStringSubmatch(page); m != nil {
		credit := html.UnescapeString(strings.TrimSpace(m[1]))
		credit = strings.TrimSpace(strings.TrimPrefix(credit, "BY"))
		credit = strings.TrimSpace(strings.TrimPrefix(credit, "by"))
		scene.Director = credit
	}
	return scene
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	return decodeLatin1(raw), nil
}

// decodeLatin1 converts an ISO-8859-1 byte slice to UTF-8; on a decode error it
// returns the original bytes unchanged.
func decodeLatin1(b []byte) []byte {
	out, _, err := transform.Bytes(charmap.ISO8859_1.NewDecoder(), b)
	if err != nil {
		return b
	}
	return out
}

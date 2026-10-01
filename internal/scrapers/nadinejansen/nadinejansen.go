// Package nadinejansen scrapes Nadine Jansen (nadine-j.de), a hand-rolled PHP
// site.
//
// Every field — date, performers, title, description and a real runtime — is on
// the listing card, so detail pages are never fetched. That matters here: the
// detail pages sit behind HTTP Basic auth and return 401, which would make a
// detail-fetching scraper useless.
//
// Two listings exist and both are walked:
//
//   - /models/videos/{N} — the model catalogue (12/page, ~29 pages), whose
//     cards carry the full field set; and
//   - /nadine/videos/{N} — Nadine's own videos, on a thinner card that has only
//     a date and a title.
//
// Descriptions are wrapped in editor junk (Mozilla `moz-text-html` blocks,
// Google-Translate `HwtZe`/`ryNqvb` spans), so markup is stripped rather than
// matched.
package nadinejansen

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
	siteID     = "nadinejansen"
	studioName = "Nadine Jansen"
)

var siteBase = "https://nadine-j.de"

// listings are the two catalogues, walked in order.
var listings = []string{"/models/videos", "/nadine/videos"}

// Scraper implements scraper.StudioScraper for Nadine Jansen.
type Scraper struct {
	Client *http.Client
}

// New constructs a Nadine Jansen scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"nadine-j.de/models/videos",
		"nadine-j.de",
		"nadine-j.de/models/videos/{N}",
		"nadine-j.de/nadine/videos/{N}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?nadine-j\.de(?:/|$)`)

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
	// listingIdx walks the two catalogues in sequence; Paginate's page counter
	// is reset against each one by tracking an offset.
	listingIdx := 0
	pageOffset := 0

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		for listingIdx < len(listings) {
			localPage := page - pageOffset
			body, err := s.fetchPage(ctx, listingURL(listings[listingIdx], localPage))
			if err != nil {
				return scraper.PageResult{}, err
			}

			items := parseListing(body)
			fresh := make([]models.Scene, 0, len(items))
			for _, it := range items {
				if seen[it.id] {
					continue
				}
				seen[it.id] = true
				fresh = append(fresh, it.toScene(studioURL, now))
			}
			if len(fresh) > 0 {
				return scraper.PageResult{Scenes: fresh}, nil
			}

			// This catalogue is exhausted; continue with the next one from the
			// following page number.
			listingIdx++
			pageOffset = page
			scraper.Debugf(1, "%s: listing %d exhausted at page %d", siteID, listingIdx-1, localPage)
		}
		return scraper.PageResult{Done: true}, nil
	})
}

func listingURL(path string, page int) string {
	if page <= 1 {
		return siteBase + path
	}
	return fmt.Sprintf("%s%s/%d", siteBase, path, page)
}

// ---- listing ----

var (
	// The two catalogues use different card markup, so both are recognised.
	cardRe = regexp.MustCompile(`<div class="video-teaser"|<a href="/member/video/\d+" class="item"`)
	idRe   = regexp.MustCompile(`/member/video/(\d+)`)
	dateRe = regexp.MustCompile(`<(?:div|span) class="(?:text-right text-medium|date)">\s*([A-Z][a-z]{2} \d{1,2}, \d{4})\s*<`)
	// The model catalogue puts performers in the first h2 and the title in the
	// second; Nadine's own catalogue has only an h3 title.
	performerRe = regexp.MustCompile(`<h2 class="text-dark">([^<]*)</h2>`)
	titleRe     = regexp.MustCompile(`<h2 class="mt0 text-sans-serif text-light">([^<]*)</h2>`)
	altTitleRe  = regexp.MustCompile(`<h3 class="theme">([^<]*)</h3>`)
	descRe      = regexp.MustCompile(`(?s)<p class="text-medium mt20">(.*?)<a class="btn`)
	durationRe  = regexp.MustCompile(`See the (\d{1,2}:\d{2}(?::\d{2})?) min`)
	thumbRe     = regexp.MustCompile(`src="(/open/videos/previews/\d+/[^"]+)"`)
	tagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

type listItem struct {
	id, title, description, thumb string
	date                          time.Time
	duration                      int
	performers                    []string
}

func parseListing(body []byte) []listItem {
	page := string(body)
	starts := cardRe.FindAllStringIndex(page, -1)
	items := make([]listItem, 0, len(starts))

	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		card := page[loc[0]:end]

		m := idRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{id: m[1]}

		if d := dateRe.FindStringSubmatch(card); d != nil {
			if ts, err := time.Parse("Jan 2, 2006", strings.TrimSpace(d[1])); err == nil {
				it.date = ts.UTC()
			}
		}
		if t := titleRe.FindStringSubmatch(card); t != nil {
			it.title = cleanText(t[1])
		} else if t := altTitleRe.FindStringSubmatch(card); t != nil {
			it.title = cleanText(t[1])
		}
		if p := performerRe.FindStringSubmatch(card); p != nil {
			// Co-starring scenes read "Sophia & Roxanne Miller".
			for _, name := range strings.Split(cleanText(p[1]), "&") {
				if name = strings.TrimSpace(name); name != "" {
					it.performers = append(it.performers, name)
				}
			}
		}
		if d := descRe.FindStringSubmatch(card); d != nil {
			it.description = cleanText(tagStripRe.ReplaceAllString(d[1], " "))
		}
		if du := durationRe.FindStringSubmatch(card); du != nil {
			it.duration = parseutil.ParseDurationColon(du[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = siteBase + th[1]
		}

		items = append(items, it)
	}
	return items
}

func (it listItem) toScene(studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		// Detail pages are HTTP Basic protected and 401, but the URL is still
		// the scene's canonical anchor.
		URL:         fmt.Sprintf("%s/member/video/%s", siteBase, it.id),
		Date:        it.date,
		Description: it.description,
		Thumbnail:   it.thumb,
		Duration:    it.duration,
		Performers:  it.performers,
		Studio:      studioName,
		ScrapedAt:   now,
	}
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

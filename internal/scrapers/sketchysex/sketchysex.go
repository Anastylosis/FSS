// Package sketchysex scrapes Sketchy Sex (sketchysex.com), a hand-rolled PHP
// tour.
//
// The site's `mjedge.net` CDN host is a red herring — it serves thumbnails
// only. There is no `__NEXT_DATA__`, no `nats_site_id` and no JSON API, so this
// is not the Next.js/NATS platform the `nextcontents` package covers.
//
// The listing gives id, title, date and thumbnail; the description and tags
// come from a detail worker pool.
//
// Two things the site does not supply:
//
//   - Duration appears nowhere public.
//   - Performers are effectively absent. The detail page renders a
//     `<ul class="ModelNames">` container but leaves it empty (the site's
//     premise is anonymous), so cast is best-effort and usually nil.
//
// The detail page's own `<div class="date">` is empty; the listing card is the
// only structured date. The description is additionally prefixed with a prose
// date ("July 8th, 2026 - "), which is stripped.
package sketchysex

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "sketchysex"
	studioName    = "Sketchy Sex"
	detailWorkers = 4
	dateLayout    = "Jan 02, 2006"
)

var siteBase = "https://sketchysex.com"

// Scraper implements scraper.StudioScraper for Sketchy Sex.
type Scraper struct {
	Client *http.Client
}

// New constructs a Sketchy Sex scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"sketchysex.com",
		"sketchysex.com/index.php?page={N}",
		"sketchysex.com/trailer.php?id={id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?sketchysex\.com(?:/|$)`)

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
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/index.php?page=%d", siteBase, page))
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}, nil
	})
}

// ---- listing ----

var (
	cardRe  = regexp.MustCompile(`<div class="video-item">`)
	idRe    = regexp.MustCompile(`trailer\.php\?id=(\d+)`)
	titleRe = regexp.MustCompile(`<span class="video-title">([^<]*)</span>`)
	dateRe  = regexp.MustCompile(`<span class="video-date">\s*([A-Z][a-z]{2} \d{2}, \d{4})\s*</span>`)
	thumbRe = regexp.MustCompile(`<img src="(https://[^"]+)"`)
)

type listItem struct {
	id, title, thumb string
	date             time.Time
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

		if t := titleRe.FindStringSubmatch(card); t != nil {
			it.title = cleanText(t[1])
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if ts, err := time.Parse(dateLayout, d[1]); err == nil {
				it.date = ts.UTC()
			}
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = th[1]
		}

		items = append(items, it)
	}
	return items
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
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
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

var (
	descRe = regexp.MustCompile(`(?s)<div class="VideoDescription">(.*?)</div>`)
	// Descriptions open with a prose date, e.g. "July 8th, 2026 - ".
	descDatePrefixRe = regexp.MustCompile(`^[A-Z][a-z]+ \d{1,2}(?:st|nd|rd|th), \d{4}\s*-\s*`)
	tagRe            = regexp.MustCompile(`<span class="tag-text">([^<]+)</span>`)
	modelRe          = regexp.MustCompile(`(?s)<ul class="ModelNames">(.*?)</ul>`)
	modelNameRe      = regexp.MustCompile(`>([^<>]{2,60})</a>`)
	tagStripRe       = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	sceneURL := fmt.Sprintf("%s/trailer.php?id=%s", siteBase, it.id)

	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       sceneURL,
		Date:      it.date,
		Thumbnail: it.thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}

	body, err := s.fetchPage(ctx, sceneURL)
	if err != nil {
		return scene
	}
	applyDetail(&scene, string(body))
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := descRe.FindStringSubmatch(detail); m != nil {
		text := cleanText(tagStripRe.ReplaceAllString(m[1], " "))
		scene.Description = strings.TrimSpace(descDatePrefixRe.ReplaceAllString(text, ""))
	}

	seen := make(map[string]bool)
	for _, m := range tagRe.FindAllStringSubmatch(detail, -1) {
		tag := cleanText(m[1])
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		scene.Tags = append(scene.Tags, tag)
	}

	// The cast container is usually rendered empty — the site's premise is
	// anonymous — so this is best-effort.
	if mb := modelRe.FindStringSubmatch(detail); mb != nil {
		for _, pm := range modelNameRe.FindAllStringSubmatch(mb[1], -1) {
			if name := cleanText(pm[1]); name != "" {
				scene.Performers = append(scene.Performers, name)
			}
		}
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

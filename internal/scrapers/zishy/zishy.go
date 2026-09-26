// Package zishy scrapes Zishy (zishy.com), a Ruby on Rails site.
//
// Zishy publishes photo albums rather than video scenes, so what a "scene"
// means here is an album: it has a title, a date, a description and a model,
// but **no duration** — the site has no video section at all. Six preview
// images per album are public; the rest of the gallery is behind `/login`.
// Metadata is public either way.
//
// Enumeration walks `/albums?page=N`, 16 albums per page over ~172 pages. There
// is no sitemap (`/sitemap.xml` 404s) and no JSON API.
//
// **The site cannot be reached with a default Go client**: its TLS stack offers
// only DHE and static-RSA key exchange, neither of which Go's default cipher
// list will negotiate, so requests go through httpx.NewLegacyTLSClient.
//
// Two things the markup forces:
//
//   - Album links are relative **without a leading slash** (`albums/2718-...`),
//     so URLs are rebuilt against the site base.
//   - The detail page's "added on" date and its model tag are both split
//     across newlines and carry inline styling, so neither can be matched with
//     a tight single-line pattern. The date is taken from the listing card,
//     which carries it in a simpler form.
//
// The model is exposed only as a numeric `tag_id`, with its display name in the
// link text; there is no tag index page mapping names to ids.
package zishy

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
	siteID        = "zishy"
	studioName    = "Zishy"
	detailWorkers = 4
	dateLayout    = "Jan 2, 2006"
)

var siteBase = "https://www.zishy.com"

// Scraper implements scraper.StudioScraper for Zishy.
type Scraper struct {
	Client *http.Client
}

// New constructs a Zishy scraper.
func New() *Scraper {
	// The site's TLS stack offers only DHE (which crypto/tls does not
	// implement) and static-RSA key exchange (which Go no longer offers by
	// default), so a default client cannot complete the handshake at all.
	return &Scraper{Client: httpx.NewLegacyTLSClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"zishy.com",
		"zishy.com/albums?page={N}",
		"zishy.com/albums/{id}-{slug}",
		"zishy.com/albums?tag_id={id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?zishy\.com(?:/|$)`)

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
		body, err := s.fetchPage(ctx, fmt.Sprintf("%s/albums?page=%d", siteBase, page))
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
	cardRe = regexp.MustCompile(`<div class='albumcover'`)
	// Album links are relative without a leading slash.
	linkRe  = regexp.MustCompile(`<a href="albums/(\d+)-([^"]*)"`)
	thumbRe = regexp.MustCompile(`<img[^>]*src="([^"]+)"`)
	titleRe = regexp.MustCompile(`<strong>([^<]+)</strong>`)
	// "added Jun 20, 2026" on the card; the detail page splits the same value
	// across newlines.
	dateRe = regexp.MustCompile(`added\s+([A-Z][a-z]{2}\s+\d{1,2},\s+\d{4})`)
)

type listItem struct {
	id, url, title, thumb string
	date                  time.Time
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

		m := linkRe.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		it := listItem{
			id:  m[1],
			url: siteBase + "/albums/" + m[1] + "-" + m[2],
		}
		// The first <strong> is the title; the second is the photo count.
		if tm := titleRe.FindStringSubmatch(card); tm != nil {
			it.title = cleanText(tm[1])
		}
		if th := thumbRe.FindStringSubmatch(card); th != nil {
			it.thumb = normalizeURL(th[1])
		}
		if d := dateRe.FindStringSubmatch(card); d != nil {
			if t, err := time.Parse(dateLayout, normalizeSpace(d[1])); err == nil {
				it.date = t.UTC()
			}
		}

		items = append(items, it)
	}
	return items
}

func normalizeURL(u string) string {
	switch {
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return siteBase + u
	case strings.HasPrefix(u, "http"):
		return u
	default:
		return siteBase + "/" + u
	}
}

// normalizeSpace collapses the whitespace time.Parse will not tolerate; the
// site writes dates across newlines in places.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
	descRe = regexp.MustCompile(`(?s)<div id="descrip"[^>]*>(.*?)</div>`)
	// The tag link carries inline styling between the href and the text, so the
	// attribute list is skipped rather than matched exactly.
	tagRe = regexp.MustCompile(`<a href="/albums\?tag_id=\d+"[^>]*>#([^<]+)</a>`)
	// The detail page's own date is split across newlines.
	detailDateRe = regexp.MustCompile(`(?s)added on\s+([A-Z][a-z]{2}\s+\d{1,2},\s+\d{4})`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       it.url,
		Date:      it.date,
		Thumbnail: it.thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}

	// Only the description and model need the detail page; a failure there
	// still leaves a usable album.
	if body, err := s.fetchPage(ctx, it.url); err == nil {
		applyDetail(&scene, string(body))
	}
	return scene
}

func applyDetail(scene *models.Scene, detail string) {
	if m := descRe.FindStringSubmatch(detail); m != nil {
		scene.Description = cleanText(tagStripRe.ReplaceAllString(m[1], " "))
	}
	if scene.Date.IsZero() {
		if m := detailDateRe.FindStringSubmatch(detail); m != nil {
			if t, err := time.Parse(dateLayout, normalizeSpace(m[1])); err == nil {
				scene.Date = t.UTC()
			}
		}
	}
	// The model is exposed only as a tag; its display name is the link text.
	seen := make(map[string]bool)
	for _, m := range tagRe.FindAllStringSubmatch(detail, -1) {
		name := cleanText(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		scene.Performers = append(scene.Performers, name)
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

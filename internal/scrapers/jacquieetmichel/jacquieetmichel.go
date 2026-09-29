// Package jacquieetmichel scrapes public scene metadata from
// jacquieetmicheltv.net. The browsable French listing
// (/fr/content/list) paginates newest-first with ?page=N and links to
// /fr/content/{hexid}/{slug} detail pages. Each detail page carries a
// schema.org VideoObject in an @graph JSON-LD block, which is the primary
// source for title, description, performers, tags, duration, and date.
package jacquieetmichel

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func init() { scraper.Register(New()) }

const (
	studioName = "Jacquie & Michel TV"
	siteID     = "jacquieetmichel"
)

// Scraper implements scraper.StudioScraper for jacquieetmicheltv.net.
type Scraper struct {
	client *http.Client
	base   string // origin, e.g. https://www.jacquieetmicheltv.net
}

// New returns a Scraper configured for the live site.
func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www.jacquieetmicheltv.net",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"jacquieetmicheltv.net",
		"jacquieetmicheltv.net/fr/content/list",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?jacquieetmicheltv\.net(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// contentHrefRe matches the root-relative detail links on listing pages:
// /fr/content/{24-hex-id}/{slug}. The id is the scene ID.
var contentHrefRe = regexp.MustCompile(`href="(/fr/content/([a-f0-9]{24})/[^"]+)"`)

// pageNumRe pulls page numbers from the ?page=N pagination links so the
// total scene count can be estimated for progress display.
var pageNumRe = regexp.MustCompile(`[?&]page=(\d+)`)

func (s *Scraper) run(ctx context.Context, _ string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "jacquieetmichel: scraping content listing")
	now := time.Now().UTC()
	firstPage := true

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/fr/content/list"
		if page > 1 {
			pageURL = fmt.Sprintf("%s?page=%d", pageURL, page)
		}

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		items := parseListing(body)
		if len(items) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if firstPage {
			firstPage = false
			total = maxPageNum(body) * len(items)
		}

		scenes := s.fetchDetails(ctx, items, opts, now)
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

// listItem is a detail link parsed from a listing page.
type listItem struct {
	id  string
	url string // root-relative /fr/content/{id}/{slug}
}

// parseListing returns the detail links on a listing page in document order,
// deduped by scene ID (listings can repeat a card in sidebars).
func parseListing(body []byte) []listItem {
	seen := make(map[string]bool)
	var items []listItem
	for _, m := range contentHrefRe.FindAllSubmatch(body, -1) {
		id := string(m[2])
		if seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, listItem{id: id, url: string(m[1])})
	}
	return items
}

func maxPageNum(body []byte) int {
	maxPage := 1
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

// fetchDetails enriches each listing item with its detail-page VideoObject
// using a worker pool. Order is preserved so Paginate's KnownIDs early-stop
// works: known IDs become lightweight stubs (no detail fetch) and detail
// failures are dropped.
func (s *Scraper) fetchDetails(ctx context.Context, items []listItem, opts scraper.ListOpts, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "jacquieetmichel: fetching %d details with %d workers", len(items), workers)

	results := make([]models.Scene, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, it := range items {
		if ctx.Err() != nil {
			break
		}
		if opts.KnownIDs[it.id] {
			results[i] = models.Scene{ID: it.id, SiteID: siteID}
			continue
		}
		wg.Add(1)
		go func(idx int, item listItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if !scraper.Pace(ctx, opts.Delay) {
				// Leaving results[idx] unwritten is deliberate: the zero value has an
				// empty ID and the consumer below skips those, so a cancelled fetch
				// contributes no scene. Do not "fix" this by writing a stub here —
				// a populated slot is emitted as a real scene with no title or URL.
				return
			}

			body, err := s.fetchPage(ctx, s.base+item.url)
			if err != nil {
				scraper.Debugf(1, "jacquieetmichel: detail %s failed: %v (skipping)", item.id, err)
				return
			}
			if sc, ok := s.toScene(body, item, now); ok {
				results[idx] = sc
			}
		}(i, it)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(results))
	for _, sc := range results {
		if sc.ID == "" { // failed fetch (zero value); known stubs keep their ID
			continue
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

// toScene builds a Scene from a detail page. The VideoObject lives in an
// @graph wrapper, which parseutil.ExtractVideoObject does not unwrap, so the
// JSON-LD is parsed here directly.
func (s *Scraper) toScene(body []byte, it listItem, now time.Time) (models.Scene, bool) {
	vo := extractGraphVideoObject(body)
	if vo == nil || strings.TrimSpace(vo.Name) == "" {
		return models.Scene{}, false
	}

	scene := models.Scene{
		ID:          it.id,
		SiteID:      siteID,
		StudioURL:   s.base,
		Studio:      studioName,
		Title:       cleanText(vo.Name),
		URL:         s.base + it.url,
		Description: cleanText(vo.Description),
		Thumbnail:   html.UnescapeString(vo.ThumbnailURL),
		Preview:     html.UnescapeString(vo.ContentURL),
		Performers:  cleanAll(vo.actors()),
		Tags:        cleanAll(vo.tags()),
		Duration:    parseutil.ParseDurationISO(vo.Duration),
		ScrapedAt:   now,
	}

	date := strings.TrimSpace(vo.DatePublished)
	if date == "" {
		date = strings.TrimSpace(vo.UploadDate)
	}
	if t, err := parseutil.TryParseDate(date,
		"2006-01-02T15:04:05.000Z07:00",
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02",
	); err == nil {
		scene.Date = t.UTC()
	}

	return scene, true
}

// graphVideoObject mirrors the JSON-LD VideoObject fields, accepting the
// array-valued "keywords" and object-valued "actor" shapes the site emits.
type graphVideoObject struct {
	Type          string          `json:"@type"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	ThumbnailURL  string          `json:"thumbnailUrl"`
	ContentURL    string          `json:"contentUrl"`
	Duration      string          `json:"duration"`
	UploadDate    string          `json:"uploadDate"`
	DatePublished string          `json:"datePublished"`
	Keywords      []string        `json:"keywords"`
	Actor         json.RawMessage `json:"actor"`
}

func (v *graphVideoObject) tags() []string { return v.Keywords }

// actors parses the "actor" field, which is an array of {name, ...} objects.
func (v *graphVideoObject) actors() []string {
	if len(v.Actor) == 0 {
		return nil
	}
	var arr []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(v.Actor, &arr) != nil {
		return nil
	}
	names := make([]string, 0, len(arr))
	for _, a := range arr {
		if n := strings.TrimSpace(a.Name); n != "" {
			names = append(names, n)
		}
	}
	return names
}

var jsonLDBlockRe = regexp.MustCompile(`(?s)<script[^>]+type="application/ld\+json"[^>]*>(.*?)</script>`)

// extractGraphVideoObject finds the first VideoObject inside an @graph
// JSON-LD block. Returns nil if none is found.
func extractGraphVideoObject(body []byte) *graphVideoObject {
	for _, m := range jsonLDBlockRe.FindAllSubmatch(body, -1) {
		var doc struct {
			Graph []graphVideoObject `json:"@graph"`
		}
		if json.Unmarshal(m[1], &doc) != nil {
			continue
		}
		for i := range doc.Graph {
			if doc.Graph[i].Type == "VideoObject" {
				return &doc.Graph[i]
			}
		}
	}
	return nil
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

func cleanText(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}

func cleanAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if c := cleanText(s); c != "" {
			out = append(out, c)
		}
	}
	return out
}

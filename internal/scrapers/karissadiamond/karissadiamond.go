// Package karissadiamond scrapes Karissa Diamond (karissa-diamond.com), the
// standalone site for the MPL Studios model of the same name.
//
// The video collection is rendered by an infinite scroll backed by
// /workFiles/loadMore.php, which answers with a three-element JSON array:
// [items, nextOffset, isMember]. Each item carries the id, title, release date
// and detail link, so the listing alone builds a near-complete scene; only the
// duration lives on the detail page, which a small worker pool fetches.
//
// Items come back newest-first, so the KnownIDs early-stop applies. The
// endpoint takes an offset rather than a page number and reports the next
// offset in the response, so the offset is threaded through the pagination
// callback rather than derived from the page number.
package karissadiamond

import (
	"context"
	"encoding/json"
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
	siteID        = "karissadiamond"
	studioName    = "Karissa Diamond"
	performerName = "Karissa Diamond"
	detailWorkers = 4
	dateLayout    = "January 2, 2006"
)

var siteBase = "https://karissa-diamond.com"

// Scraper implements scraper.StudioScraper for Karissa Diamond.
type Scraper struct {
	Client *http.Client
}

// New constructs a Karissa Diamond scraper.
func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"karissa-diamond.com/videoCollection/",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?karissa-diamond\.com(?:/|$)`)

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
	offset := 0
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, _ int) (scraper.PageResult, error) {
		items, next, err := s.fetchBatch(ctx, offset)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(items) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		// The server reports where the next batch starts. If it ever fails to
		// advance, stop rather than re-requesting the same offset forever.
		if next <= offset {
			return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, items, now, opts.Delay), Done: true}, nil
		}
		offset = next
		return scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, items, now, opts.Delay)}, nil
	})
}

// ---- listing ----

type item struct {
	ID    json.Number `json:"id"`
	Title string      `json:"title"`
	Date  string      `json:"relDate"`
	Link  string      `json:"link"`
}

func (s *Scraper) fetchBatch(ctx context.Context, offset int) ([]item, int, error) {
	url := fmt.Sprintf("%s/workFiles/loadMore.php?a=%d&b=videoCollection&c=relDate", siteBase, offset)

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     url,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	// The endpoint answers with a heterogeneous array: [items, nextOffset,
	// isMember]. Decode into json.RawMessage slots and unpack the two we need.
	var raw []json.RawMessage
	if err := httpx.DecodeJSON(resp.Body, &raw); err != nil {
		return nil, 0, fmt.Errorf("decoding loadMore response: %w", err)
	}
	if len(raw) < 2 {
		return nil, 0, fmt.Errorf("loadMore response has %d elements, want at least 2", len(raw))
	}

	var items []item
	if err := json.Unmarshal(raw[0], &items); err != nil {
		return nil, 0, fmt.Errorf("decoding loadMore items: %w", err)
	}
	var next int
	if err := json.Unmarshal(raw[1], &next); err != nil {
		return nil, 0, fmt.Errorf("decoding loadMore next offset: %w", err)
	}

	scraper.Debugf(1, "%s: offset %d -> %d videos (next %d)", siteID, offset, len(items), next)
	return items, next, nil
}

// ---- detail enrichment ----

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []item, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it item) {
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

var durationRe = regexp.MustCompile(`(?s)id="videoDuration"[^>]*>.*?(\d{1,2}:\d{2}(?::\d{2})?)`)

func (s *Scraper) toScene(ctx context.Context, studioURL string, it item, now time.Time) models.Scene {
	id := it.ID.String()
	if id == "" {
		return models.Scene{}
	}

	scene := models.Scene{
		ID:         id,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      html.UnescapeString(strings.TrimSpace(it.Title)),
		URL:        siteBase + it.Link,
		Thumbnail:  fmt.Sprintf("%s/media/video/%s/cover_720.jpg", siteBase, id),
		Studio:     studioName,
		Performers: []string{performerName},
		ScrapedAt:  now,
	}
	if d, err := time.Parse(dateLayout, strings.TrimSpace(it.Date)); err == nil {
		scene.Date = d.UTC()
	}

	// The listing carries everything but duration; a detail failure is not
	// fatal, the scene is still worth emitting.
	if body, err := s.get(ctx, scene.URL); err == nil {
		if m := durationRe.FindSubmatch(body); m != nil {
			scene.Duration = parseutil.ParseDurationColon(string(m[1]))
		}
	}
	return scene
}

// ---- HTTP ----

func (s *Scraper) get(ctx context.Context, rawURL string) ([]byte, error) {
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

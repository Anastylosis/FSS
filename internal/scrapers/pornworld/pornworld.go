package pornworld

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

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://pornworld.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "pornworld" }

func (s *Scraper) Patterns() []string {
	return []string{
		"pornworld.com",
		"pornworld.com/watch/{id}/{slug}",
		"pornworld.com/model/{id}/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?(?:` +
	`pornworld\.com|` +
	`ddfnetwork\.com|` +
	`1by-day\.com|` +
	`ddfbusty\.com|` +
	`eurogirlsongirls\.com|` +
	`handsonhardcore\.com|` +
	`hotlegsandfeet\.com|` +
	`houseoftaboo\.com|` +
	`onlyblowjob\.com|` +
	`bustyworld\.com)(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type listEntry struct {
	id         string
	title      string
	url        string
	thumbnail  string
	date       time.Time
	duration   string
	performers []string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	work := make(chan listEntry, opts.Workers)
	var wg sync.WaitGroup

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range work {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				scene, err := s.fetchDetail(ctx, entry, studioURL)
				if err != nil {
					select {
					case out <- scraper.Error(err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	s.runPaginated(ctx, opts, work, out)

	close(work)
	wg.Wait()
}

func (s *Scraper) runPaginated(ctx context.Context, opts scraper.ListOpts, work chan<- listEntry, out chan<- scraper.SceneResult) {
	sentTotal := false
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			break
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		scraper.Debugf(1, "pornworld: fetching page %d", page)

		pageURL := fmt.Sprintf("%s/videos?page=%d", s.base, page)
		entries, err := s.fetchListing(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			break
		}

		if len(entries) == 0 {
			break
		}

		if !sentTotal {
			sentTotal = true
			select {
			case out <- scraper.Progress(len(entries) * 600):
			case <-ctx.Done():
				return
			}
		}

		hitKnown := false
		for _, e := range entries {
			if opts.KnownIDs[e.id] {
				hitKnown = true
				break
			}
			select {
			case work <- e:
			case <-ctx.Done():
				return
			}
		}
		if hitKnown {
			scraper.Debugf(1, "pornworld: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			break
		}
	}
}

var (
	cardRe      = regexp.MustCompile(`(?s)<article class="card scene">.*?</article>`)
	cardHrefRe  = regexp.MustCompile(`href="(?:https?:)?//pornworld\.com(/watch/(\d+-\d+)/[^"]+)"`)
	cardTitleRe = regexp.MustCompile(`<p class="card-title[^"]*">\s*<a[^>]*title="([^"]*)"`)
	cardDateRe  = regexp.MustCompile(`<div class="release-date">([^<]+)</div>`)
	cardDurRe   = regexp.MustCompile(`(?s)<span class="video-duration">.*?</i>\s*([^<]+)</span>`)
	cardPerfRe  = regexp.MustCompile(`(?s)<div class="starring[^"]*">\s*<span>(.*?)</span>`)
	cardPerfLRe = regexp.MustCompile(`title="([^"]+)"`)
	cardImgRe   = regexp.MustCompile(`<img class="card-img[^"]*"[^>]*data-src="([^"]+)"`)
)

func (s *Scraper) fetchListing(ctx context.Context, pageURL string) ([]listEntry, error) {
	body, err := s.fetchHTML(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	return parseListing(body), nil
}

// parseCardDate reads the listing's release date. The site renders the full
// month name ("2026 September, 15"); the short form is kept as a fallback, and
// the unpadded day verb accepts both spellings. See docs/scrapers.md.
func parseCardDate(raw string) time.Time {
	t, err := parseutil.TryParseDate(strings.TrimSpace(raw), "2006 January, 2", "2006 Jan, 2")
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func parseListing(body []byte) []listEntry {
	cards := cardRe.FindAll(body, -1)
	entries := make([]listEntry, 0, len(cards))

	for _, card := range cards {
		hm := cardHrefRe.FindSubmatch(card)
		if hm == nil {
			continue
		}

		e := listEntry{
			url: string(hm[1]),
			id:  string(hm[2]),
		}

		if m := cardTitleRe.FindSubmatch(card); m != nil {
			e.title = html.UnescapeString(string(m[1]))
		}

		if m := cardDateRe.FindSubmatch(card); m != nil {
			e.date = parseCardDate(string(m[1]))
		}

		if m := cardDurRe.FindSubmatch(card); m != nil {
			e.duration = strings.TrimSpace(string(m[1]))
		}

		if m := cardPerfRe.FindSubmatch(card); m != nil {
			for _, pm := range cardPerfLRe.FindAllSubmatch(m[1], -1) {
				e.performers = append(e.performers, html.UnescapeString(string(pm[1])))
			}
		}

		if m := cardImgRe.FindSubmatch(card); m != nil {
			e.thumbnail = html.UnescapeString(string(m[1]))
		}

		entries = append(entries, e)
	}
	return entries
}

var detailTag = regexp.MustCompile(`<a href="/videos\?tags=[^"]*" class="link-secondary">([^<]+)</a>`)

func (s *Scraper) fetchDetail(ctx context.Context, entry listEntry, studioURL string) (models.Scene, error) {
	detailURL := s.base + entry.url

	body, err := s.fetchHTML(ctx, detailURL)
	if err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", entry.id, err)
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:         entry.id,
		SiteID:     "pornworld",
		StudioURL:  studioURL,
		Title:      entry.title,
		URL:        detailURL,
		Thumbnail:  entry.thumbnail,
		Duration:   parseutil.ParseDurationColon(entry.duration),
		Performers: entry.performers,
		ScrapedAt:  now,
	}

	if !entry.date.IsZero() {
		scene.Date = entry.date
	}

	if vo := parseutil.ExtractVideoObject(body); vo != nil {
		if vo.Description != "" {
			scene.Description = html.UnescapeString(vo.Description)
		}
	}

	for _, m := range detailTag.FindAllSubmatch(body, -1) {
		scene.Tags = append(scene.Tags, html.UnescapeString(string(m[1])))
	}

	return scene, nil
}

func (s *Scraper) fetchHTML(ctx context.Context, rawURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

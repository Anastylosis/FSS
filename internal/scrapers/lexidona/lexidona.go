// Package lexidona scrapes lexidona.com.
//
// Listing at `/videos/` then `/videos/page-{N}/`:
//
//	<li class="col-md-3 …">
//	  <a href="/videos/video-doggiefuck/">
//	    <figure><img src="https://media.lexidona.com/videos/video-doggiefuck/cover/l.jpg"></figure>
//	    <figcaption>DoggieFuck</figcaption>
//	    <em>Home,Shaved,Vaginal<br />04:15</em>
//	  </a>
//	</li>
//
// The card's `<em>` packs the tag list and the runtime into one element,
// separated by a `<br>`. The detail page adds the publication date
// ("Released on: February 27 2019"), the description and a trailer mp4.
package lexidona

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
	siteID     = "lexidona"
	studioName = "Lexi Dona"
	defaultURL = "https://lexidona.com"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"lexidona.com", "lexidona.com/videos/"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?lexidona\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	seen := make(map[string]bool)
	lastPage := 0
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base + "/videos/"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/videos/page-%d/", s.base, page)
		}
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		parsed := parseListing(body)
		if len(parsed) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			lastPage = maxPage(body)
			total = lastPage * len(parsed)
		}

		fresh := parsed[:0]
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			fresh = append(fresh, item)
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		scenes := s.enrichPage(ctx, fresh, studioURL, opts, out, now)
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   lastPage > 0 && page >= lastPage,
		}, nil
	})
}

// enrichPage fetches one page's detail pages concurrently, so scenes stream out
// per page rather than after the whole walk.
func (s *Scraper) enrichPage(ctx context.Context, items []listItem, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time) []models.Scene {
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(items), workers)

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	errs := make([]error, len(items))

	for i := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			body, err := s.fetchPage(ctx, s.base+items[idx].path)
			if err != nil {
				errs[idx] = err
				return
			}
			enrichFromDetail(body, &items[idx])
		}(i)
	}
	wg.Wait()

	scenes := make([]models.Scene, 0, len(items))
	for i, item := range items {
		if errs[i] != nil {
			// The card already names the scene, its tags and its runtime; a
			// detail page that will not load costs the date, description and
			// trailer.
			select {
			case out <- scraper.Error(fmt.Errorf("detail %s: %w", item.path, errs[i])):
			case <-ctx.Done():
				return scenes
			}
		}
		scenes = append(scenes, s.toScene(item, studioURL, now))
	}
	return scenes
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// ---- parsing ----

type listItem struct {
	id          string
	path        string
	title       string
	thumbnail   string
	preview     string
	tags        []string
	duration    int
	date        string
	description string
}

var (
	cardRe      = regexp.MustCompile(`(?s)<a href="(/videos/(video-[a-z0-9-]+)/)">(.*?)</a>`)
	cardThumbRe = regexp.MustCompile(`<img src="([^"]+)"`)
	cardCapRe   = regexp.MustCompile(`(?s)<figcaption>(.*?)</figcaption>`)
	// The <em> packs the tag list and the runtime, split by a <br>.
	cardMetaRe = regexp.MustCompile(`(?s)<em>(.*?)</em>`)
	brRe       = regexp.MustCompile(`(?i)<br\s*/?>`)
	pageNumRe  = regexp.MustCompile(`/videos/page-(\d+)/`)
	durationRe = regexp.MustCompile(`^\d{1,2}:\d{2}(?::\d{2})?$`)

	detailDateRe = regexp.MustCompile(`(?s)Released on:\s*<strong><em>(.*?)</em></strong>`)
	detailDurRe  = regexp.MustCompile(`(?s)Duration:\s*<strong>(.*?)</strong>`)
	detailDescRe = regexp.MustCompile(`(?s)<div class="movie-description">(.*?)</div>`)
	detailPrevRe = regexp.MustCompile(`<source src="([^"]+\.mp4)"`)
	detailPostRe = regexp.MustCompile(`poster="([^"]+)"`)
	tagStripRe   = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	var items []listItem
	seen := make(map[string]bool)
	for _, m := range cardRe.FindAllSubmatch(body, -1) {
		inner := m[3]
		// The "next video" link on a detail page has the same shape but no
		// figcaption, so a card is recognised by having one.
		caption := cardCapRe.FindSubmatch(inner)
		if caption == nil {
			continue
		}
		item := listItem{
			id:    string(m[2]),
			path:  html.UnescapeString(string(m[1])),
			title: cleanText(string(caption[1])),
		}
		if item.title == "" || seen[item.id] {
			continue
		}
		seen[item.id] = true
		if t := cardThumbRe.FindSubmatch(inner); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := cardMetaRe.FindSubmatch(inner); t != nil {
			item.tags, item.duration = parseCardMeta(string(t[1]))
		}
		items = append(items, item)
	}
	return items
}

// parseCardMeta splits the card's combined "tags<br>runtime" element.
func parseCardMeta(raw string) (tags []string, duration int) {
	parts := brRe.Split(raw, -1)
	for _, part := range parts {
		text := cleanText(part)
		if text == "" {
			continue
		}
		if durationRe.MatchString(text) {
			duration = parseutil.ParseDurationColon(text)
			continue
		}
		for _, t := range strings.Split(text, ",") {
			if t = cleanText(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	return tags, duration
}

func enrichFromDetail(body []byte, item *listItem) {
	if m := detailDateRe.FindSubmatch(body); m != nil {
		item.date = cleanText(string(m[1]))
	}
	if m := detailDurRe.FindSubmatch(body); m != nil {
		if d := parseutil.ParseDurationColon(cleanText(string(m[1]))); d > 0 {
			item.duration = d
		}
	}
	if m := detailDescRe.FindSubmatch(body); m != nil {
		item.description = cleanText(string(m[1]))
	}
	if m := detailPrevRe.FindSubmatch(body); m != nil {
		item.preview = html.UnescapeString(string(m[1]))
	}
	// The detail's poster is the same still at a larger size than the card's.
	if m := detailPostRe.FindSubmatch(body); m != nil {
		item.thumbnail = html.UnescapeString(string(m[1]))
	}
}

func maxPage(body []byte) int {
	last := 0
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > last {
			last = n
		}
	}
	return last
}

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         s.base + item.path,
		Description: item.description,
		Thumbnail:   item.thumbnail,
		Preview:     item.preview,
		Tags:        item.tags,
		Studio:      studioName,
		Duration:    item.duration,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "January 2 2006", "January 2, 2006"); err == nil {
		sc.Date = d
	}
	return sc
}

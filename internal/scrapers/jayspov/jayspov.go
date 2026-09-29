// Package jayspov scrapes jayspov.net, a standalone Adult Empire / AEBN
// "Enhanced Web Component" hosted studio tour for the POV studio Jay's POV.
//
// The site is gated behind an age-confirmation redirect; sending an
// "ageConfirmed=1" cookie bypasses it. The public scene-update listing
// (/jays-pov-updates.html?page=N) exposes the scene id, title, performer,
// release date and thumbnail on each card. The per-scene detail page
// (/{id}/{slug}-streaming-scene-video.html) adds the runtime ("Length"),
// tags and series, which a worker pool fetches to enrich each scene.
package jayspov

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
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

const (
	siteID      = "jayspov"
	studioName  = "Jay's POV"
	defaultBase = "https://www.jayspov.net"
	listingPath = "/jays-pov-updates.html"
	ageCookie   = "ageConfirmed=1"
)

type Scraper struct {
	client  *http.Client
	baseURL string // overridable in tests
}

func New() *Scraper {
	return &Scraper{
		client:  httpx.NewClient(30 * time.Second),
		baseURL: defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"jayspov.net/jays-pov-updates.html",
		"jayspov.net/{id}/{slug}-streaming-scene-video.html",
		"jayspov.net/streaming-video-by-scene.html?series={id}",
		"jayspov.net/jays-pov-updates.html?cast={id}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?jayspov\.net(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// resolveBase returns the scheme+host of studioURL, falling back to the
// scraper's configured base (which the test server overrides).
func (s *Scraper) resolveBase(studioURL string) string {
	if u, err := url.Parse(studioURL); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return s.baseURL
}

// resolveListing returns the listing URL to walk. A bare domain root maps to
// the default updates listing; any other path (a cast/series/category filter)
// is used verbatim so filtered views work.
func (s *Scraper) resolveListing(studioURL, base string) string {
	if u, err := url.Parse(studioURL); err == nil {
		if strings.Trim(u.Path, "/") == "" {
			return base + listingPath
		}
		return studioURL
	}
	return base + listingPath
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	base := s.resolveBase(studioURL)
	listingURL := s.resolveListing(studioURL, base)
	scraper.Debugf(1, "%s: listing URL %s", siteID, listingURL)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	scraper.Debugf(1, "%s: %d detail workers", siteID, workers)

	work := make(chan listingScene)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ls := range work {
				scene, err := s.fetchDetail(ctx, ls, studioURL, opts.Delay)
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

	go func() {
		defer close(work)
		s.enqueuePages(ctx, base, listingURL, opts, out, work)
	}()

	wg.Wait()
}

func (s *Scraper) enqueuePages(ctx context.Context, base, listingURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, work chan<- listingScene) {
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}

		pageURL := listingURL
		if page > 1 {
			sep := "?"
			if strings.Contains(listingURL, "?") {
				sep = "&"
			}
			pageURL = fmt.Sprintf("%s%spage=%d", listingURL, sep, page)
		}
		scraper.Debugf(1, "%s: fetching page %d (%s)", siteID, page, pageURL)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		scenes := parseListingPage(body, base)
		if len(scenes) == 0 {
			scraper.Debugf(1, "%s: page %d empty, stopping", siteID, page)
			return
		}

		for _, ls := range scenes {
			if opts.KnownIDs[ls.ID] {
				scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, ls.ID)
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case work <- ls:
			case <-ctx.Done():
				return
			}
		}

		if !hasNextPage(body, page) {
			scraper.Debugf(1, "%s: no page beyond %d, stopping", siteID, page)
			return
		}
	}
}

type listingScene struct {
	ID        string
	URL       string
	Title     string
	Performer string
	Date      time.Time
	Thumb     string
}

var (
	cardRe      = regexp.MustCompile(`(?s)<div class="grid-item" id="ascene_(\d+)">(.*?)</article>`)
	cardLinkRe  = regexp.MustCompile(`href="(/\d+/[^"]*streaming-scene-video\.html)"`)
	cardTitleRe = regexp.MustCompile(`alt="Image of[^"]*"\s+title="([^"]+)"`)
	cardDateRe  = regexp.MustCompile(`(?s)<span class="date">\s*(.*?)\s*</span>`)
	cardPerfRe  = regexp.MustCompile(`(?s)<h5>\s*(.*?)\s*</h5>`)
	cardThumbRe = regexp.MustCompile(`data-src="(https://imgs1cdn[^"]+)"`)
	pageNumRe   = regexp.MustCompile(`[?&]page=(\d+)`)
)

func parseListingPage(body []byte, base string) []listingScene {
	matches := cardRe.FindAllSubmatch(body, -1)
	scenes := make([]listingScene, 0, len(matches))

	for _, m := range matches {
		id := string(m[1])
		block := m[2]

		ls := listingScene{ID: id}

		linkM := cardLinkRe.FindSubmatch(block)
		if linkM == nil {
			// Not a real scene card (e.g. a promo/banner tile).
			continue
		}
		href := string(linkM[1])
		if strings.HasPrefix(href, "/") {
			href = base + href
		}
		ls.URL = href

		if tm := cardTitleRe.FindSubmatch(block); tm != nil {
			ls.Title = strings.TrimSpace(html.UnescapeString(string(tm[1])))
		}
		if pm := cardPerfRe.FindSubmatch(block); pm != nil {
			ls.Performer = strings.TrimSpace(html.UnescapeString(string(pm[1])))
		}
		if dm := cardDateRe.FindSubmatch(block); dm != nil {
			if t, err := parseutil.TryParseDate(strings.TrimSpace(string(dm[1])), "Jan 2, 2006", "January 2, 2006"); err == nil {
				ls.Date = t.UTC()
			}
		}
		if tm := cardThumbRe.FindSubmatch(block); tm != nil {
			ls.Thumb = string(tm[1])
		}

		scenes = append(scenes, ls)
	}
	return scenes
}

// hasNextPage reports whether the page markup links to a page number greater
// than current (the pagination control lists page=N anchors).
func hasNextPage(body []byte, current int) bool {
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > current {
			return true
		}
	}
	return false
}

// ---- detail page ----

var (
	detReleasedRe = regexp.MustCompile(`(?s)Released:</span>\s*(.*?)\s*</div>`)
	detLengthRe   = regexp.MustCompile(`Length:</span>\s*(\d+)\s*min`)
	detSeriesRe   = regexp.MustCompile(`(?s)Series:</span>\s*<a[^>]*>\s*(.*?)\s*</a>`)
	detTagsRe     = regexp.MustCompile(`(?s)Tags:</span>(.*?)</div>`)
	detTagLinkRe  = regexp.MustCompile(`>([^<]+)</a>`)
)

type detailData struct {
	Date     time.Time
	Duration int
	Series   string
	Tags     []string
}

func parseDetailPage(body []byte) detailData {
	var d detailData

	if m := detReleasedRe.FindSubmatch(body); m != nil {
		if t, err := parseutil.TryParseDate(strings.TrimSpace(string(m[1])), "Jan 2, 2006", "January 2, 2006"); err == nil {
			d.Date = t.UTC()
		}
	}
	if m := detLengthRe.FindSubmatch(body); m != nil {
		if mins, err := strconv.Atoi(string(m[1])); err == nil {
			d.Duration = mins * 60
		}
	}
	if m := detSeriesRe.FindSubmatch(body); m != nil {
		d.Series = strings.TrimSpace(html.UnescapeString(string(m[1])))
	}
	if m := detTagsRe.FindSubmatch(body); m != nil {
		for _, tm := range detTagLinkRe.FindAllSubmatch(m[1], -1) {
			tag := strings.TrimSpace(html.UnescapeString(string(tm[1])))
			if tag != "" {
				d.Tags = append(d.Tags, tag)
			}
		}
	}
	return d
}

func (s *Scraper) fetchDetail(ctx context.Context, ls listingScene, studioURL string, delay time.Duration) (models.Scene, error) {
	if !scraper.Pace(ctx, delay) {
		return models.Scene{}, ctx.Err()
	}

	now := time.Now().UTC()
	scene := models.Scene{
		ID:        ls.ID,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     ls.Title,
		URL:       ls.URL,
		Date:      ls.Date,
		Thumbnail: ls.Thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if ls.Performer != "" {
		scene.Performers = []string{ls.Performer}
	}

	if ls.URL != "" {
		body, err := s.fetchPage(ctx, ls.URL)
		if err != nil {
			return models.Scene{}, fmt.Errorf("detail %s: %w", ls.ID, err)
		}
		d := parseDetailPage(body)
		if !d.Date.IsZero() {
			scene.Date = d.Date
		}
		if d.Duration > 0 {
			scene.Duration = d.Duration
		}
		if d.Series != "" {
			scene.Series = d.Series
		}
		scene.Tags = d.Tags
	}

	return scene, nil
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	scraper.Debugf(2, "%s: GET %s", siteID, pageURL)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: pageURL,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Cookie"] = ageCookie
			return h
		}(),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

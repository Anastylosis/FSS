// Package tiedvirgins scrapes Tied Virgins (tiedvirgins.com), a 2010-era
// static-HTML site. `updates.html`, `updates2.html`, … hold six or eight
// entries each (date, title, description, preview images) with no per-scene
// page. The pager is broken and the page files have holes (58-59 and 71-85 are
// 404, yet 60-70 and 86-103 exist), so the walk skips 404s and stops after a
// run of them; entries repeat across pages and dates do not sort, so entries
// are de-duplicated and there is no known-ID early stop. See docs/scrapers.md.
package tiedvirgins

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "tiedvirgins"
	studioName = "Tied Virgins"
	// maxGap is how many consecutive missing page files end the walk; the
	// widest hole inside the catalogue is 15.
	maxGap  = 30
	maxPage = 400
)

var siteBase = "https://tiedvirgins.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?tiedvirgins\.com(?:/|$)`)
	tableMark = "<table border=0 cellpadding=0 cellspacing=0"
	boxMark   = `<div class="centeredbox">`
	imgRe     = regexp.MustCompile(`src="(/awizicon/[^"]+)"`)
	dateRe    = regexp.MustCompile(`font-weight:bold;font-color-white;">([^<]+)</span>`)
	titleRe   = regexp.MustCompile(`class="title">([^<]*)</span>`)
	descRe    = regexp.MustCompile(`(?s)class=descrip>(.*?)</div>`)
	slugStrip = regexp.MustCompile(`[^a-z0-9]+`)
	tagStrip  = regexp.MustCompile(`<[^>]*>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"tiedvirgins.com", "tiedvirgins.com/updates.html"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func pageURL(base string, page int) string {
	if page == 1 {
		return base + "/updates.html"
	}
	return fmt.Sprintf("%s/updates%d.html", base, page)
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	seen := make(map[string]bool)
	gap := 0

	for page := 1; page <= maxPage && gap < maxGap; page++ {
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return
		}
		u := pageURL(siteBase, page)
		scraper.Debugf(1, "%s: fetching page %d (%s)", siteID, page, u)
		body, err := s.fetch(ctx, u)
		if err != nil {
			var se *httpx.StatusError
			if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				if page == 1 {
					send(ctx, out, scraper.Error(err))
					return
				}
				gap++
				continue
			}
			send(ctx, out, scraper.Error(fmt.Errorf("page %d: %w", page, err)))
			return
		}
		gap = 0
		entries := parsePage(string(body), studioURL, u, now)
		if len(entries) == 0 {
			send(ctx, out, scraper.Error(scraper.ParseError(u, errors.New("no entries on page"))))
			return
		}
		for _, sc := range entries {
			if seen[sc.ID] {
				continue
			}
			seen[sc.ID] = true
			if !send(ctx, out, scraper.Scene(sc)) {
				return
			}
		}
	}
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parsePage(body, studioURL, pageURL string, now time.Time) []models.Scene {
	parts := strings.Split(body, boxMark)
	var scenes []models.Scene
	for i := 1; i < len(parts); i++ {
		d := dateRe.FindStringSubmatch(parts[i])
		t := titleRe.FindStringSubmatch(parts[i])
		if d == nil || t == nil {
			continue
		}
		date, err := time.Parse("January 02, 2006", strings.TrimSpace(d[1]))
		if err != nil {
			continue
		}
		title := clean(t[1])
		sc := models.Scene{
			ID:        date.Format("2006-01-02") + "-" + strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(title), "-"), "-"),
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     title,
			URL:       pageURL,
			Date:      date.UTC(),
			Studio:    studioName,
			ScrapedAt: now,
		}
		if m := descRe.FindStringSubmatch(parts[i]); m != nil {
			sc.Description = clean(m[1])
		}
		prev := parts[i-1]
		if at := strings.LastIndex(prev, tableMark); at >= 0 {
			prev = prev[at:]
		}
		if m := imgRe.FindStringSubmatch(prev); m != nil {
			sc.Thumbnail = siteBase + m[1]
		}
		scenes = append(scenes, sc)
	}
	return scenes
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStrip.ReplaceAllString(s, " "))), " ")
}

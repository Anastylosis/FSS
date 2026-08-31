// Package chloemorgane scrapes chloemorgane.com.
//
// The whole catalogue is one page: `/updates` lists every sample, with no
// pagination. Each card is
//
//	<li class="list__item update__list-item">
//	  <a href="/sample/dfykwr9edlc">
//	    <img src="/media/images/posters/2017-06-16-th@2x.jpg" alt="Anal Play in The Woods"></a>
//	  <h4 class="title"><a href="/sample/dfykwr9edlc">Anal Play in The Woods</a></h4>
//	</li>
//
// There is no detail fetch: `/sample/{id}` adds nothing — its description
// block is empty on the public page and the poster is the same image at a
// larger size. **The publication date is only in the poster's file name**
// (`2017-06-16`), which is why the thumbnail is parsed rather than merely
// stored; the card carries no date field of its own.
package chloemorgane

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
	siteID     = "chloemorgane"
	studioName = "Chloe Morgane"
	defaultURL = "https://chloemorgane.com"
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
	return []string{"chloemorgane.com", "chloemorgane.com/updates"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?chloemorgane\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: fetching the whole catalogue from /updates", siteID)
	body, err := s.fetchPage(ctx, s.base+"/updates")
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("updates: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	items := parseListing(body)
	if len(items) == 0 {
		select {
		case out <- scraper.Error(scraper.ParseError(s.base+"/updates", fmt.Errorf("no sample cards on the updates page"))):
		case <-ctx.Done():
		}
		return
	}
	scraper.Debugf(1, "%s: %d scenes", siteID, len(items))

	select {
	case out <- scraper.Progress(len(items)):
	case <-ctx.Done():
		return
	}

	now := time.Now().UTC()
	for _, item := range items {
		if opts.KnownIDs[item.id] {
			scraper.Debugf(1, "%s: hit known ID %s, stopping early", siteID, item.id)
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(s.toScene(item, studioURL, now)):
		case <-ctx.Done():
			return
		}
	}
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
	id        string
	title     string
	thumbnail string
	date      string
}

var (
	cardRe = regexp.MustCompile(`(?s)<li class="list__item update__list-item">(.*?)</li>`)
	linkRe = regexp.MustCompile(`href="/sample/([a-z0-9]+)"`)
	imgRe  = regexp.MustCompile(`<img src="(/media/images/posters/[^"]+)"[^>]*alt="([^"]*)"`)
	// The poster's file name is the only place the publication date appears.
	posterDateRe = regexp.MustCompile(`/(\d{4}-\d{2}-\d{2})-[a-z]{2}@`)
	titleRe      = regexp.MustCompile(`(?s)<h4[^>]*>\s*<a[^>]*>(.*?)</a>`)
)

func parseListing(body []byte) []listItem {
	var items []listItem
	seen := make(map[string]bool)
	for _, m := range cardRe.FindAllSubmatch(body, -1) {
		block := m[1]
		lm := linkRe.FindSubmatch(block)
		if lm == nil {
			continue
		}
		item := listItem{id: string(lm[1])}
		if seen[item.id] {
			continue
		}
		if t := titleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if im := imgRe.FindSubmatch(block); im != nil {
			item.thumbnail = html.UnescapeString(string(im[1]))
			if item.title == "" {
				item.title = cleanText(string(im[2]))
			}
			if d := posterDateRe.FindSubmatch(im[1]); d != nil {
				item.date = string(d[1])
			}
		}
		if item.title == "" {
			continue
		}
		seen[item.id] = true
		items = append(items, item)
	}
	return items
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        item.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     item.title,
		URL:       s.base + "/sample/" + item.id,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if item.thumbnail != "" {
		// The listing links the small poster; the same file at `lg` is what
		// the sample page shows.
		sc.Thumbnail = s.base + strings.Replace(item.thumbnail, "-th@2x", "-lg@2x", 1)
	}
	if d, err := parseutil.TryParseDate(item.date, "2006-01-02"); err == nil {
		sc.Date = d
	}
	return sc
}

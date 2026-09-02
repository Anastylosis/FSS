// Package lostbetsgames scrapes lostbetsgames.com.
//
// Listing at `/site/index` then `/site/index/p/{N}`, ~45 cards a page over 22
// pages. Everything is on the card:
//
//	<div class="content-box"><figure>
//	  <h2 class="title">Raven & Frankie Sexy Couple Loser Cums First</h2>
//	  <a href="https://lostbetsgames.com/site/videoPreview/id/3932/Raven-Frankie-….html"
//	     miniclip="//cdnpb.lostbetsgames.com/media/video/39/32/3932/miniclip.mp4">
//	    <img src="/media/video/39/32/3932/thumb.jpg"></a>
//	  <figcaption><time>12:45</time>
//	    <em class="added">Added on <time datetime="2026-08-28">August 28th, 2026</time></em>
//	  </figcaption>
//	</figure></div>
//
// There is no detail fetch: `/site/videoPreview/…` is a wall of preview frames
// behind a join link and carries no description, cast or tags the card does
// not already have.
//
// Past the last page the listing 404s, which is the end of the walk — a 404 on
// page 1 stays an error, because that is a broken site rather than an end.
package lostbetsgames

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "lostbetsgames"
	studioName = "Lost Bets Games"
	defaultURL = "https://lostbetsgames.com"
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
	return []string{"lostbetsgames.com", "lostbetsgames.com/site/index"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?lostbetsgames\.com(?:/|$)`)

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
		pageURL := fmt.Sprintf("%s/site/index/p/%d", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			// Past the last page the listing 404s. Only past page 1: a 404 on
			// the first page is a broken site, not an empty catalogue, and
			// --full's authoritative Save must not act on it.
			if page > 1 && isNotFound(err) {
				scraper.Debugf(1, "%s: page %d is past the last page (404) — done", siteID, page)
				return scraper.PageResult{Done: true}, nil
			}
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

		scenes := make([]models.Scene, 0, len(parsed))
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			scenes = append(scenes, s.toScene(item, studioURL, now))
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(scenes) == 0 || (lastPage > 0 && page >= lastPage),
		}, nil
	})
}

// isNotFound reports whether err is the listing answering 404.
func isNotFound(err error) bool {
	var se *httpx.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
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
	url       string
	title     string
	thumbnail string
	preview   string
	duration  int
	date      string
}

var (
	cardStartRe = regexp.MustCompile(`<div class="col-md-4 col-sm-4 col-xs-12 content-box">`)
	cardTitleRe = regexp.MustCompile(`(?s)<h2 class="title">(.*?)</h2>`)
	cardLinkRe  = regexp.MustCompile(`(?s)<a\s+href="([^"]*/site/videoPreview/id/(\d+)/[^"]*)"`)
	cardClipRe  = regexp.MustCompile(`miniclip="([^"]+\.mp4)"`)
	cardThumbRe = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	cardDurRe   = regexp.MustCompile(`<time>\s*(\d{1,2}:\d{2}(?::\d{2})?)\s*</time>`)
	// The machine-readable date is on the <time> element; the text beside it
	// carries an English ordinal.
	cardDateRe = regexp.MustCompile(`datetime="(\d{4}-\d{2}-\d{2})"`)
	pageNumRe  = regexp.MustCompile(`/site/index/p/(\d+)`)
	tagStripRe = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		m := cardLinkRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item := listItem{url: html.UnescapeString(string(m[1])), id: string(m[2])}
		if t := cardTitleRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if t := cardThumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := cardClipRe.FindSubmatch(block); t != nil {
			item.preview = html.UnescapeString(string(t[1]))
		}
		if t := cardDurRe.FindSubmatch(block); t != nil {
			item.duration = parseutil.ParseDurationColon(string(t[1]))
		}
		if t := cardDateRe.FindSubmatch(block); t != nil {
			item.date = string(t[1])
		}
		if item.title == "" {
			continue
		}
		items = append(items, item)
	}
	return items
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
		ID:        item.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     item.title,
		URL:       item.url,
		Thumbnail: absURL(s.base, item.thumbnail),
		Preview:   absURL(s.base, item.preview),
		Studio:    studioName,
		Duration:  item.duration,
		ScrapedAt: now,
	}
	if d, err := parseutil.TryParseDate(item.date, "2006-01-02"); err == nil {
		sc.Date = d
	}
	return sc
}

// absURL resolves the site's root-relative and protocol-relative asset URLs.
func absURL(base, u string) string {
	switch {
	case u == "", strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	default:
		return base + "/" + strings.TrimPrefix(u, "/")
	}
}

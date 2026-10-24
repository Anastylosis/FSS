// Package transexhot scrapes Transex Hot (transexhot.com.br), a WordPress
// site of Brazilian trans scenes. See docs/scrapers.md.
package transexhot

import (
	"context"
	"encoding/xml"
	"errors"
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
	siteID         = "transexhot"
	studioName     = "Transex Hot"
	defaultWorkers = 4
)

var siteBase = "https://transexhot.com.br"

// migrationDay is the date WordPress stamped on ~130 scenes when the site
// was moved in 2020; it is not when any of them was released.
var migrationDay = time.Date(2020, 11, 9, 0, 0, 0, 0, time.UTC)

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?transexhot\.com\.br(?:/|$)`)
	categoryRe = regexp.MustCompile(`/categoria/([^/?#]+)`)
	modelRe    = regexp.MustCompile(`/travestis/([^/?#]+)`)
	videoRe    = regexp.MustCompile(`^https?://[^/]+/videos/([^/?#]+)/?$`)

	cardRe      = regexp.MustCompile(`<div class="thumb">\s*<a href="(https?://[^/"]+/videos/[^"/]+/)"`)
	h1Re        = regexp.MustCompile(`(?s)<div class="title">\s*<h1>(.*?)</h1>`)
	castBlockRe = regexp.MustCompile(`(?s)<div class="cast">(.*?)</div>`)
	castRe      = regexp.MustCompile(`<a href="[^"]*/travestis/[^"]*">([^<]+)</a>`)
	tagBlockRe  = regexp.MustCompile(`(?s)<div class="right">(.*?)</div>`)
	tagRe       = regexp.MustCompile(`<a href='[^']*/categoria/[^']*'>([^<]+)</a>`)
	publishedRe = regexp.MustCompile(`"datePublished":"([^"]+)"`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"transexhot.com.br",
		"transexhot.com.br/videos/",
		"transexhot.com.br/categoria/{slug}/",
		"transexhot.com.br/travestis/{slug}/",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	var urls []string
	var err error
	switch {
	case categoryRe.MatchString(studioURL):
		slug := categoryRe.FindStringSubmatch(studioURL)[1]
		scraper.Debugf(1, "%s: scraping category %s", siteID, slug)
		urls, err = s.collectListing(ctx, siteBase+"/categoria/"+slug+"/", opts)
	case modelRe.MatchString(studioURL):
		slug := modelRe.FindStringSubmatch(studioURL)[1]
		scraper.Debugf(1, "%s: scraping performer %s", siteID, slug)
		urls, err = s.collectListing(ctx, siteBase+"/travestis/"+slug+"/", opts)
	default:
		urls, err = s.fetchSitemap(ctx)
	}
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	if len(urls) == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(studioURL, errors.New("no video links found"))))
		return
	}
	if !send(ctx, out, scraper.Progress(len(urls))) {
		return
	}

	// The sitemap is not in release order, so a stored scene is skipped
	// rather than ending the walk.
	fresh := urls[:0]
	skipped := false
	for _, u := range urls {
		if opts.KnownIDs[videoSlug(u)] {
			skipped = true
			continue
		}
		fresh = append(fresh, u)
	}
	s.fetchDetails(ctx, studioURL, fresh, opts, out)
	if skipped && ctx.Err() == nil {
		send(ctx, out, scraper.StoppedEarly())
	}
}

func videoSlug(u string) string {
	if m := videoRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

type urlset struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func (s *Scraper) fetchSitemap(ctx context.Context) ([]string, error) {
	u := siteBase + "/videos-sitemap.xml"
	body, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var us urlset
	if err := parseutil.DecodeXML(body, &us); err != nil {
		var se *xml.SyntaxError
		if errors.As(err, &se) {
			return nil, scraper.ParseError(u, err)
		}
		return nil, scraper.ParseError(u, err)
	}
	var urls []string
	seen := map[string]bool{}
	for _, e := range us.URLs {
		loc := strings.TrimSpace(e.Loc)
		if slug := videoSlug(loc); slug != "" && !seen[slug] {
			seen[slug] = true
			urls = append(urls, siteBase+"/videos/"+slug+"/")
		}
	}
	return urls, nil
}

// collectListing walks a category or performer listing (`{base}page/N/`)
// until a page adds no new cards or does not exist.
func (s *Scraper) collectListing(ctx context.Context, base string, opts scraper.ListOpts) ([]string, error) {
	var urls []string
	seen := map[string]bool{}
	for page := 1; ; page++ {
		if page > 1 && !scraper.Pace(ctx, opts.Delay) {
			return urls, ctx.Err()
		}
		u := base
		if page > 1 {
			u = fmt.Sprintf("%spage/%d/", base, page)
		}
		body, err := s.get(ctx, u)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				return urls, nil
			}
			return urls, err
		}
		added := 0
		for _, m := range cardRe.FindAllStringSubmatch(listingBlock(string(body)), -1) {
			slug := videoSlug(m[1])
			if slug != "" && !seen[slug] {
				seen[slug] = true
				urls = append(urls, siteBase+"/videos/"+slug+"/")
				added++
			}
		}
		if added == 0 {
			return urls, nil
		}
	}
}

// listingBlock is the page's card grid, which starts at the one
// `videocarousel list` block; the header and sidebars carry no cards.
func listingBlock(body string) string {
	if i := strings.Index(body, `class="videocarousel list"`); i >= 0 {
		return body[i:]
	}
	return ""
}

func (s *Scraper) fetchDetails(ctx context.Context, studioURL string, urls []string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(urls), workers)
	now := time.Now().UTC()

	jobs := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				body, err := s.get(ctx, u)
				var r scraper.SceneResult
				if err != nil {
					r = scraper.Error(err)
				} else if sc, err := parseDetail(string(body), u, studioURL, now); err != nil {
					r = scraper.Error(err)
				} else {
					r = scraper.Scene(sc)
				}
				if !send(ctx, out, r) {
					return
				}
			}
		}()
	}
	for _, u := range urls {
		select {
		case jobs <- u:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
}

func parseDetail(body, pageURL, studioURL string, now time.Time) (models.Scene, error) {
	sc := models.Scene{
		ID:        videoSlug(pageURL),
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       pageURL,
		Studio:    studioName,
		ScrapedAt: now,
	}
	og := parseutil.OpenGraph([]byte(body))
	if m := h1Re.FindStringSubmatch(body); m != nil {
		sc.Title = cleanText(m[1])
	}
	if sc.Title == "" {
		sc.Title = strings.TrimSuffix(cleanText(og["og:title"]), " - Transex Hot")
	}
	if sc.Title == "" {
		return sc, scraper.ParseError(pageURL, errors.New("no title"))
	}
	sc.Description = cleanText(og["og:description"])
	sc.Thumbnail = html.UnescapeString(og["og:image"])
	if m := publishedRe.FindStringSubmatch(body); m != nil {
		if t, err := time.Parse(time.RFC3339, m[1]); err == nil {
			d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			if !d.Equal(migrationDay) {
				sc.Date = d
			}
		}
	}
	if m := castBlockRe.FindStringSubmatch(body); m != nil {
		for _, c := range castRe.FindAllStringSubmatch(m[1], -1) {
			if name := cleanText(c[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
	if m := tagBlockRe.FindStringSubmatch(body); m != nil {
		for _, t := range tagRe.FindAllStringSubmatch(m[1], -1) {
			if tag := cleanText(t[1]); tag != "" {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
	return sc, nil
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

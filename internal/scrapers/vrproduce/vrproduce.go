// Package vrproduce scrapes V&R PRODUCE (vr-produce.co.jp), a Japanese AV
// label whose WordPress site files releases as a "works" post type. See
// docs/scrapers.md.
package vrproduce

import (
	"context"
	"errors"
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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "vrproduce"
	studioName     = "V&R Produce"
	defaultWorkers = 3
	// firstYear bounds the year walk: the archive starts in 2014, and empty
	// years cost one request each.
	firstYear = 2008
)

var siteBase = "http://www.vr-produce.co.jp"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?vr-produce\.co\.jp(?:/|$)`)
	genreRe   = regexp.MustCompile(`/genrecat/([^/?#]+)`)
	workRe    = regexp.MustCompile(`/works/([a-z]+-\d+)/`)
	titleRe   = regexp.MustCompile(`(?s)<h2 class="title">(.*?)</h2>`)
	descRe    = regexp.MustCompile(`(?s)<p class="txt">(.*?)</p>`)
	specRe    = regexp.MustCompile(`(?s)<dt><img[^>]*alt="([^"]+)"[^>]*></dt>\s*<dd>(.*?)</dd>`)
	linkRe    = regexp.MustCompile(`<a [^>]*>([^<]+)</a>`)
	jacketRe  = regexp.MustCompile(`<div class="jacketzoom">\s*<a href="([^"]+)"`)
	dateRe    = regexp.MustCompile(`(\d{4})/(\d{1,2})/(\d{1,2})`)
	minutesRe = regexp.MustCompile(`(\d+)\s*分`)
	yenRe     = regexp.MustCompile(`([\d,]+)円`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"vr-produce.co.jp", "vr-produce.co.jp/genrecat/{slug}/"}
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

	var listings []string
	if m := genreRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping genre %s", siteID, m[1])
		listings = []string{siteBase + "/genrecat/" + m[1] + "/"}
	} else {
		// No archive lists everything; each release year has its own page.
		for y := time.Now().Year(); y >= firstYear; y-- {
			listings = append(listings, fmt.Sprintf("%s/%d/?post_type=works", siteBase, y))
		}
	}

	// Each archive's works are fetched as soon as it is read, so scenes
	// stream instead of waiting on the whole year walk. Archive order is not
	// release order, so stored works are skipped rather than ending the walk.
	seen := map[string]bool{}
	found, skipped := 0, false
	for _, base := range listings {
		codes, err := s.collect(ctx, base, opts)
		if err != nil {
			send(ctx, out, scraper.Error(err))
		}
		var fresh []string
		for _, c := range codes {
			if seen[c] {
				continue
			}
			seen[c] = true
			found++
			if opts.KnownIDs[strings.ToUpper(c)] {
				skipped = true
				continue
			}
			fresh = append(fresh, c)
		}
		s.fetchWorks(ctx, fresh, studioURL, opts, out)
		if ctx.Err() != nil {
			return
		}
	}
	if found == 0 {
		send(ctx, out, scraper.Error(scraper.ParseError(studioURL, errors.New("no works found"))))
		return
	}
	if skipped {
		send(ctx, out, scraper.StoppedEarly())
	}
}

// collect reads one archive and its /page/N/ continuations.
func (s *Scraper) collect(ctx context.Context, base string, opts scraper.ListOpts) ([]string, error) {
	var codes []string
	seen := map[string]bool{}
	for page := 1; page <= 100; page++ {
		if !scraper.Pace(ctx, opts.Delay) {
			return codes, ctx.Err()
		}
		u := base
		if page > 1 {
			u = pageURL(base, page)
		}
		body, err := s.get(ctx, u)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
				return codes, nil
			}
			return codes, err
		}
		added := 0
		for _, m := range workRe.FindAllStringSubmatch(body, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				codes = append(codes, m[1])
				added++
			}
		}
		if added == 0 || !strings.Contains(body, "/page/"+strconv.Itoa(page+1)+"/") {
			return codes, nil
		}
	}
	return codes, nil
}

// pageURL inserts /page/N/ before an archive's query string.
func pageURL(base string, page int) string {
	path, query, _ := strings.Cut(base, "?")
	u := strings.TrimSuffix(path, "/") + "/page/" + strconv.Itoa(page) + "/"
	if query != "" {
		u += "?" + query
	}
	return u
}

func (s *Scraper) fetchWorks(ctx context.Context, codes []string, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d works with %d workers", siteID, len(codes), workers)
	now := time.Now().UTC()
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				u := siteBase + "/works/" + code + "/"
				body, err := s.get(ctx, u)
				var r scraper.SceneResult
				if err != nil {
					r = scraper.Error(err)
				} else if sc, err := parseWork(body, code, u, studioURL, now); err != nil {
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
feed:
	for _, c := range codes {
		select {
		case jobs <- c:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}

func parseWork(body, code, pageURL, studioURL string, now time.Time) (models.Scene, error) {
	sc := models.Scene{
		ID:        strings.ToUpper(code),
		SiteID:    siteID,
		StudioURL: studioURL,
		URL:       pageURL,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if m := titleRe.FindStringSubmatch(body); m != nil {
		sc.Title = clean(m[1])
	}
	if sc.Title == "" {
		return sc, scraper.ParseError(pageURL, errors.New("no title"))
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := jacketRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(m[1])
	}
	for _, m := range specRe.FindAllStringSubmatch(body, -1) {
		label, value := m[1], m[2]
		names := func() []string {
			var out []string
			for _, l := range linkRe.FindAllStringSubmatch(value, -1) {
				if n := clean(l[1]); n != "" {
					out = append(out, n)
				}
			}
			return out
		}
		switch label {
		case "主演":
			sc.Performers = names()
		case "ジャンル":
			sc.Tags = names()
		case "監督":
			if n := names(); len(n) > 0 {
				sc.Director = n[0]
			}
		case "レーベル":
			if n := names(); len(n) > 0 {
				sc.Series = n[0]
			}
		case "発売日":
			if d := dateRe.FindStringSubmatch(value); d != nil {
				y, _ := strconv.Atoi(d[1])
				mo, _ := strconv.Atoi(d[2])
				da, _ := strconv.Atoi(d[3])
				sc.Date = time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
			}
		case "時間":
			if d := minutesRe.FindStringSubmatch(value); d != nil {
				n, _ := strconv.Atoi(d[1])
				sc.Duration = n * 60
			}
		case "価格":
			if d := yenRe.FindStringSubmatch(value); d != nil {
				if n, err := strconv.Atoi(strings.ReplaceAll(d[1], ",", "")); err == nil && n > 0 {
					sc.AddPrice(models.PriceSnapshot{Date: now, Regular: float64(n)})
				}
			}
		}
	}
	return sc, nil
}

func clean(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	return string(b), err
}

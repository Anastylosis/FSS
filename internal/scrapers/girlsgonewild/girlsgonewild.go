// Package girlsgonewild scrapes girlsgonewild.com, today a Vendo-powered
// aggregator tube. See docs/scrapers.md.
package girlsgonewild

import (
	"context"
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

const (
	siteID         = "girlsgonewild"
	defaultWorkers = 4
)

var siteBase = "https://girlsgonewild.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?girlsgonewild\.com(/new-videos|/studios/[^/?#]+|/model/\d+/[^/?#]+|/genre/[^/?#]+)?(?:/\d+)?/?(?:[?#].*)?$`)
	cardRe    = regexp.MustCompile(`(?s)<div class="card-scene" data-content="(\d+)">(.*?)<div class="card-scene__text"><a href="([^"]+)"[^>]*>(.*?)</a>`)
	cardTime  = regexp.MustCompile(`label--time">([^<]*)</div><div class="label label--time">(\d{4}-\d{2}-\d{2})<`)
	cardThumb = regexp.MustCompile(`data-src="([^"]+)"`)
	hourMinRe = regexp.MustCompile(`(?:(\d+)\s*h)?\s*(\d+)\s*min`)
	titleRe   = regexp.MustCompile(`(?s)<h1 class="watch__title[^"]*">(.*?)(?:<span|</h1>)`)
	modelsRe  = regexp.MustCompile(`(?s)watch__featuring_models.*?</span>`)
	anchorRe  = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	studioRe  = regexp.MustCompile(`(?s)Studio:&nbsp;</span><a [^>]*>(.*?)</a>`)
	dateRe    = regexp.MustCompile(`bi-calendar3[^>]*>\s*(\d{4}-\d{2}-\d{2})`)
	clockRe   = regexp.MustCompile(`bi-clock[^>]*>\s*([\d:]+)`)
	tagsRe    = regexp.MustCompile(`(?s)Tags:</div><div class="genres-list[^"]*">(.*?)</div>`)
	descRe    = regexp.MustCompile(`(?s)Description:</div><div class="text-mob-more[^"]*">(.*?)</div>`)
	stripRe   = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"girlsgonewild.com",
		"girlsgonewild.com/new-videos",
		"girlsgonewild.com/studios/{slug}",
		"girlsgonewild.com/model/{id}/{slug}",
		"girlsgonewild.com/genre/{slug}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id, url, title, thumb string
	date                  time.Time
	duration              int
}

func listPath(studioURL string) string {
	m := matchRe.FindStringSubmatch(studioURL)
	if m == nil || m[1] == "" {
		return "/new-videos"
	}
	return m[1]
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	path := listPath(studioURL)
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: listing %s, %d detail workers", siteID, path, workers)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := siteBase + path
		if page > 1 {
			u = fmt.Sprintf("%s/%d", u, page)
		}
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseCards(string(body))
		last := lastPage(string(body), path)
		scenes := make([]models.Scene, len(cards))
		errs := make([]error, len(cards))

		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, c := range cards {
			if opts.KnownIDs[c.id] {
				scenes[i] = models.Scene{ID: c.id}
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				if !scraper.Pace(ctx, opts.Delay) {
					errs[i] = ctx.Err()
					return
				}
				detail, err := s.get(ctx, c.url)
				if err != nil {
					errs[i] = err
					return
				}
				scenes[i] = toScene(c, string(detail), studioURL, now)
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return scraper.PageResult{}, err
			}
		}
		return scraper.PageResult{Scenes: scenes, Done: page >= last}, nil
	})
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

// lastPage is the highest page number the pagination links mention; a
// listing with none (a studio or model page) is a single page.
func lastPage(page, path string) int {
	last := 1
	re := regexp.MustCompile(`href="[^"]*` + regexp.QuoteMeta(path) + `/(\d+)"`)
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		if n, _ := strconv.Atoi(m[1]); n > last && n < 100000 {
			last = n
		}
	}
	return last
}

func parseCards(page string) []card {
	var cards []card
	seen := map[string]bool{}
	for _, m := range cardRe.FindAllStringSubmatch(page, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		c := card{id: m[1], url: html.UnescapeString(m[3]), title: clean(m[4])}
		if t := cardTime.FindStringSubmatch(m[2]); t != nil {
			c.duration = cardDuration(t[1])
			c.date, _ = time.Parse("2006-01-02", t[2])
		}
		if th := cardThumb.FindStringSubmatch(m[2]); th != nil {
			c.thumb = html.UnescapeString(th[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func cardDuration(s string) int {
	m := hourMinRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return h*3600 + mi*60
}

func toScene(c card, page, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       c.url,
		Date:      c.date,
		Duration:  c.duration,
		Thumbnail: c.thumb,
		ScrapedAt: now,
	}
	if m := titleRe.FindStringSubmatch(page); m != nil {
		if t := clean(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := modelsRe.FindString(page); m != "" {
		for _, a := range anchorRe.FindAllStringSubmatch(m, -1) {
			if name := clean(a[1]); name != "" {
				sc.Performers = append(sc.Performers, name)
			}
		}
	}
	if m := studioRe.FindStringSubmatch(page); m != nil {
		sc.Studio = clean(m[1])
	}
	if m := dateRe.FindStringSubmatch(page); m != nil {
		if t, err := time.Parse("2006-01-02", m[1]); err == nil {
			sc.Date = t
		}
	}
	if m := clockRe.FindStringSubmatch(page); m != nil {
		if d := parseutil.ParseDurationColon(m[1]); d > 0 {
			sc.Duration = d
		}
	}
	if m := tagsRe.FindStringSubmatch(page); m != nil {
		for _, a := range anchorRe.FindAllStringSubmatch(m[1], -1) {
			if tag := clean(a[1]); tag != "" {
				sc.Tags = append(sc.Tags, tag)
			}
		}
	}
	if m := descRe.FindStringSubmatch(page); m != nil {
		sc.Description = clean(m[1])
	}
	return sc
}

func clean(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(html.UnescapeString(stripRe.ReplaceAllString(s, " ")), " ", " ")), " ")
}

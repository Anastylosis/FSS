// Package materiall scrapes Materiall (materiall.jp), a Japanese AV maker
// on WordPress. See docs/scrapers.md.
package materiall

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
	siteID     = "materiall"
	studioName = "Materiall"
	perPage    = 100
)

var siteBase = "https://materiall.jp"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?materiall\.jp(?:/|$)`)
	descRe  = regexp.MustCompile(`(?s)box_works01_text">\s*<p>(.*?)</p>`)
	thumbRe = regexp.MustCompile(`(?s)box_works01_img">\s*<img[^>]*data-src="([^"]+)"`)
	infoRe  = regexp.MustCompile(`(?s)<li class="clearfix"><span>([^<]+)</span>\s*<p>(.*?)</p>`)
	dateRe  = regexp.MustCompile(`(\d{4})/(\d{1,2})/(\d{1,2})`)
	minRe   = regexp.MustCompile(`(\d+)\s*分`)
	stripRe = regexp.MustCompile(`<[^>]+>`)
	splitRe = regexp.MustCompile(`[、,／/\s]+`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"materiall.jp"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type work struct {
	ID    int    `json:"id"`
	Date  string `json:"date_gmt"`
	Slug  string `json:"slug"`
	Link  string `json:"link"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Tags []int `json:"tags"`
}

type detail struct {
	Description string
	Thumbnail   string
	Performers  []string
	Duration    int
	Date        time.Time
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	tags, err := s.fetchTags(ctx)
	if err != nil {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
		}
		return
	}
	workers := scraper.WorkerCount(opts, 4)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/?rest_route=/wp/v2/works&per_page=%d&page=%d&_fields=id,date_gmt,slug,link,title,tags", siteBase, perPage, page)
		var works []work
		total, pages, err := s.getJSON(ctx, u, &works)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(works), workers)
		scenes := make([]models.Scene, len(works))
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, w := range works {
			scenes[i] = toScene(w, tags, studioURL, now)
			if opts.KnownIDs[scenes[i].ID] {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				d, err := s.fetchDetail(ctx, w.Link)
				if err != nil {
					scraper.Debugf(1, "%s: detail %s: %v", siteID, w.Link, err)
					return
				}
				applyDetail(&scenes[i], d)
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return scraper.PageResult{}, err
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(works) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) fetchTags(ctx context.Context) (map[int]string, error) {
	tags := map[int]string{}
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		u := fmt.Sprintf("%s/?rest_route=/wp/v2/tags&per_page=100&page=%d&_fields=id,name", siteBase, page)
		var batch []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		_, pages, err := s.getJSON(ctx, u, &batch)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				break
			}
			return nil, err
		}
		for _, t := range batch {
			tags[t.ID] = html.UnescapeString(t.Name)
		}
		if len(batch) < 100 || (pages > 0 && page >= pages) {
			break
		}
	}
	return tags, nil
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (total, pages int, err error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, siteBase)})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	total, _ = strconv.Atoi(resp.Header.Get("X-WP-Total"))
	pages, _ = strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	if err := httpx.DecodeJSON(resp.Body, v); err != nil {
		return 0, 0, scraper.ParseError(u, err)
	}
	return total, pages, nil
}

func (s *Scraper) fetchDetail(ctx context.Context, u string) (detail, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return detail{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return detail{}, err
	}
	return parseDetail(string(body)), nil
}

func parseDetail(page string) detail {
	var d detail
	if m := descRe.FindStringSubmatch(page); m != nil {
		d.Description = clean(m[1])
	}
	if m := thumbRe.FindStringSubmatch(page); m != nil {
		d.Thumbnail = html.UnescapeString(m[1])
	}
	for _, m := range infoRe.FindAllStringSubmatch(page, -1) {
		v := clean(m[2])
		switch strings.TrimSpace(m[1]) {
		case "出演女優":
			for _, n := range splitRe.Split(v, -1) {
				if n != "" && n != "-" {
					d.Performers = append(d.Performers, n)
				}
			}
		case "収録時間":
			if x := minRe.FindStringSubmatch(v); x != nil {
				n, _ := strconv.Atoi(x[1])
				d.Duration = n * 60
			}
		case "配信開始日":
			if x := dateRe.FindStringSubmatch(v); x != nil {
				y, _ := strconv.Atoi(x[1])
				mo, _ := strconv.Atoi(x[2])
				da, _ := strconv.Atoi(x[3])
				d.Date = time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
			}
		}
	}
	return d
}

func toScene(w work, tags map[int]string, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        w.Slug,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     clean(w.Title.Rendered),
		URL:       w.Link,
		Studio:    studioName,
		ScrapedAt: now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", w.Date); err == nil {
		sc.Date = t.UTC()
	}
	for _, id := range w.Tags {
		if n := tags[id]; n != "" {
			sc.Tags = append(sc.Tags, n)
		}
	}
	return sc
}

func applyDetail(sc *models.Scene, d detail) {
	sc.Description = d.Description
	sc.Thumbnail = d.Thumbnail
	sc.Performers = d.Performers
	sc.Duration = d.Duration
	if !d.Date.IsZero() {
		sc.Date = d.Date
	}
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

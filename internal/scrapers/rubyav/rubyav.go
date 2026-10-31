// Package rubyav scrapes Ruby (ruby-av.com), a Japanese DVD maker whose shop
// also sells ACROSS, ILLUMINATI and 現映社 titles. See docs/scrapers.md.
package rubyav

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID         = "rubyav"
	studioName     = "Ruby"
	perPage        = 20
	defaultWorkers = 4
)

var siteBase = "https://www.ruby-av.com"

var (
	matchRe   = regexp.MustCompile(`^https?://(?:www\.)?ruby-av\.com(?:/|$)`)
	itemRe    = regexp.MustCompile(`(?s)<table class="product_table1">\s*<tr><td[^>]*><a href="[^"]*code=([^"&]+)"><img src="([^"]+)"[^>]*alt="([^"]*)"`)
	totalRe   = regexp.MustCompile(`([\d,]+)件中`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	rowRe     = regexp.MustCompile(`(?s)<tr><th[^>]*>([^<]+)</th><td[^>]*>(.*?)</td></tr>`)
	titleRe   = regexp.MustCompile(`(?s)<h4 class="prod_title">(.*?)<em`)
	descRe    = regexp.MustCompile(`(?s)<p class="exp">(.*?)</p>`)
	priceRe   = regexp.MustCompile(`(?s)<em class="red"[^>]*>([\d,]+)</em>円`)
	dateRe    = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	minRe     = regexp.MustCompile(`(\d+)分`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
	listRe    = regexp.MustCompile(`\s+/\s+`)
	splitRe   = regexp.MustCompile(`[、,／]\s*`)
	makerRe   = regexp.MustCompile(`[?&]mkr=([^&#]+)`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"ruby-av.com", "ruby-av.com/search.php?mkr={maker}"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type item struct {
	code, thumb, title string
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	workers := scraper.WorkerCount(opts, defaultWorkers)

	filter := ""
	if m := makerRe.FindStringSubmatch(studioURL); m != nil {
		scraper.Debugf(1, "%s: scraping maker %s", siteID, m[1])
		filter = "&mkr=" + m[1]
	}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/search.php?search_flag=dvd&sort=sale_day%s&p=%d", siteBase, filter, page)
		body, err := s.get(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseList(body)
		res := scraper.PageResult{Done: len(items) < perPage}
		if page == 1 {
			res.Total = parseTotal(body)
		}
		if len(items) == 0 && page == 1 {
			return res, scraper.ParseError(u, fmt.Errorf("no DVD listed"))
		}

		scenes := make([]models.Scene, len(items))
		var fetch []int
		for i, it := range items {
			scenes[i] = listScene(it, studioURL, now)
			if !opts.KnownIDs[it.code] {
				fetch = append(fetch, i)
			}
		}
		scraper.Debugf(1, "%s: page %d, fetching %d details with %d workers", siteID, page, len(fetch), workers)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var firstErr error
		jobs := make(chan int)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					if !scraper.Pace(ctx, opts.Delay) {
						continue
					}
					du := siteBase + "/dvd_detail.php?code=" + url.QueryEscape(items[i].code)
					dbody, err := s.get(ctx, du)
					mu.Lock()
					if err != nil {
						if firstErr == nil {
							firstErr = err
						}
					} else {
						applyDetail(&scenes[i], dbody, now)
					}
					mu.Unlock()
				}
			}()
		}
		for _, i := range fetch {
			select {
			case jobs <- i:
			case <-ctx.Done():
			}
		}
		close(jobs)
		wg.Wait()
		if firstErr != nil {
			return scraper.PageResult{}, firstErr
		}
		res.Scenes = scenes
		return res, nil
	})
}

func (s *Scraper) get(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseTotal(body string) int {
	if m := totalRe.FindStringSubmatch(body); m != nil {
		n, _ := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
		return n
	}
	return 0
}

func parseList(body string) []item {
	var items []item
	for _, m := range itemRe.FindAllStringSubmatch(body, -1) {
		thumb := html.UnescapeString(m[2])
		if strings.HasPrefix(thumb, "./") {
			thumb = siteBase + thumb[1:]
		}
		items = append(items, item{code: m[1], thumb: thumb, title: strings.TrimSpace(html.UnescapeString(m[3]))})
	}
	return items
}

func listScene(it item, studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        it.code,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       siteBase + "/dvd_detail.php?code=" + url.QueryEscape(it.code),
		Thumbnail: it.thumb,
		Studio:    studioName,
		ScrapedAt: now,
	}
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}

func applyDetail(sc *models.Scene, body string, now time.Time) {
	body = commentRe.ReplaceAllString(body, "")
	if sc.Title == "" {
		if m := titleRe.FindStringSubmatch(body); m != nil {
			sc.Title = clean(m[1])
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(strings.ReplaceAll(m[1], "<br />", "\n"))
	}
	if m := priceRe.FindStringSubmatch(body); m != nil {
		if n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", "")); err == nil && n > 0 {
			sc.AddPrice(models.PriceSnapshot{Date: now, Regular: float64(n)})
		}
	}
	for _, m := range rowRe.FindAllStringSubmatch(body, -1) {
		v := clean(m[2])
		switch strings.TrimSpace(m[1]) {
		case "出演女優":
			for _, name := range splitRe.Split(v, -1) {
				if name = strings.TrimSpace(name); name != "" && name != "-" {
					sc.Performers = append(sc.Performers, name)
				}
			}
		case "メーカー":
			if v != "" {
				sc.Studio = v
			}
		case "シリーズ":
			if v != "" {
				sc.Series = v
			}
		case "監督":
			sc.Director = v
		case "収録時間":
			if d := minRe.FindStringSubmatch(v); d != nil {
				n, _ := strconv.Atoi(d[1])
				sc.Duration = n * 60
			}
		case "発売日":
			if d := dateRe.FindStringSubmatch(v); d != nil {
				y, _ := strconv.Atoi(d[1])
				mo, _ := strconv.Atoi(d[2])
				da, _ := strconv.Atoi(d[3])
				sc.Date = time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
			}
		case "ジャンル":
			for _, g := range listRe.Split(v, -1) {
				if g = strings.TrimSpace(g); g != "" {
					sc.Tags = append(sc.Tags, g)
				}
			}
		}
	}
}

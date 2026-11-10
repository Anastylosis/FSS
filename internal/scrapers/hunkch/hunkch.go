// Package hunkch scrapes MEGA HUNK CHANNEL (hunk-ch.com), a Japanese gay VOD
// marketplace that sells many makers' catalogues. A maker is a brand id: the
// scraper accepts any `search.php?...b={id}` URL (and any other search
// filter), and maps trance-video.com onto TRANCE's brand, b=234.
//
// Pagination is `p=N`; `c=` is a category filter, not a page number. The site
// answers a burst of requests with HTTP 404 on every path for a while (the
// home page included), so detail fetches are paced and kept to two at a time.
package hunkch

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
	siteID        = "hunkch"
	pageUnit      = 20
	tranceBrand   = "234"
	defaultWorker = 2

	// RecommendedDelay is the pace at which the site has been seen to keep
	// answering; a faster burst earns an IP-wide 404.
	RecommendedDelay = 500 * time.Millisecond
)

var siteBase = "https://www.hunk-ch.com"

var (
	matchRe  = regexp.MustCompile(`^https?://(?:www\.)?(?:hunk-ch\.com|trance-video\.com)(?:/|$)`)
	tranceRe = regexp.MustCompile(`^https?://(?:www\.)?trance-video\.com(?:/|$)`)
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"hunk-ch.com/search.php?b={brand}",
		"hunk-ch.com/search.php?b={brand}&c={category}",
		"trance-video.com",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	q, err := searchQuery(studioURL)
	if err != nil {
		return nil, err
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, q, opts, out)
	return out, nil
}

// searchQuery turns the operator's URL into the listing query. Anything but a
// filtered search would walk the whole marketplace, so it is refused.
func searchQuery(studioURL string) (url.Values, error) {
	if tranceRe.MatchString(studioURL) {
		return url.Values{"b": {tranceBrand}}, nil
	}
	u, err := url.Parse(studioURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if !strings.HasSuffix(u.Path, "/search.php") || (q.Get("b") == "" && q.Get("c") == "" && q.Get("s") == "") {
		return nil, fmt.Errorf("hunkch: %s is not a filtered search; use hunk-ch.com/search.php?b={brand} (or trance-video.com)", studioURL)
	}
	return q, nil
}

func pageURL(q url.Values, page int) string {
	v := url.Values{}
	for k, vals := range q {
		v[k] = vals
	}
	if v.Get("search_flag") == "" {
		v.Set("search_flag", "movie")
	}
	if v.Get("sort") == "" {
		v.Set("sort", "new")
	}
	v.Set("p", strconv.Itoa(page))
	return siteBase + "/search.php?" + v.Encode()
}

func (s *Scraper) run(ctx context.Context, studioURL string, q url.Values, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	scraper.WarnDelayBelow(siteID, opts.Delay, RecommendedDelay)
	workers := scraper.WorkerCount(opts, defaultWorker)
	unit := pageUnit
	if n, err := strconv.Atoi(q.Get("page_unit")); err == nil && n > 0 {
		unit = n
	}
	scraper.Debugf(1, "%s: searching %v with %d detail workers", siteID, q, workers)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := pageURL(q, page)
		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards, total := parseList(string(body))
		if len(cards) == 0 {
			if page == 1 && total > 0 {
				return scraper.PageResult{}, scraper.ParseError(u, fmt.Errorf("%d results but no cards parsed", total))
			}
			return scraper.PageResult{Done: true}, nil
		}
		now := time.Now().UTC()
		scenes := make([]models.Scene, len(cards))
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, c := range cards {
			scenes[i] = c.scene(studioURL, now)
			if opts.KnownIDs[c.code] {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				s.addDetail(ctx, &scenes[i], opts, out)
			}()
		}
		wg.Wait()
		res := scraper.PageResult{Scenes: scenes, Done: page*unit >= total}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) addDetail(ctx context.Context, sc *models.Scene, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	if !scraper.Pace(ctx, opts.Delay) {
		return
	}
	body, err := s.fetch(ctx, sc.URL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("detail %s: %w", sc.ID, err)):
		case <-ctx.Done():
		}
		return
	}
	applyDetail(sc, string(body))
}

func (s *Scraper) fetch(ctx context.Context, u string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		Method:  http.MethodGet,
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

type card struct {
	code, title, brand, thumb, desc string
	price                           float64
	date                            time.Time
}

var (
	movieSplitRe = regexp.MustCompile(`<div class="movie"`)
	cardLinkRe   = regexp.MustCompile(`(?s)<h4[^>]*><a href="[^"]*code=([^&"]+)[^"]*">(.*?)</a>`)
	cardImgRe    = regexp.MustCompile(`<img src="(\./video/img/[^"]+)"`)
	cardPriceRe  = regexp.MustCompile(`([\d,]+)円`)
	cardDescRe   = regexp.MustCompile(`(?s)<p style="margin:1px[^>]*>(.*?)</p>`)
	cardDateRe   = regexp.MustCompile(`発売日：(\d{4})-(\d{2})-(\d{2})`)
	totalRe      = regexp.MustCompile(`(\d+)件中`)
	prefixRe     = regexp.MustCompile(`^【([^】：:]+)[：:][^】]*】\s*`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	brRe         = regexp.MustCompile(`(?i)<br\s*/?>`)
	brandRe      = regexp.MustCompile(`ブランド：<a href="search\.php\?b=\d+">(.*?)</a>`)
	minRe        = regexp.MustCompile(`収録時間：(\d+)分`)
	storyRe      = regexp.MustCompile(`(?s)titlebar_story[^>]*/></div><p>(.*?)</p>`)
	dataRe       = regexp.MustCompile(`(?s)<div class="data">(.*?)</div>`)
	catRe        = regexp.MustCompile(`(?s)<a href="search\.php\?c=\d+"[^>]*>(.*?)</a>`)
)

func parseList(body string) ([]card, int) {
	total := 0
	if m := totalRe.FindStringSubmatch(body); m != nil {
		total, _ = strconv.Atoi(m[1])
	}
	var cards []card
	seen := map[string]bool{}
	chunks := movieSplitRe.Split(body, -1)
	for _, ch := range chunks[1:] {
		m := cardLinkRe.FindStringSubmatch(ch)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		c := card{code: m[1]}
		c.title = clean(m[2])
		if p := prefixRe.FindStringSubmatch(c.title); p != nil {
			c.brand = strings.TrimSpace(p[1])
			c.title = strings.TrimSpace(c.title[len(p[0]):])
		}
		if t := cardImgRe.FindStringSubmatch(ch); t != nil {
			c.thumb = siteBase + "/" + strings.TrimPrefix(t[1], "./")
		}
		if t := cardPriceRe.FindStringSubmatch(ch); t != nil {
			c.price, _ = strconv.ParseFloat(strings.ReplaceAll(t[1], ",", ""), 64)
		}
		if t := cardDescRe.FindStringSubmatch(ch); t != nil {
			c.desc = cleanLines(t[1])
		}
		if d := cardDateRe.FindStringSubmatch(ch); d != nil {
			y, _ := strconv.Atoi(d[1])
			mo, _ := strconv.Atoi(d[2])
			dd, _ := strconv.Atoi(d[3])
			c.date = time.Date(y, time.Month(mo), dd, 0, 0, 0, 0, time.UTC)
		}
		cards = append(cards, c)
	}
	return cards, total
}

func (c card) scene(studioURL string, now time.Time) models.Scene {
	studio := c.brand
	if studio == "" {
		studio = "MEGA HUNK CHANNEL"
	}
	sc := models.Scene{
		ID:          c.code,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       c.title,
		URL:         siteBase + "/movie_detail.php?code=" + url.QueryEscape(c.code),
		Description: c.desc,
		Thumbnail:   c.thumb,
		Date:        c.date,
		Studio:      studio,
		ScrapedAt:   now,
	}
	if c.price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: c.price})
	}
	return sc
}

func applyDetail(sc *models.Scene, body string) {
	if m := storyRe.FindStringSubmatch(body); m != nil {
		if d := cleanLines(m[1]); d != "" {
			sc.Description = d
		}
	}
	data := body
	if m := dataRe.FindStringSubmatch(body); m != nil {
		data = m[1]
	}
	if m := brandRe.FindStringSubmatch(data); m != nil {
		if b := clean(m[1]); b != "" {
			sc.Studio = b
		}
	}
	if m := minRe.FindStringSubmatch(clean(data)); m != nil {
		n, _ := strconv.Atoi(m[1])
		sc.Duration = n * 60
	}
	for _, m := range catRe.FindAllStringSubmatch(data, -1) {
		if t := clean(m[1]); t != "" && !contains(sc.Tags, t) {
			sc.Tags = append(sc.Tags, t)
		}
	}
}

func contains(list []string, v string) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// cleanLines keeps the line breaks of a synopsis, which are its paragraphs.
func cleanLines(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// Package aircontrol scrapes Aircontrol (i-dol.tv), a Japanese idol
// image-video maker. Works are listed per label (`/works/list/label/{id}/{page}/`,
// 12 per page); the detail page carries cast, release date, duration, genres
// and the product code. The site is not an Up-Timely install despite the
// similar URLs.
package aircontrol

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "aircontrol"
	studioName    = "Air Control"
	defaultWorker = 4
)

// labelIDs are the site's labels, in the order the root URL walks them:
// 1 antenna, 2 MeLON, 3 qute, 4 Aircon+, 5 ENTAME.
var labelIDs = []string{"1", "2", "3", "4", "5"}

var siteBase = "https://www.i-dol.tv"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?i-dol\.tv(?:/|$)`)
	labelRe = regexp.MustCompile(`/works/list/label/(\d+)`)
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
		"i-dol.tv",
		"i-dol.tv/works/list/label/{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// labelsFor returns the one label a label URL names, or every label.
func labelsFor(studioURL string) []string {
	if m := labelRe.FindStringSubmatch(studioURL); m != nil {
		return []string{m[1]}
	}
	return labelIDs
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	workers := scraper.WorkerCount(opts, defaultWorker)
	for _, label := range labelsFor(studioURL) {
		if ctx.Err() != nil {
			return
		}
		scraper.Debugf(1, "%s: walking label %s with %d detail workers", siteID, label, workers)
		s.walkLabel(ctx, label, studioURL, workers, opts, out)
	}
}

func (s *Scraper) walkLabel(ctx context.Context, label, studioURL string, workers int, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/works/list/label/%s/%d/", siteBase, label, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards, total := parseList(string(body))
		if len(cards) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no works on the first page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		now := time.Now().UTC()
		scenes := make([]models.Scene, len(cards))
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, c := range cards {
			scenes[i] = c.scene(studioURL, now)
			if opts.KnownIDs[c.id] {
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
		res := scraper.PageResult{Scenes: scenes}
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

type card struct{ id, title, thumb, label string }

var (
	itemRe      = regexp.MustCompile(`(?s)<li class="c-works-list-item-wrap">.*?</li>`)
	itemIDRe    = regexp.MustCompile(`href="/works/detail/([^/"]+)/"`)
	itemImgRe   = regexp.MustCompile(`<img src="([^"]+)"`)
	itemTitleRe = regexp.MustCompile(`(?s)works-list-item-ttl[^>]*>(.*?)</a>`)
	itemLblRe   = regexp.MustCompile(`(?s)works-list-item-label"[^>]*>(.*?)</a>`)
	countRe     = regexp.MustCompile(`<title>[^<（(]*[（(](\d+)本`)
	tagRe       = regexp.MustCompile(`<[^>]*>`)
	h1Re        = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	commentRe   = regexp.MustCompile(`(?s)works-info-comment">\s*<p>(.*?)</p>`)
	dlRe        = regexp.MustCompile(`(?s)<dt class="works-info-def">(.*?)</dt>\s*<dd[^>]*>(.*?)</dd>`)
	anchorRe    = regexp.MustCompile(`(?s)<a [^>]*>(.*?)</a>`)
	dateRe      = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	minRe       = regexp.MustCompile(`(\d+)分`)
	videoRe     = regexp.MustCompile(`<video src="([^"]+)"`)
	ogImgRe     = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
)

func parseList(body string) ([]card, int) {
	total := 0
	if m := countRe.FindStringSubmatch(body); m != nil {
		total, _ = strconv.Atoi(m[1])
	}
	var cards []card
	seen := map[string]bool{}
	for _, li := range itemRe.FindAllString(body, -1) {
		m := itemIDRe.FindStringSubmatch(li)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		c := card{id: m[1]}
		if t := itemTitleRe.FindStringSubmatch(li); t != nil {
			c.title = clean(t[1])
		}
		if t := itemImgRe.FindStringSubmatch(li); t != nil {
			c.thumb = absolute(t[1])
		}
		if t := itemLblRe.FindStringSubmatch(li); t != nil {
			c.label = clean(t[1])
		}
		cards = append(cards, c)
	}
	return cards, total
}

func (c card) scene(studioURL string, now time.Time) models.Scene {
	return models.Scene{
		ID:        c.id,
		SiteID:    siteID,
		StudioURL: studioURL,
		Title:     c.title,
		URL:       siteBase + "/works/detail/" + c.id + "/",
		Thumbnail: c.thumb,
		Series:    c.label,
		Studio:    studioName,
		ScrapedAt: now,
	}
}

func applyDetail(sc *models.Scene, body string) {
	if m := h1Re.FindStringSubmatch(body); m != nil {
		if t := clean(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := commentRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := ogImgRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(m[1])
	}
	if m := videoRe.FindStringSubmatch(body); m != nil {
		sc.Preview = html.UnescapeString(m[1])
	}
	for _, dl := range dlRe.FindAllStringSubmatch(body, -1) {
		key, val := clean(dl[1]), dl[2]
		switch key {
		case "出演":
			sc.Performers = anchorTexts(val)
		case "発売日":
			if d := dateRe.FindStringSubmatch(val); d != nil {
				y, _ := strconv.Atoi(d[1])
				mo, _ := strconv.Atoi(d[2])
				dd, _ := strconv.Atoi(d[3])
				sc.Date = time.Date(y, time.Month(mo), dd, 0, 0, 0, 0, time.UTC)
			}
		case "ジャンル":
			sc.Tags = anchorTexts(val)
		case "レーベル":
			if a := anchorTexts(val); len(a) > 0 {
				sc.Series = a[0]
			}
		case "収録時間":
			if d := minRe.FindStringSubmatch(clean(val)); d != nil {
				n, _ := strconv.Atoi(d[1])
				sc.Duration = n * 60
			}
		}
	}
}

func anchorTexts(s string) []string {
	var out []string
	for _, m := range anchorRe.FindAllStringSubmatch(s, -1) {
		if t := clean(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func absolute(ref string) string {
	if strings.HasPrefix(ref, "/") {
		return siteBase + ref
	}
	return ref
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

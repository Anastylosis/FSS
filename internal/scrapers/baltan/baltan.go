// Package baltan scrapes the ZeTToN group's Japanese AV-maker sites that share
// one PHP CMS: Baltan (baltan-av.com), AQUA VR (aqua-vr.com) and SEX AGENT
// (sex-agent.com).
//
// Each site sits behind an age gate that is satisfied by an "age=1" cookie. The
// listing (/items/search/page:N, ~20 items a page) carries only the product
// code, title and cover, so every item needs a detail fetch
// (/items/detail/{CODE}) for the release date, runtime, performers, price and
// story. Listings are not date-sorted, so KnownIDs only skips known items.
package baltan

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

const detailWorkers = 4

type siteConfig struct {
	id, domain, studio string
}

var sites = []siteConfig{
	{"baltan", "baltan-av.com", "BALTAN"},
	{"aquavr", "aqua-vr.com", "AQUA VR"},
	{"sexagent", "sex-agent.com", "SEX AGENT"},
}

// baseOverride redirects every site to a test server.
var baseOverride string

// Scraper implements scraper.StudioScraper for one site of the group.
type Scraper struct {
	Client *http.Client
	cfg    siteConfig
	re     *regexp.Regexp
}

// New constructs a scraper for one site.
func newScraper(cfg siteConfig) *Scraper {
	return &Scraper{
		Client: httpx.NewClient(30 * time.Second),
		cfg:    cfg,
		re:     regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.domain) + `(?:/|$)`),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	for _, c := range sites {
		scraper.Register(newScraper(c))
	}
}

func (s *Scraper) ID() string { return s.cfg.id }

func (s *Scraper) Patterns() []string {
	return []string{
		s.cfg.domain,
		s.cfg.domain + "/items/search",
		s.cfg.domain + "/items/detail/{code}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return s.re.MatchString(u) }

func (s *Scraper) base() string {
	if baseOverride != "" {
		return baseOverride
	}
	return "https://" + s.cfg.domain
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	seen := make(map[string]bool)
	scraper.Paginate(ctx, opts, s.cfg.id, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := s.base() + "/items/search"
		if page > 1 {
			pageURL = fmt.Sprintf("%s/items/search/page:%d", s.base(), page)
		}
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items := parseListing(body)
		fresh := items[:0]
		for _, it := range items {
			if !seen[it.id] {
				seen[it.id] = true
				fresh = append(fresh, it)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		res := scraper.PageResult{Scenes: s.enrich(ctx, scraper.WorkerCount(opts, detailWorkers), studioURL, fresh, now, opts.Delay)}
		if page == 1 {
			res.Total = parseTotal(body)
		}
		return res, nil
	})
}

var (
	totalRe = regexp.MustCompile(`計(\d+)点`)
	itemRe  = regexp.MustCompile(`<li[^>]*><a href="/items/detail/([A-Za-z0-9_-]+)"><img src="([^"]+)" alt="([^"]*)"`)
)

type listItem struct {
	id, title, thumb string
}

func parseTotal(body []byte) int {
	if m := totalRe.FindSubmatch(body); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	return 0
}

func parseListing(body []byte) []listItem {
	var items []listItem
	for _, m := range itemRe.FindAllStringSubmatch(string(body), -1) {
		items = append(items, listItem{
			id:    m[1],
			thumb: html.UnescapeString(m[2]),
			title: html.UnescapeString(strings.TrimSpace(m[3])),
		})
	}
	return items
}

func (s *Scraper) enrich(ctx context.Context, workers int, studioURL string, items []listItem, now time.Time, delay time.Duration) []models.Scene {
	scenes := make([]models.Scene, len(items))
	scraper.Debugf(1, "%s: fetching %d details with %d workers", s.cfg.id, len(items), workers)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, it := range items {
		wg.Add(1)
		go func(i int, it listItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, delay) {
				return
			}
			scenes[i] = s.toScene(ctx, studioURL, it, now)
		}(i, it)
	}
	wg.Wait()

	kept := scenes[:0]
	for _, sc := range scenes {
		if sc.ID != "" {
			kept = append(kept, sc)
		}
	}
	return kept
}

func (s *Scraper) toScene(ctx context.Context, studioURL string, it listItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        it.id,
		SiteID:    s.cfg.id,
		StudioURL: studioURL,
		Title:     it.title,
		URL:       s.base() + "/items/detail/" + it.id,
		Thumbnail: it.thumb,
		Studio:    s.cfg.studio,
		ScrapedAt: now,
	}
	body, err := s.fetchPage(ctx, scene.URL)
	if err != nil {
		return scene
	}
	applyDetail(&scene, string(body), now)
	return scene
}

var (
	h2Re       = regexp.MustCompile(`(?s)<h2>(.*?)</h2>`)
	explainRe  = regexp.MustCompile(`(?s)<p class="explain">(.*?)</p>`)
	rowRe      = regexp.MustCompile(`(?s)<th>([^<]*)</th>\s*<td>(.*?)</td>`)
	stripRe    = regexp.MustCompile(`(?s)<[^>]+>`)
	linkTextRe = regexp.MustCompile(`<a [^>]*>([^<]+)</a>`)
	minutesRe  = regexp.MustCompile(`(\d+)\s*分`)
	dateRe     = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	yenRe      = regexp.MustCompile(`([\d,]+)\s*円`)
)

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

func cells(td string) []string {
	var out []string
	for _, m := range linkTextRe.FindAllStringSubmatch(td, -1) {
		if v := clean(m[1]); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		if v := clean(td); v != "" && v != "-" {
			out = append(out, v)
		}
	}
	return out
}

func applyDetail(scene *models.Scene, detail string, now time.Time) {
	if i := strings.Index(detail, `class="item_detail"`); i >= 0 {
		detail = detail[i:]
	}
	if m := h2Re.FindStringSubmatch(detail); m != nil {
		if t := clean(m[1]); t != "" {
			scene.Title = t
		}
	}
	if m := explainRe.FindStringSubmatch(detail); m != nil {
		scene.Description = clean(strings.ReplaceAll(m[1], "<br />", " "))
	}
	for _, m := range rowRe.FindAllStringSubmatch(detail, -1) {
		vals := cells(m[2])
		if len(vals) == 0 {
			continue
		}
		switch strings.TrimSpace(m[1]) {
		case "収録時間":
			if d := minutesRe.FindStringSubmatch(vals[0]); d != nil {
				n, _ := strconv.Atoi(d[1])
				scene.Duration = n * 60
			}
		case "発売日":
			if d := dateRe.FindStringSubmatch(vals[0]); d != nil {
				y, _ := strconv.Atoi(d[1])
				mo, _ := strconv.Atoi(d[2])
				dd, _ := strconv.Atoi(d[3])
				scene.Date = time.Date(y, time.Month(mo), dd, 0, 0, 0, 0, time.UTC)
			}
		case "レーベル":
			if scene.Series == "" {
				scene.Series = vals[0]
			}
		case "シリーズ":
			scene.Series = vals[0]
		case "監督":
			scene.Director = vals[0]
		case "ジャンル":
			scene.Tags = append(scene.Tags, vals...)
		case "出演者":
			scene.Performers = splitNames(vals)
		case "価格(税込)":
			if d := yenRe.FindStringSubmatch(vals[0]); d != nil {
				if n, err := strconv.Atoi(strings.ReplaceAll(d[1], ",", "")); err == nil && n > 0 {
					scene.AddPrice(models.PriceSnapshot{Date: now, Regular: float64(n)})
				}
			}
		}
	}
}

func splitNames(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, n := range strings.Split(v, "、") {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]byte, error) {
	h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
	h["Cookie"] = "age=1"
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: rawURL, Headers: h})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

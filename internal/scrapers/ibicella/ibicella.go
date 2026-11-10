// Package ibicella scrapes ibicella.com, a French-language femdom shop. The
// listing (`/videos?page=N`) names each product; the detail page carries the
// publication date, duration, full synopsis, hashtags and the unblurred cover.
package ibicella

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID        = "ibicella"
	studioName    = "Ibicella"
	defaultList   = "/videos"
	defaultWorker = 4
)

var siteBase = "https://ibicella.com"

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?ibicella\.com(?:/|$)`)

type Scraper struct {
	client *http.Client
}

func New() *Scraper { return &Scraper{client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"ibicella.com",
		"ibicella.com/videos",
		"ibicella.com/videos/{category}",
		"ibicella.com/videos/{category}/{subcategory}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// listPath returns the listing to walk: the whole shop, or the category the
// operator named (`/videos/{category}[/{sub}]`).
func listPath(studioURL string) string {
	if u, err := url.Parse(studioURL); err == nil && strings.HasPrefix(u.Path, "/videos/") {
		return strings.TrimSuffix(u.Path, "/")
	}
	return defaultList
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	path := listPath(studioURL)
	workers := scraper.WorkerCount(opts, defaultWorker)
	scraper.Debugf(1, "%s: walking %s with %d detail workers", siteID, path, workers)

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s%s?page=%d", siteBase, path, page)
		body, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards := parseList(string(body))
		if len(cards) == 0 {
			if page == 1 {
				return scraper.PageResult{}, scraper.ParseError(pageURL, fmt.Errorf("no products on the first page"))
			}
			return scraper.PageResult{Done: true}, nil
		}
		now := time.Now().UTC()
		scenes := make([]models.Scene, len(cards))
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, c := range cards {
			scenes[i] = c.scene(studioURL, now)
			if opts.KnownIDs[c.slug] {
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
		return scraper.PageResult{Scenes: scenes}, nil
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
	slug, title string
	price       float64
	duration    int
}

var (
	cardSplitRe = regexp.MustCompile(`data-product-mini-parent`)
	cardLinkRe  = regexp.MustCompile(`(?s)<h3 class="h6 mb-0">\s*<a href="[^"]*/shop/([^"/]+)">(.*?)</a>`)
	cardPriceRe = regexp.MustCompile(`<span class="me-2">\s*([\d.,]+)\s*€`)
	cardDurRe   = regexp.MustCompile(`badge">\s*([\d:]+)\s*<`)
	tagRe       = regexp.MustCompile(`<[^>]*>`)
	metaRe      = regexp.MustCompile(`<meta\s+(?:property|name)="([^"]+)"\s+content="([^"]*)"`)
	dateRe      = regexp.MustCompile(`itemprop="datePublished"\s+dateTime="([^"]+)"`)
	durRe       = regexp.MustCompile(`(?s)Durée:\s*<span[^>]*>\s*([\d:]+)\s*</span>`)
	descRe      = regexp.MustCompile(`(?s)<h2 class="h3 mb-sm-4">.*?</h2>\s*<p[^>]*>(.*?)</p>\s*(?:</p>)?\s*</div>`)
	hashtagRe   = regexp.MustCompile(`(?s)href="[^"]*\?s=[^"]*"[^>]*>\s*<span[^>]*>#</span>(.*?)</a>`)
	crumbRe     = regexp.MustCompile(`(?s)<li class="breadcrumb-item[^"]*"[^>]*>\s*<a href="https?://[^/"]+/[^/"]+/[^"]+">(.*?)</a>`)
)

func parseList(body string) []card {
	var cards []card
	seen := map[string]bool{}
	chunks := cardSplitRe.Split(body, -1)
	for _, ch := range chunks[1:] {
		m := cardLinkRe.FindStringSubmatch(ch)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		c := card{slug: m[1], title: clean(m[2])}
		if p := cardPriceRe.FindStringSubmatch(ch); p != nil {
			c.price = parsePrice(p[1])
		}
		if d := cardDurRe.FindStringSubmatch(ch); d != nil {
			c.duration = parseutil.ParseDurationColon(d[1])
		}
		cards = append(cards, c)
	}
	return cards
}

func (c card) scene(studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         c.slug,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      c.title,
		URL:        siteBase + "/shop/" + c.slug,
		Duration:   c.duration,
		Studio:     studioName,
		Performers: []string{studioName},
		ScrapedAt:  now,
	}
	setPrice(&sc, c.price, now)
	return sc
}

func setPrice(sc *models.Scene, price float64, now time.Time) {
	if price > 0 {
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: price})
	}
}

// applyDetail overlays what only the detail page knows. The listing's cover is
// blurred for logged-out visitors; og:image is the real one.
func applyDetail(sc *models.Scene, body string) {
	og := map[string]string{}
	for _, m := range metaRe.FindAllStringSubmatch(body, -1) {
		if _, ok := og[m[1]]; !ok {
			og[m[1]] = html.UnescapeString(m[2])
		}
	}
	if v := og["og:title"]; v != "" {
		sc.Title = v
	}
	if v := og["og:image"]; v != "" {
		sc.Thumbnail = v
	}
	if v := og["og:video"]; v != "" {
		sc.Preview = v
	}
	sc.Description = og["og:description"]
	if m := descRe.FindStringSubmatch(body); m != nil {
		if d := clean(m[1]); d != "" {
			sc.Description = d
		}
	}
	if v := og["product:price:amount"]; v != "" {
		if p, err := strconv.ParseFloat(v, 64); err == nil && p > 0 && len(sc.PriceHistory) == 0 {
			setPrice(sc, p, sc.ScrapedAt)
		}
	}
	if m := dateRe.FindStringSubmatch(body); m != nil {
		if d, err := parseutil.TryParseDate(m[1], time.RFC3339); err == nil {
			sc.Date = d.UTC()
		}
	}
	if m := durRe.FindStringSubmatch(body); m != nil {
		if d := parseutil.ParseDurationColon(m[1]); d > 0 {
			sc.Duration = d
		}
	}
	for _, m := range hashtagRe.FindAllStringSubmatch(body, -1) {
		sc.Tags = appendUnique(sc.Tags, clean(m[1]))
	}
	for _, m := range crumbRe.FindAllStringSubmatch(body, -1) {
		sc.Categories = appendUnique(sc.Categories, clean(m[1]))
	}
}

func parsePrice(s string) float64 {
	s = strings.ReplaceAll(s, ",", ".")
	p, _ := strconv.ParseFloat(s, 64)
	return p
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// Package digitalark scrapes the Digital Ark makeshop store
// (shop.digital-ark.com), including its WEEKENDER label. See docs/scrapers.md.
package digitalark

import (
	"context"
	"errors"
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
	siteID         = "digitalark"
	studioName     = "Digital Ark"
	pageSize       = 50
	defaultWorkers = 3
)

var siteBase = "https://shop.digital-ark.com"

var labels = map[string]string{
	"ct5": "WEEKENDER",
}

var (
	matchRe    = regexp.MustCompile(`^https?://(?:(?:www|shop)\.)?digital-ark\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`/view/category/([\w-]+)`)
	cardRe     = regexp.MustCompile(`<li class="item-list">`)
	itemRe     = regexp.MustCompile(`href="/view/item/(\d+)`)
	imgRe      = regexp.MustCompile(`<img src="([^"]+)"`)
	nameRe     = regexp.MustCompile(`(?s)class="item-list-name"><a [^>]*>(.*?)</a>`)
	priceRe    = regexp.MustCompile(`item-list-price">￥([\d,]+)`)
	totalRe    = regexp.MustCompile(`全(\d+)件中`)
	titleRe    = regexp.MustCompile(`(?s)<h1 class="item-name">(.*?)</h1>`)
	seriesRe   = regexp.MustCompile(`(?s)<p class="item-category-name"><a [^>]*>(.*?)</a>`)
	infoRe     = regexp.MustCompile(`(?s)<ul class="dvdlist-info">(.*?)</ul>`)
	fieldRe    = regexp.MustCompile(`(?s)<li>([^：<]+)：(.*?)</li>`)
	hrefRe     = regexp.MustCompile(`href="/view/category/([\w-]+)"`)
	dateRe     = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	minutesRe  = regexp.MustCompile(`(\d+)分`)
	descRe     = regexp.MustCompile(`(?s)<p class="item-description">(.*?)</p>`)
	castRe     = regexp.MustCompile(`(?s)<!-- キーワード用 -->\s*<div class="item-description-wrap">\s*<div class="keyword-links" data-keywords="([^"]*)"`)
	tagsRe     = regexp.MustCompile(`(?s)<!-- スタッフ名用 -->\s*<div class="item-description-wrap">\s*<div class="keyword-links" data-keywords="([^"]*)"`)
	ogImageRe  = regexp.MustCompile(`<meta property="og:image" content="([^"]+)"`)
	stripRe    = regexp.MustCompile(`<[^>]+>`)
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
		"digital-ark.com",
		"shop.digital-ark.com",
		"shop.digital-ark.com/view/category/{id}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type card struct {
	id, title, thumb string
	price            float64
}

func listingPath(studioURL string) string {
	if m := categoryRe.FindStringSubmatch(studioURL); m != nil {
		return "/view/category/" + m[1]
	}
	return "/view/category/all_items"
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	path := listingPath(studioURL)
	scraper.Debugf(1, "%s: scraping %s", siteID, path)
	now := time.Now().UTC()
	seen := map[string]bool{}
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := siteBase + path + "?page=" + strconv.Itoa(page)
		body, err := s.fetch(ctx, u)
		if err != nil {
			return scraper.PageResult{}, err
		}
		cards, total := parseListing(body)
		if page == 1 && len(cards) == 0 {
			return scraper.PageResult{}, scraper.ParseError(u, errors.New("no items listed"))
		}
		var fresh []card
		for _, c := range cards {
			if !seen[c.id] {
				seen[c.id] = true
				fresh = append(fresh, c)
			}
		}
		if len(fresh) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		return scraper.PageResult{
			Scenes: s.details(ctx, fresh, studioURL, opts, now, out),
			Total:  total,
			Done:   len(cards) < pageSize,
		}, nil
	})
}

func parseListing(body string) ([]card, int) {
	total := 0
	if m := totalRe.FindStringSubmatch(body); m != nil {
		total, _ = strconv.Atoi(m[1])
	}
	starts := cardRe.FindAllStringIndex(body, -1)
	cards := make([]card, 0, len(starts))
	for i, m := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := body[m[1]:end]
		id := itemRe.FindStringSubmatch(block)
		name := nameRe.FindStringSubmatch(block)
		if id == nil || name == nil {
			continue
		}
		c := card{id: id[1], title: clean(name[1])}
		if img := imgRe.FindStringSubmatch(block); img != nil {
			c.thumb = html.UnescapeString(img[1])
		}
		if p := priceRe.FindStringSubmatch(block); p != nil {
			c.price, _ = strconv.ParseFloat(strings.ReplaceAll(p[1], ",", ""), 64)
		}
		cards = append(cards, c)
	}
	return cards, total
}

// details fetches each item page for the release date, cast and label. Known
// IDs keep a stub so Paginate stops after the page; a failed fetch is reported
// and the card's title and thumbnail kept.
func (s *Scraper) details(ctx context.Context, cards []card, studioURL string, opts scraper.ListOpts, now time.Time, out chan<- scraper.SceneResult) []models.Scene {
	workers := scraper.WorkerCount(opts, defaultWorkers)
	scraper.Debugf(1, "%s: fetching %d details with %d workers", siteID, len(cards), workers)
	scenes := make([]models.Scene, len(cards))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, c := range cards {
		scenes[i] = models.Scene{
			ID:        c.id,
			SiteID:    siteID,
			StudioURL: studioURL,
			Title:     c.title,
			URL:       siteBase + "/view/item/" + c.id,
			Thumbnail: c.thumb,
			Studio:    studioName,
			ScrapedAt: now,
		}
		if c.price > 0 {
			scenes[i].AddPrice(models.PriceSnapshot{Date: now, Regular: c.price, Currency: "JPY"})
		}
		if opts.KnownIDs[c.id] {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if !scraper.Pace(ctx, opts.Delay) {
				return
			}
			body, err := s.fetch(ctx, scenes[i].URL)
			if err != nil {
				select {
				case out <- scraper.Error(err):
				case <-ctx.Done():
				}
				return
			}
			applyDetail(&scenes[i], body)
		}(i)
	}
	wg.Wait()
	return scenes
}

func applyDetail(sc *models.Scene, body string) {
	if m := titleRe.FindStringSubmatch(body); m != nil {
		if t := clean(m[1]); t != "" {
			sc.Title = t
		}
	}
	if m := ogImageRe.FindStringSubmatch(body); m != nil {
		sc.Thumbnail = html.UnescapeString(m[1])
	}
	if m := seriesRe.FindStringSubmatch(body); m != nil {
		if series := clean(m[1]); !strings.HasPrefix(series, "その他") {
			sc.Series = series
		}
	}
	if m := infoRe.FindStringSubmatch(body); m != nil {
		for _, f := range fieldRe.FindAllStringSubmatch(m[1], -1) {
			applyField(sc, strings.TrimSpace(f[1]), f[2])
		}
	}
	if m := descRe.FindStringSubmatch(body); m != nil {
		sc.Description = clean(m[1])
	}
	if m := castRe.FindStringSubmatch(body); m != nil {
		sc.Performers = splitKeywords(m[1])
	}
	if m := tagsRe.FindStringSubmatch(body); m != nil {
		sc.Tags = splitKeywords(m[1])
	}
}

func applyField(sc *models.Scene, key, value string) {
	text := clean(value)
	switch key {
	case "発売日":
		if d := dateRe.FindStringSubmatch(text); d != nil {
			y, _ := strconv.Atoi(d[1])
			mo, _ := strconv.Atoi(d[2])
			da, _ := strconv.Atoi(d[3])
			sc.Date = time.Date(y, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
		}
	case "収録時間":
		if d := minutesRe.FindStringSubmatch(text); d != nil {
			mins, _ := strconv.Atoi(d[1])
			sc.Duration = mins * 60
		}
	case "監督":
		if text != "" && text != "0" {
			sc.Director = text
		}
	case "レーベル":
		if m := hrefRe.FindStringSubmatch(value); m != nil {
			if label, ok := labels[m[1]]; ok {
				sc.Studio = label
			}
		}
	}
}

func splitKeywords(s string) []string {
	var out []string
	for _, k := range strings.FieldsFunc(html.UnescapeString(s), func(r rune) bool { return r == ',' || r == '、' || r == '・' }) {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(stripRe.ReplaceAllString(s, " "))), " ")
}

func (s *Scraper) fetch(ctx context.Context, u string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox)})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := httpx.ReadBody(resp.Body)
	return string(b), err
}

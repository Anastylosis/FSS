// Package dfwknight scrapes DFW Knight (dfwknight.com/tour), a WordPress +
// WooCommerce video store. The core REST `product` listing supplies the
// publish date; the WooCommerce Store API (`wc/store/products?include=`)
// supplies price, image and categories for the same page. See docs/scrapers.md.
package dfwknight

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "dfwknight"
	studioName = "DFW Knight"
	perPage    = 100
)

var siteBase = "https://dfwknight.com/tour"

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?dfwknight\.com(?:/|$)`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"dfwknight.com", "dfwknight.com/tour/video-store/"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type post struct {
	ID    int    `json:"id"`
	Date  string `json:"date"`
	Link  string `json:"link"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
}

type product struct {
	ID     int `json:"id"`
	Prices struct {
		Price        string `json:"price"`
		RegularPrice string `json:"regular_price"`
		Minor        int    `json:"currency_minor_unit"`
	} `json:"prices"`
	Images []struct {
		Src string `json:"src"`
	} `json:"images"`
	Categories []struct {
		Name string `json:"name"`
	} `json:"categories"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		var posts []post
		total, pages, err := s.getJSON(ctx, fmt.Sprintf(
			"%s/wp-json/wp/v2/product?per_page=%d&page=%d&orderby=date&order=desc&_fields=id,date,title,link,content",
			siteBase, perPage, page), &posts)
		if err != nil {
			var se *httpx.StatusError
			// WordPress answers 400 for a page past the last.
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		if len(posts) == 0 {
			return scraper.PageResult{Done: true}, nil
		}
		ids := make([]string, len(posts))
		for i, p := range posts {
			ids[i] = strconv.Itoa(p.ID)
		}
		var products []product
		if _, _, err := s.getJSON(ctx, fmt.Sprintf("%s/wp-json/wc/store/products?per_page=%d&include=%s",
			siteBase, perPage, strings.Join(ids, ",")), &products); err != nil {
			return scraper.PageResult{}, err
		}
		byID := make(map[int]product, len(products))
		for _, p := range products {
			byID[p.ID] = p
		}
		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			scenes = append(scenes, toScene(p, byID[p.ID], studioURL, now))
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(posts) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) getJSON(ctx context.Context, u string, v any) (total, pages int, err error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, "https://dfwknight.com")})
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

func toScene(p post, pr product, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          strconv.Itoa(p.ID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       clean(p.Title.Rendered),
		URL:         p.Link,
		Description: clean(p.Content.Rendered),
		Studio:      studioName,
		ScrapedAt:   now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", p.Date); err == nil {
		sc.Date = t.UTC()
	}
	if len(pr.Images) > 0 {
		sc.Thumbnail = pr.Images[0].Src
	}
	for _, c := range pr.Categories {
		if n := clean(c.Name); n != "" {
			sc.Tags = append(sc.Tags, n)
		}
	}
	if amount, err := strconv.Atoi(pr.Prices.Price); err == nil && amount > 0 {
		regular, _ := strconv.Atoi(pr.Prices.RegularPrice)
		snap := models.PriceSnapshot{Date: now, Regular: minor(amount, pr.Prices.Minor)}
		if regular > amount {
			snap.Regular = minor(regular, pr.Prices.Minor)
			snap.Discounted = minor(amount, pr.Prices.Minor)
			snap.IsOnSale = true
		}
		sc.AddPrice(snap)
	}
	return sc
}

func minor(v, unit int) float64 {
	f := float64(v)
	for range unit {
		f /= 10
	}
	return f
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

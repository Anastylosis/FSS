// Package loracross scrapes Lora Cross (loracrossmodel.com), a WordPress +
// WooCommerce store whose videos are products in category 21. Descriptions,
// dates and the model/genre taxonomies come from the WP REST API; the price
// and featured image from the WooCommerce Store API and the media endpoint.
// See docs/scrapers.md.
package loracross

import (
	"context"
	"encoding/json"
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
	siteID     = "loracross"
	studioName = "Lora Cross"
	perPage    = 100
	videosCat  = 21
)

var siteBase = "https://loracrossmodel.com"

var (
	matchRe = regexp.MustCompile(`^https?://(?:www\.)?loracrossmodel\.com(?:/|$)`)
	stripRe = regexp.MustCompile(`<[^>]+>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(45 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string { return []string{"loracrossmodel.com"} }

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type product struct {
	ID    int    `json:"id"`
	Date  string `json:"date_gmt"`
	Link  string `json:"link"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
	Media int   `json:"featured_media"`
	Model []int `json:"model"`
	Genre []int `json:"genre"`
}

type term struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type storeProduct struct {
	ID     int `json:"id"`
	Prices struct {
		Price        string `json:"price"`
		RegularPrice string `json:"regular_price"`
		Currency     string `json:"currency_code"`
		MinorUnit    int    `json:"currency_minor_unit"`
	} `json:"prices"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scraper.Debugf(1, "%s: loading model and genre taxonomies", siteID)
	performers, err := s.fetchTerms(ctx, "model")
	if err != nil {
		sendErr(ctx, out, err)
		return
	}
	genres, err := s.fetchTerms(ctx, "genre")
	if err != nil {
		sendErr(ctx, out, err)
		return
	}
	s.paginate(ctx, studioURL, opts, out, now, performers, genres)
}

func sendErr(ctx context.Context, out chan<- scraper.SceneResult, err error) {
	select {
	case out <- scraper.Error(err):
	case <-ctx.Done():
	}
}

func (s *Scraper) paginate(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult, now time.Time, performers, genres map[int]string) {
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wp/v2/product?per_page=%d&page=%d&product_cat=%d&orderby=date&order=desc&_fields=id,date_gmt,link,title,content,featured_media,model,genre",
			siteBase, perPage, page, videosCat)
		var products []product
		total, pages, err := s.getJSON(ctx, u, &products)
		if err != nil {
			var se *httpx.StatusError
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}

		ids := make([]string, 0, len(products))
		var mediaIDs []string
		for _, p := range products {
			ids = append(ids, strconv.Itoa(p.ID))
			if p.Media > 0 {
				mediaIDs = append(mediaIDs, strconv.Itoa(p.Media))
			}
		}
		prices := s.fetchPrices(ctx, ids)
		images := s.fetchMedia(ctx, mediaIDs)

		scenes := make([]models.Scene, 0, len(products))
		for _, p := range products {
			sc := toScene(p, studioURL, now, performers, genres)
			sc.Thumbnail = images[p.Media]
			if sp, ok := prices[p.ID]; ok {
				addPrice(&sc, sp, now)
			}
			scenes = append(scenes, sc)
		}
		res := scraper.PageResult{Scenes: scenes, Done: len(products) < perPage || (pages > 0 && page >= pages)}
		if page == 1 {
			res.Total = total
		}
		return res, nil
	})
}

func (s *Scraper) fetchTerms(ctx context.Context, taxonomy string) (map[int]string, error) {
	names := map[int]string{}
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return names, ctx.Err()
		}
		u := fmt.Sprintf("%s/wp-json/wp/v2/%s?per_page=100&page=%d&_fields=id,name", siteBase, taxonomy, page)
		var terms []term
		_, pages, err := s.getJSON(ctx, u, &terms)
		if err != nil {
			return names, err
		}
		for _, t := range terms {
			names[t.ID] = clean(t.Name)
		}
		if len(terms) < 100 || page >= pages {
			return names, nil
		}
	}
}

// fetchPrices and fetchMedia are enrichments: a failure costs the price or the
// image, not the scene, so it only logs.
func (s *Scraper) fetchPrices(ctx context.Context, ids []string) map[int]storeProduct {
	out := map[int]storeProduct{}
	if len(ids) == 0 {
		return out
	}
	u := fmt.Sprintf("%s/wp-json/wc/store/products?per_page=100&include=%s", siteBase, strings.Join(ids, ","))
	var items []storeProduct
	if _, _, err := s.getJSON(ctx, u, &items); err != nil {
		scraper.Debugf(1, "%s: prices unavailable: %v", siteID, err)
		return out
	}
	for _, it := range items {
		out[it.ID] = it
	}
	return out
}

func (s *Scraper) fetchMedia(ctx context.Context, ids []string) map[int]string {
	out := map[int]string{}
	if len(ids) == 0 {
		return out
	}
	u := fmt.Sprintf("%s/wp-json/wp/v2/media?per_page=100&include=%s&_fields=id,source_url", siteBase, strings.Join(ids, ","))
	var items []struct {
		ID        int    `json:"id"`
		SourceURL string `json:"source_url"`
	}
	if _, _, err := s.getJSON(ctx, u, &items); err != nil {
		scraper.Debugf(1, "%s: media unavailable: %v", siteID, err)
		return out
	}
	for _, it := range items {
		out[it.ID] = it.SourceURL
	}
	return out
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
		var se *json.SyntaxError
		if errors.As(err, &se) {
			return 0, 0, scraper.ParseError(u, err)
		}
		return 0, 0, err
	}
	return total, pages, nil
}

func toScene(p product, studioURL string, now time.Time, performers, genres map[int]string) models.Scene {
	sc := models.Scene{
		ID:          strconv.Itoa(p.ID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       clean(p.Title.Rendered),
		URL:         p.Link,
		Description: clean(strings.ReplaceAll(p.Content.Rendered, "</p>", "\n")),
		Studio:      studioName,
		ScrapedAt:   now,
	}
	if t, err := time.Parse("2006-01-02T15:04:05", p.Date); err == nil {
		sc.Date = t.UTC()
	}
	for _, id := range p.Model {
		if n := performers[id]; n != "" {
			sc.Performers = append(sc.Performers, n)
		}
	}
	for _, id := range p.Genre {
		if n := genres[id]; n != "" {
			sc.Tags = append(sc.Tags, n)
		}
	}
	return sc
}

func addPrice(sc *models.Scene, sp storeProduct, now time.Time) {
	price, err := strconv.Atoi(sp.Prices.Price)
	if err != nil || price <= 0 {
		return
	}
	scale := 1.0
	for i := 0; i < sp.Prices.MinorUnit; i++ {
		scale *= 10
	}
	snap := models.PriceSnapshot{Date: now, Regular: float64(price) / scale}
	if reg, err := strconv.Atoi(sp.Prices.RegularPrice); err == nil && reg > price {
		snap.Regular = float64(reg) / scale
		snap.Discounted = float64(price) / scale
		snap.IsOnSale = true
	}
	sc.AddPrice(snap)
}

// clean keeps paragraph breaks: only tags and runs of spaces are collapsed.
func clean(s string) string {
	s = html.UnescapeString(stripRe.ReplaceAllString(s, " "))
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

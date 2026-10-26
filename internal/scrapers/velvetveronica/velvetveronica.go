// Package velvetveronica scrapes Velvet Veronica (velvetveronica.com), a
// WooCommerce clip shop, through the store's public Store API. See
// docs/scrapers.md.
package velvetveronica

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
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "velvetveronica"
	studioName = "Velvet Veronica"
	performer  = "Velvet Veronica"
	perPage    = 100
	// videosCategory is the shop's "Videos" category. Its sub-categories
	// (bundles, sponsor-a-video) are purchases, not scenes, and are dropped.
	videosCategory = 29
)

var siteBase = "https://velvetveronica.com"

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?velvetveronica\.com(?:/|$)`)
	durationRe = regexp.MustCompile(`(?i)(\d{1,2}:\d{2}(?::\d{2})?)\s*min`)
	codeRe     = regexp.MustCompile(`\s*\(([A-Z]{1,4}\d{1,4})\)\s*$`)
	tagStripRe = regexp.MustCompile(`<[^>]+>`)
	tableRe    = regexp.MustCompile(`(?is)<table.*?</table>`)
)

type Scraper struct {
	Client *http.Client
}

func New() *Scraper { return &Scraper{Client: httpx.NewClient(30 * time.Second)} }

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"velvetveronica.com", "velvetveronica.com/product-category/videos/"}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type product struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Permalink   string `json:"permalink"`
	Description string `json:"description"`
	Prices      struct {
		Price        string `json:"price"`
		RegularPrice string `json:"regular_price"`
		Currency     string `json:"currency_code"`
		Minor        int    `json:"currency_minor_unit"`
	} `json:"prices"`
	Images []struct {
		Src string `json:"src"`
	} `json:"images"`
	Categories []struct {
		Slug string `json:"slug"`
	} `json:"categories"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		u := fmt.Sprintf("%s/wp-json/wc/store/v1/products?category=%d&per_page=%d&page=%d&orderby=date&order=desc",
			siteBase, videosCategory, perPage, page)
		resp, err := httpx.Do(ctx, s.Client, httpx.Request{URL: u, Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, siteBase)})
		if err != nil {
			var se *httpx.StatusError
			// The Store API answers 400 for a page past the last.
			if page > 1 && errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		total, _ := strconv.Atoi(resp.Header.Get("X-WP-Total"))
		pages, _ := strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
		var products []product
		if err := httpx.DecodeJSON(resp.Body, &products); err != nil {
			return scraper.PageResult{}, scraper.ParseError(u, err)
		}
		var scenes []models.Scene
		for _, p := range products {
			if !isScene(p) {
				continue
			}
			scenes = append(scenes, toScene(p, studioURL, now))
		}
		return scraper.PageResult{
			Scenes:   scenes,
			Total:    total,
			Done:     len(products) < perPage || (pages > 0 && page >= pages),
			Continue: len(products) > 0 && len(scenes) == 0,
		}, nil
	})
}

// isScene keeps products filed only under Videos: bundles and sponsorships
// sit in Videos sub-categories.
func isScene(p product) bool {
	for _, c := range p.Categories {
		if c.Slug != "videos" {
			return false
		}
	}
	return true
}

func toScene(p product, studioURL string, now time.Time) models.Scene {
	name := cleanText(p.Name)
	name = strings.TrimSpace(strings.TrimLeft(name, "🎥 "))
	// The trailing (HJ76)-style product code is the shop's SKU, not part of
	// the title.
	name = strings.TrimSpace(codeRe.ReplaceAllString(name, ""))
	// The description ends in a spec table (format, runtime, size); the
	// runtime is read from it and the table is kept out of the text.
	desc := cleanText(tableRe.ReplaceAllString(p.Description, " "))
	sc := models.Scene{
		ID:          strconv.Itoa(p.ID),
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       name,
		URL:         p.Permalink,
		Description: desc,
		Studio:      studioName,
		Performers:  []string{performer},
		ScrapedAt:   now,
	}
	if m := durationRe.FindStringSubmatch(tagStripRe.ReplaceAllString(p.Description, " ")); m != nil {
		sc.Duration = parseutil.ParseDurationColon(m[1])
	}
	if len(p.Images) > 0 {
		sc.Thumbnail = p.Images[0].Src
	}
	for _, t := range p.Tags {
		if n := cleanText(t.Name); n != "" {
			sc.Tags = append(sc.Tags, n)
		}
	}
	if amount, err := strconv.Atoi(p.Prices.Price); err == nil && amount > 0 {
		regular, _ := strconv.Atoi(p.Prices.RegularPrice)
		snap := models.PriceSnapshot{Date: now, Regular: minor(amount, p.Prices.Minor)}
		if regular > amount {
			snap.Regular = minor(regular, p.Prices.Minor)
			snap.Discounted = minor(amount, p.Prices.Minor)
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

func cleanText(s string) string {
	s = html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}

// Package tma scrapes Total Media Agency (tma.co.jp), a Shopify storefront
// selling each release as a product. See docs/scrapers.md.
package tma

import (
	"context"
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
	siteID     = "tma"
	studioName = "Total Media Agency"
	siteBase   = "https://www.tma.co.jp"
	pageSize   = 250
)

var (
	matchRe      = regexp.MustCompile(`^https?://(?:www\.)?tma\.co\.jp(?:/|$)`)
	collectionRe = regexp.MustCompile(`/collections/([^/?#]+)`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
)

// photoTypes are product types that are not video releases.
var photoTypes = map[string]bool{"AI写真集": true, "写真集": true}

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"tma.co.jp",
		"tma.co.jp/collections/{collection}",
	}
}

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	apiPath := "/products.json"
	// "vendors" is a search page, not a collection: it has no products.json.
	if m := collectionRe.FindStringSubmatch(studioURL); m != nil && m[1] != "all" && m[1] != "vendors" {
		apiPath = "/collections/" + m[1] + "/products.json"
		scraper.Debugf(1, "tma: scraping collection %s", m[1])
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		products, err := s.fetchPage(ctx, apiPath, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		if len(products) == 0 {
			return scraper.PageResult{}, nil
		}
		scenes := make([]models.Scene, 0, len(products))
		for _, p := range products {
			if photoTypes[p.ProductType] {
				continue
			}
			scenes = append(scenes, toScene(p, studioURL, now))
		}
		// A page of nothing but photo books is not the end of the catalogue.
		return scraper.PageResult{
			Scenes:   scenes,
			Continue: len(scenes) == 0,
			Done:     len(products) < pageSize,
		}, nil
	})
}

type product struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Handle      string   `json:"handle"`
	BodyHTML    string   `json:"body_html"`
	PublishedAt string   `json:"published_at"`
	Vendor      string   `json:"vendor"`
	ProductType string   `json:"product_type"`
	Tags        []string `json:"tags"`
	Variants    []struct {
		Price string `json:"price"`
	} `json:"variants"`
	Images []struct {
		Src string `json:"src"`
	} `json:"images"`
}

func (s *Scraper) fetchPage(ctx context.Context, apiPath string, page int) ([]product, error) {
	u := fmt.Sprintf("%s%s?limit=%d&page=%d", s.base, apiPath, pageSize, page)
	var payload struct {
		Products []product `json:"products"`
	}
	if err := httpx.DoJSON(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	}, &payload); err != nil {
		return nil, err
	}
	return payload.Products, nil
}

// toScene maps a Shopify product to a scene. The handle is the release's own
// catalogue code, which is what survives a retitle.
func toScene(p product, studioURL string, now time.Time) models.Scene {
	scene := models.Scene{
		ID:          p.Handle,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       strings.TrimSpace(p.Title),
		URL:         siteBase + "/products/" + p.Handle,
		Studio:      studio(p.Vendor),
		Description: plainText(p.BodyHTML),
		Tags:        p.Tags,
		ScrapedAt:   now,
	}
	if p.ProductType != "" {
		scene.Categories = []string{p.ProductType}
	}
	if len(p.Images) > 0 {
		scene.Thumbnail = p.Images[0].Src
	}
	if t, err := time.Parse(time.RFC3339, p.PublishedAt); err == nil {
		scene.Date = t.UTC()
	}
	if len(p.Variants) > 0 {
		if price, err := strconv.ParseFloat(p.Variants[0].Price, 64); err == nil && price > 0 {
			scene.AddPrice(models.PriceSnapshot{Date: now, Regular: price})
		}
	}
	return scene
}

// studio prefers the product's own vendor: the store carries sister labels
// (I.B.WORKS, Aozora Soft) alongside TMA's own releases.
func studio(vendor string) string {
	if v := strings.TrimSpace(vendor); v != "" {
		return v
	}
	return studioName
}

func plainText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagStripRe.ReplaceAllString(s, " "))), " ")
}

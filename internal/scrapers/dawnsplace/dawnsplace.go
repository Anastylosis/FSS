package dawnsplace

import (
	"context"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
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
	siteID    = "dawnsplace"
	studio    = "Dawn's Place"
	performer = "Dawn Allison"
	perPage   = 100
	// videosCategory is "Videos Downloads Sorted By Year", the parent of every
	// per-year video category. Filtering on it includes the children and leaves
	// out photo zips, combos, custom orders and tips.
	videosCategory = 16
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://dawnsplace.com/store",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"dawnsplace.com",
		"dawnsplace.com/store",
		"dawnsplace.com/store/product-category/{slug}",
		"dawnsplace.com/store/product-category/{parent}/{slug}",
	}
}

var (
	matchRe    = regexp.MustCompile(`^https?://(?:www\.)?dawnsplace\.com(?:/|$)`)
	categoryRe = regexp.MustCompile(`/product-category/(?:[^/?#]+/)*([^/?#]+)/?(?:[?#]|$)`)
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) headers() map[string]string {
	return httpx.XHRHeaders(httpx.UserAgentFirefox, "")
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	category := videosCategory
	if m := categoryRe.FindStringSubmatch(studioURL); m != nil {
		id, err := s.categoryID(ctx, m[1])
		if err != nil {
			select {
			case out <- scraper.Error(err):
			case <-ctx.Done():
			}
			return
		}
		category = id
		scraper.Debugf(1, "%s: scraping category %q (id %d)", siteID, m[1], id)
	}

	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		products, total, pages, err := s.fetchProducts(ctx, category, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, 0, len(products))
		for _, p := range products {
			scenes = append(scenes, toScene(p, studioURL, now))
		}
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(products) < perPage || (pages > 0 && page >= pages),
		}, nil
	})
}

type category struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type product struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	Permalink   string     `json:"permalink"`
	Description string     `json:"description"`
	OnSale      bool       `json:"on_sale"`
	Categories  []category `json:"categories"`
	Tags        []category `json:"tags"`
	Prices      struct {
		Price        string `json:"price"`
		RegularPrice string `json:"regular_price"`
		SalePrice    string `json:"sale_price"`
		MinorUnit    int    `json:"currency_minor_unit"`
	} `json:"prices"`
	Images []struct {
		Src string `json:"src"`
	} `json:"images"`
}

func (s *Scraper) categoryID(ctx context.Context, slug string) (int, error) {
	u := s.base + "/wp-json/wc/store/v1/products/categories?per_page=100"
	var cats []category
	if err := httpx.DoJSON(ctx, s.client, httpx.Request{URL: u, Headers: s.headers()}, &cats); err != nil {
		return 0, err
	}
	for _, c := range cats {
		if c.Slug == slug {
			return c.ID, nil
		}
	}
	return 0, fmt.Errorf("%s: unknown product category %q", siteID, slug)
}

func (s *Scraper) fetchProducts(ctx context.Context, cat, page int) ([]product, int, int, error) {
	q := url.Values{}
	q.Set("category", strconv.Itoa(cat))
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("page", strconv.Itoa(page))
	q.Set("orderby", "date")
	q.Set("order", "desc")
	u := s.base + "/wp-json/wc/store/v1/products?" + q.Encode()

	resp, err := httpx.Do(ctx, s.client, httpx.Request{URL: u, Headers: s.headers()})
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var products []product
	if err := httpx.DecodeJSON(resp.Body, &products); err != nil {
		return nil, 0, 0, scraper.ParseError(u, err)
	}
	total, _ := strconv.Atoi(resp.Header.Get("X-WP-Total"))
	pages, _ := strconv.Atoi(resp.Header.Get("X-WP-TotalPages"))
	return products, total, pages, nil
}

var (
	tagRe        = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>`)
	stripRe      = regexp.MustCompile(`<[^>]+>`)
	spaceRe      = regexp.MustCompile(`[ \t\x{a0}]+`)
	durationRe   = regexp.MustCompile(`(?i)(\d+(?::\d{2}){1,2})\s*min`)
	approxMinRe  = regexp.MustCompile(`(?i)approx\.?\s*(\d+)\s*min`)
	resolutionRe = regexp.MustCompile(`(\d{3,4})\s*×\s*(\d{3,4})`)
	techLineRe   = regexp.MustCompile(`(?i)\d{3,4}\s*×\s*\d{3,4}|kbps|hi-res format`)
	leadInRe     = regexp.MustCompile(`(?i)^(?:(?:and|&)\s*\d+\s*images\s*[–-]\s*|-\s*)`)
)

// descriptionLines turns the product description HTML into trimmed text lines.
func descriptionLines(raw string) []string {
	text := tagRe.ReplaceAllString(raw, "\n")
	text = html.UnescapeString(stripRe.ReplaceAllString(text, ""))
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(spaceRe.ReplaceAllString(l, " "))
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func toScene(p product, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:         strconv.Itoa(p.ID),
		SiteID:     siteID,
		StudioURL:  studioURL,
		Title:      strings.TrimSpace(html.UnescapeString(p.Name)),
		URL:        p.Permalink,
		Studio:     studio,
		Performers: []string{performer},
		ScrapedAt:  now,
	}

	var desc []string
	for _, l := range descriptionLines(p.Description) {
		if techLineRe.MatchString(l) {
			if sc.Duration == 0 {
				if m := durationRe.FindStringSubmatch(l); m != nil {
					sc.Duration = parseutil.ParseDurationColon(m[1])
				} else if m := approxMinRe.FindStringSubmatch(l); m != nil {
					n, _ := strconv.Atoi(m[1])
					sc.Duration = n * 60
				}
			}
			if sc.Height == 0 {
				if m := resolutionRe.FindStringSubmatch(l); m != nil {
					sc.Width, _ = strconv.Atoi(m[1])
					sc.Height, _ = strconv.Atoi(m[2])
					sc.Resolution = m[2] + "p"
				}
			}
			continue
		}
		if l = strings.TrimSpace(leadInRe.ReplaceAllString(l, "")); l != "" {
			desc = append(desc, l)
		}
	}
	sc.Description = strings.Join(desc, "\n")

	for _, img := range p.Images {
		if sc.Thumbnail == "" && !strings.HasSuffix(strings.ToLower(img.Src), ".gif") {
			sc.Thumbnail = img.Src
		}
	}
	if sc.Thumbnail == "" && len(p.Images) > 0 {
		sc.Thumbnail = p.Images[0].Src
	}

	for _, c := range p.Categories {
		sc.Categories = append(sc.Categories, html.UnescapeString(c.Name))
	}
	for _, t := range p.Tags {
		sc.Tags = append(sc.Tags, html.UnescapeString(t.Name))
	}

	minor := p.Prices.MinorUnit
	regular := minorToFloat(p.Prices.RegularPrice, minor)
	if regular > 0 {
		snap := models.PriceSnapshot{Date: now, Regular: regular}
		if sale := minorToFloat(p.Prices.SalePrice, minor); p.OnSale && sale < regular {
			snap.IsOnSale = true
			snap.Discounted = sale
			snap.DiscountPercent = int(math.Round((regular - sale) / regular * 100))
			snap.IsFree = sale == 0
		}
		sc.AddPrice(snap)
	}
	return sc
}

func minorToFloat(s string, minor int) float64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return float64(n) / math.Pow10(minor)
}

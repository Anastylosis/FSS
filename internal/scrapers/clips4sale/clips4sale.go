package clips4sale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
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
	defaultSiteBase  = "https://www.clips4sale.com"
	defaultPageLimit = 24
	// routeKey is the Remix loader data key embedded in the page HTML.
	routeKey = `routes/($lang).studio.$id_.$studioSlug.$`
	// remixMarker is the script prefix before the JSON context object.
	remixMarker = "window.__remixContext = "
)

// Scraper implements scraper.StudioScraper for Clips4Sale.
type Scraper struct {
	client    *http.Client
	siteBase  string
	pageLimit int
}

func New() *Scraper {
	return &Scraper{
		client:    httpx.NewClient(30 * time.Second),
		siteBase:  defaultSiteBase,
		pageLimit: defaultPageLimit,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

// ---- StudioScraper interface ----

func (s *Scraper) ID() string { return "clips4sale" }

func (s *Scraper) Patterns() []string {
	return []string{
		"clips4sale.com/studio/{studioId}/{studioSlug}",
	}
}

// studioRe matches studio URLs but not individual clip URLs. Studio slugs
// start with a letter; clip IDs are purely numeric. The slug is optional: the
// site publishes bare /studio/{id} links and redirects them to the slugged form.
var studioRe = regexp.MustCompile(`^https?://(?:www\.)?clips4sale\.com/studio/(\d+)(?:/([a-zA-Z][^/?]*)|/?(?:$|\?))`)

func (s *Scraper) MatchesURL(u string) bool {
	return studioRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	sid, slug, err := studioParams(studioURL)
	if err != nil {
		return nil, err
	}
	if slug == "" {
		if slug, err = s.resolveSlug(ctx, sid); err != nil {
			return nil, err
		}
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, sid, slug, opts, out)
	return out, nil
}

// ---- worker orchestration ----

// run paginates the Clips4Sale studio page HTML. All metadata is embedded in
// window.__remixContext, so no separate API call is needed. KnownIDs are
// skipped per-clip rather than stopping pagination early, because the default
// C4SSort-recommended ordering is not date-ordered.
func (s *Scraper) run(ctx context.Context, studioURL, sid, slug string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()

	// C4S uses recommended sort, not date order, so KnownIDs early-stop
	// cannot be used. Clear KnownIDs so Paginate never triggers it.
	paginateOpts := opts
	paginateOpts.KnownIDs = nil

	scraper.Paginate(ctx, paginateOpts, "clips4sale", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		clips, clipsCount, err := s.fetchPage(ctx, sid, slug, page)
		if err != nil {
			return scraper.PageResult{}, err
		}

		var scenes []models.Scene
		for _, clip := range clips {
			scene, sceneErr := toScene(studioURL, s.siteBase, clip, now)
			if sceneErr != nil {
				select {
				case out <- scraper.Error(sceneErr):
				case <-ctx.Done():
				}
				continue
			}
			scenes = append(scenes, scene)
		}
		// C4S may return fewer than pageLimit clips per page even mid-catalogue.
		// Stop only when the page is empty; one extra empty-page request is fine.
		return scraper.PageResult{Scenes: scenes, Total: clipsCount}, nil
	})
}

// ---- page fetch ----

func (s *Scraper) fetchPage(ctx context.Context, studioID, slug string, page int) ([]c4sClip, int, error) {
	u := fmt.Sprintf(
		"%s/studio/%s/%s/Cat0-AllCategories/Page%d/C4SSort-recommended/Limit%d/?onlyClips=true",
		s.siteBase, studioID, slug, page, s.pageLimit,
	)
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("reading response: %w", err)
	}

	return extractClips(body)
}

// extractClips finds window.__remixContext in page HTML and returns the clips
// embedded in the route loader data, along with the total clip count.
func extractClips(body []byte) ([]c4sClip, int, error) {
	idx := bytes.Index(body, []byte(remixMarker))
	if idx < 0 {
		return nil, 0, fmt.Errorf("remixContext not found in page")
	}

	var rctx remixContext
	if err := json.NewDecoder(bytes.NewReader(body[idx+len(remixMarker):])).Decode(&rctx); err != nil {
		return nil, 0, fmt.Errorf("parsing remixContext: %w", err)
	}

	raw, ok := rctx.State.LoaderData[routeKey]
	if !ok {
		return nil, 0, fmt.Errorf("route key %q not found in loaderData", routeKey)
	}

	var ld loaderData
	if err := json.Unmarshal(raw, &ld); err != nil {
		return nil, 0, fmt.Errorf("parsing loader data: %w", err)
	}

	return ld.Clips, ld.ClipsCount, nil
}

// ---- mapping ----

func toScene(studioURL, siteBase string, clip c4sClip, now time.Time) (models.Scene, error) {
	// Tags: related categories + keywords, deduped.
	seen := make(map[string]bool)
	var tags []string
	for _, rc := range clip.RelatedCategoryLinks {
		if rc.Category != "" && !seen[rc.Category] {
			seen[rc.Category] = true
			tags = append(tags, rc.Category)
		}
	}
	for _, kw := range clip.KeywordLinks {
		if kw.Keyword != "" && !seen[kw.Keyword] {
			seen[kw.Keyword] = true
			tags = append(tags, kw.Keyword)
		}
	}

	performers := make([]string, 0, len(clip.Performers))
	for _, p := range clip.Performers {
		if p.StageName != "" {
			performers = append(performers, p.StageName)
		}
	}

	width, height := parseScreenSize(clip.ScreenSize)

	var categories []string
	if clip.CategoryName != "" {
		categories = []string{clip.CategoryName}
	}

	scene := models.Scene{
		ID:          clip.ClipID,
		SiteID:      "clips4sale",
		StudioURL:   studioURL,
		Title:       clip.Title,
		URL:         siteBase + clip.Link,
		Date:        parseDate(clip.DateDisplay),
		Description: stripHTML(clip.Description),
		Thumbnail:   clip.CDNPreviewLgLink,
		Preview:     clip.CustomPreviewURL,
		Performers:  performers,
		Studio:      clip.StudioTitle,
		Tags:        tags,
		Categories:  categories,
		Duration:    int(clip.TimeMinutes * 60),
		Resolution:  strings.ToUpper(clip.ResolutionText),
		Width:       width,
		Height:      height,
		Format:      strings.ToUpper(clip.Format),
		ScrapedAt:   now,
	}

	discounted, percent := 0.0, 0
	if clip.DiscountedPrice != nil {
		discounted = clip.DiscountedPrice.Discount
		percent = clip.DiscountedPrice.Percent
	}
	isOnSale := clip.OnSale != nil && *clip.OnSale
	if discounted > 0 {
		// The sale object is the authority. A clip has been seen carrying one
		// with onSale still null, and recording a discount the snapshot then
		// ignores would understate LowestPrice.
		isOnSale = true
	}
	scene.AddPrice(models.PriceSnapshot{
		Date:            now,
		Regular:         clip.Price,
		Discounted:      discounted,
		IsOnSale:        isOnSale,
		DiscountPercent: percent,
	})

	return scene, nil
}

// ---- helpers ----

// resolveSlug follows the bare /studio/{id} redirect to learn the slug, which
// the paginated listing URL needs.
func (s *Scraper) resolveSlug(ctx context.Context, sid string) (string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     fmt.Sprintf("%s/studio/%s", s.siteBase, sid),
		Headers: httpx.BrowserHeaders(httpx.UserAgentChrome),
	})
	if err != nil {
		return "", fmt.Errorf("resolving studio %s: %w", sid, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if m := studioRe.FindStringSubmatch(resp.Request.URL.String()); m != nil && m[2] != "" {
		return m[2], nil
	}
	return "", fmt.Errorf("studio %s: no slug in %s", sid, resp.Request.URL)
}

func studioParams(u string) (id, slug string, err error) {
	m := studioRe.FindStringSubmatch(u)
	if m == nil {
		return "", "", fmt.Errorf("cannot extract studio params from %q", u)
	}
	return m[1], m[2], nil
}

// parseDate parses Clips4Sale date strings like "1/24/25 10:22 PM".
func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse("1/2/06 3:04 PM", s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

var (
	htmlBlockRe  = regexp.MustCompile(`(?i)\s*<\s*(?:br\s*/?\s*|/?p|/?div|/?li)\s*>\s*`)
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	multiSpaceRe = regexp.MustCompile(`[ \t]{3,}`)
	blankLinesRe = regexp.MustCompile(`\n{3,}`)
)

func stripHTML(s string) string {
	s = html.UnescapeString(s)
	s = htmlBlockRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = multiSpaceRe.ReplaceAllString(s, "\n")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func parseScreenSize(s string) (width, height int) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	return w, h
}

// ---- remixContext types ----

type remixContext struct {
	State struct {
		LoaderData map[string]json.RawMessage `json:"loaderData"`
	} `json:"state"`
}

type loaderData struct {
	Clips      []c4sClip `json:"clips"`
	ClipsCount int       `json:"clipsCount"`
	Page       int       `json:"page"`
}

// ---- clip struct ----

type c4sClip struct {
	ClipID               string          `json:"clipId"`
	Title                string          `json:"title"`
	Link                 string          `json:"link"`
	DateDisplay          string          `json:"date_display"`
	Description          string          `json:"description"`
	CDNPreviewLgLink     string          `json:"cdn_previewlg_link"`
	CustomPreviewURL     string          `json:"customPreviewUrl"`
	Performers           []c4sPerformer  `json:"performers"`
	StudioTitle          string          `json:"studioTitle"`
	CategoryName         string          `json:"category_name"`
	RelatedCategoryLinks []c4sRelatedCat `json:"related_category_links"`
	KeywordLinks         []c4sKeyword    `json:"keyword_links"`
	TimeMinutes          float64         `json:"time_minutes"`
	ResolutionText       string          `json:"resolution_text"`
	ScreenSize           string          `json:"screen_size"`
	Format               string          `json:"format"`
	Price                float64         `json:"price"`
	DiscountedPrice      *c4sDiscount    `json:"discounted_price"`
	OnSale               *bool           `json:"onSale"`
}

// c4sDiscount is clips4sale's `discounted_price`. It is null on a clip that is
// not on sale and an object describing the sale on one that is — decoding it as
// a bare float64 failed the whole page the moment one discounted clip appeared
// on it, losing that page's other 23 clips with it. A bare number is still
// accepted because that is the shape this field used to have.
//
// `discount` is the price actually charged, not the amount taken off: a clip
// listed at 18.99 with percent 50 carries discount 9.5, and the clip page
// renders 18.99 struck through followed by 9.50.
type c4sDiscount struct {
	SaleType string  `json:"sale_type"`
	Percent  int     `json:"percent"`
	Discount float64 `json:"discount"`
}

func (d *c4sDiscount) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if len(b) > 0 && b[0] == '{' {
		type raw c4sDiscount
		var r raw
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		*d = c4sDiscount(r)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("discounted_price is neither an object nor a number: %s", b)
	}
	d.Discount = f
	return nil
}

type c4sPerformer struct {
	StageName string `json:"stage_name"`
}

type c4sRelatedCat struct {
	Category string `json:"category"`
}

type c4sKeyword struct {
	Keyword string `json:"keyword"`
}

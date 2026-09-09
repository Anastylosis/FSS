package yezzclips

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// siteHost is the production origin, used for the canonical studio key. Page
// fetches derive their origin from the studio URL instead, so a test server
// can drive the scraper end to end.
const siteHost = "https://www.yezzclips.com"

// pageSize is how many clips a store listing page holds. A short page is the
// last one.
const pageSize = 20

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(45 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)
var _ scraper.StudioURLCanonicalizer = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "yezzclips" }

func (s *Scraper) Patterns() []string {
	return []string{
		"yezzclips.com/store_view.php?id={id}",
		"yezzclips.com/store_view.php?id={id}&item={item}",
	}
}

var hostRe = regexp.MustCompile(`^https?://(?:www\.)?yezzclips\.com/store_view\.php\?`)

// MatchesURL claims store pages only. main.php is a site-wide index of every
// store on the site, so scraping it would file 120-odd unrelated storefronts
// under one studio key.
func (s *Scraper) MatchesURL(u string) bool {
	return hostRe.MatchString(u) && storeID(u) != ""
}

// param returns a query parameter of u, or "" if it is absent or not a number.
func param(u, name string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	v := parsed.Query().Get(name)
	if v == "" {
		return ""
	}
	if _, err := strconv.Atoi(v); err != nil {
		return ""
	}
	return v
}

func storeID(u string) string { return param(u, "id") }
func itemID(u string) string  { return param(u, "item") }

// PreferredStudioURL drops the item and page parameters so that a single-clip
// URL, a deep page of the listing and the bare store URL all key to one
// studio rather than three.
func (s *Scraper) PreferredStudioURL(studioURL string) string {
	if !s.MatchesURL(studioURL) {
		return ""
	}
	return siteHost + "/store_view.php?id=" + storeID(studioURL)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	id := storeID(studioURL)
	if id == "" {
		return nil, fmt.Errorf("cannot extract yezzclips store id from %q", studioURL)
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, id, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL, id string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	origin := originOf(studioURL)

	// A URL naming a single clip scrapes just that clip. The page it lives on
	// also carries the store's first listing page, so the wanted block is
	// picked by item id rather than by position.
	if item := itemID(studioURL); item != "" {
		scraper.Debugf(1, "yezzclips: scraping single item %s", item)
		s.runItem(ctx, studioURL, origin, id, item, now, out)
		return
	}

	scraper.Debugf(1, "yezzclips: scraping store %s", id)
	scraper.Paginate(ctx, opts, "yezzclips", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/store_view.php?id=%s&page=%d", origin, id, page)
		body, final, err := s.fetch(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}
		// Past the last page — and for a store id that does not exist — the
		// site 302s to an age-gate splash. That page parses to zero clips, so
		// the redirect is caught explicitly instead: past page 1 it is simply
		// the end of the listing, but on page 1 it means the store was never
		// there, which must stay loud rather than save an empty catalogue.
		if !strings.Contains(final, "store_view.php") {
			if page > 1 {
				scraper.Debugf(1, "yezzclips: page %d redirected away — done", page)
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, scraper.ParseError(pageURL,
				fmt.Errorf("store %s redirected to %s (no such store?)", id, final))
		}

		blocks := clipBlocks(body)
		store := storeName(body)
		scenes := make([]models.Scene, 0, len(blocks))
		for _, b := range blocks {
			if sc, ok := toScene(b, studioURL, origin, id, store, now); ok {
				scenes = append(scenes, sc)
			}
		}
		return scraper.PageResult{Scenes: scenes, Done: len(blocks) < pageSize}, nil
	})
}

func (s *Scraper) runItem(ctx context.Context, studioURL, origin, id, item string, now time.Time, out chan<- scraper.SceneResult) {
	pageURL := fmt.Sprintf("%s/store_view.php?id=%s&item=%s", origin, id, item)
	body, final, err := s.fetch(ctx, pageURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	if !strings.Contains(final, "store_view.php") {
		send(ctx, out, scraper.Error(scraper.ParseError(pageURL,
			fmt.Errorf("item %s redirected to %s", item, final))))
		return
	}
	store := storeName(body)
	for _, b := range clipBlocks(body) {
		if clipID(b) != item {
			continue
		}
		if sc, ok := toScene(b, studioURL, origin, id, store, now); ok {
			send(ctx, out, scraper.Scene(sc))
		}
		return
	}
	send(ctx, out, scraper.Error(scraper.ParseError(pageURL,
		fmt.Errorf("item %s not found on its own page", item))))
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) {
	select {
	case out <- r:
	case <-ctx.Done():
	}
}

// originOf returns the scheme://host of u, so page and scene URLs stay on
// whatever host the run was pointed at.
func originOf(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return siteHost
	}
	return parsed.Scheme + "://" + parsed.Host
}

// ---- fetch ----

// fetch returns the decoded page and the URL it was finally served from, which
// differs from the requested one when the site redirects.
func (s *Scraper) fetch(ctx context.Context, rawURL string) (string, string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", rawURL, err)
	}
	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return decodeCP1252(raw), final, nil
}

// decodeCP1252 converts a page body to UTF-8. Every page declares
// iso-8859-1 but the bytes are really windows-1252 — smart quotes arrive as
// 0x91-0x94, which iso-8859-1 leaves undefined — and cp1252 decodes both. A
// body that is already valid UTF-8 is returned untouched so an ASCII-only
// page is never rewritten.
func decodeCP1252(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(raw)
	if err != nil {
		return string(raw)
	}
	return string(decoded)
}

// ---- parsing ----

var (
	blockSplit  = "<div class=\"row storeview_clip\">"
	clipIDRe    = regexp.MustCompile(`item_id=(\d+)`)
	titleRe     = regexp.MustCompile(`(?s)<h4[^>]*>(.*?)</h4>`)
	descRe      = regexp.MustCompile(`(?s)</h4>\s*<p>(.*?)</p>`)
	staticImgRe = regexp.MustCompile(`data-staticimg="([^"]+)"`)
	previewRe   = regexp.MustCompile(`data-loadaniimg="([^"]+)"`)
	lengthRe    = regexp.MustCompile(`Length:\s*([^<]+)`)
	formatRe    = regexp.MustCompile(`Format:\s*([^<\s]+)`)
	videoInfoRe = regexp.MustCompile(`Video Info:\s*(\d+)x(\d+)`)
	categoryRe  = regexp.MustCompile(`(?s)Category:\s*(.*?)(?:</div>|Size:)`)
	anchorRe    = regexp.MustCompile(`(?s)<a[^>]*>(.*?)</a>`)
	priceRe     = regexp.MustCompile(`class="text-right price">\s*([\d.,]+)`)
	storeNameRe = regexp.MustCompile(`(?s)<title>\s*(.*?)\s+at Yezzclips\.com`)
	tagRe       = regexp.MustCompile(`(?s)<[^>]+>`)
	hoursRe     = regexp.MustCompile(`(\d+)\s*h`)
	minsRe      = regexp.MustCompile(`(\d+)\s*min`)
	secsRe      = regexp.MustCompile(`(\d+)\s*sec`)
)

// clipBlocks splits a store or item page into its per-clip markup blocks.
func clipBlocks(body string) []string {
	parts := strings.Split(body, blockSplit)
	if len(parts) < 2 {
		return nil
	}
	return parts[1:]
}

func clipID(block string) string {
	if m := clipIDRe.FindStringSubmatch(block); m != nil {
		return m[1]
	}
	return ""
}

// storeName reads the storefront's display name out of the page title, which
// reads "<store> at Yezzclips.com".
func storeName(body string) string {
	if m := storeNameRe.FindStringSubmatch(body); m != nil {
		return clean(m[1])
	}
	return ""
}

// clean strips markup and resolves entities from a fragment of page text.
func clean(s string) string {
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}

// parseLength converts the site's "41min." running time to seconds. Hours and
// seconds are accepted too, though the catalogue is written in whole minutes.
func parseLength(s string) int {
	total := 0
	if m := hoursRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		total += h * 3600
	}
	if m := minsRe.FindStringSubmatch(s); m != nil {
		mins, _ := strconv.Atoi(m[1])
		total += mins * 60
	}
	if m := secsRe.FindStringSubmatch(s); m != nil {
		sec, _ := strconv.Atoi(m[1])
		total += sec
	}
	return total
}

// parsePrice reads a European-formatted amount ("19,99") as a number. Prices
// are listed in euro; PriceSnapshot carries no currency, so the figure is
// stored bare.
func parsePrice(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// "1.234,56" -> "1234.56"
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseCategories(block string) []string {
	m := categoryRe.FindStringSubmatch(block)
	if m == nil {
		return nil
	}
	var cats []string
	for _, a := range anchorRe.FindAllStringSubmatch(m[1], -1) {
		if c := clean(a[1]); c != "" {
			cats = append(cats, c)
		}
	}
	return cats
}

// toScene builds a scene from one clip block. The site publishes no release
// date anywhere on a store page, so Scene.Date is left zero.
func toScene(block, studioURL, origin, id, store string, now time.Time) (models.Scene, bool) {
	item := clipID(block)
	if item == "" {
		return models.Scene{}, false
	}

	sc := models.Scene{
		ID:        item,
		SiteID:    "yezzclips",
		StudioURL: studioURL,
		URL:       fmt.Sprintf("%s/store_view.php?id=%s&item=%s", origin, id, item),
		Studio:    store,
		ScrapedAt: now,
	}

	if m := titleRe.FindStringSubmatch(block); m != nil {
		sc.Title = clean(m[1])
	}
	if m := descRe.FindStringSubmatch(block); m != nil {
		sc.Description = clean(m[1])
	}
	if m := staticImgRe.FindStringSubmatch(block); m != nil {
		sc.Thumbnail = html.UnescapeString(m[1])
	}
	if m := previewRe.FindStringSubmatch(block); m != nil {
		sc.Preview = html.UnescapeString(m[1])
	}
	if m := lengthRe.FindStringSubmatch(block); m != nil {
		sc.Duration = parseLength(m[1])
	}
	if m := formatRe.FindStringSubmatch(block); m != nil {
		sc.Format = strings.ToUpper(clean(m[1]))
	}
	if m := videoInfoRe.FindStringSubmatch(block); m != nil {
		sc.Width, _ = strconv.Atoi(m[1])
		sc.Height, _ = strconv.Atoi(m[2])
		sc.Resolution = m[1] + "x" + m[2]
	}
	sc.Categories = parseCategories(block)

	if m := priceRe.FindStringSubmatch(block); m != nil {
		if v, ok := parsePrice(m[1]); ok {
			sc.AddPrice(models.PriceSnapshot{Date: now, Regular: v})
		}
	}

	return sc, true
}

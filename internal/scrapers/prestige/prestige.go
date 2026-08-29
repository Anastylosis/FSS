package prestige

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	defaultBase = "https://www.prestige-av.com"
	perPage     = 30
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "prestige" }

func (s *Scraper) Patterns() []string {
	return []string{
		"prestige-av.com/goods",
		"prestige-av.com/goods?maker={name}",
		"prestige-av.com/goods?label={name}",
		"prestige-av.com/goods?date={date}",
		"kanbi-av.com/",
		"kanbi-av.com/list/all/on_sale",
	}
}

var matchRe = regexp.MustCompile(
	`^https?://(?:www\.)?(?:prestige-av\.com(?:/?$|/goods(?:\?|$))|kanbi-av\.com(?:/|$))`,
)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- JSON API types ----

type listResponse struct {
	Data  []listProduct `json:"-"`
	Total int           `json:"-"`
	// TotalKnown reports whether Total came from the response. The bare-array
	// shape carries no count, so termination falls back to a short page.
	TotalKnown bool `json:"-"`
}

// firstToken returns the first non-whitespace byte of a JSON document, or 0.
func firstToken(b []byte) byte {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return c
		}
	}
	return 0
}

func (lr *listResponse) UnmarshalJSON(b []byte) error {
	// The endpoint answers in two shapes: an object carrying `data` and
	// `total`, or a bare array of products. Which one it is is decided by the
	// first token, not by whether the decode produced anything — an object
	// with an empty `data` is a legitimate "no results", and treating it as a
	// failed object decode made it fall through to the array decode and come
	// back as a format error.
	switch firstToken(b) {
	case '{':
		var obj struct {
			Data  []listProduct `json:"data"`
			Total int           `json:"total"`
		}
		if err := json.Unmarshal(b, &obj); err != nil {
			return fmt.Errorf("prestige: decoding listing object: %w", err)
		}
		lr.Data = obj.Data
		lr.Total = obj.Total
		lr.TotalKnown = true
		return nil
	case '[':
		var arr []listProduct
		if err := json.Unmarshal(b, &arr); err != nil {
			return fmt.Errorf("prestige: decoding listing array: %w", err)
		}
		lr.Data = arr
		// A bare array carries no catalogue size. Recording len(arr) as the
		// total made an exactly-full first page look like the whole
		// catalogue, and the walk stopped there.
		lr.Total = 0
		lr.TotalKnown = false
		return nil
	}
	return fmt.Errorf("prestige: unexpected listing response format")
}

type listProduct struct {
	UUID string `json:"uuid"`
	// SKU is decoded so the early-stop can compare against the same value
	// that becomes Scene.ID (see sceneIDFor). The listing endpoint does not
	// always include it; when absent, listingKeys falls back to the UUID.
	SKU []sku `json:"sku"`
}

type product struct {
	UUID      string        `json:"uuid"`
	Title     string        `json:"title"`
	Body      string        `json:"body"`
	PlayTime  int           `json:"playTime"`
	MgsStart  string        `json:"mgsStartAt"`
	Maker     namedEntity   `json:"maker"`
	Label     namedEntity   `json:"label"`
	Series    *namedEntity  `json:"series"`
	Genre     []namedEntity `json:"genre"`
	Actress   []namedEntity `json:"actress"`
	Directors []namedEntity `json:"directors"`
	Media     []media       `json:"media"`
	SKU       []sku         `json:"sku"`
}

type namedEntity struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type media struct {
	UUID string `json:"uuid"`
	Path string `json:"path"`
	Sort int    `json:"sort"`
}

type sku struct {
	UUID           string       `json:"uuid"`
	DeliveryItemID string       `json:"deliveryItemId"`
	Price          string       `json:"price"`
	SalesStartAt   string       `json:"salesStartAt"`
	Category       *skuCategory `json:"category"`
}

type skuCategory struct {
	Title string `json:"title"`
}

type makerEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// ---- domain helpers ----

func detectHost(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// ---- filters ----

type listingParams struct {
	makerID string
	labelID string
	date    string
}

func parseFilters(studioURL string) (maker, label, date string) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return
	}
	maker = u.Query().Get("maker")
	label = u.Query().Get("label")
	date = u.Query().Get("date")
	return
}

func (s *Scraper) resolveListingParams(ctx context.Context, studioURL string) (listingParams, error) {
	maker, label, date := parseFilters(studioURL)
	var params listingParams
	params.date = date

	if maker == "" && detectHost(studioURL) == "kanbi-av.com" {
		scraper.Debugf(1, "prestige: detected kanbi-av.com, filtering by KANBi maker")
		maker = "KANBi"
	}

	if maker != "" {
		scraper.Debugf(1, "prestige: resolving maker %q", maker)
		uuid, err := s.resolveMakerUUID(ctx, maker)
		if err != nil {
			return params, err
		}
		params.makerID = uuid
	}

	if label != "" {
		params.labelID = label
	}

	return params, nil
}

func (s *Scraper) resolveMakerUUID(ctx context.Context, name string) (string, error) {
	var makers []makerEntry
	if err := s.fetchJSON(ctx, s.base+"/api/maker", &makers); err != nil {
		return "", fmt.Errorf("fetching makers: %w", err)
	}
	for _, m := range makers {
		if strings.EqualFold(m.Name, name) {
			return m.UUID, nil
		}
	}
	return "", fmt.Errorf("prestige: maker %q not found", name)
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}

	params, err := s.resolveListingParams(ctx, studioURL)
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}

	work := make(chan listProduct)
	// hitKnown is set by a worker that fetches a scene already in KnownIDs.
	// The listing goroutine checks it between pages: the listing payload does
	// not reliably carry the SKU that becomes Scene.ID, so this post-detail
	// check is the only early-stop guaranteed to fire. listingKeys still
	// short-circuits earlier when the SKU is present.
	var hitKnown atomic.Bool
	var wg sync.WaitGroup
	scraper.Debugf(1, "prestige: fetching details with %d workers", workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				scene, fetchErr := s.fetchDetail(ctx, studioURL, item.UUID, opts.Delay)
				if fetchErr != nil {
					select {
					case out <- scraper.Error(fetchErr):
					case <-ctx.Done():
						return
					}
					continue
				}
				if opts.KnownIDs[scene.ID] {
					scraper.Debugf(1, "prestige: detail %s already known, stopping early", scene.ID)
					hitKnown.Store(true)
					continue
				}
				select {
				case out <- scraper.Scene(scene):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)

		seen := map[string]bool{}

		for page := 1; ; page++ {
			if page > 1 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}
			scraper.Debugf(1, "prestige: fetching page %d", page)

			apiURL := s.buildListURL(params, page)
			var lr listResponse
			if err := s.fetchJSON(ctx, apiURL, &lr); err != nil {
				select {
				case out <- scraper.Error(fmt.Errorf("page %d: %w", page, err)):
				case <-ctx.Done():
				}
				return
			}

			if len(lr.Data) == 0 {
				return
			}

			if page == 1 {
				total := lr.Total
				if total <= 0 {
					total = len(lr.Data)
				}
				scraper.Debugf(1, "prestige: %d total scenes", total)
				select {
				case out <- scraper.Progress(total):
				case <-ctx.Done():
					return
				}
			}

			// A worker has already reached a known scene, so every later page
			// is known too.
			if hitKnown.Load() {
				return
			}

			for _, item := range lr.Data {
				if item.UUID == "" {
					continue
				}
				if known(opts.KnownIDs, listingKeys(item)) {
					scraper.Debugf(1, "prestige: hit known ID, stopping early")
					hitKnown.Store(true)
					return
				}
				if seen[item.UUID] {
					continue
				}
				seen[item.UUID] = true
				select {
				case work <- item:
				case <-ctx.Done():
					return
				}
			}

			if lr.TotalKnown && lr.Total > 0 {
				if page*perPage >= lr.Total {
					return
				}
			} else if len(lr.Data) < perPage {
				return
			}
		}
	}()

	wg.Wait()

	// Emitted once, after every worker has finished sending, so the signal
	// cannot interleave with scenes still in flight.
	if hitKnown.Load() {
		send(ctx, out, scraper.StoppedEarly())
	}
}

// ---- URL helpers ----

func (s *Scraper) buildListURL(params listingParams, page int) string {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("limit", strconv.Itoa(perPage))
	if params.makerID != "" {
		q.Set("makerId", params.makerID)
	}
	if params.labelID != "" {
		q.Set("labelId", params.labelID)
	}
	if params.date != "" {
		q.Set("date", params.date)
	}
	return s.base + "/api/kanbi/sku?" + q.Encode()
}

func (s *Scraper) sceneURL(studioURL, productUUID string) string {
	if detectHost(studioURL) == "kanbi-av.com" {
		return "https://www.kanbi-av.com/product/detail/" + productUUID
	}
	return s.base + "/goods/" + productUUID
}

func (s *Scraper) thumbnailURL(path string) string {
	return s.base + "/api/media/" + path + "?w=800&f=jpg"
}

// ---- detail fetching ----

func (s *Scraper) fetchDetail(ctx context.Context, studioURL, productUUID string, delay time.Duration) (models.Scene, error) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return models.Scene{}, ctx.Err()
		}
	}

	var p product
	if err := s.fetchJSON(ctx, s.base+"/api/kanbi/sku/"+productUUID, &p); err != nil {
		return models.Scene{}, fmt.Errorf("detail %s: %w", productUUID, err)
	}

	return s.toScene(studioURL, p), nil
}

func (s *Scraper) toScene(studioURL string, p product) models.Scene {
	id := extractSceneID(p)

	scene := models.Scene{
		ID:        id,
		SiteID:    "prestige",
		StudioURL: studioURL,
		URL:       s.sceneURL(studioURL, p.UUID),
		ScrapedAt: time.Now().UTC(),
	}

	scene.Title = p.Title
	scene.Description = p.Body

	if p.Maker.Name != "" {
		scene.Studio = p.Maker.Name
	}

	if p.PlayTime > 0 {
		scene.Duration = p.PlayTime * 60
	}

	if len(p.SKU) > 0 && p.SKU[0].SalesStartAt != "" {
		if t, err := time.Parse("2006-01-02", p.SKU[0].SalesStartAt); err == nil {
			scene.Date = t
		}
	}
	if scene.Date.IsZero() && p.MgsStart != "" {
		if t, err := time.Parse(time.RFC3339Nano, p.MgsStart); err == nil {
			scene.Date = t.UTC()
		}
	}

	if len(p.Media) > 0 && p.Media[0].Path != "" {
		scene.Thumbnail = s.thumbnailURL(p.Media[0].Path)
	}

	for _, a := range p.Actress {
		if a.Name != "" {
			scene.Performers = append(scene.Performers, a.Name)
		}
	}

	if len(p.Directors) > 0 && p.Directors[0].Name != "" {
		scene.Director = p.Directors[0].Name
	}

	for _, g := range p.Genre {
		if g.Name != "" {
			scene.Tags = append(scene.Tags, g.Name)
		}
	}

	if p.Series != nil && p.Series.Name != "" {
		scene.Series = p.Series.Name
	}

	if len(p.SKU) > 0 {
		if price, err := strconv.Atoi(p.SKU[0].Price); err == nil && price > 0 {
			scene.AddPrice(models.PriceSnapshot{
				Date:    time.Now().UTC(),
				Regular: float64(price),
			})
		}
	}

	return scene
}

func extractSceneID(p product) string {
	return sceneIDFor(p.SKU, p.UUID)
}

// sceneIDFor derives a scene ID from a product's SKU list: the DVD SKU's
// delivery-item ID if there is one, else any delivery-item ID, else the
// product UUID. Shared by extractSceneID and the listing early-stop so both
// agree on what a scene's ID is.
func sceneIDFor(skus []sku, uuid string) string {
	for _, sk := range skus {
		if sk.Category != nil && sk.Category.Title == "DVD" && sk.DeliveryItemID != "" {
			return strings.ToUpper(sk.DeliveryItemID)
		}
	}
	for _, sk := range skus {
		if sk.DeliveryItemID != "" {
			return strings.ToUpper(sk.DeliveryItemID)
		}
	}
	return uuid
}

// listingKeys returns every identifier a stored KnownIDs entry could plausibly
// hold for this listing item, most-specific first.
//
// Scene.ID is the SKU delivery-item ID, not the product UUID, so an early-stop
// that only checked item.UUID never matched and every incremental run re-walked
// the whole catalogue. When the listing payload carries the SKU the first key
// is an exact Scene.ID match; the UUID is kept as a fallback both for products
// with no delivery-item ID (where Scene.ID *is* the UUID) and for listing
// responses that omit the SKU block.
func listingKeys(item listProduct) []string {
	keys := make([]string, 0, 2)
	if id := sceneIDFor(item.SKU, ""); id != "" {
		keys = append(keys, id)
	}
	if item.UUID != "" {
		keys = append(keys, item.UUID)
	}
	return keys
}

// ---- HTTP ----

func (s *Scraper) fetchJSON(ctx context.Context, rawURL string, v any) error {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentChrome)
			h["Accept"] = "application/json"
			return h
		}(),
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.DecodeJSON(resp.Body, v)
}

// known reports whether any of keys is present in the KnownIDs set.
func known(knownIDs map[string]bool, keys []string) bool {
	for _, k := range keys {
		if knownIDs[k] {
			return true
		}
	}
	return false
}

func send(ctx context.Context, ch chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case ch <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

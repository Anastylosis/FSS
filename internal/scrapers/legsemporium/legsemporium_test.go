package legsemporium

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://legsemporium.com", true},
		{"https://legsemporium.com/", true},
		{"https://www.legsemporium.com", true},
		{"https://legsemporium.com/product-category/madalaine", true},
		{"https://legsemporium.com/product-category/gymnasts", true},
		{"https://legsemporium.com/product/some-video", true},
		{"https://www.manyvids.com/Profile/123", false},
		{"https://example.com", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestExtractSlug(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://legsemporium.com/product-category/madalaine", "madalaine"},
		{"https://legsemporium.com/product-category/gymnasts/", "gymnasts"},
		{"https://legsemporium.com/product-category/gymnasts/some-sub", "gymnasts/some-sub"},
		{"https://legsemporium.com", ""},
		{"https://legsemporium.com/", ""},
	}
	for _, c := range cases {
		got := extractSlug(c.url)
		if got != c.want {
			t.Errorf("extractSlug(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestParseCount(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"123", 123},
		{"1,234", 1234},
		{"2.5k", 2500},
		{"10k", 10000},
		{"0", 0},
		{"", 0},
	}
	for _, c := range cases {
		got := parseCount(c.input)
		if got != c.want {
			t.Errorf("parseCount(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestDecodeHTMLEntities(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"foo &amp; bar", "foo & bar"},
		{"&#039;hello&#039;", "'hello'"},
		{"a &lt; b &gt; c", "a < b > c"},
		{"&quot;quoted&quot;", `"quoted"`},
		{"no entities", "no entities"},
	}
	for _, c := range cases {
		got := decodeHTMLEntities(c.input)
		if got != c.want {
			t.Errorf("decodeHTMLEntities(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseProductCards(t *testing.T) {
	html := `
<div class="o-cat-item e--video">
  <button class="a-btn-buy add_to_cart" data-id="101" data-title="Flexy Splits" data-price="9.99"></button>
  <a href="https://legsemporium.com/product/flexy-splits" class="a-card">
    <img class="a-img" src="/uploads/thumb1.jpg" alt="Flexy Splits">
  </a>
  <div class="stats">
    <i class="icon-eye"></i> <span>1,234</span>
    <i class="icon-clap"></i> <span>56</span>
  </div>
</div>
<div class="o-cat-item e--video">
  <button class="a-btn-buy add_to_cart" data-id="102" data-title="High Kicks &amp; Splits" data-price="12.50"></button>
  <a href="https://legsemporium.com/product/high-kicks" class="a-card">
    <img class="a-img" src="/uploads/thumb2.jpg" alt="High Kicks">
  </a>
  <div class="stats">
    <i class="icon-eye"></i> <span>2.5k</span>
    <i class="icon-clap"></i> <span>100</span>
  </div>
</div>`

	entries := parseProductCards(html, defaultBaseURL)
	if len(entries) != 2 {
		t.Fatalf("parseProductCards returned %d entries, want 2", len(entries))
	}

	e := entries[0]
	if e.id != "101" {
		t.Errorf("id = %q, want %q", e.id, "101")
	}
	if e.title != "Flexy Splits" {
		t.Errorf("title = %q, want %q", e.title, "Flexy Splits")
	}
	if e.price != 9.99 {
		t.Errorf("price = %f, want 9.99", e.price)
	}
	if e.url != "https://legsemporium.com/product/flexy-splits" {
		t.Errorf("url = %q", e.url)
	}
	if e.thumbnail != "https://legsemporium.com/uploads/thumb1.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}
	if e.views != 1234 {
		t.Errorf("views = %d, want 1234", e.views)
	}
	if e.likes != 56 {
		t.Errorf("likes = %d, want 56", e.likes)
	}

	e2 := entries[1]
	if e2.title != "High Kicks & Splits" {
		t.Errorf("title = %q, want %q", e2.title, "High Kicks & Splits")
	}
	if e2.views != 2500 {
		t.Errorf("views = %d, want 2500", e2.views)
	}
}

func TestParseProductCardsSalePrice(t *testing.T) {
	html := `
<div class="o-cat-item e--video">
  <button class="a-btn-buy add_to_cart" data-id="200" data-title="Sale Video" data-price="15.00"></button>
  <a href="https://legsemporium.com/product/sale-video" class="a-card">
    <img class="a-img" src="/uploads/sale.jpg" alt="Sale">
  </a>
  <div class="price"><u>$15.00</u> <span class="u-cl-red">$10.00</span></div>
  <div class="stats">
    <i class="icon-eye"></i> <span>50</span>
    <i class="icon-clap"></i> <span>5</span>
  </div>
</div>`

	entries := parseProductCards(html, defaultBaseURL)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].salePrice != 10.0 {
		t.Errorf("salePrice = %f, want 10.0", entries[0].salePrice)
	}
}

func TestDedup(t *testing.T) {
	entries := []productEntry{
		{id: "1", title: "A"},
		{id: "2", title: "B"},
		{id: "1", title: "A duplicate"},
		{id: "3", title: "C"},
	}
	got := dedup(entries)
	if len(got) != 3 {
		t.Fatalf("dedup returned %d entries, want 3", len(got))
	}
	if got[0].id != "1" || got[1].id != "2" || got[2].id != "3" {
		t.Errorf("dedup result = %v", got)
	}
}

func TestFetchDetail(t *testing.T) {
	detailHTML := `<html><body>
<nav class="o-breadcrumbs">
  <a class="o-breadcrumbs-link" href="/">Home</a>
  <a class="o-breadcrumbs-link" href="/product-category/gymnasts">Gymnasts</a>
  <a class="o-breadcrumbs-link" href="/product-category/madalaine">Madalaine</a>
</nav>
<div class="duration">Duration 12:34</div>
<a href="https://legsemporium.com/product-tag/flexible" class="a-tag">flexible</a>
<a href="https://legsemporium.com/product-tag/splits" class="a-tag">splits</a>
<video poster="https://cdn.legsemporium.com/poster.jpg"></video>
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML)
	}))
	defer ts.Close()

	sess := &session{client: ts.Client()}
	e := productEntry{
		id:    "101",
		title: "Flexy Splits",
		url:   ts.URL + "/product/flexy-splits",
		price: 9.99,
	}

	scene, err := fetchDetail(context.Background(), sess, e, "https://legsemporium.com/product-category/madalaine")
	if err != nil {
		t.Fatalf("fetchDetail error: %v", err)
	}

	if scene.ID != "101" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "legsemporium" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Duration != 754 {
		t.Errorf("Duration = %d, want 754 (12:34)", scene.Duration)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "flexible" || scene.Tags[1] != "splits" {
		t.Errorf("Tags = %v, want [flexible splits]", scene.Tags)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Madalaine" {
		t.Errorf("Performers = %v, want [Madalaine]", scene.Performers)
	}
	if len(scene.Categories) != 1 || scene.Categories[0] != "Gymnasts" {
		t.Errorf("Categories = %v, want [Gymnasts]", scene.Categories)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 9.99 {
		t.Errorf("PriceHistory = %v", scene.PriceHistory)
	}
}

func TestFetchDetailSalePrice(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body></body></html>")
	}))
	defer ts.Close()

	sess := &session{client: ts.Client()}
	e := productEntry{
		id:        "202",
		title:     "On Sale",
		url:       ts.URL + "/product/on-sale",
		price:     20.00,
		salePrice: 15.00,
	}

	scene, err := fetchDetail(context.Background(), sess, e, "https://legsemporium.com")
	if err != nil {
		t.Fatalf("fetchDetail error: %v", err)
	}

	if len(scene.PriceHistory) != 1 {
		t.Fatalf("PriceHistory len = %d, want 1", len(scene.PriceHistory))
	}
	snap := scene.PriceHistory[0]
	if !snap.IsOnSale {
		t.Error("IsOnSale = false, want true")
	}
	if snap.Discounted != 15.00 {
		t.Errorf("Discounted = %f, want 15.00", snap.Discounted)
	}
	if snap.DiscountPercent != 25 {
		t.Errorf("DiscountPercent = %d, want 25", snap.DiscountPercent)
	}
}

func TestFetchDetailPosterFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><video poster="/media/poster.jpg"></video></html>`)
	}))
	defer ts.Close()

	sess := &session{client: ts.Client(), base: defaultBaseURL}
	e := productEntry{
		id:    "303",
		title: "No Thumb",
		url:   ts.URL + "/product/no-thumb",
	}

	scene, err := fetchDetail(context.Background(), sess, e, defaultBaseURL)
	if err != nil {
		t.Fatalf("fetchDetail error: %v", err)
	}
	if scene.Thumbnail != defaultBaseURL+"/media/poster.jpg" {
		t.Errorf("Thumbnail = %q, want poster fallback", scene.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	homepageHTML := `<html><script>this.csrf = "test-token-123"</script></html>`

	ajaxPage1 := ajaxResponse{
		HTMLMod: []struct {
			El    string `json:"el"`
			Value string `json:"value"`
		}{
			{El: ".products-block", Value: `
<div class="o-cat-item e--video">
  <button class="a-btn-buy add_to_cart" data-id="1" data-title="Video One" data-price="5.00"></button>
  <a href="DETAIL_URL/product/video-one">
    <img class="a-img" src="/thumb1.jpg">
  </a>
  <i class="icon-eye"></i> <span>100</span>
  <i class="icon-clap"></i> <span>10</span>
</div>
<div class="o-cat-item e--video">
  <button class="a-btn-buy add_to_cart" data-id="2" data-title="Video Two" data-price="8.00"></button>
  <a href="DETAIL_URL/product/video-two">
    <img class="a-img" src="/thumb2.jpg">
  </a>
  <i class="icon-eye"></i> <span>200</span>
  <i class="icon-clap"></i> <span>20</span>
</div>`},
		},
		IsLast:   true,
		NextPage: 0,
	}

	detailHTML := `<html><body>
<nav class="o-breadcrumbs">
  <a class="o-breadcrumbs-link" href="/">Home</a>
  <a class="o-breadcrumbs-link" href="/product-category/cats">Category</a>
  <a class="o-breadcrumbs-link" href="/product-category/model">Model</a>
</nav>
<div>Duration 5:30</div>
<a href="https://legsemporium.com/product-tag/legs" class="a-tag">legs</a>
</body></html>`

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" || r.URL.Path == "":
			_, _ = fmt.Fprint(w, homepageHTML)
		case r.URL.Path == "/product-category" && r.Method == http.MethodPost:
			html := strings.ReplaceAll(ajaxPage1.HTMLMod[0].Value, "DETAIL_URL", ts.URL)
			resp := ajaxResponse{
				HTMLMod: []struct {
					El    string `json:"el"`
					Value string `json:"value"`
				}{{Value: html}},
				IsLast: true,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/product-category/testmodel":
			_, _ = fmt.Fprint(w, `<html><body>no subcategories here</body></html>`)
		case strings.HasPrefix(r.URL.Path, "/product/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newWithBase(ts.URL)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/product-category/testmodel", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	scenes := map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			scenes[r.Scene.ID] = r.Scene.Title
		}
	}

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
	if scenes["1"] != "Video One" {
		t.Errorf("scene 1 title = %q, want %q", scenes["1"], "Video One")
	}
	if scenes["2"] != "Video Two" {
		t.Errorf("scene 2 title = %q, want %q", scenes["2"], "Video Two")
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	homepageHTML := `<html><script>this.csrf = "test-token"</script></html>`

	ajaxResp := ajaxResponse{
		HTMLMod: []struct {
			El    string `json:"el"`
			Value string `json:"value"`
		}{
			{El: ".products-block", Value: `
<div class="o-cat-item e--video">
  <button class="a-btn-buy add_to_cart" data-id="1" data-title="New" data-price="5.00"></button>
  <a href="DETAIL_URL/product/new">
    <img class="a-img" src="/t1.jpg">
  </a>
  <i class="icon-eye"></i> <span>10</span>
  <i class="icon-clap"></i> <span>1</span>
</div>
<div class="o-cat-item e--video">
  <button class="a-btn-buy add_to_cart" data-id="2" data-title="Known" data-price="5.00"></button>
  <a href="DETAIL_URL/product/known">
    <img class="a-img" src="/t2.jpg">
  </a>
  <i class="icon-eye"></i> <span>20</span>
  <i class="icon-clap"></i> <span>2</span>
</div>`},
		},
		IsLast: true,
	}

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" || r.URL.Path == "":
			_, _ = fmt.Fprint(w, homepageHTML)
		case r.URL.Path == "/product-category" && r.Method == http.MethodPost:
			html := strings.ReplaceAll(ajaxResp.HTMLMod[0].Value, "DETAIL_URL", ts.URL)
			resp := ajaxResponse{
				HTMLMod: []struct {
					El    string `json:"el"`
					Value string `json:"value"`
				}{{Value: html}},
				IsLast: true,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/product-category/leaf":
			_, _ = fmt.Fprint(w, `<html><body>leaf page</body></html>`)
		case strings.HasPrefix(r.URL.Path, "/product/"):
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newWithBase(ts.URL)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/product-category/leaf", scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var count int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d scenes, want 1 (early stop at known ID)", count)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// TestListScenes builds ajaxResponse values in Go, so encode and decode share the
// struct tag and a renamed one round-trips unnoticed. This is a byte-verbatim
// capture of a live POST to https://legsemporium.com/product-category, kept whole
// and unedited.
//
// Reaching it takes two steps and no credential: GET a page and scrape
// `this.csrf = "…"` out of its HTML, then POST a form-encoded body with an
// `X-CSRF-TOKEN` header. The token travels in a request header, so it is absent
// from the response — TestGoldenAjaxPageCarriesNoToken asserts that.
//
// Shapes a hand-written fixture would have got wrong:
//   - **`htmlMod` carries more than one block.** The response holds
//     `.products-block` *and* `.pagination`, and the struct literal in
//     TestListScenes only ever had one entry — which is why reading
//     `HTMLMod[0]` looked safe. See productsHTML for why it no longer is.
//   - each block has an `el` selector and a `type`, neither of which the struct
//     originally decoded.
//   - the payload uses WordPress-style escaped forward slashes (`\/`).
//   - `per_page` in the request body is **ignored** by the server: asking for 2
//     still returns 24 cards, so the fixture is 47KB rather than something
//     smaller. It is kept whole rather than truncated, since editing the HTML
//     inside `value` would stop it being a capture.
func TestGoldenAjaxPage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "ajax_page.json"))
	if err != nil {
		t.Fatal(err)
	}

	var ar ajaxResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(ar.HTMLMod) != 2 {
		t.Fatalf("decoded %d htmlMod blocks, want 2 (.products-block and .pagination)", len(ar.HTMLMod))
	}
	if ar.IsLast {
		t.Error("IsLast is true (isLast) on page 1 of a multi-page category")
	}
	if ar.NextPage != 2 {
		t.Errorf("NextPage = %d (nextPage), want 2", ar.NextPage)
	}

	// The block names, and that products are not simply first by luck.
	var names []string
	for _, m := range ar.HTMLMod {
		names = append(names, m.El)
	}
	if names[0] != ".products-block" || names[1] != ".pagination" {
		t.Errorf("htmlMod block order = %v; productsHTML selects by name so this is not fatal, "+
			"but the comment describing the order is now stale", names)
	}

	// productsHTML must pick the cards regardless of order.
	html := ar.productsHTML()
	if html == "" {
		t.Fatal("productsHTML returned nothing")
	}
	entries := parseProductCards(html, "https://legsemporium.com")
	if len(entries) == 0 {
		t.Fatal("no product cards parsed from the real .products-block markup")
	}
	if entries[0].url == "" || entries[0].title == "" {
		t.Errorf("first card = %+v, want a url and title", entries[0])
	}

	// Per-card parsing means every card in the real markup must carry its own
	// fields; under the old index-zip a short list left the tail of the page
	// with borrowed or missing values.
	seen := map[string]bool{}
	for i, e := range entries {
		if e.id == "" || e.title == "" || e.url == "" || e.price == 0 || e.views == 0 {
			t.Errorf("card %d is incomplete: %+v", i, e)
		}
		if seen[e.url] {
			t.Errorf("card %d repeats url %q — cards are being read across boundaries", i, e.url)
		}
		seen[e.url] = true
	}
}

// The ordering guard: reverse the blocks and productsHTML must still find the
// cards. This is the failure the index-0 read would have caused — a silent empty
// scrape rather than an error.
func TestProductsHTMLSelectsByNameNotPosition(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "ajax_page.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ar ajaxResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		t.Fatal(err)
	}
	ar.HTMLMod[0], ar.HTMLMod[1] = ar.HTMLMod[1], ar.HTMLMod[0]

	entries := parseProductCards(ar.productsHTML(), "https://legsemporium.com")
	if len(entries) == 0 {
		t.Error("no cards found after the server reordered its htmlMod blocks; " +
			"reading HTMLMod[0] would silently end the paging loop here")
	}
}

func TestGoldenAjaxPageCarriesNoToken(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "ajax_page.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"X-CSRF-TOKEN", "this.csrf", "laravel_session", "XSRF-TOKEN"} {
		if bytes.Contains(body, []byte(marker)) {
			t.Errorf("fixture contains %q — re-capture without the session material", marker)
		}
	}
	if !bytes.Contains(body, []byte(`\/`)) {
		t.Error(`fixture lost the escaped forward slashes (\/) — it looks re-encoded`)
	}
}

// The listing used to be parsed by sweeping each field's regex over the whole
// page and zipping the seven result lists by index. A card that omits a field —
// here the first card has no view/clap counters and no sale price — shortened
// those lists and shifted every later card's stats and price onto the wrong
// product, with nothing about the result looking wrong.
func TestParseProductCardsWithMissingFieldsStayAligned(t *testing.T) {
	html := `
<div class="o-cat-item e--video">
  <a href="https://legsemporium.com/product/no-stats" class="o-cat-video">x</a>
  <button class="a-btn-buy add_to_cart" data-id="301" data-title="No Stats" data-price="5.00"></button>
</div>
<div class="o-cat-item e--video">
  <a href="https://legsemporium.com/product/full-card" class="o-cat-video">x</a>
  <div class="stats">
    <i class="icon-eye"></i> <span>900</span>
    <i class="icon-clap"></i> <span>42</span>
  </div>
  <div class="price"><u>$20.00</u> <span class="u-cl-red">$12.00</span></div>
  <button class="a-btn-buy add_to_cart" data-id="302" data-title="Full Card" data-price="20.00"></button>
</div>`

	entries := parseProductCards(html, defaultBaseURL)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	first := entries[0]
	if first.id != "301" || first.url != "https://legsemporium.com/product/no-stats" {
		t.Errorf("first card = %+v", first)
	}
	if first.views != 0 || first.likes != 0 || first.salePrice != 0 {
		t.Errorf("first card borrowed its neighbour's stats: views=%d likes=%d sale=%v",
			first.views, first.likes, first.salePrice)
	}

	second := entries[1]
	if second.id != "302" || second.url != "https://legsemporium.com/product/full-card" {
		t.Errorf("second card = %+v", second)
	}
	if second.views != 900 || second.likes != 42 || second.salePrice != 12.0 {
		t.Errorf("second card lost its own stats: views=%d likes=%d sale=%v",
			second.views, second.likes, second.salePrice)
	}
}

// The real listing markup nests two product links per card and puts data-id on
// the buy button rather than the wrapper, so the split has to key on the card
// wrapper itself.
func TestSplitCardsOnLiveMarkupShape(t *testing.T) {
	html := `<div class="products-block">
  <div class="u-wd50p">
    <div class="o-cat-item e--video u-mv4 js-cat-video ">
      <a class="u-cl-white" href="https://legsemporium.com/product/a">A</a>
      <button class="a-btn-buy add_to_cart" data-id="1" data-title="A" data-price="4.99"></button>
      <a href="https://legsemporium.com/product/a" class="o-cat-video e--shot"></a>
    </div>
  </div>
  <div class="u-wd50p">
    <div class="o-cat-item e--video u-mv4 js-cat-video ">
      <a class="u-cl-white" href="https://legsemporium.com/product/b">B</a>
      <button class="a-btn-buy add_to_cart" data-id="2" data-title="B" data-price="4.99"></button>
      <a href="https://legsemporium.com/product/b" class="o-cat-video e--shot"></a>
    </div>
  </div>
</div>`

	if got := len(splitCards(html)); got != 2 {
		t.Fatalf("splitCards returned %d blocks, want 2", got)
	}
	entries := parseProductCards(html, defaultBaseURL)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].url != "https://legsemporium.com/product/a" {
		t.Errorf("entry 0 url = %q", entries[0].url)
	}
	if entries[1].url != "https://legsemporium.com/product/b" {
		t.Errorf("entry 1 url = %q", entries[1].url)
	}
}

// The category tree cross-links: a child category links back to its parent, and
// a subtree is often reachable two ways. discoverLeaves had no depth cap and no
// visited set, so the first case recursed until the stack ran out and the
// second refetched whole subtrees.
func TestDiscoverLeavesTerminatesOnACategoryCycle(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()

		// Sub-category links are matched by their canonical host; only the
		// slug is used to build the next request, which goes to sess.base.
		const site = "https://legsemporium.com"
		switch r.URL.Path {
		case "/product-category/parent":
			// Model cards mark this as a branch, and it links to a child that
			// links straight back here.
			_, _ = fmt.Fprintf(w, `<div class="o-cat-item-models"></div>
				<a href="%s/product-category/child">child</a>`, site)
		case "/product-category/child":
			_, _ = fmt.Fprintf(w, `<div class="o-cat-item-models"></div>
				<a href="%s/product-category/parent">back to parent</a>
				<a href="%s/product-category/leaf">leaf</a>`, site, site)
		case "/product-category/leaf":
			_, _ = fmt.Fprint(w, `<div class="products-block"></div>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	sess := &session{client: ts.Client(), base: ts.URL}
	done := make(chan struct{})
	var leaves []string
	var err error
	go func() {
		defer close(done)
		leaves, err = discoverLeaves(context.Background(), sess, "parent", ts.URL+"/product-category/parent",
			map[string]bool{}, 0)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("discoverLeaves did not terminate on a category cycle")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(leaves, "leaf") {
		t.Errorf("leaves = %v, want the leaf category", leaves)
	}

	mu.Lock()
	defer mu.Unlock()
	for path, n := range hits {
		if n > 1 {
			t.Errorf("fetched %s %d times — the visited set is not holding", path, n)
		}
	}
}

// A tree deeper than the cap stops descending rather than walking forever, and
// the category it stopped on is still returned as something to scrape.
func TestDiscoverLeavesStopsAtTheDepthCap(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/product-category/level%d", &n); err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `<div class="o-cat-item-models"></div>
			<a href="https://legsemporium.com/product-category/level%d">deeper</a>`, n+1)
	}))
	defer ts.Close()

	sess := &session{client: ts.Client(), base: ts.URL}
	leaves, err := discoverLeaves(context.Background(), sess, "level0", ts.URL, map[string]bool{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) == 0 {
		t.Fatal("an infinitely deep tree yielded nothing to scrape")
	}
	for _, l := range leaves {
		var n int
		if _, err := fmt.Sscanf(l, "level%d", &n); err == nil && n > maxCategoryDepth+1 {
			t.Errorf("descended to %s, past the depth cap of %d", l, maxCategoryDepth)
		}
	}
}

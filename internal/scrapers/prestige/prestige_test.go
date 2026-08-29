package prestige

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

func sampleProduct(uuid, deliveryID, title string) product {
	return product{
		UUID:     uuid,
		Title:    title,
		Body:     "テスト説明文",
		PlayTime: 120,
		MgsStart: "2024-03-01T01:00:00.000Z",
		Maker:    namedEntity{UUID: "maker-1", Name: "Prestige"},
		Label:    namedEntity{UUID: "label-1", Name: "ABF"},
		Series:   &namedEntity{UUID: "series-1", Name: "テストシリーズ"},
		Genre: []namedEntity{
			{UUID: "g1", Name: "ドラマ"},
			{UUID: "g2", Name: "巨乳"},
		},
		Actress: []namedEntity{
			{UUID: "a1", Name: "七嶋 舞"},
		},
		Directors: []namedEntity{
			{UUID: "d1", Name: "田中太郎"},
		},
		Media: []media{
			{UUID: "m1", Path: "0/0/test-image.jpg", Sort: 0},
		},
		SKU: []sku{
			{
				UUID:           "sku-1",
				DeliveryItemID: deliveryID,
				Price:          "3500",
				SalesStartAt:   "2024-03-18",
				Category:       &skuCategory{Title: "DVD"},
			},
		},
	}
}

func listJSON(items []listProduct, total int) string {
	lr := struct {
		Data  []listProduct `json:"data"`
		Total int           `json:"total"`
	}{Data: items, Total: total}
	b, _ := json.Marshal(lr)
	return string(b)
}

func productJSON(p product) string {
	b, _ := json.Marshal(p)
	return string(b)
}

func newTestServer(products []product) *httptest.Server {
	listing := make([]listProduct, len(products))
	for i, p := range products {
		listing[i] = listProduct{UUID: p.UUID}
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/kanbi/sku" && r.Method == http.MethodGet:
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page <= 0 {
				page = 1
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit <= 0 {
				limit = perPage
			}
			start := (page - 1) * limit
			end := start + limit
			if start >= len(listing) {
				_, _ = fmt.Fprint(w, listJSON(nil, len(listing)))
				return
			}
			if end > len(listing) {
				end = len(listing)
			}
			_, _ = fmt.Fprint(w, listJSON(listing[start:end], len(listing)))

		case strings.HasPrefix(r.URL.Path, "/api/kanbi/sku/"):
			uuid := strings.TrimPrefix(r.URL.Path, "/api/kanbi/sku/")
			for _, p := range products {
				if p.UUID == uuid {
					_, _ = fmt.Fprint(w, productJSON(p))
					return
				}
			}
			http.NotFound(w, r)

		case r.URL.Path == "/api/maker":
			makers := []makerEntry{
				{UUID: "maker-1", Name: "Prestige"},
				{UUID: "maker-2", Name: "DOC"},
				{UUID: "maker-3", Name: "KANBi"},
			}
			b, _ := json.Marshal(makers)
			_, _ = fmt.Fprint(w, string(b))

		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	s := New()
	s.base = ts.URL
	return s
}

// ---- tests ----

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.prestige-av.com/", true},
		{"https://prestige-av.com/", true},
		{"https://prestige-av.com", true},
		{"https://www.prestige-av.com/goods", true},
		{"https://prestige-av.com/goods?maker=Prestige", true},
		{"https://prestige-av.com/goods?label=ABF", true},
		{"https://prestige-av.com/goods?date=2024-03-29", true},
		{"https://prestige-av.com/goods?maker=DOC&label=ABF", true},
		{"https://www.kanbi-av.com/", true},
		{"https://kanbi-av.com/", true},
		{"https://www.kanbi-av.com/list/all/on_sale", true},
		{"https://kanbi-av.com/list/all/on_sale", true},
		{"https://prestige-av.com/goods/some-uuid", false},
		{"https://prestige-av.com/actress", false},
		{"https://example.com/goods", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestExtractSceneID(t *testing.T) {
	tests := []struct {
		name string
		p    product
		want string
	}{
		{
			name: "DVD SKU",
			p: product{
				UUID: "test-uuid",
				SKU: []sku{
					{DeliveryItemID: "abf-022", Category: &skuCategory{Title: "DVD"}},
				},
			},
			want: "ABF-022",
		},
		{
			name: "non-DVD SKU fallback",
			p: product{
				UUID: "test-uuid",
				SKU: []sku{
					{DeliveryItemID: "vr-001", Category: &skuCategory{Title: "VR"}},
				},
			},
			want: "VR-001",
		},
		{
			name: "prefers DVD over other categories",
			p: product{
				UUID: "test-uuid",
				SKU: []sku{
					{DeliveryItemID: "gooe-abf-022", Category: &skuCategory{Title: "限定"}},
					{DeliveryItemID: "abf-022", Category: &skuCategory{Title: "DVD"}},
				},
			},
			want: "ABF-022",
		},
		{
			name: "no SKU falls back to UUID",
			p: product{
				UUID: "test-uuid",
			},
			want: "test-uuid",
		},
		{
			name: "empty deliveryItemId falls back to UUID",
			p: product{
				UUID: "test-uuid",
				SKU:  []sku{{DeliveryItemID: "", Category: &skuCategory{Title: "DVD"}}},
			},
			want: "test-uuid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractSceneID(tt.p); got != tt.want {
				t.Errorf("extractSceneID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseFilters(t *testing.T) {
	tests := []struct {
		url                            string
		wantMaker, wantLabel, wantDate string
	}{
		{"https://prestige-av.com/goods", "", "", ""},
		{"https://prestige-av.com/goods?maker=DOC", "DOC", "", ""},
		{"https://prestige-av.com/goods?label=ABF", "", "ABF", ""},
		{"https://prestige-av.com/goods?date=2024-03-29", "", "", "2024-03-29"},
		{"https://prestige-av.com/goods?maker=Prestige&date=2024-01-01", "Prestige", "", "2024-01-01"},
	}
	for _, tt := range tests {
		maker, label, date := parseFilters(tt.url)
		if maker != tt.wantMaker || label != tt.wantLabel || date != tt.wantDate {
			t.Errorf("parseFilters(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.url, maker, label, date, tt.wantMaker, tt.wantLabel, tt.wantDate)
		}
	}
}

func TestToScene(t *testing.T) {
	p := sampleProduct("uuid-1", "ABF-022", "テストタイトル")
	s := &Scraper{base: "https://www.prestige-av.com"}
	scene := s.toScene("https://www.prestige-av.com/goods", p)

	if scene.ID != "ABF-022" {
		t.Errorf("ID = %q, want ABF-022", scene.ID)
	}
	if scene.SiteID != "prestige" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "テストタイトル" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Description != "テスト説明文" {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Studio != "Prestige" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Duration != 7200 {
		t.Errorf("Duration = %d, want 7200", scene.Duration)
	}
	wantDate := time.Date(2024, 3, 18, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if scene.Thumbnail != "https://www.prestige-av.com/api/media/0/0/test-image.jpg?w=800&f=jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "七嶋 舞" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Director != "田中太郎" {
		t.Errorf("Director = %q", scene.Director)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "ドラマ" || scene.Tags[1] != "巨乳" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Series != "テストシリーズ" {
		t.Errorf("Series = %q", scene.Series)
	}
	if scene.URL != "https://www.prestige-av.com/goods/uuid-1" {
		t.Errorf("URL = %q", scene.URL)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 3500 {
		t.Errorf("PriceHistory = %v", scene.PriceHistory)
	}
}

func TestToSceneKANBiDomain(t *testing.T) {
	p := sampleProduct("uuid-k1", "KBI-001", "KANBiタイトル")
	p.Maker = namedEntity{UUID: "maker-3", Name: "KANBi"}
	s := &Scraper{base: "https://www.prestige-av.com"}
	scene := s.toScene("https://www.kanbi-av.com/list/all/on_sale", p)

	if scene.URL != "https://www.kanbi-av.com/product/detail/uuid-k1" {
		t.Errorf("URL = %q, want kanbi-av.com product detail URL", scene.URL)
	}
	if scene.SiteID != "prestige" {
		t.Errorf("SiteID = %q, want prestige", scene.SiteID)
	}
	if scene.Studio != "KANBi" {
		t.Errorf("Studio = %q, want KANBi", scene.Studio)
	}
}

func TestToSceneNoOptionalFields(t *testing.T) {
	p := product{
		UUID:  "bare-uuid",
		Title: "タイトルのみ",
		Maker: namedEntity{Name: "Prestige"},
	}
	s := &Scraper{base: "https://www.prestige-av.com"}
	scene := s.toScene("https://www.prestige-av.com/goods", p)

	if scene.ID != "bare-uuid" {
		t.Errorf("ID = %q, want bare-uuid", scene.ID)
	}
	if scene.Duration != 0 {
		t.Errorf("Duration should be 0, got %d", scene.Duration)
	}
	if len(scene.Performers) != 0 {
		t.Errorf("Performers should be empty, got %v", scene.Performers)
	}
	if scene.Director != "" {
		t.Errorf("Director should be empty, got %q", scene.Director)
	}
	if scene.Series != "" {
		t.Errorf("Series should be empty, got %q", scene.Series)
	}
	if len(scene.Tags) != 0 {
		t.Errorf("Tags should be empty, got %v", scene.Tags)
	}
}

func TestToSceneDateFallback(t *testing.T) {
	p := product{
		UUID:     "uuid-1",
		Title:    "日付テスト",
		MgsStart: "2024-05-01T01:00:00.000Z",
		SKU:      []sku{{DeliveryItemID: "TEST-001", Price: "0"}},
	}
	s := &Scraper{base: "https://www.prestige-av.com"}
	scene := s.toScene("https://www.prestige-av.com/goods", p)

	wantDate := time.Date(2024, 5, 1, 1, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
}

func TestListScenes(t *testing.T) {
	products := []product{
		sampleProduct("uuid-1", "ABF-001", "タイトル1"),
		sampleProduct("uuid-2", "ABF-002", "タイトル2"),
	}
	ts := newTestServer(products)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/goods", scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	products := []product{
		sampleProduct("uuid-new", "NEW-001", "新しい"),
		sampleProduct("uuid-old", "OLD-001", "古い"),
	}
	ts := newTestServer(products)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/goods", scraper.ListOpts{
		KnownIDs: map[string]bool{"uuid-old": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	var scenes, stopped int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stopped, want 1", stopped)
	}
}

func TestListScenesPagination(t *testing.T) {
	products := make([]product, perPage+1)
	for i := range products {
		products[i] = sampleProduct(
			fmt.Sprintf("uuid-%03d", i),
			fmt.Sprintf("P-%03d", i),
			fmt.Sprintf("タイトル%d", i),
		)
	}
	ts := newTestServer(products)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/goods", scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != perPage+1 {
		t.Errorf("got %d scenes, want %d", scenes, perPage+1)
	}
}

func TestListScenesDedup(t *testing.T) {
	products := []product{
		sampleProduct("uuid-1", "ABF-001", "タイトル1"),
	}
	// Serve duplicates in listing
	listing := []listProduct{{UUID: "uuid-1"}, {UUID: "uuid-1"}}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/kanbi/sku" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, listJSON(listing, 2))
		case strings.HasPrefix(r.URL.Path, "/api/kanbi/sku/"):
			_, _ = fmt.Fprint(w, productJSON(products[0]))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/goods", scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1 (dedup should remove duplicate)", scenes)
	}
}

func TestListScenesWithMakerFilter(t *testing.T) {
	products := []product{
		sampleProduct("uuid-1", "DOC-001", "DOCタイトル"),
	}
	ts := newTestServer(products)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/goods?maker=DOC", scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
}

func TestListScenesKANBiAutoFilter(t *testing.T) {
	products := []product{
		sampleProduct("uuid-k1", "KBI-001", "KANBiタイトル"),
	}
	ts := newTestServer(products)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), "https://www.kanbi-av.com/list/all/on_sale", scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.URL != "https://www.kanbi-av.com/product/detail/uuid-k1" {
				t.Errorf("scene URL = %q, want kanbi-av.com product detail URL", r.Scene.URL)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
}

func TestDetectHost(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.prestige-av.com/goods", "prestige-av.com"},
		{"https://prestige-av.com/goods?maker=DOC", "prestige-av.com"},
		{"https://www.kanbi-av.com/", "kanbi-av.com"},
		{"https://kanbi-av.com/list/all/on_sale", "kanbi-av.com"},
	}
	for _, tt := range tests {
		if got := detectHost(tt.url); got != tt.want {
			t.Errorf("detectHost(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestListResponseUnmarshalArray(t *testing.T) {
	raw := `[{"uuid":"a"},{"uuid":"b"}]`
	var lr listResponse
	if err := json.Unmarshal([]byte(raw), &lr); err != nil {
		t.Fatal(err)
	}
	if len(lr.Data) != 2 {
		t.Errorf("len(Data) = %d, want 2", len(lr.Data))
	}
	// A bare array carries no catalogue size. Recording len(arr) as the total
	// made an exactly-full first page look like the whole catalogue.
	if lr.TotalKnown {
		t.Error("TotalKnown = true for a bare array")
	}
	if lr.Total != 0 {
		t.Errorf("Total = %d, want 0", lr.Total)
	}
}

// An object whose `data` is empty is a legitimate "no results", not a
// malformed response.
func TestListResponseUnmarshalEmptyObject(t *testing.T) {
	var lr listResponse
	if err := json.Unmarshal([]byte(`{"data":[],"total":0}`), &lr); err != nil {
		t.Fatalf("empty result should decode, got %v", err)
	}
	if len(lr.Data) != 0 || !lr.TotalKnown {
		t.Errorf("Data = %v, TotalKnown = %v", lr.Data, lr.TotalKnown)
	}
}

func TestListResponseUnmarshalGarbage(t *testing.T) {
	var lr listResponse
	if err := json.Unmarshal([]byte(`"nope"`), &lr); err == nil {
		t.Error("a non-object, non-array body should be an error")
	}
}

func TestListResponseUnmarshalObject(t *testing.T) {
	raw := `{"data":[{"uuid":"a"}],"total":5}`
	var lr listResponse
	if err := json.Unmarshal([]byte(raw), &lr); err != nil {
		t.Fatal(err)
	}
	if len(lr.Data) != 1 {
		t.Errorf("len(Data) = %d, want 1", len(lr.Data))
	}
	if lr.Total != 5 {
		t.Errorf("Total = %d, want 5", lr.Total)
	}
}

// ---- KnownIDs key-mismatch regression ----
//
// Scene.ID is the SKU delivery-item ID, not the product UUID. An early-stop
// keyed on the UUID never matched a stored KnownIDs entry, so every incremental
// run silently re-walked the whole catalogue. The tests below seed KnownIDs the
// way the store actually does — from emitted Scene.IDs — which is what the
// original test failed to do.

// knownIDsFromScenes builds a KnownIDs set the way the store does: from the IDs
// the scraper itself emitted. Seeding from any other key masks a mismatch.
func knownIDsFromScenes(t *testing.T, s *Scraper, studioURL string) map[string]bool {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for r := range ch {
		if r.Kind == scraper.KindScene {
			ids[r.Scene.ID] = true
		}
	}
	if len(ids) == 0 {
		t.Fatal("first pass emitted no scenes")
	}
	return ids
}

func runCounting(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) (scenes, stopped int) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	return scenes, stopped
}

// A re-scrape with every scene already known must emit nothing and stop early.
// Before the fix this re-walked and re-emitted the entire catalogue.
func TestListScenesKnownIDsUsesSceneID(t *testing.T) {
	products := []product{
		sampleProduct("uuid-new", "NEW-001", "新しい"),
		sampleProduct("uuid-old", "OLD-001", "古い"),
	}
	ts := newTestServer(products)
	defer ts.Close()
	s := newTestScraper(ts)

	known := knownIDsFromScenes(t, s, ts.URL+"/goods")
	// Scene.ID is the uppercased delivery-item ID, never the UUID.
	for _, want := range []string{"NEW-001", "OLD-001"} {
		if !known[want] {
			t.Fatalf("KnownIDs = %v, expected it to contain %q", known, want)
		}
	}

	scenes, stopped := runCounting(t, s, ts.URL+"/goods", scraper.ListOpts{
		KnownIDs: known,
		Delay:    time.Millisecond,
	})
	if scenes != 0 {
		t.Errorf("got %d scenes, want 0 — all were already known", scenes)
	}
	if stopped == 0 {
		t.Error("expected a StoppedEarly signal")
	}
}

// Only the newest scene is unknown, so exactly it should come back.
func TestListScenesKnownIDsEmitsOnlyNewScenes(t *testing.T) {
	products := []product{
		sampleProduct("uuid-new", "NEW-001", "新しい"),
		sampleProduct("uuid-old", "OLD-001", "古い"),
	}
	ts := newTestServer(products)
	defer ts.Close()
	s := newTestScraper(ts)

	scenes, stopped := runCounting(t, s, ts.URL+"/goods", scraper.ListOpts{
		KnownIDs: map[string]bool{"OLD-001": true},
		Delay:    time.Millisecond,
	})
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if stopped == 0 {
		t.Error("expected a StoppedEarly signal")
	}
}

// ---- listingKeys ----

func TestListingKeys(t *testing.T) {
	cases := []struct {
		name string
		item listProduct
		want []string
	}{
		{
			name: "sku present yields the Scene.ID first",
			item: listProduct{
				UUID: "uuid-1",
				SKU:  []sku{{DeliveryItemID: "abf-022", Category: &skuCategory{Title: "DVD"}}},
			},
			want: []string{"ABF-022", "uuid-1"},
		},
		{
			name: "DVD sku wins over other categories",
			item: listProduct{
				UUID: "uuid-2",
				SKU: []sku{
					{DeliveryItemID: "gooe-abf-022", Category: &skuCategory{Title: "限定"}},
					{DeliveryItemID: "abf-022", Category: &skuCategory{Title: "DVD"}},
				},
			},
			want: []string{"ABF-022", "uuid-2"},
		},
		{
			name: "listing without sku falls back to uuid",
			item: listProduct{UUID: "uuid-3"},
			want: []string{"uuid-3"},
		},
		{
			name: "empty item yields no keys",
			item: listProduct{},
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := listingKeys(c.item)
			if len(got) != len(c.want) {
				t.Fatalf("listingKeys = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("listingKeys[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// listingKeys must agree with extractSceneID on what a scene's ID is.
func TestListingKeysMatchesExtractSceneID(t *testing.T) {
	p := sampleProduct("uuid-9", "xyz-100", "タイトル")
	item := listProduct{UUID: p.UUID, SKU: p.SKU}

	want := extractSceneID(p)
	got := listingKeys(item)
	if len(got) == 0 || got[0] != want {
		t.Errorf("listingKeys first key = %v, want %q (extractSceneID)", got, want)
	}
}

// An exactly-full first page in the bare-array shape used to end the walk: the
// total was set to the page length, so `page*perPage >= total` was true on
// page 1 and --full's authoritative Save saw one page as the whole catalogue.
func TestBareArrayExactlyFullFirstPageKeepsWalking(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/kanbi/sku":
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			var items []string
			switch page {
			case 1:
				for i := 0; i < perPage; i++ {
					items = append(items, fmt.Sprintf(`{"uuid":"p1-%d"}`, i))
				}
			case 2:
				items = append(items, `{"uuid":"p2-0"}`)
			}
			// The bare-array shape: no envelope, no total.
			_, _ = fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
		case strings.HasPrefix(r.URL.Path, "/api/kanbi/sku/"):
			uuid := strings.TrimPrefix(r.URL.Path, "/api/kanbi/sku/")
			_, _ = fmt.Fprint(w, productJSON(sampleProduct(uuid, "ABF-001", "タイトル")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/goods", scraper.ListOpts{Delay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	scenes := 0
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != perPage+1 {
		t.Errorf("got %d scenes, want %d — page 2 must still be walked", scenes, perPage+1)
	}
}

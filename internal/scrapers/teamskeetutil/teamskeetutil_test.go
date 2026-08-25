package teamskeetutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestClassifyURL(t *testing.T) {
	tests := []struct {
		url      string
		wantKind filterKind
		wantVal  string
	}{
		{"https://www.mylf.com/", filterAll, ""},
		{"https://www.mylf.com/videos", filterAll, ""},
		{"https://www.mylf.com/models/penny-barber", filterModel, "penny-barber"},
		{"https://www.mylf.com/series/features-ts", filterSeries, "features-ts"},
		{"https://www.mylf.com/categories/amateur", filterCategory, "amateur"},
		{"https://www.teamskeet.com/series/exxxtrasmall", filterSeries, "exxxtrasmall"},
		{"https://www.teamskeet.com/models/alex-adams", filterModel, "alex-adams"},
	}
	for _, tt := range tests {
		kind, val := classifyURL(tt.url)
		if kind != tt.wantKind || val != tt.wantVal {
			t.Errorf("classifyURL(%q) = (%v, %q), want (%v, %q)", tt.url, kind, val, tt.wantKind, tt.wantVal)
		}
	}
}

func TestBuildQuery(t *testing.T) {
	t.Run("all", func(t *testing.T) {
		q := buildQuery(filterAll, "")
		data, _ := json.Marshal(q)
		s := string(data)
		if !jsonContains(s, `"_doc_type.keyword":"tour_movie"`) {
			t.Error("missing doc_type filter")
		}
		if !jsonContains(s, `"isUpcoming":false`) {
			t.Error("missing isUpcoming filter")
		}
	})

	t.Run("model", func(t *testing.T) {
		q := buildQuery(filterModel, "penny-barber")
		data, _ := json.Marshal(q)
		if !jsonContains(string(data), `"models.id.keyword":"penny-barber"`) {
			t.Error("missing model filter")
		}
	})

	t.Run("series", func(t *testing.T) {
		q := buildQuery(filterSeries, "exxxtrasmall")
		data, _ := json.Marshal(q)
		if !jsonContains(string(data), `"site.nickName.keyword":"exxxtrasmall"`) {
			t.Error("missing series filter")
		}
	})

	t.Run("category", func(t *testing.T) {
		q := buildQuery(filterCategory, "amateur")
		data, _ := json.Marshal(q)
		if !jsonContains(string(data), `"match_phrase"`) || !jsonContains(string(data), `"tags":"amateur"`) {
			t.Error("missing category filter")
		}
	})
}

func jsonContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestHitToScene(t *testing.T) {
	src := esScene{
		ID:            "polar-opposites-slug",
		ItemID:        31954,
		Title:         "Polar Opposites",
		Description:   "<p>Two women explore.</p>",
		Img:           "https://images.psmcdn.net/teamskeet/mlf/scene/shared/med_v2.jpg",
		VideoTrailer:  "https://images.psmcdn.net/mlf/tour/pics/trailer.mp4",
		PublishedDate: "2026-04-26T00:00:00",
		Tags:          []string{"MILF", "Step Mom"},
		Models: []esModel{
			{ID: "penny-barber", ItemID: 1234, Name: "Penny Barber"},
			{ID: "kaden-kole", ItemID: 5678, Name: "Kaden Kole"},
		},
	}
	src.Site.Name = "MYLF Singles"
	src.Site.NickName = "mylf-singles-ts"

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	scene := hitToScene(src, "https://www.mylf.com/", SiteConfig{SiteBase: "https://www.mylf.com"}, now)

	if scene.ID != "31954" {
		t.Errorf("ID = %q, want 31954", scene.ID)
	}
	if scene.Title != "Polar Opposites" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.URL != "https://www.mylf.com/videos/polar-opposites-slug" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Description != "Two women explore." {
		t.Errorf("Description = %q (HTML not stripped?)", scene.Description)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Penny Barber" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 2 {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Studio != "MYLF Singles" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	wantDate := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if scene.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
	if scene.Preview == "" {
		t.Error("Preview is empty")
	}
	if scene.SiteID != "mylf-singles-ts" {
		t.Errorf("SiteID = %q, want mylf-singles-ts", scene.SiteID)
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2026-04-26T00:00:00", time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)},
		{"2024-01-15T14:30:00", time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)},
		{"bad", time.Time{}},
	}
	for _, tt := range tests {
		got := parseDate(tt.in)
		if !got.Equal(tt.want) {
			t.Errorf("parseDate(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func fakeESResponse(scenes []esScene, total int) []byte {
	resp := struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []esHit `json:"hits"`
		} `json:"hits"`
	}{}
	resp.Hits.Total.Value = total
	for _, s := range scenes {
		dateBytes, _ := json.Marshal(s.PublishedDate)
		idBytes, _ := json.Marshal(s.ItemID)
		resp.Hits.Hits = append(resp.Hits.Hits, esHit{
			Source: s,
			Sort:   []json.RawMessage{dateBytes, idBytes},
		})
	}
	data, _ := json.Marshal(resp)
	return data
}

func makeScene(id int, title string) esScene {
	return esScene{
		ID:            fmt.Sprintf("scene-%d", id),
		ItemID:        id,
		Title:         title,
		PublishedDate: "2026-04-20T00:00:00",
		Models:        []esModel{{Name: "Test Model"}},
		Tags:          []string{"MILF"},
	}
}

// TestRunUsesSearchAfter's fake server used to key its response on an
// unsynchronised call counter incremented in request order. That made the
// test dependent on requests actually arriving in the order Run() issues
// them — a guarantee HTTP keep-alive does not provide: Go's Transport can
// transparently retry a request on a fresh connection when it reused a
// connection the server was concurrently tearing down, which reaches the
// fake server as a second handler invocation for what Run() sees as a single
// logical request. Run() itself is a strictly sequential loop (one s.search
// call per page, search_after computed from the prior response before the
// next request is built) and httpx's retry always resends the exact same
// marshaled body, so this was never a Run/search bug — but an order-keyed
// fake server can still hand out the same page twice whenever two requests
// land close together. Keying the response on the request's own
// search_after cursor instead removes the dependency on arrival order.
func TestRunUsesSearchAfter(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any

	page1Scenes := make([]esScene, pageSize)
	for i := range page1Scenes {
		page1Scenes[i] = makeScene(1000-i, fmt.Sprintf("Scene %d", i))
		page1Scenes[i].Site.Name = "TestSite"
		page1Scenes[i].Site.NickName = "testsite"
	}
	page2Scenes := []esScene{makeScene(500, "Last Scene")}
	page2Scenes[0].Site.Name = "TestSite"
	page2Scenes[0].Site.NickName = "testsite"

	page1 := fakeESResponse(page1Scenes, pageSize+1)
	page2 := fakeESResponse(page2Scenes, pageSize+1)
	empty := fakeESResponse(nil, pageSize+1)

	// page1Cursor is the search_after value Run() sends for page 2: it's
	// built from the last hit of page 1, round-tripped through JSON the same
	// way the request body is, so it compares equal to what the handler
	// decodes regardless of concrete Go type (float64 vs int, etc).
	page1Cursor := sortCursor(page1Scenes[len(page1Scenes)-1])

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()

		switch sa := body["search_after"]; {
		case sa == nil:
			_, _ = w.Write(page1)
		case reflect.DeepEqual(sa, page1Cursor):
			_, _ = w.Write(page2)
		default:
			_, _ = w.Write(empty)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client:    ts.Client(),
		cfg:       SiteConfig{Index: "test", SiteBase: ts.URL, Domain: "test.com", SiteID: "test"},
		esBaseURL: ts.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out := make(chan scraper.SceneResult, 100)
	go s.Run(ctx, ts.URL, scraper.ListOpts{Workers: 3}, out)

	var scenes int
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if scenes != pageSize+1 {
		t.Errorf("got %d scenes, want %d", scenes, pageSize+1)
	}
	if len(requests) < 2 {
		t.Fatalf("got %d requests, want at least 2", len(requests))
	}

	if _, ok := requests[0]["from"]; ok {
		t.Error("first request should not have 'from' (using search_after)")
	}
	if _, ok := requests[0]["search_after"]; ok {
		t.Error("first request should not have search_after")
	}

	if _, ok := requests[1]["search_after"]; !ok {
		t.Error("second request missing search_after cursor")
	}
	if _, ok := requests[1]["from"]; ok {
		t.Error("second request should not have 'from'")
	}
}

// sortCursor returns the JSON round-tripped value that a search_after built
// from this scene's sort tuple would decode to on the wire (e.g. an ItemID
// becomes float64, as encoding/json decodes any number into map[string]any).
func sortCursor(s esScene) any {
	dateBytes, _ := json.Marshal(s.PublishedDate)
	idBytes, _ := json.Marshal(s.ItemID)
	raw, _ := json.Marshal([]json.RawMessage{dateBytes, idBytes})
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

// A bare sub-site domain (filterAll) with a configured NickName must constrain
// the search to that site's nickName rather than scraping the whole index.
func TestRunBareDomainFiltersByNickName(t *testing.T) {
	var firstQuery map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if firstQuery == nil {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			firstQuery = body
		}
		_, _ = w.Write(fakeESResponse(nil, 0))
	}))
	defer ts.Close()

	s := &Scraper{
		client:    ts.Client(),
		cfg:       SiteConfig{Index: "ts_network", SiteBase: ts.URL, Domain: "teenpies.com", SiteID: "teenpies", NickName: "teenpies"},
		esBaseURL: ts.URL,
	}

	out := make(chan scraper.SceneResult, 10)
	s.Run(context.Background(), "https://www.teenpies.com/", scraper.ListOpts{}, out)

	must, _ := firstQuery["query"].(map[string]any)
	b, _ := must["bool"].(map[string]any)
	clauses, _ := b["must"].([]any)
	found := false
	for _, c := range clauses {
		cm, _ := c.(map[string]any)
		term, _ := cm["term"].(map[string]any)
		if v, ok := term["site.nickName.keyword"]; ok {
			if v != "teenpies" {
				t.Errorf("nickName filter = %v, want teenpies", v)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("bare domain query did not filter by site.nickName.keyword: %v", firstQuery)
	}
}

// A network-hub config (no NickName) must NOT add a nickName filter — it covers
// the whole index by design.
func TestRunHubNoNickNameFilter(t *testing.T) {
	var firstQuery map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if firstQuery == nil {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			firstQuery = body
		}
		_, _ = w.Write(fakeESResponse(nil, 0))
	}))
	defer ts.Close()

	s := &Scraper{
		client:    ts.Client(),
		cfg:       SiteConfig{Index: "ts_network", SiteBase: ts.URL, Domain: "teamskeet.com", SiteID: "teamskeet"},
		esBaseURL: ts.URL,
	}

	out := make(chan scraper.SceneResult, 10)
	s.Run(context.Background(), "https://www.teamskeet.com/", scraper.ListOpts{}, out)

	must, _ := firstQuery["query"].(map[string]any)
	b, _ := must["bool"].(map[string]any)
	clauses, _ := b["must"].([]any)
	for _, c := range clauses {
		cm, _ := c.(map[string]any)
		term, _ := cm["term"].(map[string]any)
		if _, ok := term["site.nickName.keyword"]; ok {
			t.Errorf("hub config must not add a nickName filter: %v", firstQuery)
		}
	}
}

func TestSearchParsesResponse(t *testing.T) {
	page1 := fakeESResponse([]esScene{makeScene(100, "Scene A"), makeScene(101, "Scene B")}, 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(page1)
	}))
	defer ts.Close()

	s := &Scraper{
		client: ts.Client(),
		cfg:    SiteConfig{Index: "test", SiteBase: ts.URL},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	origBase := esBase
	_ = origBase

	result, err := s.searchWithURL(ctx, ts.URL, map[string]any{
		"query": map[string]any{"match_all": map[string]any{}},
		"from":  0,
		"size":  30,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.Hits.Total.Value != 2 {
		t.Errorf("total = %d, want 2", result.Hits.Total.Value)
	}
	if len(result.Hits.Hits) != 2 {
		t.Errorf("hits = %d, want 2", len(result.Hits.Hits))
	}
	if result.Hits.Hits[0].Source.ItemID != 100 {
		t.Errorf("first hit itemId = %d, want 100", result.Hits.Hits[0].Source.ItemID)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build esResponse values in Go, so encode and decode share the
// struct tag and a renamed one round-trips unnoticed. This is a live
// POST https://tours-store.psmcdn.net/ts_network/_search response, kept whole and
// unedited. The endpoint is an unauthenticated Elasticsearch tour index — no
// credential, only a JSON body and an Origin header.
//
// Shapes a hand-written fixture would have got wrong:
//   - **`hits.total` is an object (`{"value":…,"relation":"gte"}`), not a
//     number.** That is the Elasticsearch 7+ format; a fixture written with a
//     bare `"total": 10000` decodes to zero against the real struct.
//   - **`relation` is "gte", so `total.value` is a lower bound, not a count.** ES
//     caps it at 10000 unless `track_total_hits` is set. This only feeds
//     scraper.Progress — pagination terminates on an empty `hits` array — so a
//     large network under-reports its progress total and scrapes fully anyway.
//     Documented here so the 10000 is not mistaken for a real catalogue size.
//   - `sort` mixes types (`[1785024000000, "movie_32365"]` — epoch millis and a
//     doc id), which is why esHit.Sort is []json.RawMessage rather than a
//     concrete type. It is the search_after cursor.
//   - the scene payload lives under `_source`, and `id` is a slug string while
//     `itemId` is numeric — two different identifiers side by side.
//   - `publishedDate` carries no timezone ("2026-07-26T00:00:00").
func TestGoldenESSearch(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "es_search.json"))
	if err != nil {
		t.Fatal(err)
	}

	var resp esResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(resp.Hits.Hits) != 2 {
		t.Fatalf("decoded %d hits, want 2", len(resp.Hits.Hits))
	}
	if resp.Hits.Total.Value != 10000 {
		t.Errorf("Total.Value = %d (hits.total.value) — note total is an object, not a number",
			resp.Hits.Total.Value)
	}

	h := resp.Hits.Hits[0]
	if len(h.Sort) != 2 {
		t.Errorf("Sort has %d elements (sort), want 2 — it is the search_after cursor", len(h.Sort))
	}

	src := h.Source
	if src.ID == "" {
		t.Error("Source.ID is empty (_source.id) — a slug, not the numeric id")
	}
	if src.ItemID != 32365 {
		t.Errorf("ItemID = %d (_source.itemId), want 32365", src.ItemID)
	}
	if src.Title == "" || src.Description == "" {
		t.Errorf("title/description empty: %q / %q", src.Title, src.Description)
	}
	if src.Img == "" {
		t.Error("Img is empty (_source.img) — every scene loses its thumbnail")
	}
	if src.PublishedDate != "2026-07-26T00:00:00" {
		t.Errorf("PublishedDate = %q (_source.publishedDate), want a bare timestamp with no zone",
			src.PublishedDate)
	}
	if len(src.Tags) == 0 {
		t.Error("Tags is empty (_source.tags)")
	}
	if len(src.Models) == 0 || src.Models[0].Name == "" {
		t.Errorf("models = %+v (_source.models[].name)", src.Models)
	}
	if src.Site.NickName == "" || src.Site.Name == "" {
		t.Errorf("site = %+v (_source.site.name/nickName) — nickName is the series filter key", src.Site)
	}
}

// The ES total format, pinned separately because it is the single most likely
// thing to be written wrong by hand and it fails silently (decodes to zero).
func TestGoldenESTotalIsAnObject(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "es_search.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"total":{"value":`)) {
		t.Error(`fixture no longer has the ES7 object form "total":{"value":…}; ` +
			`if the cluster downgraded to a bare number, esResponse must change too`)
	}
	if !bytes.Contains(body, []byte(`"relation":"gte"`)) {
		t.Log(`note: total.relation is no longer "gte" — the count may now be exact, ` +
			`which would make scraper.Progress accurate for large networks`)
	}
}

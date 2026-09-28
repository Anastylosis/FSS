package iwantclips

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const testStudioURL = "https://iwantclips.com/store/327/Diane-Andrews"

// ---- MatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://iwantclips.com/store/327/Diane-Andrews", true},
		{"http://iwantclips.com/store/999/some-model", true},
		{"https://iwantclips.com/store/327/Diane-Andrews/6182700/Some-Title", true},
		{"https://www.manyvids.com/Profile/123", false},
		{"https://www.mydirtyhobby.com/profil/123-user", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- storeParams ----

func TestStoreParams(t *testing.T) {
	cases := []struct {
		url          string
		wantMemberID string
		wantUsername string
		wantErr      bool
	}{
		{"https://iwantclips.com/store/327/Diane-Andrews", "327", "Diane-Andrews", false},
		{"https://iwantclips.com/store/999/some-model", "999", "some-model", false},
		{"https://www.manyvids.com/Profile/123", "", "", true},
	}
	for _, c := range cases {
		mid, uname, err := storeParams(c.url)
		if (err != nil) != c.wantErr {
			t.Errorf("storeParams(%q) error = %v, wantErr %v", c.url, err, c.wantErr)
			continue
		}
		if mid != c.wantMemberID {
			t.Errorf("storeParams(%q) memberID = %q, want %q", c.url, mid, c.wantMemberID)
		}
		if uname != c.wantUsername {
			t.Errorf("storeParams(%q) username = %q, want %q", c.url, uname, c.wantUsername)
		}
	}
}

// ---- parseVideoLength ----

func TestParseVideoLength(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"00:11:02", 662},
		{"01:00:00", 3600},
		{"00:05:30", 330},
		{"10:00", 600},
		{"", 0},
	}
	for _, c := range cases {
		got := parseVideoLength(c.input)
		if got != c.want {
			t.Errorf("parseVideoLength(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

// ---- toScene ----

func TestToScene(t *testing.T) {
	doc := iwcDoc{
		ContentID:     "6310185",
		Title:         "MILF JOI Countdown",
		Description:   "Some &amp;quot;description&amp;quot;",
		ContentURL:    "https://iwantclips.com/store/327/Diane-Andrews/6310185/MILF-JOI-Countdown",
		ThumbnailURL:  "https://cdn05.iwantclips.com/thumb.jpg",
		PreviewURL:    "https://cdn05.iwantclips.com/preview.png",
		Price:         9.99,
		PublishTime:   1700000000,
		VideoLength:   "00:11:02",
		Categories:    []string{"JOI", "MILF"},
		Keywords:      []string{"joi", "milf"},
		ModelUsername: "Diane Andrews",
	}
	now := time.Now().UTC()
	scene := toScene(testStudioURL, doc, now)

	if scene.ID != "6310185" {
		t.Errorf("ID = %q, want %q", scene.ID, "6310185")
	}
	if scene.SiteID != "iwantclips" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "iwantclips")
	}
	if scene.Title != "MILF JOI Countdown" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 662 {
		t.Errorf("Duration = %d, want 662", scene.Duration)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 9.99 {
		t.Errorf("PriceHistory = %v", scene.PriceHistory)
	}
	// &amp;quot; → &quot; after one unescape (toScene calls html.UnescapeString twice).
	if scene.Description != `Some "description"` {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Studio != "Diane Andrews" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	wantDate := time.Unix(1700000000, 0).UTC()
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
}

// ---- helpers for httptest ----

// storeHTML returns a minimal page embedding the Typesense config pointing at tsURL.
func storeHTML(tsURL string) []byte {
	host := strings.TrimPrefix(tsURL, "http://")
	host = strings.TrimPrefix(host, "https://")
	proto := "http"
	if strings.HasPrefix(tsURL, "https://") {
		proto = "https"
	}
	return []byte(fmt.Sprintf(`<html><body><script>
const typesenseInstantsearchAdapter = new TypesenseInstantSearchAdapter({
    server: {
        apiKey: 'test-api-key',
        nodes: [{
            host: '%s',
            port: '80',
            protocol: '%s',
        }, ],
    },
});
</script></body></html>`, host, proto))
}

func makeTSResponse(docs []iwcDoc, found int) []byte {
	hits := make([]tsHit, len(docs))
	for i, d := range docs {
		hits[i] = tsHit{Document: d}
	}
	b, _ := json.Marshal(tsResponse{Found: found, Hits: hits})
	return b
}

func testDoc(id string, publishTime int64) iwcDoc {
	return iwcDoc{
		ContentID:     id,
		Title:         "Scene " + id,
		Price:         9.99,
		PublishTime:   publishTime,
		VideoLength:   "00:10:00",
		ModelUsername: "Diane Andrews",
	}
}

// ---- ListScenes ----

func TestListScenes(t *testing.T) {
	page1Docs := []iwcDoc{testDoc("101", 1700002000), testDoc("102", 1700001000)}
	page2Docs := []iwcDoc{testDoc("103", 1700000000)}

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/collections/") {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write(makeTSResponse(page2Docs, 3))
			} else {
				_, _ = w.Write(makeTSResponse(page1Docs, 3))
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(storeHTML(ts.URL))
	}))
	defer ts.Close()

	s := &Scraper{
		client:     ts.Client(),
		siteBase:   ts.URL,
		collection: defaultCollection,
		perPage:    2,
	}

	studioURL := ts.URL + "/store/327/Diane-Andrews"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}

	if len(results) == 0 || results[0].Total != 3 {
		t.Fatalf("expected Total=3 hint as first result, got %+v", results)
	}
	var scenesOnly []scraper.SceneResult
	for _, r := range results {
		if r.Total == 0 && r.Err == nil {
			scenesOnly = append(scenesOnly, r)
		}
	}
	if len(scenesOnly) != 3 {
		t.Errorf("got %d scenes, want 3", len(scenesOnly))
	}
	if len(scenesOnly) > 0 && scenesOnly[0].Scene.ID != "101" {
		t.Errorf("first scene ID = %q, want %q", scenesOnly[0].Scene.ID, "101")
	}
}

// ---- KnownIDs early stop ----

func TestListScenesKnownIDs(t *testing.T) {
	docs := []iwcDoc{
		testDoc("201", 1700003000),
		testDoc("202", 1700002000), // known → triggers early stop
		testDoc("203", 1700001000),
	}

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/collections/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(makeTSResponse(docs, 3))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(storeHTML(ts.URL))
	}))
	defer ts.Close()

	s := &Scraper{
		client:     ts.Client(),
		siteBase:   ts.URL,
		collection: defaultCollection,
		perPage:    250,
	}

	studioURL := ts.URL + "/store/327/Diane-Andrews"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{
		KnownIDs: map[string]bool{"202": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenesOnly []scraper.SceneResult
	sawStoppedEarly := false
	for r := range ch {
		if r.Kind == scraper.KindStoppedEarly {
			sawStoppedEarly = true
			continue
		}
		if r.Total == 0 && r.Err == nil {
			scenesOnly = append(scenesOnly, r)
		}
	}
	if len(scenesOnly) != 2 {
		t.Errorf("got %d scenes, want 2 — the page is finished before the stop", len(scenesOnly))
	}
	if !sawStoppedEarly {
		t.Error("expected StoppedEarly signal, got none")
	}
	if len(scenesOnly) > 0 && scenesOnly[0].Scene.ID != "201" {
		t.Errorf("scene ID = %q, want %q", scenesOnly[0].Scene.ID, "201")
	}
}

// ---- fetchAPIKey ----

func TestFetchAPIKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(storeHTML("http://typesense.example.com"))
	}))
	defer ts.Close()

	s := New()
	s.client = ts.Client()

	apiKey, tsBase, err := s.fetchAPIKey(context.Background(), ts.URL+"/store/327/Diane-Andrews")
	if err != nil {
		t.Fatalf("fetchAPIKey error: %v", err)
	}
	if apiKey != "test-api-key" {
		t.Errorf("apiKey = %q, want %q", apiKey, "test-api-key")
	}
	if tsBase != "http://typesense.example.com" {
		t.Errorf("tsBase = %q, want %q", tsBase, "http://typesense.example.com")
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build iwcDoc values in Go, so encode and decode share the
// struct tag and a renamed tag round-trips unnoticed. This one decodes a
// Typesense search response captured verbatim from the live index and runs a
// document through toScene. (No credential is involved: the search key is
// scraped from the store page's own source, as fetchAPIKey does at runtime.)
//
// Notable shapes: documents are wrapped one level deep under `hits[].document`,
// `publish_time` is a **unix epoch** integer, `content_id` is a string while
// `price` is a float, and `video_length` is a duration string alongside them.
func TestGoldenTypesenseSearch(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "typesense_search.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp tsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if resp.Found == 0 {
		t.Error("Found is 0 (found) — pagination depends on it")
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("decoded %d hits, want 2 (hits[].document)", len(resp.Hits))
	}

	doc := resp.Hits[0].Document
	if doc.ContentID != "6467412" {
		t.Errorf("ContentID = %q (content_id, a string), want 6467412", doc.ContentID)
	}
	if doc.Title != "Seduced By Your Best Friends Stepmom" {
		t.Errorf("Title = %q (title)", doc.Title)
	}
	if doc.PublishTime == 0 {
		t.Fatal("PublishTime is 0 (publish_time, a unix epoch)")
	}
	if doc.Price != 14.99 {
		t.Errorf("Price = %v (price, a float), want 14.99", doc.Price)
	}
	if doc.ModelUsername == "" {
		t.Error("ModelUsername is empty (model_username)")
	}
	if len(doc.Keywords) == 0 {
		t.Error("Keywords is empty (keywords)")
	}

	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	sc := toScene("https://iwantclips.com/store/327/Diane-Andrews", doc, now)

	if sc.ID != "6467412" {
		t.Errorf("scene ID = %q", sc.ID)
	}
	if sc.Title == "" {
		t.Error("scene Title is empty")
	}
	// publish_time is seconds; treating it as ms would land far in the future.
	if sc.Date.Year() != 2026 {
		t.Errorf("scene Date = %v, want 2026 (publish_time is a second-epoch)", sc.Date)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 14.99 {
		t.Errorf("PriceHistory = %+v, want one snapshot at 14.99", sc.PriceHistory)
	}
	if len(sc.Tags) == 0 {
		t.Error("scene Tags is empty (keywords/categories)")
	}
}

// A document with no publish_time decodes to 0, which stored the scene as
// 1970-01-01 — a date that looks real and sorts first.
func TestPublishedAtGuardsTheEpoch(t *testing.T) {
	if got := publishedAt(0); !got.IsZero() {
		t.Errorf("publishedAt(0) = %v, want the zero time", got)
	}
	if got := publishedAt(-5); !got.IsZero() {
		t.Errorf("publishedAt(-5) = %v, want the zero time", got)
	}
	if got := publishedAt(1757944800); got.Year() != 2025 && got.Year() != 2026 {
		t.Errorf("publishedAt(valid) = %v", got)
	}
}

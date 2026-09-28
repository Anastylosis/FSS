package fancentro

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

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://fancentro.com/cherie-deville", true},
		{"https://www.fancentro.com/cherie-deville", true},
		{"https://fancentro.com/cherie-deville/", true},
		{"https://fancentro.com/someone_else", true},
		{"https://fancentro.com/cherie-deville?tab=videos", true},
		{"https://fancentro.com/", false},
		{"https://fancentro.com/cherie-deville/some/path", false},
		{"https://example.com/fancentro", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSlugFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://fancentro.com/cherie-deville", "cherie-deville"},
		{"https://www.fancentro.com/someone/", "someone"},
	}
	for _, c := range cases {
		if got := slugFromURL(c.url); got != c.want {
			t.Errorf("slugFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"14:03", 843},
		{"0:45", 45},
		{"1:05:30", 3930},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseutil.ParseDurationColon(c.input); got != c.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	clip := apiClip{
		ID:          12345,
		Title:       "Test Clip",
		Duration:    "14:03",
		PublishedAt: 1714000000,
		Link:        "/post/12345/test-clip",
		Tags:        []apiTag{{Name: "milf"}, {Name: "pov"}},
		Price: &apiPrice{
			OriginalPrice:  9.99,
			DiscountPrice:  4.99,
			HasDiscount:    true,
			Currency:       "USD",
			DiscountAmount: 50,
		},
		Model:      &apiModel{StageName: "Cherie DeVille"},
		Thumbnails: &apiThumbnails{Thumbnail: &apiThumb{Src: "https://cdn/thumb.jpg"}},
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sc := toScene(clip, "https://fancentro.com/cherie-deville", "https://fancentro.com", now)

	if sc.ID != "12345" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "fancentro" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.URL != "https://fancentro.com/post/12345/test-clip" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Title != "Test Clip" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 843 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.Studio != "Cherie DeVille" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Cherie DeVille" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Thumbnail != "https://cdn/thumb.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if len(sc.Tags) != 2 || sc.Tags[0] != "milf" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if len(sc.PriceHistory) != 1 {
		t.Fatalf("PriceHistory len = %d", len(sc.PriceHistory))
	}
	ph := sc.PriceHistory[0]
	if ph.Regular != 9.99 {
		t.Errorf("Regular = %f", ph.Regular)
	}
	if !ph.IsOnSale {
		t.Error("expected IsOnSale")
	}
	if ph.Discounted != 4.99 {
		t.Errorf("Discounted = %f", ph.Discounted)
	}
	if ph.DiscountPercent != 50 {
		t.Errorf("DiscountPercent = %d", ph.DiscountPercent)
	}
}

func TestToSceneFree(t *testing.T) {
	clip := apiClip{
		ID:    1,
		Title: "Free Clip",
		Link:  "/post/1/free",
		Price: &apiPrice{IsFree: true},
	}
	sc := toScene(clip, "https://fancentro.com/x", "https://fancentro.com", fixedTime())
	if len(sc.PriceHistory) != 1 || !sc.PriceHistory[0].IsFree {
		t.Errorf("expected IsFree, got %+v", sc.PriceHistory)
	}
}

func TestToSceneNoPrice(t *testing.T) {
	clip := apiClip{
		ID:    1,
		Title: "No Price",
		Link:  "/post/1/no-price",
	}
	sc := toScene(clip, "https://fancentro.com/x", "https://fancentro.com", fixedTime())
	if len(sc.PriceHistory) != 0 {
		t.Errorf("expected no price history, got %+v", sc.PriceHistory)
	}
}

type testClip struct {
	id    int
	title string
	dur   string
	date  int64
	price float64
}

func pageResponse(clips []testClip, total, lastPage int, modelName string) []byte {
	data := make([]map[string]any, len(clips))
	for i, c := range clips {
		data[i] = map[string]any{
			"id":          c.id,
			"title":       c.title,
			"duration":    c.dur,
			"publishedAt": c.date,
			"link":        fmt.Sprintf("/post/%d/slug", c.id),
			"tags":        []map[string]string{{"name": "tag1"}},
			"price": map[string]any{
				"originalPrice": c.price,
				"currency":      "USD",
				"isFree":        false,
				"hasDiscount":   false,
			},
			"model": map[string]string{"stageName": modelName},
			"thumbnails": map[string]any{
				"thumbnail": map[string]string{"src": "https://cdn/thumb.jpg"},
			},
		}
	}
	resp := map[string]any{
		"success":     true,
		"data":        data,
		"total_items": total,
		"last_page":   lastPage,
	}
	b, _ := json.Marshal(resp)
	return b
}

func newTestServer(pages [][]testClip, total int, modelName string) *httptest.Server {
	pageIdx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		lastPage := len(pages)
		if pageIdx >= len(pages) {
			_, _ = fmt.Fprint(w, `{"success":true,"data":[],"total_items":0,"last_page":1}`)
			return
		}
		clips := pages[pageIdx]
		pageIdx++
		_, _ = w.Write(pageResponse(clips, total, lastPage, modelName))
	}))
}

func TestListScenes(t *testing.T) {
	clips := []testClip{
		{id: 100, title: "Clip One", dur: "10:00", date: 1714000000, price: 9.99},
		{id: 200, title: "Clip Two", dur: "5:30", date: 1713900000, price: 4.99},
	}

	ts := newTestServer([][]testClip{clips}, 2, "TestModel")
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/cherie-deville", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Title != "Clip One" {
		t.Errorf("first title = %q", results[0].Title)
	}
	if results[1].Title != "Clip Two" {
		t.Errorf("second title = %q", results[1].Title)
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := make([]testClip, perPage)
	for i := range page1 {
		page1[i] = testClip{
			id: i + 1, title: fmt.Sprintf("Clip %d", i+1),
			dur: "1:00", date: 1714000000, price: 5,
		}
	}
	page2 := []testClip{
		{id: 25, title: "Clip 25", dur: "1:00", date: 1714000000, price: 5},
		{id: 26, title: "Clip 26", dur: "1:00", date: 1714000000, price: 5},
	}

	ts := newTestServer([][]testClip{page1, page2}, 26, "TestModel")
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/model", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 26 {
		t.Fatalf("got %d scenes, want 26", len(results))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	clips := []testClip{
		{id: 1, title: "New", dur: "1:00", date: 1714000000, price: 5},
		{id: 2, title: "Also New", dur: "1:00", date: 1713900000, price: 5},
		{id: 3, title: "Known", dur: "1:00", date: 1713800000, price: 5},
		{id: 4, title: "Old", dur: "1:00", date: 1713700000, price: 5},
	}

	ts := newTestServer([][]testClip{clips}, 4, "TestModel")
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/model", scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3 — the page is finished before the stop", len(results))
	}
	if results[0].ID != "1" || results[1].ID != "2" {
		t.Errorf("scenes = %v, %v", results[0].ID, results[1].ID)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build apiClip values in Go, so encode and decode share the
// struct tag and a renamed tag round-trips unnoticed. This one serves a page
// captured verbatim from fancentro.com. Each assertion names the field it pins.
//
// Notable shapes: `publishedAt` is a **unix timestamp**, not a date string;
// `duration` is "14:03" rather than seconds; and price/model/thumbnails are
// nested pointers that are easy to leave nil without noticing.
func TestGoldenContent(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "content.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`{"data":[],"success":true,"last_page":1,"current_page":2}`))
			return
		}
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/cherie-deville", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes from the captured page, want 2", len(scenes))
	}

	var sc *models.Scene
	for i := range scenes {
		if scenes[i].ID == "2456181" {
			sc = &scenes[i]
		}
	}
	if sc == nil {
		t.Fatalf("scene 2456181 missing (data[].id); got %v", scenes)
	}
	if sc.Title != "Suck & Fuck Out The Devil" {
		t.Errorf("Title = %q (data[].title)", sc.Title)
	}
	// duration is "14:03".
	if sc.Duration != 14*60+3 {
		t.Errorf("Duration = %d (data[].duration \"14:03\"), want %d", sc.Duration, 14*60+3)
	}
	// publishedAt is a unix timestamp (1719356896).
	if sc.Date.IsZero() {
		t.Error("Date is zero (data[].publishedAt, a unix timestamp)")
	}
	if sc.Date.Year() != 2024 {
		t.Errorf("Date = %v (data[].publishedAt), want 2024 — a mis-scaled timestamp is the usual bug", sc.Date)
	}
	if !strings.Contains(sc.URL, "suck-fuck-out-the-devil") {
		t.Errorf("URL = %q (data[].link)", sc.URL)
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail is empty (data[].thumbnails.thumbnail.src)")
	}
	if len(sc.Performers) == 0 {
		t.Error("Performers is empty (data[].model.stageName)")
	}
}

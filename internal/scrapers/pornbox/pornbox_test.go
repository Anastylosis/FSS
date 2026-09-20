package pornbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestScraperInterface(t *testing.T) {
	var s scraper.StudioScraper = New()
	if s.ID() != "pornbox" {
		t.Fatalf("ID = %q, want pornbox", s.ID())
	}
	if len(s.Patterns()) == 0 {
		t.Fatal("Patterns() is empty")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://pornbox.com/application/studio/123", true},
		{"https://www.pornbox.com/application/studio/381", true},
		{"https://teen.pornbox.com/application/studio/3233", true},
		{"https://pornbox.com/application/model/5339", true},
		{"https://www.pornbox.com/application/model/123", true},
		{"https://pornbox.com/", false},
		{"https://pornbox.com/application/watch/12345", false},
		{"https://analvids.com/studios/foo", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestClassifyURL(t *testing.T) {
	tests := []struct {
		url      string
		wantMode urlMode
		wantID   string
	}{
		{"https://pornbox.com/application/studio/123", modeStudio, "123"},
		{"https://www.pornbox.com/application/studio/381", modeStudio, "381"},
		{"https://pornbox.com/application/model/5339", modeModel, "5339"},
		{"https://pornbox.com/", modeStudio, ""},
	}
	for _, tt := range tests {
		mode, id := classifyURL(tt.url)
		if mode != tt.wantMode || id != tt.wantID {
			t.Errorf("classifyURL(%q) = (%v, %q), want (%v, %q)", tt.url, mode, id, tt.wantMode, tt.wantID)
		}
	}
}

func TestParseRuntime(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"00:28:51", 1731},
		{"01:00:00", 3600},
		{"30:00", 1800},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseRuntime(tt.input); got != tt.want {
			t.Errorf("parseRuntime(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestToScene(t *testing.T) {
	item := contentItem{
		ID:          217308,
		SceneName:   "Test Scene Title",
		PublishDate: "2021-07-11T22:00:00.000Z",
		Runtime:     "00:28:51",
		Studio:      "Test Studio",
		Models: []modelRef{
			{ModelName: "Model One", ModelID: 1},
			{ModelName: "Model Two", ModelID: 2},
		},
		Niches: []nicheRef{
			{Niche: "anal"},
			{Niche: "asian"},
		},
		Thumbnail: thumbnailSet{
			Large: "https://cdn77-image.gtflixtv.com/large.jpg",
			List:  "https://cdn77-image.gtflixtv.com/list.jpg",
		},
		PriceUSD: 10.05,
	}

	sc := toScene(item, "https://pornbox.com/application/studio/123", "Override Studio")

	if sc.ID != "217308" {
		t.Errorf("ID = %q, want 217308", sc.ID)
	}
	if sc.SiteID != "pornbox" {
		t.Errorf("SiteID = %q, want pornbox", sc.SiteID)
	}
	if sc.Title != "Test Scene Title" {
		t.Errorf("Title = %q, want Test Scene Title", sc.Title)
	}
	if sc.URL != "https://pornbox.com/application/watch/217308" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Duration != 1731 {
		t.Errorf("Duration = %d, want 1731", sc.Duration)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Model One" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 2 || sc.Tags[0] != "anal" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if sc.Studio != "Override Studio" {
		t.Errorf("Studio = %q, want Override Studio", sc.Studio)
	}
	if sc.Thumbnail != "https://cdn77-image.gtflixtv.com/large.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Date.IsZero() {
		t.Error("Date is zero")
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 10.05 {
		t.Errorf("PriceHistory = %v", sc.PriceHistory)
	}
}

func TestToSceneFallbackStudio(t *testing.T) {
	item := contentItem{
		ID:        1,
		SceneName: "Test",
		Studio:    "Fallback Studio",
	}
	sc := toScene(item, "https://pornbox.com/application/studio/1", "")
	if sc.Studio != "Fallback Studio" {
		t.Errorf("Studio = %q, want Fallback Studio", sc.Studio)
	}
}

func TestToSceneThumbnailFallback(t *testing.T) {
	item := contentItem{
		ID:        1,
		SceneName: "Test",
		Thumbnail: thumbnailSet{List: "https://cdn77-image.gtflixtv.com/list.jpg"},
	}
	sc := toScene(item, "https://pornbox.com/application/studio/1", "")
	if sc.Thumbnail != "https://cdn77-image.gtflixtv.com/list.jpg" {
		t.Errorf("Thumbnail = %q, want list.jpg fallback", sc.Thumbnail)
	}
}

func newTestScraper(ts *httptest.Server) *Scraper {
	c := ts.Client()
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
	return &Scraper{
		client:  c,
		baseURL: ts.URL,
	}
}

func TestRunStudio(t *testing.T) {
	listing := listingResp{
		TotalCount:  2,
		TotalPages:  1,
		CurrentPage: 0,
		Contents: []contentItem{
			{
				ID:          100,
				SceneName:   "Scene One",
				PublishDate: "2024-01-01T00:00:00.000Z",
				Runtime:     "00:30:00",
				Studio:      "Test Studio",
				Models:      []modelRef{{ModelName: "Star", ModelID: 1}},
				Niches:      []nicheRef{{Niche: "tag1"}},
				Thumbnail:   thumbnailSet{Large: "https://example.com/thumb.jpg"},
			},
			{
				ID:          101,
				SceneName:   "Scene Two",
				PublishDate: "2024-01-02T00:00:00.000Z",
				Runtime:     "00:25:00",
				Studio:      "Test Studio",
			},
		},
	}

	studioInfo := studioInfoResp{Name: "Test Studio"}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/studio/info/42":
			_ = json.NewEncoder(w).Encode(studioInfo)
		case "/studio/42/":
			_ = json.NewEncoder(w).Encode(listing)
		case "/":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)

	ch, err := s.ListScenes(context.Background(), "https://pornbox.com/application/studio/42", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var totalSent, sceneCount int
	for r := range ch {
		switch r.Kind {
		case scraper.KindTotal:
			totalSent = r.Total
		case scraper.KindScene:
			sceneCount++
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}

	if totalSent != 2 {
		t.Errorf("total = %d, want 2", totalSent)
	}
	if sceneCount != 2 {
		t.Errorf("scene count = %d, want 2", sceneCount)
	}
}

func TestRunKnownIDs(t *testing.T) {
	listing := listingResp{
		TotalCount:  3,
		TotalPages:  1,
		CurrentPage: 0,
		Contents: []contentItem{
			{ID: 100, SceneName: "New Scene", Studio: "S"},
			{ID: 101, SceneName: "Known Scene", Studio: "S"},
			{ID: 102, SceneName: "Should Not Reach", Studio: "S"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/studio/info/1":
			_ = json.NewEncoder(w).Encode(studioInfoResp{Name: "S"})
		case "/studio/1/":
			_ = json.NewEncoder(w).Encode(listing)
		case "/":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	opts := scraper.ListOpts{
		KnownIDs: map[string]bool{"101": true},
	}

	ch, err := s.ListScenes(context.Background(), "https://pornbox.com/application/studio/1", opts)
	if err != nil {
		t.Fatal(err)
	}

	var sceneCount int
	var stoppedEarly bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}

	if sceneCount != 1 {
		t.Errorf("scene count = %d, want 1", sceneCount)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestRunPagination(t *testing.T) {
	page0 := listingResp{
		TotalCount:  3,
		TotalPages:  2,
		CurrentPage: 0,
		Contents: []contentItem{
			{ID: 100, SceneName: "Scene 1", Studio: "S"},
			{ID: 101, SceneName: "Scene 2", Studio: "S"},
		},
	}
	page1 := listingResp{
		TotalCount:  3,
		TotalPages:  2,
		CurrentPage: 1,
		Contents: []contentItem{
			{ID: 102, SceneName: "Scene 3", Studio: "S"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/studio/info/1":
			_ = json.NewEncoder(w).Encode(studioInfoResp{Name: "S"})
		case "/studio/1/":
			skip := r.URL.Query().Get("skip")
			switch skip {
			case "0":
				_ = json.NewEncoder(w).Encode(page0)
			case "1":
				_ = json.NewEncoder(w).Encode(page1)
			default:
				_ = json.NewEncoder(w).Encode(listingResp{})
			}
		case "/":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)

	ch, err := s.ListScenes(context.Background(), "https://pornbox.com/application/studio/1", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var sceneCount int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}

	if sceneCount != 3 {
		t.Errorf("scene count = %d, want 3", sceneCount)
	}
}

func TestRunModel(t *testing.T) {
	listing := listingResp{
		TotalCount:  1,
		TotalPages:  1,
		CurrentPage: 0,
		Contents: []contentItem{
			{
				ID:        200,
				SceneName: "Model Scene",
				Studio:    "Some Studio",
				Models:    []modelRef{{ModelName: "Test Model", ModelID: 99}},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/model/content/99/":
			_ = json.NewEncoder(w).Encode(listing)
		case "/":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)

	ch, err := s.ListScenes(context.Background(), "https://pornbox.com/application/model/99", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var sceneCount int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
			if r.Scene.Studio != "Some Studio" {
				t.Errorf("Studio = %q, want Some Studio", r.Scene.Studio)
			}
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}

	if sceneCount != 1 {
		t.Errorf("scene count = %d, want 1", sceneCount)
	}
}

func TestSessionBootstrap(t *testing.T) {
	var sessionHit bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			sessionHit = true
			http.SetCookie(w, &http.Cookie{Name: "JDIALOG3", Value: "TESTVALUE"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/studio/info/1" {
			_ = json.NewEncoder(w).Encode(studioInfoResp{Name: "S"})
			return
		}
		if r.URL.Path == "/studio/1/" {
			if c, err := r.Cookie("JDIALOG3"); err != nil || c.Value != "TESTVALUE" {
				t.Errorf("expected JDIALOG3 cookie, got err=%v cookie=%v", err, c)
			}
			_ = json.NewEncoder(w).Encode(listingResp{
				TotalCount: 1, TotalPages: 1,
				Contents: []contentItem{{ID: 1, SceneName: "S", Studio: "S"}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), "https://pornbox.com/application/studio/1", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if !sessionHit {
		t.Error("session bootstrap request was never made")
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build contentItem values in Go, so encode and decode share the
// struct tag and a renamed tag round-trips unnoticed. This one decodes a studio
// listing captured verbatim from pornbox.com and runs an item through toScene.
//
// Notable shapes: the ID is `content_id` (there is a separate `id` alongside it,
// so a wrong pick still decodes and looks fine), `runtime` is "00:28:51" rather
// than seconds, and price is a float under `content_price_usd`.
func TestGoldenStudioContents(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "studio_contents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp listingResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if resp.TotalCount == 0 || resp.TotalPages == 0 {
		t.Errorf("totalCount/totalPages = %d/%d — pagination depends on them", resp.TotalCount, resp.TotalPages)
	}
	if len(resp.Contents) != 2 {
		t.Fatalf("decoded %d contents, want 2", len(resp.Contents))
	}

	it := resp.Contents[0]
	if it.ID != 217308 {
		t.Errorf("ID = %d (content_id, not the sibling id), want 217308", it.ID)
	}
	if it.PublishDate == "" {
		t.Error("PublishDate is empty (publish_date)")
	}
	if it.Runtime != "00:28:51" {
		t.Errorf("Runtime = %q (runtime)", it.Runtime)
	}
	if it.PriceUSD == 0 {
		t.Error("PriceUSD is 0 (content_price_usd)")
	}

	sc := toScene(it, "https://pornbox.com/application/studio/123", "Test Studio")
	if sc.ID != "217308" {
		t.Errorf("scene ID = %q", sc.ID)
	}
	// runtime "00:28:51" must parse to seconds.
	if sc.Duration != 28*60+51 {
		t.Errorf("scene Duration = %d, want %d (runtime %q)", sc.Duration, 28*60+51, it.Runtime)
	}
	if sc.Date.IsZero() {
		t.Error("scene Date is zero (publish_date)")
	}
	if len(sc.PriceHistory) == 0 {
		t.Error("scene PriceHistory is empty (content_price_usd)")
	}
}

// The session bootstrap is a plain homepage fetch used to seed the cookie jar.
// Caching its failure for the process lifetime turned one blip into a dead
// scraper, so a failed attempt must be retried on the next call.
func TestInitSessionRetriesAfterFailure(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = fmt.Fprint(w, "<html></html>")
	}))
	defer ts.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	s := newTestScraper(ts)
	s.baseURL = deadURL
	if err := s.initSession(context.Background()); err == nil {
		t.Fatal("first initSession: want an error")
	}

	s.baseURL = ts.URL
	if err := s.initSession(context.Background()); err != nil {
		t.Fatalf("second initSession: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}
	// A success is remembered, so later calls do not re-fetch.
	if err := s.initSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("hits = %d after a cached call, want 1", got)
	}
}

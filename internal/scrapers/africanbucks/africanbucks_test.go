package africanbucks

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
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://africancasting.com/", true},
		{"https://www.africancasting.com/", true},
		{"https://africanlesbians.com", true},
		{"https://africansextrip.com/some/path", true},
		{"https://africangf.com/", true},
		{"https://analfucktour.com/", true},
		{"https://blackfucktour.com/", true},
		{"https://www.facefucktour.com/", true},
		{"https://fuckmyjeans.com/", true},
		{"https://latinacasting.com/", true},
		{"https://latinafucktour.com/", true},
		{"https://realafricans.com/", true},
		{"https://ripherup.com/", true},
		{"https://www.sexpacker.com/", true},
		{"https://africanbucks.com/", true},
		{"https://africanfucktour.com/", true},
		{"https://example.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestAPIBase(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://africancasting.com/", "https://members.africancasting.com"},
		{"https://www.sexpacker.com/", "https://members.sexpacker.com"},
		{"https://africanbucks.com/", "https://members.africancasting.com"},
		{"https://www.realafricans.com/", "https://members.realafricans.com"},
	}
	for _, c := range cases {
		got, err := apiBase(c.url)
		if err != nil {
			t.Errorf("apiBase(%q) error: %v", c.url, err)
			continue
		}
		if got != c.want {
			t.Errorf("apiBase(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	v := apiVideo{
		ID:          "2621",
		Title:       " Test Scene ",
		Length:      "1122",
		Description: " A description ",
		Channels:    "Amateur, Big cock, Blowjob",
		Models:      "Lena Rum",
		URL:         "https://members.africancasting.com/video/test-scene-2621.html",
		MainThumb:   "https://cdn.example.com/thumbs/abc-2024-09-02-lena-rum-sl.mp4/thumb-3.jpg",
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sc := toScene(v, "https://africancasting.com/", now)

	if sc.ID != "2621" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "africanbucks" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Title != "Test Scene" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 1122 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.Description != "A description" {
		t.Errorf("Description = %q", sc.Description)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Lena Rum" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 3 || sc.Tags[0] != "Amateur" || sc.Tags[2] != "Blowjob" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if sc.Date.Format("2006-01-02") != "2024-09-02" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Thumbnail != v.MainThumb {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != v.URL {
		t.Errorf("URL = %q", sc.URL)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].IsFree {
		t.Errorf("PriceHistory = %+v", sc.PriceHistory)
	}
}

func TestToSceneNoDate(t *testing.T) {
	v := apiVideo{
		ID:        "1700",
		Title:     "Old Scene",
		Length:    "300",
		URL:       "https://members.africancasting.com/video/old-scene-1700.html",
		MainThumb: "https://cdn.example.com/thumbs/abc-old-video.mp4/thumb-3.jpg",
	}
	sc := toScene(v, "https://africancasting.com/", time.Now().UTC())
	if !sc.Date.IsZero() {
		t.Errorf("expected zero date, got %v", sc.Date)
	}
}

func TestToSceneMultiplePerformers(t *testing.T) {
	v := apiVideo{
		ID:     "100",
		Title:  "Multi",
		Length: "600",
		Models: "Anina, Kiki Tonx",
		URL:    "https://members.africancasting.com/video/multi-100.html",
	}
	sc := toScene(v, "https://africancasting.com/", time.Now().UTC())
	if len(sc.Performers) != 2 || sc.Performers[0] != "Anina" || sc.Performers[1] != "Kiki Tonx" {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"Amateur, Big cock, Blowjob", []string{"Amateur", "Big cock", "Blowjob"}},
		{"Lena Rum", []string{"Lena Rum"}},
		{"", []string{}},
	}
	for _, c := range cases {
		got := splitCSV(c.input)
		if c.input == "" {
			if len(got) != 0 {
				t.Errorf("splitCSV(%q) = %v, want empty", c.input, got)
			}
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("splitCSV(%q) len = %d, want %d", c.input, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", c.input, i, got[i], c.want[i])
			}
		}
	}
}

func apiJSON(videos []apiVideo, total int) []byte {
	resp := apiResponse{
		Success:      true,
		TotalResults: total,
		Data:         videos,
	}
	b, _ := json.Marshal(resp)
	return b
}

func newTestServer(pages [][]apiVideo, total int) *httptest.Server {
	callIdx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if callIdx >= len(pages) {
			_, _ = w.Write(apiJSON(nil, total))
			return
		}
		vids := pages[callIdx]
		callIdx++
		_, _ = w.Write(apiJSON(vids, total))
	}))
}

func makeVideos(start, count int) []apiVideo {
	videos := make([]apiVideo, count)
	for i := range count {
		id := start + i
		videos[i] = apiVideo{
			ID:        fmt.Sprintf("%d", id),
			Title:     fmt.Sprintf("Scene %d", id),
			Length:    "300",
			Models:    "Test Model",
			Channels:  "Amateur",
			URL:       fmt.Sprintf("https://members.africancasting.com/video/scene-%d.html", id),
			MainThumb: fmt.Sprintf("https://cdn.example.com/thumbs/abc-2024-01-%02d-model.mp4/thumb.jpg", (i%28)+1),
		}
	}
	return videos
}

func TestListScenes(t *testing.T) {
	videos := []apiVideo{
		{ID: "100", Title: "Scene One", Length: "600", Models: "Model A", URL: "https://members.x.com/video/one-100.html",
			MainThumb: "https://cdn.example.com/abc-2024-05-01-model.mp4/thumb.jpg"},
		{ID: "99", Title: "Scene Two", Length: "900", Models: "Model B", URL: "https://members.x.com/video/two-99.html",
			MainThumb: "https://cdn.example.com/abc-2024-04-15-model.mp4/thumb.jpg"},
	}

	ts := newTestServer([][]apiVideo{videos}, 2)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), "https://africancasting.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Title != "Scene One" {
		t.Errorf("first title = %q", results[0].Title)
	}
	if results[0].Duration != 600 {
		t.Errorf("first duration = %d", results[0].Duration)
	}
	if results[1].Title != "Scene Two" {
		t.Errorf("second title = %q", results[1].Title)
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := makeVideos(1, perPage)
	page2 := makeVideos(perPage+1, 3)

	ts := newTestServer([][]apiVideo{page1, page2}, perPage+3)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), "https://africancasting.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != perPage+3 {
		t.Fatalf("got %d scenes, want %d", len(results), perPage+3)
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	videos := []apiVideo{
		{ID: "100", Title: "New", Length: "300", URL: "https://members.x.com/video/new-100.html"},
		{ID: "99", Title: "Also New", Length: "300", URL: "https://members.x.com/video/also-99.html"},
		{ID: "98", Title: "Known", Length: "300", URL: "https://members.x.com/video/known-98.html"},
		{ID: "97", Title: "Old", Length: "300", URL: "https://members.x.com/video/old-97.html"},
	}

	ts := newTestServer([][]apiVideo{videos}, 4)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), "https://africancasting.com/", scraper.ListOpts{
		KnownIDs: map[string]bool{"98": true},
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
	if results[0].ID != "100" || results[1].ID != "99" {
		t.Errorf("scenes = %v, %v", results[0].ID, results[1].ID)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build the API response from apiVideo values, so encode and
// decode share the struct tag and a renamed tag round-trips unnoticed. This one
// serves a response captured verbatim from members.africancasting.com.
//
// The payload is full of shapes a hand-built fixture gets right by accident:
// `id` and `length` arrive as JSON *strings* rather than numbers, and
// `models`/`channels`/`keywords` are comma-separated strings rather than arrays.
// The publish date is not in the payload at all — it is scraped out of the
// thumbnail filename, which only real data exercises.
func TestGoldenMediaNewest(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "media_newest.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "0" {
			// End the walk; the captured page is the only one.
			_, _ = w.Write([]byte(`{"success":true,"total_results":0,"data":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), "https://africancasting.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) == 0 {
		t.Fatal("no scenes parsed from the captured payload")
	}

	var sc *models.Scene
	for i := range scenes {
		if scenes[i].ID == "2639" {
			sc = &scenes[i]
		}
	}
	if sc == nil {
		t.Fatalf("scene 2639 missing (data[].id, a JSON string); got %d scenes", len(scenes))
	}
	if !strings.Contains(sc.Title, "Spicy Doll") {
		t.Errorf("Title = %q (data[].title)", sc.Title)
	}
	// length is the string "3785".
	if sc.Duration != 3785 {
		t.Errorf("Duration = %d (data[].length, a JSON string), want 3785", sc.Duration)
	}
	if sc.Description == "" {
		t.Error("Description is empty (data[].description)")
	}
	if !strings.Contains(sc.URL, "spicy-doll-can-t-keep-it-professional-2639") {
		t.Errorf("URL = %q (data[].url)", sc.URL)
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail is empty (data[].main_thumb)")
	}
	// models is "Spicy Doll" — one comma-separated string, not a list.
	if len(sc.Performers) != 1 || sc.Performers[0] != "Spicy Doll" {
		t.Errorf("Performers = %v (data[].models, comma-separated)", sc.Performers)
	}
	if len(sc.Tags) == 0 {
		t.Error("Tags is empty (data[].channels, comma-separated)")
	}
	// The date lives only in the thumbnail filename (…-2022-08-22-spicy-doll-…).
	if sc.Date.Format("2006-01-02") != "2022-08-22" {
		t.Errorf("Date = %v, want 2022-08-22 parsed out of main_thumb", sc.Date)
	}
}

package wankzutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestFetchPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/videos/find.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{
				"success": true,
				"count": 2,
				"result": [
					{
						"id": 100,
						"url": "https://www.wankz.com/scene-100",
						"title": "Test Scene",
						"description": "A test",
						"duration": 1800,
						"thumb": "https://images.wankz.com/100/cover.jpg",
						"channel": "Test Channel",
						"actors": ["Alice", "Bob"],
						"tags": ["tag1", "tag2"],
						"active_date": "2025-03-15 10:00:00"
					},
					{
						"id": 99,
						"url": "https://www.wankz.com/scene-99",
						"title": "Older Scene",
						"description": "",
						"duration": 900,
						"thumb": "https://images.wankz.com/99/cover.jpg",
						"channel": "Other Channel",
						"actors": ["Charlie"],
						"tags": [],
						"active_date": "2025-03-14 08:00:00"
					}
				]
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteID: "test", SiteBase: ts.URL}}
	videos, total, err := s.FetchPage(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("FetchPage error: %v", err)
	}
	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if len(videos) != 2 {
		t.Fatalf("expected 2 videos, got %d", len(videos))
	}
	if videos[0].Title != "Test Scene" {
		t.Errorf("expected title 'Test Scene', got %q", videos[0].Title)
	}
	if len(videos[0].Actors) != 2 || videos[0].Actors[0] != "Alice" {
		t.Errorf("unexpected actors: %v", videos[0].Actors)
	}
	if videos[0].Duration != 1800 {
		t.Errorf("expected duration 1800, got %d", videos[0].Duration)
	}
	if videos[0].Channel != "Test Channel" {
		t.Errorf("expected channel 'Test Channel', got %q", videos[0].Channel)
	}
}

func TestFetchPageChannelFilter(t *testing.T) {
	var gotChannel string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotChannel = r.URL.Query().Get("channel")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":true,"count":0,"result":null}`)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteID: "test", SiteBase: ts.URL}}
	_, _, _ = s.FetchPage(context.Background(), 1, "Teen Girls")
	if gotChannel != "Teen Girls" {
		t.Errorf("expected channel 'Teen Girls', got %q", gotChannel)
	}
}

func TestFetchPageNullResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":true,"count":0,"result":null}`)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteID: "test", SiteBase: ts.URL}}
	videos, total, err := s.FetchPage(context.Background(), 1, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Errorf("expected total 0, got %d", total)
	}
	if len(videos) != 0 {
		t.Errorf("expected 0 videos, got %d", len(videos))
	}
}

func TestToScene(t *testing.T) {
	cfg := SiteConfig{SiteID: "wankz", SiteBase: "https://www.wankz.com", StudioName: "Wankz"}
	v := Video{
		ID:          42,
		URL:         "https://www.wankz.com/test-scene-42",
		Title:       "Test Scene",
		Description: "Description",
		Duration:    1234,
		Thumb:       "https://images.wankz.com/42/cover.jpg",
		Channel:     "Matrix Models",
		Actors:      []string{"Alice"},
		Tags:        []string{"tag1"},
		ActiveDate:  "2025-06-15 12:00:00",
	}

	now := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	scene := ToScene(cfg, "https://www.wankz.com/", v, now)
	if scene.ID != "42" {
		t.Errorf("expected ID '42', got %q", scene.ID)
	}
	if scene.SiteID != "wankz" {
		t.Errorf("expected SiteID 'wankz', got %q", scene.SiteID)
	}
	if scene.Studio != "Matrix Models" {
		t.Errorf("expected Studio 'Matrix Models', got %q", scene.Studio)
	}
	if scene.Duration != 1234 {
		t.Errorf("expected Duration 1234, got %d", scene.Duration)
	}
	if scene.Date.IsZero() {
		t.Fatal("expected non-zero date")
	}
	if scene.Date.Year() != 2025 || scene.Date.Month() != 6 || scene.Date.Day() != 15 {
		t.Errorf("unexpected date: %v", scene.Date)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Alice" {
		t.Errorf("unexpected performers: %v", scene.Performers)
	}
}

func TestToSceneNullTitle(t *testing.T) {
	cfg := SiteConfig{SiteID: "lethalpass", SiteBase: "https://www.lethalpass.com", StudioName: "Lethal Pass"}
	v := Video{
		ID:         1638954,
		URL:        "https://www.lethalpass.com/1638954",
		Duration:   877,
		Actors:     []string{"Syren De Mer", "Tyler Nixon"},
		Tags:       []string{"Hardcore"},
		ActiveDate: "2025-01-01 00:00:00",
	}

	scene := ToScene(cfg, "https://www.lethalpass.com/", v, time.Now().UTC())
	if scene.Title == "" {
		t.Error("expected non-empty title fallback for null API title")
	}
}

func TestTitleFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.wankz.com/sexy-scene-name-12345", "Sexy scene name"},
		{"https://www.lethalpass.com/1638954", ""},
		{"https://www.wankz.com/just-a-slug", "Just a slug"},
		{"https://www.wankz.com/", ""},
	}
	for _, tt := range tests {
		if got := titleFromURL(tt.url); got != tt.want {
			t.Errorf("titleFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestParseChannel(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.wankz.com/", ""},
		{"https://www.wankz.com/videos", ""},
		{"https://www.wankz.com/channels/exploited-18", "exploited-18"},
		{"https://www.wankz.com/channels/exploited-18/", "exploited-18"},
		{"https://www.wankz.com/channels/", ""},
	}
	for _, tt := range tests {
		got := ParseChannel(tt.url)
		if got != tt.want {
			t.Errorf("ParseChannel(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestRunPagination(t *testing.T) {
	page := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case 1:
			_, _ = fmt.Fprint(w, `{"success":true,"count":52,"result":[
				{"id":3,"url":"u3","title":"S3","duration":100,"channel":"C","actors":[],"tags":[],"active_date":"2025-03-03 00:00:00"},
				{"id":2,"url":"u2","title":"S2","duration":100,"channel":"C","actors":[],"tags":[],"active_date":"2025-03-02 00:00:00"}
			]}`)
		case 2:
			_, _ = fmt.Fprint(w, `{"success":true,"count":52,"result":[
				{"id":1,"url":"u1","title":"S1","duration":100,"channel":"C","actors":[],"tags":[],"active_date":"2025-03-01 00:00:00"}
			]}`)
		default:
			_, _ = fmt.Fprint(w, `{"success":true,"count":52,"result":null}`)
		}
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteID: "test", SiteBase: ts.URL}}

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL, scraper.ListOpts{}, out)

	var scenes []string
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}
	if len(scenes) != 3 {
		t.Errorf("expected 3 scenes, got %d: %v", len(scenes), scenes)
	}
	if page != 2 {
		t.Errorf("expected 2 page fetches, got %d", page)
	}
}

func TestRunKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":true,"count":3,"result":[
			{"id":3,"url":"u3","title":"S3","duration":100,"channel":"C","actors":[],"tags":[],"active_date":"2025-03-03 00:00:00"},
			{"id":2,"url":"u2","title":"S2","duration":100,"channel":"C","actors":[],"tags":[],"active_date":"2025-03-02 00:00:00"},
			{"id":1,"url":"u1","title":"S1","duration":100,"channel":"C","actors":[],"tags":[],"active_date":"2025-03-01 00:00:00"}
		]}`)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client(), cfg: SiteConfig{SiteID: "test", SiteBase: ts.URL}}

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
	}, out)

	var scenes []string
	var stoppedEarly bool
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		}
	}
	if len(scenes) != 2 {
		t.Errorf("expected 2 scenes — the page is finished before the stop — got %d: %v", len(scenes), scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

package ladyboyplay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://ladyboyplay.com", "http://www.ladyboyplay.com/movies"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://ladyboyplay.net/", "https://example.com/ladyboyplay.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestRunEndToEnd(t *testing.T) {
	videos, performers := read(t, "videos.json"), read(t, "performers.json")

	// Add an inactive row and an upcoming one: neither is a released scene.
	var raw []map[string]any
	if err := json.Unmarshal(videos, &raw); err != nil {
		t.Fatal(err)
	}
	inactive := map[string]any{"_id": "x1", "alias": "gone", "name": "Gone", "status": "inactive", "type": "recent"}
	upcoming := map[string]any{"_id": "x2", "alias": "soon", "name": "Soon", "status": "active", "type": "upcoming"}
	videos, _ = json.Marshal(append(raw, inactive, upcoming))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/videos":
			_, _ = w.Write(videos)
		case "/api/v1/performers":
			_, _ = w.Write(performers)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL
	ch, err := s.ListScenes(context.Background(), "https://ladyboyplay.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			got = append(got, r.Scene.ID)
			if r.Scene.ID == "626fa9dc35d78c297d55c0fb" {
				sc := r.Scene
				if sc.Title != "BOTTOM FUCK HARD, NO HAND CUM, FACIAL CUM" || sc.Date.Format("2006-01-02") != "2022-05-02" {
					t.Errorf("Title/Date = %q/%v", sc.Title, sc.Date)
				}
				if sc.URL != srv.URL+"/movies/bottom-fuck-hard-no-hand-cum-facial-cum/626fa9dc35d78c297d55c0fb" {
					t.Errorf("URL = %q", sc.URL)
				}
				if len(sc.Performers) != 1 || sc.Performers[0] == "" || sc.Thumbnail == "" {
					t.Errorf("Performers/Thumbnail = %v/%q", sc.Performers, sc.Thumbnail)
				}
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if len(got) != 2 {
		t.Errorf("scenes = %v, want the two released ones", got)
	}
}

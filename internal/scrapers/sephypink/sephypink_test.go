package sephypink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/mymemberutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://sephypink.com", true},
		{"https://www.sephypink.com/videos", true},
		{"https://persephonepink.com/", false},
		{"https://sephypink.manyvids.com/", false},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
	if s.ID() != "sephypink" {
		t.Errorf("ID = %q", s.ID())
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cancellable-request":
			page := mymemberutil.VideosPage{
				CurrentPage: 1,
				LastPage:    1,
				Total:       1,
				PerPage:     30,
				Data: []mymemberutil.APIVideo{{
					ID:          7,
					Title:       "Test Scene",
					IsPublished: true,
					PublishDate: "2026-04-01T12:00:00.000000Z",
					Duration:    600,
					PosterSrc:   "https://cdn.example.com/t.jpg",
				}},
			}
			data, _ := json.Marshal(page)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": json.RawMessage(data)})
		default:
			_, _ = w.Write([]byte(`<html><head><meta property="og:description" content="Desc"/></head></html>`))
		}
	}))
	defer ts.Close()

	s := New()
	s.mm.Client = ts.Client()
	s.mm.SiteBase = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].ID != "7" || scenes[0].Studio != "Persephone Pink" || scenes[0].Duration != 600 {
		t.Errorf("unexpected scene: %+v", scenes[0])
	}
}

package velvetveronica

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://velvetveronica.com/product-category/videos/") || s.MatchesURL("https://velvetveronica.co/") {
		t.Error("MatchesURL")
	}
}

func serve(t *testing.T) {
	t.Helper()
	body, err := os.ReadFile("testdata/products.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wc/store/v1/products" || r.URL.Query().Get("category") != "29" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-WP-Total", "3")
		w.Header().Set("X-WP-TotalPages", "1")
		_, _ = w.Write(body)
	}))
	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = old; srv.Close() })
}

func TestScrapeDropsBundlesAndParsesFields(t *testing.T) {
	serve(t)
	ch, _ := New().ListScenes(context.Background(), "https://velvetveronica.com/", scraper.ListOpts{})
	var scenes []scraper.SceneResult
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (the bundle is not a scene)", len(scenes))
	}
	sc := scenes[0].Scene
	if sc.Title != "Cock Docked! Teasing & Edging with the Vacuglide 2 Cock Dock!" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 13*60+24 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.StudioURL != "https://velvetveronica.com/" || len(sc.Performers) != 1 || len(sc.Tags) == 0 {
		t.Errorf("scene = %+v", sc)
	}
	if sc.LowestPrice != 12.99 {
		t.Errorf("LowestPrice = %v", sc.LowestPrice)
	}
	if sc.Thumbnail == "" || sc.URL == "" || sc.Description == "" {
		t.Errorf("missing fields: %+v", sc)
	}
}

func TestToSceneStripsIconAndCode(t *testing.T) {
	sc := toScene(product{ID: 1, Name: "🎥 Edging Game with a Twist! (HJ75)"}, "s", time.Now())
	if sc.Title != "Edging Game with a Twist!" {
		t.Errorf("Title = %q", sc.Title)
	}
}

func TestDescriptionDropsTheSpecTable(t *testing.T) {
	sc := toScene(product{ID: 1, Name: "x", Description: "<p>Story.</p><table><tr><td>13:24 Minutes</td></tr></table>"}, "s", time.Now())
	if sc.Description != "Story." || sc.Duration != 13*60+24 {
		t.Errorf("Description = %q, Duration = %d", sc.Description, sc.Duration)
	}
}

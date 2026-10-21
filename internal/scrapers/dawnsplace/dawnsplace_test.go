package dawnsplace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.dawnsplace.com/",
		"https://dawnsplace.com",
		"https://dawnsplace.com/store/",
		"https://dawnsplace.com/store/product-category/videos-downloads-sorted-by-year/2021-videos/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://dawnsplace.com.evil.net/",
		"https://notdawnsplace.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestToScene(t *testing.T) {
	var products []product
	if err := json.Unmarshal(loadFixture(t, "products.json"), &products); err != nil {
		t.Fatal(err)
	}
	byID := map[int]models.Scene{}
	for _, p := range products {
		sc := toScene(p, "https://www.dawnsplace.com/", time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC))
		byID[p.ID] = sc
	}

	tr := byID[9879]
	if tr.Title != "Terrace Ride" || tr.Duration != 9*60+55 || tr.Width != 1920 || tr.Height != 1080 || tr.Resolution != "1080p" {
		t.Errorf("Terrace Ride: %+v", tr)
	}
	if tr.Description != "I Ride that Cock on the Patio" {
		t.Errorf("description = %q", tr.Description)
	}
	if tr.Thumbnail != "https://dawnsplace.com/store/wp-content/uploads/2025/03/terrace-ride-1.png" {
		t.Errorf("thumbnail should skip the blurred gif, got %q", tr.Thumbnail)
	}
	if tr.URL != "https://dawnsplace.com/store/product/terrace-ride/" || tr.ID != "9879" {
		t.Errorf("url/id = %q / %q", tr.URL, tr.ID)
	}
	if tr.LowestPrice != 19.95 || len(tr.Categories) != 1 || tr.Categories[0] != "2021 Videos" {
		t.Errorf("price/categories = %v %v", tr.LowestPrice, tr.Categories)
	}
	if len(tr.Performers) != 1 || tr.Performers[0] != "Dawn Allison" || tr.Studio != "Dawn's Place" {
		t.Errorf("performers/studio = %v %q", tr.Performers, tr.Studio)
	}

	if d := byID[9827].Duration; d != 13*60+50 {
		t.Errorf("Wet N Wild duration = %d (split across <b> tags)", d)
	}
	if got := byID[5328].Description; got != "Red Wine, Hubby and a webcam, we had a blast!" {
		t.Errorf("Vino Rosso 2 description = %q", got)
	}
	bf := byID[1947]
	if bf.Duration != 120 || bf.Width != 320 || bf.Height != 240 || bf.Description != "" {
		t.Errorf("Bed-fun2: %+v", bf)
	}
	if got := byID[9043].Duration; got != 11*60+55 {
		t.Errorf("Cowgirl Riding duration = %d", got)
	}
}

func newTestServer(t *testing.T, pages int, hits *atomic.Int32, gotCategory *atomic.Value) *httptest.Server {
	t.Helper()
	products := loadFixture(t, "products.json")
	cats := loadFixture(t, "categories.json")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/store/wp-json/wc/store/v1/products/categories":
			_, _ = w.Write(cats)
		case "/store/wp-json/wc/store/v1/products":
			hits.Add(1)
			gotCategory.Store(r.URL.Query().Get("category"))
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			w.Header().Set("X-WP-Total", "5")
			w.Header().Set("X-WP-TotalPages", strconv.Itoa(pages))
			if page > pages {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_, _ = w.Write(products)
		default:
			http.NotFound(w, r)
		}
	}))
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, int) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	total := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}
	return scenes, total
}

func TestListScenesStopsOnShortPage(t *testing.T) {
	var hits atomic.Int32
	var cat atomic.Value
	srv := newTestServer(t, 3, &hits, &cat)
	defer srv.Close()

	s := New()
	s.base = srv.URL + "/store"
	s.client = srv.Client()

	studioURL := "https://www.dawnsplace.com/"
	scenes, total := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 5 || total != 5 {
		t.Fatalf("got %d scenes, total %d", len(scenes), total)
	}
	if hits.Load() != 1 {
		t.Errorf("a page shorter than per_page must end the walk, got %d requests", hits.Load())
	}
	if cat.Load() != "16" {
		t.Errorf("default category = %v, want 16", cat.Load())
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL || sc.SiteID != "dawnsplace" {
			t.Errorf("scene %s: studioURL %q siteID %q", sc.ID, sc.StudioURL, sc.SiteID)
		}
	}
}

func TestListScenesCategoryURL(t *testing.T) {
	var hits atomic.Int32
	var cat atomic.Value
	srv := newTestServer(t, 1, &hits, &cat)
	defer srv.Close()

	s := New()
	s.base = srv.URL + "/store"
	s.client = srv.Client()

	scenes, _ := collect(t, s, "https://dawnsplace.com/store/product-category/videos-downloads-sorted-by-year/spotlight-15min-videos/", scraper.ListOpts{})
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	if cat.Load() != "44" {
		t.Errorf("category = %v, want 44", cat.Load())
	}
}

func TestListScenesUnknownCategory(t *testing.T) {
	var hits atomic.Int32
	var cat atomic.Value
	srv := newTestServer(t, 1, &hits, &cat)
	defer srv.Close()

	s := New()
	s.base = srv.URL + "/store"
	s.client = srv.Client()

	ch, _ := s.ListScenes(context.Background(), "https://dawnsplace.com/store/product-category/nope/", scraper.ListOpts{})
	var gotErr bool
	for r := range ch {
		if r.Kind == scraper.KindError {
			gotErr = true
		}
	}
	if !gotErr || hits.Load() != 0 {
		t.Errorf("unknown category: err=%v product requests=%d", gotErr, hits.Load())
	}
}

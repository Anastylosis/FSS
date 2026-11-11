package dfwknight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://dfwknight.com/tour/video-store/": true,
		"https://www.dfwknight.com/":              true,
		"https://example.com/":                    false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestToScene(t *testing.T) {
	var posts []post
	var products []product
	if err := json.Unmarshal(fixture(t, "posts.json"), &posts); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture(t, "store.json"), &products); err != nil {
		t.Fatal(err)
	}
	sc := toScene(posts[0], products[0], "https://dfwknight.com/tour/video-store/", time.Now())
	if sc.ID != "7392" || sc.Title != "Sage First BBC and Breeding Session" || !strings.Contains(sc.URL, "/product/sage-first") {
		t.Errorf("scene = %+v", sc)
	}
	if !sc.Date.Equal(time.Date(2026, 11, 16, 16, 44, 6, 0, time.UTC)) {
		t.Errorf("date = %v", sc.Date)
	}
	if !strings.HasPrefix(sc.Description, "I had just gotten back") || strings.Contains(sc.Description, "<") {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.LowestPrice != 15.95 || sc.Thumbnail == "" || len(sc.Tags) != 10 || sc.Tags[0] != "Black cock" {
		t.Errorf("price/thumb/tags = %v %q %v", sc.LowestPrice, sc.Thumbnail, sc.Tags)
	}
}

func TestToSceneWithoutProduct(t *testing.T) {
	sc := toScene(post{ID: 1, Date: "2026-01-02T03:04:05"}, product{}, "u", time.Now())
	if sc.LowestPrice != 0 || len(sc.PriceHistory) != 0 {
		t.Errorf("no store entry must not record a price: %+v", sc)
	}
}

func TestListScenes(t *testing.T) {
	var includes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/product":
			if r.URL.Query().Get("page") != "1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("X-WP-Total", "2")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write(fixture(t, "posts.json"))
		case "/wp-json/wc/store/products":
			includes = append(includes, r.URL.Query().Get("include"))
			_, _ = w.Write(fixture(t, "store.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://dfwknight.com/tour/video-store/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatal(r.Err)
		case scraper.KindScene:
			n++
			if r.Scene.LowestPrice != 15.95 {
				t.Errorf("scene %s price = %v", r.Scene.ID, r.Scene.LowestPrice)
			}
		}
	}
	if n != 2 || len(includes) != 1 || includes[0] != "7392,7389" {
		t.Errorf("scenes = %d, includes = %v", n, includes)
	}
}

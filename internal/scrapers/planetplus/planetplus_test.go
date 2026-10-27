package planetplus

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

func posts(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/posts.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToScene(t *testing.T) {
	var ps []post
	if err := json.Unmarshal(posts(t), &ps); err != nil {
		t.Fatal(err)
	}
	sc := toScene(ps[0], "https://planetplus.jp/", time.Now())
	if sc.ID != "NACT-218" || sc.Series != "七狗留" || sc.Director != "ひむろっく" {
		t.Errorf("ID/Series/Director = %q %q %q", sc.ID, sc.Series, sc.Director)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "釈アリス" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if !sc.Date.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) || sc.Duration != 124*60 {
		t.Errorf("Date = %v, Duration = %d", sc.Date, sc.Duration)
	}
	if sc.LowestPrice != 3180 || sc.Thumbnail == "" || sc.Description == "" || sc.Title == "" {
		t.Errorf("scene = %+v", sc)
	}
}

func serve(t *testing.T) *[]string {
	t.Helper()
	var queries []string
	body := posts(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/categories":
			_, _ = w.Write([]byte(`[{"id":79}]`))
		case "/wp-json/wp/v2/posts":
			queries = append(queries, r.URL.RawQuery)
			if r.URL.Query().Get("page") != "1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("X-WP-Total", "2")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = old; srv.Close() })
	return &queries
}

func TestLabelURLFiltersByCategory(t *testing.T) {
	q := serve(t)
	ch, _ := New().ListScenes(context.Background(), "https://planetplus.jp/wp01/category/label/nacr/", scraper.ListOpts{})
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			if r.Scene.StudioURL != "https://planetplus.jp/wp01/category/label/nacr/" {
				t.Errorf("StudioURL = %q", r.Scene.StudioURL)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != 2 || len(*q) != 1 || !contains((*q)[0], "categories=79") {
		t.Errorf("scenes=%d queries=%v", n, *q)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

package putricinta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/film.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToScene(t *testing.T) {
	var fs []film
	if err := json.Unmarshal(fixture(t), &fs); err != nil {
		t.Fatal(err)
	}
	sc := toScene(fs[0], "https://putricinta.com/", time.Now())
	if sc.Title != "443 Naked Namaste" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Date.IsZero() || sc.Thumbnail == "" || sc.URL == "" {
		t.Errorf("scene = %+v", sc)
	}
	if len(sc.Tags) != 3 || sc.Tags[0] != "Amthai" {
		t.Errorf("Tags = %v", sc.Tags)
	}
}

func TestListScenes(t *testing.T) {
	body := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/film":
			w.Header().Set("X-WP-Total", "2")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, _ := New().ListScenes(context.Background(), "https://putricinta.com/", scraper.ListOpts{})
	n := 0
	for r := range ch {
		if r.Kind == scraper.KindScene {
			n++
		}
		if r.Kind == scraper.KindError {
			t.Fatal(r.Err)
		}
	}
	if n != 2 {
		t.Errorf("scenes = %d", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://putricinta.com/") || s.MatchesURL("https://example.com/") {
		t.Error("MatchesURL")
	}
}

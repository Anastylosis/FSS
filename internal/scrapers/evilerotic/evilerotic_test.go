package evilerotic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func page(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/page.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParsePage(t *testing.T) {
	scenes := parsePage(page(t), "https://evilerotic.com/videos", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("scenes = %d, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1000079" || sc.Title != "My Busty Boss" || sc.URL != siteBase+"/videos/my-busty-boss" {
		t.Errorf("scene = %+v", sc)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Mary" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if !sc.Date.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) || sc.Duration != 30*60+48 || sc.Views != 2032 || sc.Thumbnail == "" {
		t.Errorf("Date/Duration/Views/Thumbnail = %v %d %d %q", sc.Date, sc.Duration, sc.Views, sc.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	body := page(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte("<html></html>"))
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://evilerotic.com/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		if r.Kind == scraper.KindError {
			t.Fatal(r.Err)
		}
		if r.Kind == scraper.KindScene {
			n++
		}
	}
	if n != 3 {
		t.Errorf("scenes = %d, want 3", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://evilerotic.com/videos": true,
		"https://www.evilerotic.com/":   true,
		"https://example.com/":          false,
	} {
		if s.MatchesURL(u) != want {
			t.Errorf("MatchesURL(%q) != %v", u, want)
		}
	}
}

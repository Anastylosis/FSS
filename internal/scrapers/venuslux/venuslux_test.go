package venuslux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://venus-lux.com/categories/movies.html": true,
		"https://www.venus-lux.com/":                   true,
		"https://example.com/":                         false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestCategory(t *testing.T) {
	if category("https://venus-lux.com/categories/movies_4_d.html") != "movies" || category("https://venus-lux.com/") != "movies" {
		t.Error("default category")
	}
	if category("https://venus-lux.com/categories/photos.html") != "photos" {
		t.Error("named category")
	}
}

func TestParseListing(t *testing.T) {
	scenes := parseListing(fixture(t, "listing1.html"), "https://venus-lux.com/", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("got %d cards", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "5733" || sc.Title != "Venus In Black Latex" ||
		sc.URL != "https://venus-lux.com/trailers/Venus-In-Black-Latex.html" ||
		sc.Thumbnail != "https://venus-lux.com/content/contentthumbs/57/33/5733-1x.jpg" ||
		!strings.HasSuffix(sc.Preview, "VenusInBlackLatex.mp4") {
		t.Errorf("scene = %+v", sc)
	}
}

func TestParseDescription(t *testing.T) {
	if d := parseDescription(fixture(t, "detail.html")); !strings.HasPrefix(d, "Rounding out the fetishy fun") || strings.Contains(d, "<") {
		t.Errorf("description = %q", d)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(s string) {
			_, _ = w.Write([]byte(strings.ReplaceAll(s, "https://venus-lux.com", srv.URL)))
		}
		switch p := r.URL.Path; {
		case p == "/categories/movies_1_d.html":
			write(fixture(t, "listing1.html"))
		case p == "/categories/movies_2_d.html":
			write(fixture(t, "listing2.html"))
		case strings.HasPrefix(p, "/categories/"):
			write("<html></html>")
		case p == "/trailers/Venus-In-Black-Latex.html":
			write(fixture(t, "detail.html"))
		case strings.HasPrefix(p, "/trailers/"):
			http.Error(w, "gone", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New()
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://venus-lux.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n, described := 0, 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatal(r.Err)
		case scraper.KindScene:
			n++
			if !strings.HasPrefix(r.Scene.URL, srv.URL) {
				t.Errorf("URL = %q", r.Scene.URL)
			}
			if r.Scene.Description != "" {
				described++
			}
		}
	}
	if n != 4 || described != 1 {
		t.Errorf("got %d scenes (%d described), want 4 (1)", n, described)
	}
}

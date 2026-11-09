package nothingbutcurves

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/listing.html")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.nothingbutcurves.com", "http://nothingbutcurves.com/", "https://www.nothingbutcurves.com/models/galda-lou.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://nothingbutcurves.net/", "https://example.com/nothingbutcurves.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListingPath(t *testing.T) {
	cases := []struct{ url, kind, slug string }{
		{"https://www.nothingbutcurves.com", "categories", "movies"},
		{"https://www.nothingbutcurves.com/categories/movies.html", "categories", "movies"},
		{"https://www.nothingbutcurves.com/categories/movies_3_d.html", "categories", "movies"},
		{"https://www.nothingbutcurves.com/models/galda-lou.html", "models", "galda-lou"},
		{"https://www.nothingbutcurves.com/models/galda-lou_2_d.html", "models", "galda-lou"},
		{"https://www.nothingbutcurves.com/categories/stockings.html", "categories", "stockings"},
	}
	for _, c := range cases {
		if k, s := listingPath(c.url); k != c.kind || s != c.slug {
			t.Errorf("listingPath(%q) = %q/%q, want %q/%q", c.url, k, s, c.kind, c.slug)
		}
	}
}

func TestParseListing(t *testing.T) {
	scenes := parseListing(fixture(t), "https://www.nothingbutcurves.com", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "fruity-foreplay-galda-lou-naked-lemon-grove-seduction-16346" || sc.Title != "FRUITY FOREPLAY - Galda Lou naked lemon grove seduction" {
		t.Errorf("ID/Title = %q/%q", sc.ID, sc.Title)
	}
	if sc.URL != "https://www.nothingbutcurves.com/scenes/fruity-foreplay-galda-lou-naked-lemon-grove-seduction-16346_vids.html" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Duration != 239 || sc.Date.Format("2006-01-02") != "2025-07-24" {
		t.Errorf("Duration/Date = %d/%v", sc.Duration, sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Galda Lou" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Thumbnail != "https://www.nothingbutcurves.com/content/contentthumbs/01/44/144-2x.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
}

func TestRunPaginatesUntilEmptyPage(t *testing.T) {
	page := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, string(page))
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL
	ch, err := s.ListScenes(context.Background(), "https://www.nothingbutcurves.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != 2 {
		t.Errorf("scenes = %d, want 2", n)
	}
}

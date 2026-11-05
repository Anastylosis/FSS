package dirtycinema

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

func TestParseCast(t *testing.T) {
	m := New(sites[1]).parseCast(fixture(t, "milfuckd_detail.html"))
	if len(m) != 2 || m[0] != "Piper Press" || m[1] != "Silas Stone" {
		t.Errorf("milfuckd cast = %v", m)
	}
	d := New(sites[0]).parseCast(fixture(t, "dirtycinema_detail.html"))
	if len(d) != 2 || d[0] != "Raee Ripley" || d[1] != "Jason Sarcinelli" {
		t.Errorf("dirtycinema cast = %v", d)
	}
}

func TestListScenes(t *testing.T) {
	detail := fixture(t, "milfuckd_detail.html")
	list := fixture(t, "videos.json")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/videos":
			w.Header().Set("X-WP-Total", "1")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write([]byte(strings.ReplaceAll(list, "LINK", srv.URL+"/videos/one/")))
		case "/videos/one/":
			_, _ = w.Write([]byte(detail))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New(sites[1])
	s.base = srv.URL
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://milfuckd.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		case scraper.KindScene:
			n++
			sc := r.Scene
			if sc.ID != "4517" || sc.SiteID != "milfuckd" || sc.Title != "Busty MILF shuts up annoying roommate – part 1" {
				t.Errorf("scene = %+v", sc)
			}
			if len(sc.Performers) != 2 || len(sc.Tags) != 2 || sc.Tags[1] != "Cum on Tits" {
				t.Errorf("performers/tags = %v %v", sc.Performers, sc.Tags)
			}
			if !strings.HasSuffix(sc.Thumbnail, "md-piper-press.jpg") || sc.Description != "Piper is a woman who enjoys her peace." {
				t.Errorf("thumb/desc = %q %q", sc.Thumbnail, sc.Description)
			}
			if !sc.Date.Equal(time.Date(2026, 11, 14, 18, 12, 50, 0, time.UTC)) {
				t.Errorf("Date = %v", sc.Date)
			}
		}
	}
	if n != 1 {
		t.Fatalf("scenes = %d", n)
	}
}

func TestMatchesURL(t *testing.T) {
	if !New(sites[0]).MatchesURL("https://dirtycinema.com/videos/") || New(sites[0]).MatchesURL("https://milfuckd.com/") {
		t.Error("dirtycinema MatchesURL")
	}
	if !New(sites[1]).MatchesURL("https://www.milfuckd.com/") || New(sites[1]).MatchesURL("https://dirtycinema.com/") {
		t.Error("milfuckd MatchesURL")
	}
}

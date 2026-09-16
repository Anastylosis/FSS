package pinkyxxx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://pinkyxxx.com/", "http://www.pinkyxxx.com", "https://pinkyxxx.com/video/x/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://pinkyxxx.net/", "https://example.com/pinkyxxx.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestToScene(t *testing.T) {
	var v video
	if err := json.Unmarshal([]byte(`{
		"id": 1087,
		"date_gmt": "2026-09-06T18:16:25",
		"link": "https://pinkyxxx.com/video/tits-in-public/",
		"slug": "tits-in-public",
		"title": {"rendered": "TITS IN PUBLIC &amp; MORE"},
		"_embedded": {"wp:featuredmedia": [{"source_url": "https://pinkyxxx.com/wp-content/uploads/a.jpg"}]}
	}`), &v); err != nil {
		t.Fatal(err)
	}
	scene := toScene(v, "https://pinkyxxx.com/", time.Now().UTC())
	if scene.ID != "1087" || scene.SiteID != siteID || scene.Studio != studioName {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", scene.ID, scene.SiteID, scene.Studio)
	}
	if scene.Title != "TITS IN PUBLIC & MORE" {
		t.Errorf("Title = %q", scene.Title)
	}
	if got := scene.Date.Format("2006-01-02"); got != "2026-09-06" {
		t.Errorf("Date = %s", got)
	}
	if scene.Thumbnail == "" {
		t.Error("Thumbnail is empty; the featured media is embedded in the same request")
	}
}

func TestToSceneFallsBackToTheSlug(t *testing.T) {
	scene := toScene(video{ID: 5, Slug: "no-title-here"}, "https://pinkyxxx.com/", time.Now().UTC())
	if scene.Title != "no title here" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.URL != siteBase+"/video/no-title-here/" {
		t.Errorf("URL = %q", scene.URL)
	}
}

func page(t *testing.T, n int) string {
	t.Helper()
	vs := make([]video, 0, n)
	for i := 0; i < n; i++ {
		var v video
		v.ID = i + 1
		v.Slug = fmt.Sprintf("scene-%d", i+1)
		v.DateGMT = "2026-01-01T00:00:00"
		v.Title.Rendered = fmt.Sprintf("Scene %d", i+1)
		vs = append(vs, v)
	}
	b, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// WP answers 400 past the last page rather than an empty list, and that is the
// only status that may end the walk: treating any error as the end would turn
// a 502 into a truncated catalogue.
func TestPaginationStopsOnTheWPEndOfListStatus(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, page(t, perPage))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"code":"rest_post_invalid_page_number"}`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes, errs int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
		}
	}
	if scenes != perPage {
		t.Errorf("got %d scenes, want %d", scenes, perPage)
	}
	if errs != 0 {
		t.Errorf("got %d errors, want none — 400 past the end is the end", errs)
	}
	if pages != 2 {
		t.Errorf("fetched %d pages, want 2", pages)
	}
}

func TestAShortPageEndsTheWalk(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages++
		_, _ = fmt.Fprint(w, page(t, 3))
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	var scenes int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 3 || pages != 1 {
		t.Errorf("got %d scenes over %d pages, want 3 over 1", scenes, pages)
	}
}

// A server error is not the end of the catalogue.
func TestServerErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, page(t, perPage))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if !strings.Contains(r.Err.Error(), "502") {
				t.Errorf("error = %v, want the 502 reported", r.Err)
			}
		}
	}
	if errs == 0 {
		t.Error("a 502 must be reported, not read as the end of the listing")
	}
}

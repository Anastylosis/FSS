package globetwatters

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

func testCfg() SiteConfig {
	return SiteConfig{SiteID: "asiansexdiary", Domain: "asiansexdiary.com", StudioName: "Asian Sex Diary"}
}

func TestMatchesURL(t *testing.T) {
	s := New(testCfg())
	for _, u := range []string{"https://asiansexdiary.com/", "http://www.asiansexdiary.com", "https://asiansexdiary.com/videos/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://trikepatrol.com/", "https://example.com/asiansexdiary.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestToScene(t *testing.T) {
	var it item
	if err := json.Unmarshal([]byte(`{
		"id": 201523,
		"date_gmt": "2026-09-14T14:05:56",
		"link": "https://asiansexdiary.com/videos/big-boobs-sex-cambodia/",
		"slug": "big-boobs-sex-cambodia",
		"title": {"rendered": "Big Boobs Sex In Cambodia &amp; More"},
		"content": {"rendered": "<h2>Horny</h2><p>A description.</p>"},
		"categories": [2, 99],
		"_embedded": {"wp:featuredmedia": [{"source_url": "https://asiansexdiary.com/a.jpg"}]}
	}`), &it); err != nil {
		t.Fatal(err)
	}
	scene := toScene(it, testCfg(), "https://asiansexdiary.com/", map[int]string{2: "Cambodia"}, time.Now().UTC())

	if scene.ID != "201523" || scene.SiteID != "asiansexdiary" || scene.Studio != "Asian Sex Diary" {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", scene.ID, scene.SiteID, scene.Studio)
	}
	if scene.Title != "Big Boobs Sex In Cambodia & More" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Description != "Horny A description." {
		t.Errorf("Description = %q", scene.Description)
	}
	if got := scene.Date.Format("2006-01-02"); got != "2026-09-14" {
		t.Errorf("Date = %s", got)
	}
	// An id with no resolved name is dropped rather than stored as a number.
	if strings.Join(scene.Categories, "|") != "Cambodia" {
		t.Errorf("Categories = %v", scene.Categories)
	}
	if scene.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
}

func itemsJSON(t *testing.T, n, offset int) string {
	t.Helper()
	items := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, map[string]any{
			"id":       offset + i,
			"date_gmt": "2026-01-01T00:00:00",
			"slug":     fmt.Sprintf("scene-%d", offset+i),
			"title":    map[string]string{"rendered": fmt.Sprintf("Scene %d", offset+i)},
		})
	}
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestListScenesPaginatesAndResolvesCategories(t *testing.T) {
	var contentPages, categoryPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/categories":
			categoryPages++
			if r.URL.Query().Get("page") == "1" {
				_, _ = fmt.Fprint(w, `[{"id":2,"name":"Cambodia"}]`)
				return
			}
			_, _ = fmt.Fprint(w, `[]`)
		case "/wp-json/wp/v2/rest-content":
			contentPages++
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = fmt.Fprint(w, itemsJSON(t, perPage, 1))
			case "2":
				_, _ = fmt.Fprint(w, itemsJSON(t, 3, 200))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New(testCfg())
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			scenes++
		}
	}
	if scenes != perPage+3 {
		t.Errorf("got %d scenes, want %d", scenes, perPage+3)
	}
	// A short page ends the walk, so the past-the-end 400 is never requested.
	if contentPages != 2 {
		t.Errorf("fetched %d content pages, want 2", contentPages)
	}
	if categoryPages == 0 {
		t.Error("categories were never resolved")
	}
}

// WP answers 400 past the last page; any other failure must be reported rather
// than read as the end of the catalogue.
func TestEndOfListOnlyAcceptsThePastTheEndStatus(t *testing.T) {
	var contentPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wp-json/wp/v2/categories" {
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		contentPages++
		if r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, itemsJSON(t, perPage, 1))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	s := New(testCfg())
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
		}
	}
	if errs == 0 {
		t.Error("a 502 must be reported, not treated as the end of the listing")
	}
}

func TestSitesRegistered(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if cfg.SiteID == "" || cfg.Domain == "" || cfg.StudioName == "" {
			t.Errorf("incomplete config: %+v", cfg)
		}
		if seen[cfg.SiteID] {
			t.Errorf("duplicate SiteID %q", cfg.SiteID)
		}
		seen[cfg.SiteID] = true
	}
	if len(sites) != 5 {
		t.Errorf("got %d sites, want 5", len(sites))
	}
}

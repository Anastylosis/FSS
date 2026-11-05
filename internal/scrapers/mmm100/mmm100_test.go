package mmm100

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func sitemap(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/sitemap.videos.xml")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSitemap(t *testing.T) {
	scenes, err := parseSitemap(sitemap(t), "https://mmm100.com/", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d", len(scenes))
	}
	if scenes[0].ID != "nadia-daferro_terry" {
		t.Errorf("not newest first: %q", scenes[0].ID)
	}
	sc := scenes[1]
	if sc.ID != "martina-vidal_beach" || sc.Title != "MILF blonde Martina Vidal doing a strip by the sea" || sc.Duration != 305 {
		t.Errorf("scene = %+v", sc)
	}
	if !sc.Date.Equal(time.Date(2008, 1, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Description != "Good striptease of MILF blonde Martina Vidal & friends . Great !" || sc.Thumbnail == "" {
		t.Errorf("Description/Thumbnail = %q %q", sc.Description, sc.Thumbnail)
	}
}

func TestListScenesSkipsKnown(t *testing.T) {
	body := sitemap(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.videos.xml":
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := &Scraper{Client: srv.Client()}
	ch, err := s.ListScenes(context.Background(), "https://mmm100.com/", scraper.ListOpts{KnownIDs: map[string]bool{"martina-vidal_beach": true}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if len(ids) != 1 || ids[0] != "nadia-daferro_terry" || !stopped {
		t.Errorf("ids = %v, stopped = %v", ids, stopped)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://mmm100.com/") || !s.MatchesURL("https://www.mmm100.com") || s.MatchesURL("https://mmm100.fr/") {
		t.Error("MatchesURL")
	}
}

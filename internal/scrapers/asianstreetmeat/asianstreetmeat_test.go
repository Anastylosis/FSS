package asianstreetmeat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParsePage(t *testing.T) {
	scenes := parsePage(fixture(t), "http://asianstreetmeat.com/", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d", len(scenes))
	}
	a, b := scenes[0], scenes[1]
	if a.ID != "2019070104.nong.o" || a.Title != "Nong O" || a.Duration != 38*60 {
		t.Errorf("a = %+v", a)
	}
	if !a.Date.Equal(time.Date(2019, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v", a.Date)
	}
	if a.Description != "She loves it & says so. Lovely, lean and petite." {
		t.Errorf("Description = %q", a.Description)
	}
	if a.Thumbnail != "https://cdn.streetmeatasia.com/photogroup/2019070104.nong.o/1.jpg" || a.Preview == "" {
		t.Errorf("Thumbnail/Preview = %q %q", a.Thumbnail, a.Preview)
	}
	if len(a.Performers) != 1 || a.Performers[0] != "Nong O" || a.URL != siteBase+"/join/2019070104.nong.o/" {
		t.Errorf("Performers/URL = %v %q", a.Performers, a.URL)
	}
	if b.Title != "Ruth" || b.Duration != 29*60 || b.Description != "" {
		t.Errorf("b = %+v", b)
	}
}

func TestListScenes(t *testing.T) {
	page := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(page))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := &Scraper{Client: srv.Client()}
	ch, err := s.ListScenes(context.Background(), "http://asianstreetmeat.com/", scraper.ListOpts{KnownIDs: map[string]bool{"2017082502.ruth": true}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		}
	}
	if len(ids) != 1 || ids[0] != "2019070104.nong.o" {
		t.Errorf("ids = %v", ids)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("http://asianstreetmeat.com/") || !s.MatchesURL("https://www.asianstreetmeat.com") || s.MatchesURL("https://streetmeatasia.com/") {
		t.Error("MatchesURL")
	}
}

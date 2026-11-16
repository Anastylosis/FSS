package beddableboys

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

func TestParsePage(t *testing.T) {
	items, total, perPage, err := parsePage(fixture(t, "page1.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || total != 4 || perPage != 3 {
		t.Fatalf("items=%d total=%d perPage=%d", len(items), total, perPage)
	}
	sc := items[0].toScene("https://www.beddableboys.com", "https://www.beddableboys.com/", time.Now())
	if sc.ID != "bb198_blairmason_mikeclarkson" || sc.Title != "Blair & Mike Have Fun with a Dildo" {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.Thumbnail, "https://cloud-nexpectation.secure.yppcdn.com/") {
		t.Errorf("thumb = %q", sc.Thumbnail)
	}
	if sc.URL != "https://www.beddableboys.com/videos/blair-mike-have-fun-with-a-dildo?scene_code=bb198_blairmason_mikeclarkson" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Date.Format("2006-01-02") != "2026-11-19" || sc.Duration != 1423 || len(sc.Performers) != 2 || len(sc.Tags) == 0 {
		t.Errorf("scene = %+v", sc)
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages := map[string]string{
			"1": "page1.html",
			"2": "page2.html",
			"3": "empty.html",
		}
		name, ok := pages[r.URL.Query().Get("page")]
		if r.URL.Path != "/videos" {
			ok = false
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(fixture(t, name)))
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL
	studioURL := "https://www.beddableboys.com/"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
			if r.Scene.StudioURL != studioURL || r.Scene.SiteID != siteID || r.Scene.Studio != studioName {
				t.Errorf("scene = %+v", r.Scene)
			}
			if r.Scene.ID == "bb188_torymason" && r.Scene.Title != "Tory Mason Jacks Off in a Tent" {
				t.Errorf("title = %q", r.Scene.Title)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(ids) != 4 {
		t.Errorf("got %d scenes %v, want 4", len(ids), ids)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.beddableboys.com/", "http://beddableboys.com", "https://www.beddableboys.com/videos?page=2"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://notbeddableboys.com/", "https://example.com/beddableboys.com"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

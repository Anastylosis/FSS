package diapergirls

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

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/movies_2_d.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParsePage(t *testing.T) {
	scenes, pages := parsePage(fixture(t), "https://www.diapergirls.com/", time.Now())
	if pages != 3 || len(scenes) != 2 {
		t.Fatalf("pages = %d, scenes = %d", pages, len(scenes))
	}
	a, b := scenes[0], scenes[1]
	if a.ID != "Video_Marnie_2" || a.Title != "Fresh Shower" || len(a.Performers) != 1 || a.Performers[0] != "Marnie" {
		t.Errorf("a = %+v", a)
	}
	if !a.Date.Equal(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v (want dd/mm/yyyy)", a.Date)
	}
	if !strings.Contains(a.Thumbnail, "expires=1795169848&l=29&token=8e121f") {
		t.Errorf("Thumbnail = %q", a.Thumbnail)
	}
	if b.Title != "Wetting, Plug & Change" || len(b.Performers) != 2 || b.Performers[1] != "Chloe" {
		t.Errorf("b = %+v", b)
	}
}

func TestListScenes(t *testing.T) {
	page := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
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
	ch, err := s.ListScenes(context.Background(), "https://www.diapergirls.com/", scraper.ListOpts{})
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
		}
	}
	if n != 2 {
		t.Errorf("scenes = %d (a short page must end the walk)", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.diapergirls.com/", "https://diapergirls.com/categories/movies.html", "http://ukdiapergirls.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	if s.MatchesURL("https://example.com/") {
		t.Error("matched foreign host")
	}
}

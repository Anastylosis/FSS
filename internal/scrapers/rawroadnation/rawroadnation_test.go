package rawroadnation

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
		"https://www.rawroadnation.com/tour/categories/movies.html": true,
		"https://rawroadnation.com/":                                true,
		"https://example.com/":                                      false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestCategory(t *testing.T) {
	if category("https://www.rawroadnation.com/tour/categories/movies_3_d.html") != "movies" || category("https://www.rawroadnation.com/") != "movies" {
		t.Error("default category")
	}
	if category("https://www.rawroadnation.com/tour/categories/bears.html") != "bears" {
		t.Error("named category")
	}
}

func TestParseListing(t *testing.T) {
	scenes := parseListing(fixture(t, "listing1.html"), "https://www.rawroadnation.com/", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("got %d cards", len(scenes))
	}
	sc := scenes[1]
	if sc.ID != "237" || sc.Title != "Raw Dogging Boyfriends" ||
		sc.URL != "https://www.rawroadnation.com/tour/updates/Raw-Dogging-Boyfriends.html" ||
		!strings.Contains(sc.Thumbnail, "2120-1x.jpg") {
		t.Errorf("scene = %+v", sc)
	}
}

func TestApplyDetail(t *testing.T) {
	scenes := parseListing(fixture(t, "listing1.html"), "u", time.Now())
	applyDetail(fixture(t, "detail.html"), &scenes[1])
	sc := scenes[1]
	if len(sc.Performers) != 2 || sc.Performers[0] != "Jaks James" || sc.Performers[1] != "Manuel Kaye" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !sc.Date.Equal(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", sc.Date)
	}
	if !strings.HasPrefix(sc.Description, "Jaks and Manny") || strings.Contains(sc.Description, "\n") {
		t.Errorf("description = %q", sc.Description)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(s string) {
			_, _ = w.Write([]byte(strings.ReplaceAll(s, "https://www.rawroadnation.com", srv.URL)))
		}
		switch p := r.URL.Path; {
		case p == "/tour/categories/movies_1_d.html":
			write(fixture(t, "listing1.html"))
		case p == "/tour/categories/movies_2_d.html":
			write(fixture(t, "listing2.html"))
		case strings.HasPrefix(p, "/tour/categories/"):
			write("<html></html>")
		case strings.HasPrefix(p, "/tour/updates/"):
			write(fixture(t, "detail.html"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://www.rawroadnation.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatal(r.Err)
		case scraper.KindScene:
			n++
			if !strings.HasPrefix(r.Scene.URL, srv.URL) || len(r.Scene.Performers) != 2 || r.Scene.Date.IsZero() {
				t.Errorf("scene = %+v", r.Scene)
			}
		}
	}
	if n != 4 {
		t.Errorf("got %d scenes, want 4", n)
	}
}

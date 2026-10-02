package sheerandlace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	pages := map[string]string{
		"/tour/categories/movies/1/latest/":  fixture(t, "listing1.html"),
		"/tour/categories/movies/2/latest/":  fixture(t, "listing2.html"),
		"/tour/categories/movies/3/latest/":  fixture(t, "listing_empty.html"),
		"/tour/categories/movies/1/name/":    fixture(t, "listing2.html"),
		"/tour/categories/movies/2/name/":    fixture(t, "listing_empty.html"),
		"/tour/models/melena-maria-rya.html": fixture(t, "model1.html"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tour/sets.php" {
			body := fixture(t, "listing_empty.html")
			if r.URL.Query().Get("id") == "127" && r.URL.Query().Get("page") == "2" {
				body = fixture(t, "model2.html")
			}
			_, _ = w.Write([]byte(body))
			return
		}
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, s *Scraper, url string) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}
	return scenes
}

func testScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.base = srv.URL
	s.client = srv.Client()
	return s
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://sheerandlace.com/":                                  true,
		"https://www.sheerandlace.com/tour/":                         true,
		"https://sheerandlace.com/tour/categories/movies/2/latest/":  true,
		"https://sheerandlace.com/tour/models/melena-maria-rya.html": true,
		"http://sheerandlace.com":                                    true,
		"https://notsheerandlace.com/":                               false,
		"https://example.com/sheerandlace.com/":                      false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestResolveListing(t *testing.T) {
	cases := []struct {
		in   string
		want listing
	}{
		{"https://sheerandlace.com/tour/", listing{sort: "latest"}},
		{"https://sheerandlace.com/tour/categories/movies/5/popular/", listing{sort: "popular"}},
		{"https://sheerandlace.com/tour/models/monika-may.html", listing{model: "monika-may"}},
	}
	for _, c := range cases {
		if got := resolveListing(c.in); got != c.want {
			t.Errorf("resolveListing(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "listing1.html"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3", len(cards))
	}
	c := cards[0]
	if c.id != "14128" || c.title != "BTS of Set 17" || c.date != "2026-10-14" || c.duration != 14*60+51 {
		t.Errorf("card 0 = %+v", c)
	}
	if c.path != "/tour/trailers/Melena-Maria-Rya-FF-Stockings-BTSme.html" {
		t.Errorf("path = %q", c.path)
	}
	if len(c.performers) != 1 || c.performers[0] != "Melena Maria Rya" {
		t.Errorf("performers = %v", c.performers)
	}
	// The photo count shares the time div with the runtime.
	if cards[2].duration != 15*60+51 {
		t.Errorf("duration with photo count = %d, want %d", cards[2].duration, 15*60+51)
	}
}

func TestListScenesCatalogue(t *testing.T) {
	srv := newTestServer(t)
	scenes := collect(t, testScraper(srv), "https://sheerandlace.com/tour/")
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	sc := scenes[1]
	if sc.ID != "14089" || sc.SiteID != "sheerandlace" || sc.Studio != "Sheer and Lace" {
		t.Errorf("identity = %q %q %q", sc.ID, sc.SiteID, sc.Studio)
	}
	if sc.Title != "BTS of Set 10" || sc.Duration != 21*60+38 {
		t.Errorf("title/duration = %q %d", sc.Title, sc.Duration)
	}
	if sc.Date.Format("2006-01-02") != "2026-10-07" {
		t.Errorf("date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Katarina Hartlova" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.StudioURL != "https://sheerandlace.com/tour/" {
		t.Errorf("StudioURL = %q", sc.StudioURL)
	}
	if !strings.HasPrefix(sc.URL, srv.URL+"/tour/trailers/") || !strings.HasPrefix(sc.Thumbnail, srv.URL+"/tour/content//contentthumbs/") {
		t.Errorf("URL/thumbnail not on test server: %q %q", sc.URL, sc.Thumbnail)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt unset")
	}
}

func TestListScenesSort(t *testing.T) {
	srv := newTestServer(t)
	scenes := collect(t, testScraper(srv), "https://sheerandlace.com/tour/categories/movies/1/name/")
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
}

func TestListScenesModel(t *testing.T) {
	srv := newTestServer(t)
	scenes := collect(t, testScraper(srv), "https://sheerandlace.com/tour/models/melena-maria-rya.html")
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3 (2 from the model page, 1 from page 2)", len(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != "https://sheerandlace.com/tour/models/melena-maria-rya.html" {
			t.Errorf("StudioURL = %q", sc.StudioURL)
		}
	}
}

func TestFirstPageWithoutCardsIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()
	ch, _ := testScraper(srv).ListScenes(context.Background(), "https://sheerandlace.com/", scraper.ListOpts{})
	var gotErr bool
	for r := range ch {
		if r.Kind == scraper.KindError {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("expected a parse error")
	}
}

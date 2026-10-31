package danishfemdom

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

func page(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/trailers.html")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://www.danishfemdom.com/en/trailers/": true,
		"https://danishfemdom.com":                  true,
		"https://www.danishfemdom.com/en/":          true,
		"https://shop.danishfemdom.com/":            false,
		"https://example.com/":                      false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	scenes := parseListing(page(t), "https://www.danishfemdom.com/en/trailers/", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("scenes = %d", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "28027" || sc.Title != "Slut on the bench" || sc.Duration != 13*60+51 {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasSuffix(sc.Thumbnail, "/media/lqroeu1e/thebenchpic.jpg?width=405") || !strings.HasSuffix(sc.Preview, "/vpreview/28027.mp4") {
		t.Errorf("thumb/preview = %q %q", sc.Thumbnail, sc.Preview)
	}
	if !strings.HasPrefix(sc.Description, "The slutty latex Laura") {
		t.Errorf("description = %q", sc.Description)
	}
}

func TestListScenes(t *testing.T) {
	body := page(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/trailers/":
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://www.danishfemdom.com/en/trailers/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			if !strings.HasPrefix(r.Scene.URL, srv.URL) {
				t.Errorf("URL = %q", r.Scene.URL)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != 3 {
		t.Errorf("scenes = %d", n)
	}
}

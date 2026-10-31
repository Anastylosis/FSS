package closeup18

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func page(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/updates.html")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://18closeup.com":             true,
		"https://www.18closeup.com/":        true,
		"https://18closeup.com/updates.php": true,
		"https://18closeup.com/models.php":  false,
		"https://example.com/":              false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParsePage(t *testing.T) {
	scenes := parsePage(page(t), "https://18closeup.com/updates.php", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "733" || sc.Title != "Candy - Update 733" || sc.Duration != 13*60+33 || sc.Resolution != "4K-UHD" {
		t.Errorf("scene = %+v", sc)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Candy" || sc.Description == "" {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Thumbnail != siteBase+"/design/models/129_candy/320x180/04-27.jpg" {
		t.Errorf("thumb = %q", sc.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	body := page(t)
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/updates.php":
			pages = append(pages, r.URL.Query().Get("page"))
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://18closeup.com/updates.php", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != 2 || len(pages) != 1 || pages[0] != "1" {
		t.Errorf("scenes=%d pages=%v", n, pages)
	}
}

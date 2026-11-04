package swallowingangels

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

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://www.swallowingangels.com/home.asp") || !s.MatchesURL("http://swallowingangels.com") {
		t.Error("expected match")
	}
	if s.MatchesURL("https://swallowingangels.net/") {
		t.Error("matched a foreign host")
	}
}

func TestParseTitles(t *testing.T) {
	got := parseTitles(fixture(t, "all_titles.html"))
	if len(got) != 3 || got[0].id != "sa0036" || got[0].title != "A Week with Ellie Eden" {
		t.Errorf("titles = %+v", got)
	}
}

func TestToScene(t *testing.T) {
	sc := toScene(entry{id: "sa0082", title: "x"}, fixture(t, "title.html"), "https://www.swallowingangels.com/", "https://www.swallowingangels.com/title.asp?id=sa0082", models.Scene{}.ScrapedAt)
	if sc.Title != "Slutty Aspirations" || sc.Duration != 13*60 {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Date.Format("2006-01-02") != "2026-11-07" {
		t.Errorf("date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Mimi Rochelle" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !strings.HasPrefix(sc.Description, "Mimi is a super cute girl") {
		t.Errorf("description = %q", sc.Description)
	}
	if !strings.HasSuffix(sc.Thumbnail, "/sa0082/sa0082.1.jpg") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/all_titles.asp":
			_, _ = w.Write(fixture(t, "all_titles.html"))
		case "/title.asp":
			_, _ = w.Write(fixture(t, "title.html"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://www.swallowingangels.com", scraper.ListOpts{KnownIDs: map[string]bool{"sa0012": true}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			if !strings.HasPrefix(r.Scene.URL, srv.URL) {
				t.Errorf("url = %q", r.Scene.URL)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != 2 {
		t.Errorf("got %d scenes, want 2", n)
	}
}

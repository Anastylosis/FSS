package japanboyz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseListing(t *testing.T) {
	items := parseListing(read(t, "listing.html"))
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	it := items[0]
	if it.id != "3322" || it.title != "The Foursome" || it.duration != 24*60+5 {
		t.Errorf("item = %+v", it)
	}
	if it.date.Format("2006-01-02") != "2026-06-26" {
		t.Errorf("date = %v", it.date)
	}
	if !strings.HasSuffix(it.url, "/scenes/The-Foursome_vids.html") || !strings.Contains(it.thumb, "contentthumbs") {
		t.Errorf("url/thumb = %q %q", it.url, it.thumb)
	}
}

func TestApplyDetail(t *testing.T) {
	s := models.Scene{}
	applyDetail(&s, string(read(t, "detail.html")))
	if !strings.HasPrefix(s.Description, "Ever heard of a") {
		t.Errorf("description = %q", s.Description)
	}
	if len(s.Tags) == 0 || s.Tags[0] != "Anal" {
		t.Errorf("tags = %v", s.Tags)
	}
	if len(s.Performers) != 3 || s.Performers[0] != "Kenji 2" {
		t.Errorf("performers = %v", s.Performers)
	}
}

func TestListScenesEndToEnd(t *testing.T) {
	listing, detail := read(t, "listing.html"), read(t, "detail.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/scenes.html", "/categories/scenes_2_d.html":
			_, _ = w.Write(listing)
		default:
			_, _ = w.Write(detail)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New()
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://www.japanboyz.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		if r.Kind == scraper.KindError {
			t.Fatal(r.Err)
		}
		if r.Kind == scraper.KindScene {
			n++
		}
	}
	if n != 2 {
		t.Errorf("scenes = %d, want 2", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://www.japanboyz.com/categories/scenes.html") || s.MatchesURL("https://example.com/") {
		t.Error("MatchesURL mismatch")
	}
}

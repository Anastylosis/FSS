package plushies

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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
	if it.id != "515" || it.title != "Getting Ready For Tinder Date" || it.duration != 24*60 {
		t.Errorf("item = %+v", it)
	}
	if len(it.performers) != 1 || it.performers[0] != "Dana" {
		t.Errorf("performers = %v", it.performers)
	}
	if it.thumb != "https://plushies.tv/tour/content//contentthumbs/42/43/4243-2x.jpg" {
		t.Errorf("thumb = %q", it.thumb)
	}
}

func TestApplyDetail(t *testing.T) {
	s := models.Scene{}
	applyDetail(&s, string(read(t, "detail.html")))
	if s.Date.Format("2006-01-02") != "2026-07-18" {
		t.Errorf("date = %v", s.Date)
	}
	if len(s.Description) < 4 || s.Description[:4] != "Dana" {
		t.Errorf("description = %q", s.Description)
	}
	if len(s.Tags) == 0 {
		t.Errorf("no tags")
	}
}

func TestListScenesEndToEnd(t *testing.T) {
	listing, detail := read(t, "listing.html"), read(t, "detail.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tour/categories/movies.html", "/tour/categories/movies_2.html":
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
	ch, err := s.ListScenes(context.Background(), "https://plushies.tv", scraper.ListOpts{})
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
	if !s.MatchesURL("https://plushies.tv/tour/categories/movies.html") || s.MatchesURL("https://example.com/") {
		t.Error("MatchesURL mismatch")
	}
}

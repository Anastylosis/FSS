package baltan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

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
	body := read(t, "listing.html")
	items := parseListing(body)
	if len(items) != 2 || items[0].id != "TEST001" || items[0].title != "Sample Title One" || items[1].id != "TEST002" {
		t.Fatalf("items = %+v", items)
	}
	if parseTotal(body) != 1139 {
		t.Errorf("total = %d", parseTotal(body))
	}
}

func TestApplyDetail(t *testing.T) {
	var s models.Scene
	applyDetail(&s, string(read(t, "detail.html")), time.Now())
	if s.Title != "Sample Title One" || s.Description != "A short sample synopsis. Second line." {
		t.Errorf("title/desc = %q / %q", s.Title, s.Description)
	}
	if s.Duration != 129*60 || s.Date.Format("2006-01-02") != "2026-11-10" {
		t.Errorf("duration/date = %d / %v", s.Duration, s.Date)
	}
	if s.Series != "Sample Label" || s.Director != "Sample Director" {
		t.Errorf("series/director = %q / %q", s.Series, s.Director)
	}
	if len(s.Performers) != 2 || s.Performers[1] != "Performer B" || len(s.Tags) != 2 {
		t.Errorf("performers/tags = %v / %v", s.Performers, s.Tags)
	}
	if len(s.PriceHistory) != 1 || s.PriceHistory[0].Regular != 3498 {
		t.Errorf("price = %+v", s.PriceHistory)
	}
}

func TestListScenesEndToEnd(t *testing.T) {
	listing, detail := read(t, "listing.html"), read(t, "detail.html")
	var cookies atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("age"); err == nil && c.Value == "1" {
			cookies.Add(1)
		}
		switch r.URL.Path {
		case "/items/search", "/items/search/page:2":
			_, _ = w.Write(listing)
		default:
			_, _ = w.Write(detail)
		}
	}))
	defer srv.Close()
	baseOverride = srv.URL
	defer func() { baseOverride = "" }()

	s := newScraper(sites[0])
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://baltan-av.com/items/search", scraper.ListOpts{})
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
	if n != 2 || cookies.Load() == 0 {
		t.Errorf("scenes = %d, cookie requests = %d", n, cookies.Load())
	}
}

func TestMatchesURL(t *testing.T) {
	s := newScraper(sites[1])
	if !s.MatchesURL("https://aqua-vr.com/items/search") || s.MatchesURL("https://baltan-av.com/") {
		t.Error("MatchesURL mismatch")
	}
}

package beddableboys

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

func TestParseCards(t *testing.T) {
	cards := parseCards(fixture(t, "page1.html"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3", len(cards))
	}
	c := cards[0]
	if c.id != "bb100_robbiehart_camdenchristianson" || c.title != "From Sit-Ups to Cocks Up" {
		t.Errorf("card = %+v", c)
	}
	if c.thumb != "https://cloud.nexpectation.com/bb/bigthumbs/bb100_robbiehart_camdenchristianson.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
	if !strings.Contains(c.url, "scene=bb100_robbiehart_camdenchristianson") || strings.Contains(c.url, "&amp;") || strings.Contains(c.url, "#") {
		t.Errorf("url = %q", c.url)
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages := map[string]string{
			listPath + "1": "page1.html",
			listPath + "2": "page2.html",
			listPath + "3": "empty.html",
		}
		name, ok := pages[r.URL.Path]
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
			if r.Scene.ID == "bb154_damientelrue_radmatthews" && r.Scene.Title != "Hot Twink Shows Friend How to Pack Fudge" {
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
	for _, u := range []string{"https://www.beddableboys.com/", "http://beddableboys.com", "https://www.beddableboys.com/beddable-gay-twink-boys-bc/2"} {
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

package myboobsparadise

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://myboobsparadise.com/",
		"https://myboobsparadise.com/videos/",
		"https://myboobsparadise.com/pornstars/cute-kitty-34.html",
		"https://myboobs.eu/",
		"http://www.myboobs.eu",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://myboobsparadise.net/", "https://notmyboobs.eu/", "https://example.com/myboobs.eu/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestParseCards(t *testing.T) {
	cards := parseCards(readFixture(t, "listing_page1.html"))
	// The fixture holds video, gallery, video; the gallery is skipped.
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	c := cards[0]
	if c.id != "561" || c.url != defaultBase+"/video/huge-tits-bbw-babe-in-sexy-lingerie-and-stocking-in-bed-561.html" {
		t.Errorf("id/url = %q/%q", c.id, c.url)
	}
	if c.title != "Huge Tits BBW babe in Sexy Lingerie and Stocking in Bed" {
		t.Errorf("title = %q", c.title)
	}
	if c.duration != 12*60+21 {
		t.Errorf("duration = %d", c.duration)
	}
	if !c.date.Equal(time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", c.date)
	}
	if !strings.HasPrefix(c.thumbnail, "https://myboobsparadise.com/media/") {
		t.Errorf("thumbnail = %q", c.thumbnail)
	}
}

func TestMaxPage(t *testing.T) {
	if got := maxPage(readFixture(t, "listing_page1.html")); got != 2 {
		t.Errorf("maxPage = %d, want 2", got)
	}
}

func TestParseDetail(t *testing.T) {
	var c card
	parseDetail(readFixture(t, "detail.html"), &c)
	if len(c.performers) != 1 || c.performers[0] != "Cute Kitty" {
		t.Errorf("performers = %v", c.performers)
	}
	if !c.date.Equal(time.Date(2024, 3, 10, 17, 25, 37, 0, time.UTC)) {
		t.Errorf("date = %v", c.date)
	}
	if c.title == "" || !strings.HasPrefix(c.thumbnail, "https://myboobsparadise.com/") {
		t.Errorf("og fallback: title %q thumb %q", c.title, c.thumbnail)
	}
}

func TestFixURL(t *testing.T) {
	for in, want := range map[string]string{
		"https:////myboobsparadise.com/a.jpg": "https://myboobsparadise.com/a.jpg",
		"//myboobsparadise.com/a.jpg":         "https://myboobsparadise.com/a.jpg",
		"https://myboobsparadise.com/a.jpg":   "https://myboobsparadise.com/a.jpg",
	} {
		if got := fixURL(in); got != want {
			t.Errorf("fixURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func newServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	page1, page2 := readFixture(t, "listing_page1.html"), readFixture(t, "listing_page2.html")
	detail, model := readFixture(t, "detail.html"), readFixture(t, "model.html")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos/":
			_, _ = w.Write(page1)
		case r.URL.Path == "/videos/page2.html":
			_, _ = w.Write(page2)
		case strings.HasPrefix(r.URL.Path, "/pornstars/"):
			_, _ = w.Write(model)
		case strings.HasPrefix(r.URL.Path, "/video/"):
			if hits != nil {
				hits.Add(1)
			}
			_, _ = w.Write(detail)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func collect(t *testing.T, ch <-chan scraper.SceneResult) []models.Scene {
	t.Helper()
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	return scenes
}

func TestListScenes(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, &hits)
	defer srv.Close()
	s := &Scraper{client: srv.Client(), base: srv.URL}

	const studioURL = "https://myboobs.eu/"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := collect(t, ch)
	// Two videos on page 1, one on page 2; page 2 has no "next" link.
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if hits.Load() != 3 {
		t.Errorf("detail hits = %d, want 3", hits.Load())
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL || sc.SiteID != siteID || sc.Studio != studioName {
			t.Errorf("scene %s: studioURL %q site %q studio %q", sc.ID, sc.StudioURL, sc.SiteID, sc.Studio)
		}
		if len(sc.Performers) == 0 || sc.Date.IsZero() {
			t.Errorf("scene %s not enriched: %+v", sc.ID, sc)
		}
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	srv := newServer(t, nil)
	defer srv.Close()
	s := &Scraper{client: srv.Client(), base: srv.URL}

	ch, _ := s.ListScenes(context.Background(), "https://myboobsparadise.com/", scraper.ListOpts{KnownIDs: map[string]bool{"644": true}})
	stopped := false
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped || n != 1 {
		t.Errorf("stopped=%v scenes=%d, want early stop after 1", stopped, n)
	}
}

func TestListScenesModelPage(t *testing.T) {
	srv := newServer(t, nil)
	defer srv.Close()
	s := &Scraper{client: srv.Client(), base: srv.URL}

	const studioURL = "https://myboobsparadise.com/pornstars/cute-kitty-34.html"
	ch, _ := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	scenes := collect(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (gallery skipped)", len(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL = %q", sc.StudioURL)
		}
	}
}

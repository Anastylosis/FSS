package bdtvutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
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

var (
	harmony = SiteConfig{SiteID: "harmonyconcepts", StudioName: "Harmony Concepts", StudioID: 66, Domains: []string{"harmonyvideo.tv"}}
	closeup = SiteConfig{SiteID: "closeup", StudioName: "Close-Up Entertainment", StudioID: 58, Domains: []string{"bdtv.com"}}
)

// newServer serves the fixtures and records the listing paths asked for.
func newServer(t *testing.T, paths *[]string) *httptest.Server {
	t.Helper()
	listing := fixture(t, "listing.html")
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/movie-details/"):
			id := strings.TrimPrefix(r.URL.Path, "/movie-details/")
			b, err := os.ReadFile("testdata/detail_" + id + ".html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		default:
			mu.Lock()
			*paths = append(*paths, r.URL.RequestURI())
			mu.Unlock()
			if r.URL.Query().Get("page") != "1" && strings.HasPrefix(r.URL.Path, "/studio-display") {
				_, _ = w.Write([]byte(`<div id="content"></div>`))
				return
			}
			_, _ = w.Write([]byte(listing))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func collect(t *testing.T, s *Scraper, url string) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, scraper.ListOpts{Delay: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("scrape error: %v", r.Err)
		}
	}
	return scenes
}

func TestListScenesStudio(t *testing.T) {
	var paths []string
	ts := newServer(t, &paths)
	cfg := harmony
	cfg.Base = ts.URL
	s := New(cfg)
	s.Client = ts.Client()

	const studioURL = "https://harmonyvideo.tv/"
	scenes := collect(t, s, studioURL)

	// Domestic Disturbance is a Close-Up title and must be filtered out.
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %+v", len(scenes), scenes)
	}
	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}
	sc := byID["1729"]
	if sc.Title != "A Festival Of Fetters" || sc.SiteID != "harmonyconcepts" || sc.StudioURL != studioURL || sc.Studio != "Harmony Concepts" {
		t.Errorf("scene 1729: %+v", sc)
	}
	if sc.URL != ts.URL+"/movie-details/1729" {
		t.Errorf("URL = %q", sc.URL)
	}
	if got := sc.Date.Format("2006-01-02"); got != "2013-10-15" {
		t.Errorf("date = %s", got)
	}
	if strings.Join(sc.Performers, "|") != "Lorelei|Shawna Lake" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Director != "Oak Okork" {
		t.Errorf("director = %q", sc.Director)
	}
	if strings.Join(sc.Tags, "|") != "Harmony Concepts Video" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Thumbnail != ts.URL+"/img/movieimages/1/1729/covers/6c51a26edd2d.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	ab := byID["1648"]
	if ab.Director != "" || len(ab.Performers) != 3 || ab.Performers[2] != "Karina Santos" {
		t.Errorf("scene 1648: %+v", ab)
	}
	if len(paths) == 0 || paths[0] != "/studio-display?studio_id=66&page=1" {
		t.Errorf("listing paths = %v", paths)
	}
}

func TestListScenesActorFilter(t *testing.T) {
	var paths []string
	ts := newServer(t, &paths)
	cfg := closeup
	cfg.Base = ts.URL
	s := New(cfg)
	s.Client = ts.Client()

	scenes := collect(t, s, "https://bdtv.com/actor/846")
	if len(scenes) != 1 || scenes[0].ID != "1150" {
		t.Fatalf("scenes = %+v", scenes)
	}
	if scenes[0].Studio != "Close-Up Entertainment" || scenes[0].Date.Format("2006-01-02") != "2015-08-04" {
		t.Errorf("scene: %+v", scenes[0])
	}
	if paths[0] != "/actor/846?page=1" {
		t.Errorf("first listing path = %q", paths[0])
	}
	// The server ignores ?page=2 and repeats the page, which must end the walk.
	if len(paths) != 2 {
		t.Errorf("listing paths = %v", paths)
	}
}

func TestParseListing(t *testing.T) {
	cards, total := parseListing(fixture(t, "listing.html"))
	if total != 223 {
		t.Errorf("total = %d", total)
	}
	if len(cards) != 3 {
		t.Fatalf("cards = %+v (sidebar must not count)", cards)
	}
	if cards[1].id != "1648" || cards[1].title != "Absconding" || cards[1].thumb != "/img/movieimages/1/1648/covers/62a874afd984.jpg" {
		t.Errorf("card = %+v", cards[1])
	}
}

func TestMatchesURL(t *testing.T) {
	h, c := New(harmony), New(closeup)
	cases := []struct {
		url    string
		h, c   bool
		reason string
	}{
		{"https://harmonyvideo.tv/", true, false, "harmony root"},
		{"https://www.harmonyvideo.tv/display-movies?page=2", true, false, "harmony listing"},
		{"https://harmonyvideo.tv/actor/908", true, false, "harmony actor"},
		{"https://bdtv.com/", false, true, "bdtv root"},
		{"https://bdtv.com/display-movies/category/1469", false, true, "bdtv category"},
		{"https://bdtv.com/studio-display?studio_id=66", true, false, "studio 66 on bdtv"},
		{"https://bdtv.com/studio-display?studio_id=58&page=3", false, true, "studio 58 on bdtv"},
		{"https://harmonyvideo.tv/studio-display?studio_id=58", false, true, "studio 58 on harmony host"},
		{"https://bdtv.com/studio-display?studio_id=75", false, false, "other studio"},
		{"https://bdtv.com/display-movies/studio/75", false, false, "other studio path"},
		{"https://example.com/", false, false, "foreign host"},
		{"ftp://bdtv.com/", false, false, "scheme"},
	}
	for _, tc := range cases {
		if got := h.MatchesURL(tc.url); got != tc.h {
			t.Errorf("%s: harmony MatchesURL(%q) = %v", tc.reason, tc.url, got)
		}
		if got := c.MatchesURL(tc.url); got != tc.c {
			t.Errorf("%s: closeup MatchesURL(%q) = %v", tc.reason, tc.url, got)
		}
	}
}

func TestPatternsAreMatched(t *testing.T) {
	for _, cfg := range []SiteConfig{harmony, closeup} {
		s := New(cfg)
		for _, p := range s.Patterns() {
			u := strings.NewReplacer("{id}", "5").Replace("https://" + p)
			if !s.MatchesURL(u) {
				t.Errorf("%s: pattern %q does not match itself (%s)", cfg.SiteID, p, u)
			}
		}
	}
}

func TestListingURL(t *testing.T) {
	cfg := harmony
	cfg.Base = "https://bdtv.com"
	s := New(cfg)
	cases := map[string]struct {
		url      string
		filtered bool
	}{
		"https://harmonyvideo.tv/":                              {"https://bdtv.com/studio-display?studio_id=66&page=3", false},
		"https://bdtv.com/studio-display?studio_id=66":          {"https://bdtv.com/studio-display?studio_id=66&page=3", false},
		"https://bdtv.com/actor/846":                            {"https://bdtv.com/actor/846?page=3", true},
		"https://bdtv.com/display-movies/category/1469/":        {"https://bdtv.com/display-movies/category/1469?page=3", true},
		"https://harmonyvideo.tv/display-movies/director/20125": {"https://bdtv.com/display-movies/director/20125?page=3", true},
	}
	for in, want := range cases {
		got, filtered := s.listingURL(in, 3)
		if got != want.url || filtered != want.filtered {
			t.Errorf("listingURL(%q) = %q, %v; want %q, %v", in, got, filtered, want.url, want.filtered)
		}
	}
}

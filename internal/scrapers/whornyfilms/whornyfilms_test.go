package whornyfilms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

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

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://whornyfilms.com",
		"https://whornyfilms.com/",
		"https://www.whornyfilms.com/videos?page=2",
		"https://whornyfilms.com/models/asia",
		"https://whornyfilms.com/tag/anal",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{"https://whornyfilms.com.evil.test/", "https://example.com/videos", "https://notwhornyfilms.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestListPath(t *testing.T) {
	cases := map[string]string{
		"https://whornyfilms.com/":                 "/videos",
		"https://whornyfilms.com/videos?page=3":    "/videos",
		"https://whornyfilms.com/models/asia":      "/models/asia",
		"https://whornyfilms.com/tag/high-heels?x": "/tag/high-heels",
	}
	for in, want := range cases {
		if got := listPath(in); got != want {
			t.Errorf("listPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "list.html"))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	c := cards[0]
	if c.slug != "interracial-orgy" || c.title != "Interracial Orgy" {
		t.Errorf("card = %+v", c)
	}
	if len(c.performers) != 2 || c.performers[0] != "Asia" || c.performers[1] != "Rebeca Linares" {
		t.Errorf("performers = %v", c.performers)
	}
	if c.duration != 17*60+1 || c.views != 3071 {
		t.Errorf("duration=%d views=%d", c.duration, c.views)
	}
	if c.thumb != "/uploads/2026/08/d8ad275c74ed9a9f.webp" {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestApplyDetail(t *testing.T) {
	sc := models.Scene{}
	applyDetail(&sc, fixture(t, "detail.html"))
	if sc.Date.Format("2006-01-02") != "2026-09-16" {
		t.Errorf("date = %v", sc.Date)
	}
	if len(sc.Tags) != 9 || sc.Tags[0] != "Anal" || sc.Tags[4] != "Heels" {
		t.Errorf("tags = %v", sc.Tags)
	}
}

func newServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	list := fixture(t, "list.html")
	detail := fixture(t, "detail.html")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/videos/"):
			_, _ = w.Write([]byte(detail))
		case r.URL.Path == "/videos" || r.URL.Path == "/tag/anal" || r.URL.Path == "/models/asia":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(list))
				return
			}
			_, _ = w.Write([]byte(`<div id="videos-grid"></div>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &paths
}

func run(t *testing.T, ts *httptest.Server, studioURL string) []models.Scene {
	t.Helper()
	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("error: %v", r.Err)
		}
	}
	return scenes
}

func TestListScenes(t *testing.T) {
	ts, paths := newServer(t)
	studioURL := "https://whornyfilms.com/videos"
	scenes := run(t, ts, studioURL)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "interracial-orgy" || sc.SiteID != "whornyfilms" || sc.Title != "Interracial Orgy" {
		t.Errorf("scene = %+v", sc)
	}
	if sc.StudioURL != studioURL || sc.Studio != "Whorny Films" {
		t.Errorf("studio = %q / %q", sc.StudioURL, sc.Studio)
	}
	if sc.URL != ts.URL+"/videos/interracial-orgy" || sc.Thumbnail != ts.URL+"/uploads/2026/08/d8ad275c74ed9a9f.webp" {
		t.Errorf("urls = %q %q", sc.URL, sc.Thumbnail)
	}
	if sc.Date.Format("2006-01-02") != "2026-09-16" || len(sc.Tags) != 9 || len(sc.Performers) != 2 || sc.Duration != 1021 {
		t.Errorf("fields = %v %v %v %d", sc.Date, sc.Tags, sc.Performers, sc.Duration)
	}
	if !strings.Contains((*paths)[0], "sort=latest") {
		t.Errorf("first request = %q", (*paths)[0])
	}
}

func TestListScenesFilterModes(t *testing.T) {
	for path, want := range map[string]string{"/models/asia": "/models/asia?", "/tag/anal": "/tag/anal?"} {
		ts, paths := newServer(t)
		scenes := run(t, ts, "https://whornyfilms.com"+path)
		if len(scenes) != 2 {
			t.Errorf("%s: got %d scenes", path, len(scenes))
		}
		if !strings.HasPrefix((*paths)[0], want) {
			t.Errorf("%s: first request = %q", path, (*paths)[0])
		}
	}
}

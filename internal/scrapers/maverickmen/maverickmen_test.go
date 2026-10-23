package maverickmen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

func cfgFor(id string) siteConfig {
	for _, c := range sites {
		if c.id == id {
			return c
		}
	}
	panic(id)
}

func TestMatchesURL(t *testing.T) {
	vod := New(cfgFor("maverickmen"))
	dir := New(cfgFor("maverickmendirects"))
	cases := []struct {
		url      string
		vod, dir bool
	}{
		{"https://www.maverickmen.com", true, false},
		{"https://www.maverickmen.com/", true, false},
		{"https://vod.maverickmen.com/?page=videos", true, false},
		{"https://vod.maverickmen.com/?page=index", true, false},
		{"https://www.maverickmendirects.com/", false, true},
		{"https://vod.maverickmen.com/m/r/site/Maverick_Directs/", false, true},
		{"https://vod.maverickmen.com/m/r/site/Maverick_Directs/trailers?p=2", false, true},
		{"https://www.maverickmenmodels.com/", false, false},
		{"https://example.com/", false, false},
	}
	for _, c := range cases {
		if got := vod.MatchesURL(c.url); got != c.vod {
			t.Errorf("vod MatchesURL(%q) = %v, want %v", c.url, got, c.vod)
		}
		if got := dir.MatchesURL(c.url); got != c.dir {
			t.Errorf("directs MatchesURL(%q) = %v, want %v", c.url, got, c.dir)
		}
	}
}

func TestParseVODDetail(t *testing.T) {
	s := New(cfgFor("maverickmen"))
	var sc models.Scene
	if err := s.parseDetail([]byte(fixture(t, "vod_detail_old.html")), &sc); err != nil {
		t.Fatal(err)
	}
	if sc.Title != "XXX AMSTERDAM ADVENTURES" {
		t.Errorf("title = %q", sc.Title)
	}
	if want := time.Date(2010, 3, 11, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("date = %v", sc.Date)
	}
	if !strings.HasPrefix(sc.Description, "Hey Guys- We are back from Amsterdam") || strings.Contains(sc.Description, "<") {
		t.Errorf("description = %q", sc.Description)
	}
}

func TestParseDetailMissingTitle(t *testing.T) {
	s := New(cfgFor("maverickmendirects"))
	var sc models.Scene
	if err := s.parseDetail([]byte("<html><body>gone</body></html>"), &sc); err == nil {
		t.Fatal("expected error for page without title")
	}
}

func TestParseDirectsDetailStripsTeaser(t *testing.T) {
	s := New(cfgFor("maverickmendirects"))
	var sc models.Scene
	if err := s.parseDetail([]byte(fixture(t, "dir_detail_old.html")), &sc); err != nil {
		t.Fatal(err)
	}
	if sc.Title != "Bound Spanked & Used" {
		t.Errorf("title = %q", sc.Title)
	}
	if want := time.Date(2014, 10, 8, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("date = %v", sc.Date)
	}
	if !strings.HasPrefix(sc.Description, "This video captures the true spirit") {
		t.Errorf("description = %q", sc.Description)
	}
}

// newServer serves the fixture pages for both sites and rewrites absolute
// vod.maverickmen.com links to the test server.
func newServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		q := r.URL.Query()
		var name string
		switch r.URL.Path {
		case "/":
			switch {
			case q.Get("contentId") == "fuck-thy-neighbor":
				name = "vod_detail.html"
			case q.Get("contentId") != "":
				name = "vod_detail_old.html"
			case q.Get("p") == "1":
				name = "vod_list_p1.html"
			case q.Get("p") == "2":
				name = "vod_list_last.html"
			}
		case "/m/r/site/Maverick_Directs/trailers":
			switch q.Get("p") {
			case "1":
				name = "dir_list_p1.html"
			case "2":
				name = "dir_list_p2.html"
			}
		default:
			if strings.HasPrefix(r.URL.Path, "/m/r/site/Maverick_Directs/ms/video/") {
				if strings.Contains(r.URL.Path, "boundspanked") {
					name = "dir_detail_old.html"
				} else {
					name = "dir_detail.html"
				}
			}
		}
		if name == "" {
			http.NotFound(w, r)
			return
		}
		body := fixture(t, name)
		body = strings.ReplaceAll(body, "https://vod.maverickmen.com", srv.URL)
		body = strings.ReplaceAll(body, "http://vod.maverickmen.com", srv.URL)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := vodBase
	vodBase = srv.URL
	t.Cleanup(func() { vodBase = old })
	return srv
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error, bool) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return scenes, errs, stopped
}

func TestListScenesVOD(t *testing.T) {
	srv := newServer(t, nil)
	s := New(cfgFor("maverickmen"))
	s.Client = srv.Client()
	const studio = "https://www.maverickmen.com"
	scenes, errs, _ := collect(t, s, studio, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	first := scenes[0]
	if first.ID != "how-to-fuck-a-beautiful-latin-twink" || first.SiteID != "maverickmen" || first.Studio != "Maverick Men" {
		t.Errorf("first = %+v", first)
	}
	if scenes[1].Title != "FUCK THY NEIGHBOR" {
		t.Errorf("second title = %q", scenes[1].Title)
	}
	if want := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC); !scenes[1].Date.Equal(want) {
		t.Errorf("second date = %v", scenes[1].Date)
	}
	for _, sc := range scenes {
		if sc.StudioURL != studio {
			t.Errorf("StudioURL = %q", sc.StudioURL)
		}
		if !strings.HasPrefix(sc.URL, srv.URL) || sc.Thumbnail == "" {
			t.Errorf("url/thumb = %q / %q", sc.URL, sc.Thumbnail)
		}
	}
	if scenes[2].ID != "XXX.AMSTERDAM.ADVENTURES" {
		t.Errorf("last id = %q", scenes[2].ID)
	}
}

func TestListScenesDirects(t *testing.T) {
	srv := newServer(t, nil)
	s := New(cfgFor("maverickmendirects"))
	s.Client = srv.Client()
	const studio = "https://www.maverickmendirects.com/"
	scenes, errs, _ := collect(t, s, studio, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if scenes[0].ID != "little-twink-cum-cannon-teaser" || scenes[0].SiteID != "maverickmendirects" {
		t.Errorf("first = %+v", scenes[0])
	}
	if want := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC); !scenes[1].Date.Equal(want) {
		t.Errorf("date = %v", scenes[1].Date)
	}
	if scenes[1].Title != "How to Service a Daddy Bull Cock" {
		t.Errorf("title = %q", scenes[1].Title)
	}
	last := scenes[2]
	if last.Title != "Bound Spanked & Used" || last.Studio != "Maverick Men Directs" || last.StudioURL != studio {
		t.Errorf("last = %+v", last)
	}
}

func TestKnownIDsStopsWithoutDetailFetch(t *testing.T) {
	var hits int32
	srv := newServer(t, &hits)
	s := New(cfgFor("maverickmendirects"))
	s.Client = srv.Client()
	scenes, errs, stopped := collect(t, s, "https://www.maverickmendirects.com", scraper.ListOpts{
		KnownIDs: map[string]bool{"how-to-service-a-daddy-bull-cock-teaser": true},
	})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !stopped || len(scenes) != 1 {
		t.Fatalf("stopped=%v scenes=%d, want early stop after 1", stopped, len(scenes))
	}
	if hits != 2 { // listing page 1 + one detail
		t.Errorf("requests = %d, want 2", hits)
	}
}

func TestDetailFailureReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("contentId") != "" {
			_, _ = w.Write([]byte("<html>no title</html>"))
			return
		}
		_, _ = w.Write([]byte(fixture(t, "vod_list_last.html")))
	}))
	defer srv.Close()
	old := vodBase
	vodBase = srv.URL
	defer func() { vodBase = old }()
	s := New(cfgFor("maverickmen"))
	s.Client = srv.Client()
	scenes, errs, _ := collect(t, s, "https://www.maverickmen.com", scraper.ListOpts{})
	if len(scenes) != 0 || len(errs) != 1 {
		t.Fatalf("scenes=%d errs=%d", len(scenes), len(errs))
	}
	if scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Errorf("kind = %v", scraper.Classify(errs[0]))
	}
}

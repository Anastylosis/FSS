package laceystarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	p1, last, dvd := fixture(t, "updates_p1.html"), fixture(t, "updates_last.html"), fixture(t, "dvd.html")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/granny/updates.php":
			switch r.URL.Query().Get("p") {
			case "1":
				_, _ = w.Write(p1)
			case "2":
				_, _ = w.Write(last)
			default:
				http.NotFound(w, r)
			}
		case "/granny/dvd-19.php":
			_, _ = w.Write(dvd)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func collect(t *testing.T, studioURL string, base string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	s := New()
	s.base = base
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return scenes, errs
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.laceystarr.com",
		"https://laceystarr.com/",
		"https://www.laceystarr.com/granny/updates.php",
		"https://www.laceystarr.com/granny/model-lila-storm.php",
		"https://www.laceystarr.com/granny/dvd-19.php",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{"https://www.laceystarr.co.uk/", "https://www.notlaceystarr.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestListingWalk(t *testing.T) {
	srv, hits := newServer(t)
	studioURL := "https://www.laceystarr.com"
	scenes, errs := collect(t, studioURL, srv.URL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	if hits.Load() != 2 {
		t.Errorf("expected 2 requests (stop at the page without a next link), got %d", hits.Load())
	}
	first := scenes[0]
	if first.ID != "16136" || first.Title != "Office Fuck With Boss" {
		t.Errorf("first scene = %q %q", first.ID, first.Title)
	}
	if first.StudioURL != studioURL || first.Studio != "Lacey Starr" || first.SiteID != "laceystarr" {
		t.Errorf("studio fields: %+v", first)
	}
	if first.URL != srv.URL+"/granny/updates.php#16136" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Duration != 21*60 {
		t.Errorf("Duration = %d", first.Duration)
	}
	if want := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC); !first.Date.Equal(want) {
		t.Errorf("Date = %v", first.Date)
	}
	if first.Description != "The office always brings out the dirty in Granny Lacey!" {
		t.Errorf("Description = %q", first.Description)
	}
	if first.Thumbnail != "https://cdn.laceystarr.com//granny/contentthumbs/16136.jpg" {
		t.Errorf("Thumbnail = %q", first.Thumbnail)
	}
	// The commented-out placeholder cast ("Lacey, Joe, Mike") must not leak in.
	if !reflect.DeepEqual(first.Performers, []string{"Lacey Starr"}) {
		t.Errorf("Performers = %q", first.Performers)
	}
	if got := scenes[2].Performers; len(got) != 6 || got[5] != "Nicole Blake" {
		t.Errorf("six-performer scene = %q", got)
	}
	if scenes[1].Description != "" {
		t.Errorf("scene without trailer should have no description, got %q", scenes[1].Description)
	}
	if scenes[3].ID != "12266" || scenes[3].Title != "Black Fishnets" {
		t.Errorf("last-page scene = %q %q", scenes[3].ID, scenes[3].Title)
	}
}

func TestModelFilter(t *testing.T) {
	srv, _ := newServer(t)
	studioURL := "https://www.laceystarr.com/granny/model-lila-storm.php"
	scenes, errs := collect(t, studioURL, srv.URL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 || scenes[0].Title != "Madam Lacey Corrects Lila" {
		t.Fatalf("got %+v", scenes)
	}
	if !reflect.DeepEqual(scenes[0].Performers, []string{"Lacey Starr", "Lila Storm"}) {
		t.Errorf("Performers = %q", scenes[0].Performers)
	}
	if scenes[0].StudioURL != studioURL {
		t.Errorf("StudioURL = %q", scenes[0].StudioURL)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	srv, hits := newServer(t)
	scenes, _ := collect(t, "https://www.laceystarr.com", srv.URL,
		scraper.ListOpts{KnownIDs: map[string]bool{"16090": true}})
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
	if hits.Load() != 1 {
		t.Errorf("expected 1 request, got %d", hits.Load())
	}
}

func TestDVD(t *testing.T) {
	srv, _ := newServer(t)
	studioURL := "https://www.laceystarr.com/granny/dvd-19.php"
	scenes, errs := collect(t, studioURL, srv.URL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1292" || sc.Title != "Three Times a Lesbian" || sc.Duration != 40*60 {
		t.Errorf("scene = %+v", sc)
	}
	if sc.URL != studioURL+"#1292" || sc.StudioURL != studioURL {
		t.Errorf("URL = %q StudioURL = %q", sc.URL, sc.StudioURL)
	}
	if len(sc.Description) < 20 || sc.Description[:4] != "When" {
		t.Errorf("Description = %q", sc.Description)
	}
}

func TestEmptyFirstPageIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer srv.Close()
	_, errs := collect(t, "https://www.laceystarr.com", srv.URL, scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v", errs)
	}
}

package cumfortune

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

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

func newTestServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	routes := map[string]string{
		"/Scenes_Start.htm":                         "index.html",
		"/Promos/ZZZZ_Scenes/Scenes_476_to_now.htm": "listing_newest.html",
		"/Promos/ZZZZ_Scenes/Scenes_026_to_050.htm": "listing_oldest.html",
		// The same scenes again: a page whose entries all repeat must not
		// end the walk early or emit duplicates.
		"/Promos/ZZZZ_Scenes/Scenes_001_to_025.htm": "listing_oldest.html",
		"/Promos/AAA_GIRLS/BELLA.htm":               "model.html",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		name, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fixture(t, name))
	}))
	t.Cleanup(srv.Close)
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

func testScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.client = srv.Client()
	s.base = srv.URL
	return s
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://cumfortune.com/",
		"https://cumfortune.com",
		"http://www.cumfortune.com/",
		"https://cumfortune.com/Scenes_Start.htm",
		"https://cumfortune.com/models.htm",
		"https://cumfortune.com/Promos/AAA_GIRLS/BELLA.htm",
		"https://cumfortune.com/Promos/AAA_GIRLS/BELLE_CLAIRE.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://cumfortune.com/ccbill/index.htm",
		"https://notcumfortune.com/",
		"https://example.com/Promos/AAA_GIRLS/BELLA.htm",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListScenesWalksNewestFirst(t *testing.T) {
	srv := newTestServer(t, nil)
	s := testScraper(srv)
	studioURL := "https://cumfortune.com/"

	scenes, errs, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	var titles []string
	for _, sc := range scenes {
		titles = append(titles, sc.Title)
		if sc.StudioURL != studioURL {
			t.Errorf("%s: StudioURL = %q", sc.ID, sc.StudioURL)
		}
		if sc.SiteID != "cumfortune" || sc.Studio != "CumFortune" {
			t.Errorf("%s: SiteID/Studio = %q/%q", sc.ID, sc.SiteID, sc.Studio)
		}
	}
	want := "Hotter than Hot|The Cutie|My 1st Clip|She Is Not Innocent"
	if got := strings.Join(titles, "|"); got != want {
		t.Fatalf("titles = %q, want %q", got, want)
	}

	cutie := scenes[1]
	if cutie.ID != "bonnie/index_03/the-cutie" {
		t.Errorf("ID = %q", cutie.ID)
	}
	if !strings.HasPrefix(cutie.URL, srv.URL+"/Members/Bonnie/index_03.html") {
		t.Errorf("URL = %q", cutie.URL)
	}
	if len(cutie.Performers) != 1 || cutie.Performers[0] != "Bonnie" {
		t.Errorf("Performers = %v", cutie.Performers)
	}
	if cutie.Duration != 20*60 {
		t.Errorf("Duration = %d", cutie.Duration)
	}
	if strings.Join(cutie.Tags, ",") != "Cum in Mouth,Female Orgasm" {
		t.Errorf("Tags = %v", cutie.Tags)
	}
	if !strings.HasPrefix(cutie.Description, "After the shooting we spend some time") {
		t.Errorf("Description = %q", cutie.Description)
	}
	if cutie.Thumbnail != srv.URL+"/Promos/Bonnie_4_PX/Left.jpg" {
		t.Errorf("Thumbnail = %q", cutie.Thumbnail)
	}

	// A scene without a teaser keeps an empty description rather than
	// picking up the "Join to see the movie" link.
	if scenes[0].Description != "" {
		t.Errorf("empty teaser parsed as %q", scenes[0].Description)
	}
}

func TestListScenesKnownIDsStopsAfterNewestPage(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, &hits)
	s := testScraper(srv)

	known := map[string]bool{"lilu_moon/index_03/hotter-than-hot": true}
	scenes, errs, stopped := collect(t, s, "https://cumfortune.com/", scraper.ListOpts{KnownIDs: known})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].Title != "The Cutie" {
		t.Errorf("scenes = %+v", scenes)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("requests = %d, want 2 (index + newest page)", n)
	}
}

func TestModelPage(t *testing.T) {
	srv := newTestServer(t, nil)
	s := testScraper(srv)
	studioURL := "https://cumfortune.com/Promos/AAA_GIRLS/BELLA.htm"

	scenes, errs, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	duo := scenes[1]
	if duo.Title != "Doublefun" || duo.StudioURL != studioURL {
		t.Errorf("scene = %q / %q", duo.Title, duo.StudioURL)
	}
	if strings.Join(duo.Performers, "|") != "Bella Baby|Victoria Puppy" {
		t.Errorf("Performers = %v", duo.Performers)
	}
	if duo.ID != "bella/index_22/doublefun" {
		t.Errorf("ID = %q", duo.ID)
	}
}

func TestEmptyListingIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	t.Cleanup(srv.Close)
	s := testScraper(srv)

	_, errs, _ := collect(t, s, "https://cumfortune.com/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v", errs)
	}
}

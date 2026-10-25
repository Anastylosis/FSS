package sinnistar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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
	pages := map[string][]byte{
		modelsPath:                               fixture(t, "models.html"),
		"/amber-carmen-atogm.html":               fixture(t, "ambercarmen.html"),
		"/amber-wild-painful-anal.html":          fixture(t, "amber.html"),
		"/cindy-amateur-anal.html":               fixture(t, "cindy.html"),
		"/pregnant-breona-painful-anal-atm.html": fixture(t, "breona.html"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if b, ok := pages[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.client = srv.Client()
	s.base = srv.URL
	return s
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) ([]models.Scene, []scraper.SceneResult) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var others []scraper.SceneResult
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes = append(scenes, r.Scene)
		} else {
			others = append(others, r)
		}
	}
	return scenes, others
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://sinnistar.com",
		"https://sinnistar.com/",
		"http://www.sinnistar.com/tour.html",
		"https://sinnistar.com/cindy-amateur-anal.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://notsinnistar.com/",
		"https://sinnistar.com.evil.example/",
		"https://example.com/sinnistar.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestListScenesWholeCatalogue(t *testing.T) {
	srv := newTestServer(t, nil)
	s := newTestScraper(srv)
	const studioURL = "https://sinnistar.com"
	scenes, others := collect(t, s, studioURL, scraper.ListOpts{})

	for _, r := range others {
		if r.Kind == scraper.KindError {
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	var ids []string
	for _, sc := range scenes {
		ids = append(ids, sc.ID)
		if sc.StudioURL != studioURL {
			t.Errorf("%s: StudioURL = %q", sc.ID, sc.StudioURL)
		}
		if sc.SiteID != siteID || sc.Studio != studioName || sc.ScrapedAt.IsZero() {
			t.Errorf("%s: incomplete scene %+v", sc.ID, sc)
		}
	}
	want := []string{
		"amber-carmen-atogm",
		"amber-wild-painful-double-anal",
		"cindy-amber-ass-to-mouth",
		"sinnistar-cindy-gomez-deepthroat",
		"sinnistar-breona-pregnant-painful-anal-deepthroat-atm",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}

	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}

	shared := byID["cindy-amber-ass-to-mouth"]
	if shared.Title != "Amber Wild ass to mouth with Cindy Gomez" {
		t.Errorf("shared title = %q (first page should win)", shared.Title)
	}
	if !reflect.DeepEqual(shared.Performers, []string{"Amber Wild", "Cindy Gomez"}) {
		t.Errorf("shared performers = %v", shared.Performers)
	}
	if shared.URL != srv.URL+"/amber-wild-painful-anal.html" {
		t.Errorf("shared URL = %q", shared.URL)
	}
	if shared.Description != "Amber teaches Cindy & Buck.\n\nSecond paragraph ." {
		t.Errorf("description = %q", shared.Description)
	}
	if shared.Thumbnail != "https://sinnistar.com/amber-wild-painful-anal/cindy-amber-ass-to-mouth.jpg" {
		t.Errorf("thumbnail = %q", shared.Thumbnail)
	}

	if p := byID["amber-carmen-atogm"].Performers; len(p) != 0 {
		t.Errorf("slash label should not be credited, got %v", p)
	}
	if p := byID["sinnistar-breona-pregnant-painful-anal-deepthroat-atm"].Performers; !reflect.DeepEqual(p, []string{"Breona"}) {
		t.Errorf("pregnant prefix not stripped: %v", p)
	}
	if d := byID["amber-wild-painful-double-anal"].Description; d != "Amber takes a painful double anal creampie." {
		t.Errorf("description = %q", d)
	}
}

func TestListScenesModelPage(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, &hits)
	s := newTestScraper(srv)
	const studioURL = "https://sinnistar.com/cindy-amateur-anal.html"
	scenes, others := collect(t, s, studioURL, scraper.ListOpts{})
	for _, r := range others {
		if r.Kind == scraper.KindError {
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL = %q", sc.StudioURL)
		}
		if !reflect.DeepEqual(sc.Performers, []string{"Cindy Gomez"}) {
			t.Errorf("%s performers = %v", sc.ID, sc.Performers)
		}
	}
	if hits.Load() != 2 {
		t.Errorf("fetched %d pages, want index + one model page", hits.Load())
	}
}

func TestListScenesUnknownPage(t *testing.T) {
	srv := newTestServer(t, nil)
	s := newTestScraper(srv)
	scenes, others := collect(t, s, "https://sinnistar.com/amateur-anal.html", scraper.ListOpts{})
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes for a non-model page", len(scenes))
	}
	if len(others) != 1 || others[0].Kind != scraper.KindError || !strings.Contains(others[0].Err.Error(), "not a model page") {
		t.Fatalf("want a not-a-model-page error, got %+v", others)
	}
}

func TestListScenesEmptyModelPageIsParseError(t *testing.T) {
	mux := http.NewServeMux()
	idx := fixture(t, "models.html")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == modelsPath {
			_, _ = w.Write(idx)
			return
		}
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s := newTestScraper(srv)
	_, others := collect(t, s, "https://sinnistar.com/cindy-amateur-anal.html", scraper.ListOpts{})
	var found bool
	for _, r := range others {
		if r.Kind == scraper.KindError && scraper.Classify(r.Err) == scraper.FailureParse {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a parse error, got %+v", others)
	}
}

func TestPerformerName(t *testing.T) {
	for in, want := range map[string]string{
		"Cindy Gomez":                   "Cindy Gomez",
		"Pregnant KC":                   "KC",
		"Amber/Carmen":                  "",
		"Stacey Cash/Ashley Stephenson": "",
	} {
		if got := performerName(in); got != want {
			t.Errorf("performerName(%q) = %q, want %q", in, got, want)
		}
	}
}

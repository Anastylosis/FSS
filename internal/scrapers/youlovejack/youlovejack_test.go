package youlovejack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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

func newServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	pages := map[string]string{
		"/homemade-gay-porn-videos/index.html":  fixture(t, "index1.html"),
		"/homemade-gay-porn-videos/index2.html": fixture(t, "index2.html"),
	}
	james := fixture(t, "detail_james.html")
	tigger := fixture(t, "detail_tigger.html")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if body, ok := pages[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/homemade-gay-porn-videos/content/") {
			if strings.Contains(r.URL.Path, "tigger") {
				_, _ = w.Write([]byte(tigger))
			} else {
				_, _ = w.Write([]byte(james))
			}
			return
		}
		http.NotFound(w, r)
	}))
}

func collect(t *testing.T, s *Scraper, u string, opts scraper.ListOpts) ([]models.Scene, []error, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, u, opts)
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

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := map[string]bool{
		"https://www.youlovejack.com/":                                   true,
		"https://youlovejack.com":                                        true,
		"http://www.youlovejack.com/homemade-gay-porn-videos/index.html": true,
		"https://www.youlovejack.com.evil.org/":                          false,
		"https://footwoody.com/":                                         false,
		"https://www.thebestporn.com/review/youlovejack/":                false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestListScenes(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, &hits)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	const studio = "https://www.youlovejack.com/"
	scenes, errs, _ := collect(t, s, studio, scraper.ListOpts{Workers: 2})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	// Two cards on page 1, five on page 2; the commented-out "more videos"
	// square on the last page must not lead to a third page.
	if len(scenes) != 7 {
		t.Fatalf("got %d scenes, want 7", len(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != studio {
			t.Errorf("%s: StudioURL = %q", sc.ID, sc.StudioURL)
		}
		if !strings.HasPrefix(sc.URL, srv.URL+"/homemade-gay-porn-videos/content/") {
			t.Errorf("%s: URL = %q", sc.ID, sc.URL)
		}
	}

	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}
	james := byID["james-edwards-blonde-straight-twink-huge-uncut-cock"]
	if james.Title != "JAMES EDWARDS - BLONDE, STRAIGHT AND HUNG!" {
		t.Errorf("title = %q", james.Title)
	}
	if !reflect.DeepEqual(james.Performers, []string{"James Edwards"}) {
		t.Errorf("performers = %v", james.Performers)
	}
	if want := time.Date(2015, 11, 13, 0, 0, 0, 0, time.UTC); !james.Date.Equal(want) {
		t.Errorf("date = %v", james.Date)
	}
	if james.Duration != 14*60 {
		t.Errorf("duration = %d", james.Duration)
	}
	if james.Thumbnail != srv.URL+"/mpreview/jamesEdwards-cover860.jpg" {
		t.Errorf("thumb = %q", james.Thumbnail)
	}
	if !strings.HasPrefix(james.Description, "His pouty lips") || !strings.Contains(james.Description, "\n\nJames strips naked") {
		t.Errorf("description = %q", james.Description)
	}

	pair := byID["chad-leigh-uncut-twink-kisses-friend-and-cums"]
	if !reflect.DeepEqual(pair.Performers, []string{"Chad", "Tigger"}) {
		t.Errorf("pair performers = %v", pair.Performers)
	}
	room := byID["tigger-bedroom-cute-twink-shows-off-his-big-uncut-cock"]
	if !reflect.DeepEqual(room.Performers, []string{"Tigger"}) {
		t.Errorf("room performers = %v", room.Performers)
	}
	loft := byID["tigger-loft-uncut-twink-jerks-big-cock-and-cums"]
	if loft.Title != "TIGGER AT THE LOFT!" || loft.Duration != 600 {
		t.Errorf("loft = %q / %d", loft.Title, loft.Duration)
	}
	if want := time.Date(2005, 12, 30, 0, 0, 0, 0, time.UTC); !loft.Date.Equal(want) {
		t.Errorf("loft date = %v", loft.Date)
	}
}

func TestKnownIDsStopsAfterPage(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, &hits)
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	opts := scraper.ListOpts{Workers: 1, KnownIDs: map[string]bool{"peter-p-smooth-fit-straight-twink-finger-fuck-ass": true}}
	scenes, _, stopped := collect(t, s, "https://www.youlovejack.com/", opts)
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
}

func TestPerformers(t *testing.T) {
	cases := map[string][]string{
		"IAN T.":            {"Ian T."},
		"JT AND TANNER":     {"JT", "Tanner"},
		"PIERRE ST. PIERRE": {"Pierre St. Pierre"},
		"DAYTON O'CONNOR":   {"Dayton O'Connor"},
		"H BLOWS ANDREW":    {"H", "Andrew"},
		"NATHAN AND ROBIN":  {"Nathan", "Robin"},
	}
	for in, want := range cases {
		if got := performers(in); !reflect.DeepEqual(got, want) {
			t.Errorf("performers(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMissingTitleIsParseError(t *testing.T) {
	_, err := parseDetail("<html></html>", card{id: "x"}, "u", "s", "b")
	if scraper.Classify(err) != scraper.FailureParse {
		t.Errorf("Classify = %v", scraper.Classify(err))
	}
}

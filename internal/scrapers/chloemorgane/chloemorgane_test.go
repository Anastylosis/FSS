package chloemorgane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://chloemorgane.com/", "https://www.chloemorgane.com/updates", "http://chloemorgane.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://chloemorgane.net/", "https://notchloemorgane.com/", "https://example.com/chloemorgane.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "updates.html"))
	if len(items) != 3 {
		t.Fatalf("got %d cards, want 3", len(items))
	}
	for _, it := range items {
		if it.id == "" || it.title == "" {
			t.Errorf("incomplete card %+v", it)
		}
		// The publication date exists only in the poster's file name.
		if it.date == "" {
			t.Errorf("no date parsed from %q", it.thumbnail)
		}
	}
}

func TestParseListingSkipsCardsWithoutASample(t *testing.T) {
	body := []byte(`<li class="list__item update__list-item"><a href="/join"><img src="/media/images/posters/2017-06-16-th@2x.jpg" alt="X"></a></li>`)
	if got := parseListing(body); len(got) != 0 {
		t.Errorf("parseListing = %+v, want none", got)
	}
}

func TestParseListingDeduplicates(t *testing.T) {
	card := `<li class="list__item update__list-item"><a href="/sample/abc"><img src="/media/images/posters/2017-06-16-th@2x.jpg" alt="X"></a><h4><a href="/sample/abc">X</a></h4></li>`
	if got := parseListing([]byte(card + card)); len(got) != 1 {
		t.Errorf("parseListing = %+v, want one", got)
	}
}

func TestToScene(t *testing.T) {
	s := New()
	item := listItem{id: "dfykwr9edlc", title: "Anal Play in The Woods", thumbnail: "/media/images/posters/2017-06-16-th@2x.jpg", date: "2017-06-16"}
	sc := s.toScene(item, "https://chloemorgane.com/", time.Now().UTC())
	if sc.URL != "https://chloemorgane.com/sample/dfykwr9edlc" {
		t.Errorf("URL = %q", sc.URL)
	}
	// The sample page shows the same poster at `lg`.
	if sc.Thumbnail != "https://chloemorgane.com/media/images/posters/2017-06-16-lg@2x.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Date.Format("2006-01-02") != "2017-06-16" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}

	item.date = ""
	if got := s.toScene(item, "https://chloemorgane.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path != "/updates" {
			t.Errorf("unexpected fetch %s — the catalogue is one page", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(readFixture(t, "updates.html"))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://chloemorgane.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	total := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindTotal:
			total = res.Total
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 3 || total != 3 {
		t.Fatalf("got %d scenes / total %d, want 3", len(scenes), total)
	}
	if len(asked) != 1 {
		t.Errorf("fetched %v, want a single page", asked)
	}
	for _, sc := range scenes {
		if !strings.HasPrefix(sc.URL, srv.URL) {
			t.Errorf("URL = %q, want it under the test server", sc.URL)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(readFixture(t, "updates.html"))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "updates.html"))[1].id
	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://chloemorgane.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{known: true}})
	scenes, stopped := 0, false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected a StoppedEarly result")
	}
	if scenes != 1 {
		t.Errorf("scenes = %d, want 1", scenes)
	}
}

// A page that parses to nothing must be reported as a parse failure, not read
// as an empty catalogue — --full's authoritative Save would delete everything.
func TestEmptyPageIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://chloemorgane.com/", scraper.ListOpts{})
	errs := 0
	for res := range ch {
		if res.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(res.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("errors = %d, want 1", errs)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

package lostbetsgames

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
	for _, u := range []string{"https://lostbetsgames.com/", "https://www.lostbetsgames.com/site/index", "http://lostbetsgames.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://lostbetsgames.net/", "https://notlostbetsgames.com/", "https://example.com/lostbetsgames.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id == "" || !strings.Contains(first.url, "/videoPreview/id/"+first.id+"/") {
		t.Errorf("id/url = %q/%q", first.id, first.url)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if first.duration == 0 {
		t.Error("duration missing")
	}
	// The machine-readable date is on the <time> element; the text beside it
	// carries an English ordinal that time.Parse cannot read.
	if first.date == "" || strings.ContainsAny(first.date, "abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("date = %q, want the ISO datetime attribute", first.date)
	}
	if !strings.HasSuffix(first.preview, ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
}

func TestMaxPage(t *testing.T) {
	if got := maxPage(readFixture(t, "listing_page1.html")); got < 2 {
		t.Errorf("maxPage = %d", got)
	}
	if got := maxPage([]byte("<html></html>")); got != 0 {
		t.Errorf("maxPage = %d, want 0", got)
	}
}

func TestToScene(t *testing.T) {
	s := New()
	item := listItem{
		id: "3932", url: "https://lostbetsgames.com/site/videoPreview/id/3932/x.html", title: "T",
		thumbnail: "/media/video/39/32/3932/thumb.jpg",
		preview:   "//cdnpb.lostbetsgames.com/media/video/39/32/3932/miniclip.mp4",
		duration:  765, date: "2026-08-28",
	}
	sc := s.toScene(item, "https://lostbetsgames.com/", time.Now().UTC())
	if sc.Thumbnail != "https://lostbetsgames.com/media/video/39/32/3932/thumb.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	// The miniclip is protocol-relative.
	if sc.Preview != "https://cdnpb.lostbetsgames.com/media/video/39/32/3932/miniclip.mp4" {
		t.Errorf("Preview = %q", sc.Preview)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-28" {
		t.Errorf("Date = %v", sc.Date)
	}

	item.date = "not a date"
	if got := s.toScene(item, "https://lostbetsgames.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/p/1") {
			_, _ = w.Write(readFixture(t, "listing_page1.html"))
			return
		}
		// Past the last page the listing 404s.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://lostbetsgames.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	errs := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			errs++
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	// A 404 past the last page is the end of the listing, not a failure —
	// reporting it would demote every --full run to non-authoritative.
	if errs != 0 {
		t.Errorf("errors = %d, want 0", errs)
	}
}

// A 404 on page 1 is a broken site, not an empty catalogue, and must stay
// loud: --full's authoritative Save would otherwise delete everything.
func TestFirstPage404StillErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://lostbetsgames.com/", scraper.ListOpts{})
	errs := 0
	for res := range ch {
		if res.Kind == scraper.KindError {
			errs++
		}
	}
	if errs != 1 {
		t.Errorf("errors = %d, want 1", errs)
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(readFixture(t, "listing_page1.html"))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://lostbetsgames.com/",
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

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

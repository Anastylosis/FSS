package rawerotic

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
	s := newFor("rawerotic")
	for _, u := range []string{"https://rawerotic.com/", "https://www.rawerotic.com/videos.en.html", "http://rawerotic.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://rawerotic.net/", "https://notrawerotic.com/", "https://example.com/rawerotic.com/"} {
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
	if first.id == "" || !strings.Contains(first.url, "/watch/"+first.id+"/") {
		t.Errorf("id/url = %q/%q", first.id, first.url)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if len(first.performers) == 0 {
		t.Error("performer missing")
	}
	if !strings.HasPrefix(first.thumbnail, "https://") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	if !strings.HasSuffix(first.preview, ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
}

// The VideoObject is microdata, not JSON-LD, so parseutil's JSON-LD helpers
// do not see it.
func TestEnrichFromDetail(t *testing.T) {
	item := listItem{}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if item.duration == 0 {
		t.Error("duration missing")
	}
	if item.date == "" {
		t.Error("uploadDate missing")
	}
	if item.description == "" {
		t.Error("description missing")
	}
}

// The tour writes "T12M17S" where ISO 8601 requires the leading "P".
func TestNormalizeISODuration(t *testing.T) {
	cases := []struct{ in, want string }{
		{"T12M17S", "PT12M17S"},
		{"PT12M17S", "PT12M17S"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeISODuration(c.in); got != c.want {
			t.Errorf("normalizeISODuration(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Every description ends with the site label, repeated.
func TestTrimSiteSuffix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"A beach scene. - RawErotic - RawErotic", "A beach scene."},
		{"A beach scene. - RawErotic", "A beach scene."},
		{"A beach scene.", "A beach scene."},
		{"", ""},
	}
	for _, c := range cases {
		if got := trimSiteSuffix(c.in, "RawErotic"); got != c.want {
			t.Errorf("trimSiteSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	s := newFor("rawerotic")
	item := listItem{
		id: "76142", url: "https://rawerotic.com/watch/76142/lemonade.en.html", title: "Lemonade",
		thumbnail: "https://p.cdnc.letsdoeit.com/x.jpg", preview: "https://p.cdnc.letsdoeit.com/x.mp4",
		performers: []string{"Baby Nicols"}, date: "2026-08-21T18:20:20+00:00", duration: 737,
		description: "Text. - RawErotic - RawErotic",
	}
	sc := s.toScene(item, "https://rawerotic.com/", time.Now().UTC())
	if sc.Date.Format("2006-01-02") != "2026-08-21" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Description != "Text." {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Duration != 737 {
		t.Errorf("Duration = %d", sc.Duration)
	}

	item.date = "not a date"
	if got := s.toScene(item, "https://rawerotic.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/watch/"):
			_, _ = w.Write(readFixture(t, "detail.html"))
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		default:
			// Past the last page the listing renders no cards.
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		}
	}))
	defer srv.Close()

	s := newFor("rawerotic")
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://rawerotic.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.Duration == 0 || sc.Date.IsZero() {
			t.Errorf("detail enrichment missing on %s: %+v", sc.ID, sc)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/watch/") {
			_, _ = w.Write(readFixture(t, "detail.html"))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	s := newFor("rawerotic")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://rawerotic.com/",
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

// serveFixture rewrites the live host in the fixture's self-links onto the test
// server, so the detail fetches stay offline.
func serveFixture(t *testing.T, name, base string) []byte {
	t.Helper()
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), "https://rawerotic.com", base)
	if strings.Contains(rewritten, "https://rawerotic.com") {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

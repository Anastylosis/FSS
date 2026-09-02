package xxxjobinterviews

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
	for _, u := range []string{"https://xxxjobinterviews.com/", "https://www.xxxjobinterviews.com/videos/", "http://xxxjobinterviews.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://xxxjobinterviews.net/", "https://notxxxjobinterviews.com/", "https://example.com/xxxjobinterviews.com/"} {
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
	if first.id == "" || !strings.Contains(first.url, "-"+first.id+".html") {
		t.Errorf("id/url = %q/%q", first.id, first.url)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if first.duration == 0 {
		t.Error("duration missing")
	}
	if first.date == "" {
		t.Errorf("date = %q", first.date)
	}
	// The CDN URLs are protocol-relative.
	if !strings.HasPrefix(first.thumbnail, "https://") {
		t.Errorf("thumbnail = %q", first.thumbnail)
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

func TestEnrichFromDetail(t *testing.T) {
	item := listItem{}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if item.description == "" {
		t.Error("description missing")
	}
	if len(item.performers) == 0 {
		t.Error("performers missing")
	}
	if !strings.HasPrefix(item.preview, "https://") || !strings.HasSuffix(item.preview, ".mp4") {
		t.Errorf("preview = %q", item.preview)
	}
	// The keyword list repeats the cast; those are not tags.
	for _, tag := range item.tags {
		for _, p := range item.performers {
			if strings.EqualFold(tag, p) {
				t.Errorf("tag %q is a performer name", tag)
			}
		}
	}
	if len(item.tags) == 0 {
		t.Error("tags missing")
	}
}

func TestSplitKeywords(t *testing.T) {
	got := splitKeywords("Brandi Swan, Anal, anal, Rimming", []string{"Brandi Swan"})
	want := []string{"Anal", "Rimming"}
	if len(got) != len(want) {
		t.Fatalf("splitKeywords = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitKeywords = %v, want %v", got, want)
		}
	}
}

func TestTitleCaseSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"brandi-swan", "Brandi Swan"},
		{"brandi-swan-previously-confetti", "Brandi Swan Previously Confetti"},
		{"", ""},
	}
	for _, c := range cases {
		if got := titleCaseSlug(c.in); got != c.want {
			t.Errorf("titleCaseSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The date carries an English ordinal, which time.Parse cannot read.
func TestToSceneParsesOrdinalDate(t *testing.T) {
	item := listItem{id: "427", url: "https://xxxjobinterviews.com/video/x-427.html", title: "T", date: "August 22nd, 2026"}
	sc := toScene(item, "https://xxxjobinterviews.com/", time.Now().UTC())
	if sc.Date.Format("2006-01-02") != "2026-08-22" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}

	item.date = "not a date"
	if got := toScene(item, "https://xxxjobinterviews.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestAbsURL(t *testing.T) {
	if got := absURL("//cdn.example/x.jpg"); got != "https://cdn.example/x.jpg" {
		t.Errorf("absURL = %q", got)
	}
	if got := absURL("https://cdn.example/x.jpg"); got != "https://cdn.example/x.jpg" {
		t.Errorf("absURL = %q", got)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = w.Write(readFixture(t, "detail.html"))
		case r.URL.Path == "/videos/":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		default:
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://xxxjobinterviews.com/", scraper.ListOpts{Workers: 2})
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
		if sc.Description == "" || len(sc.Performers) == 0 {
			t.Errorf("detail enrichment missing on %s", sc.ID)
		}
		if sc.Date.IsZero() || sc.Duration == 0 {
			t.Errorf("card fields missing on %s: %+v", sc.ID, sc)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/video/") {
			_, _ = w.Write(readFixture(t, "detail.html"))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://xxxjobinterviews.com/",
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
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), defaultURL, base)
	if strings.Contains(rewritten, defaultURL) {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

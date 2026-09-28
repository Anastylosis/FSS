package helixstudios

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// newTestServer serves the listing + detail fixtures and asserts the age cookie
// is present on every request.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	listing := readFixture(t, "listing.html")
	empty := readFixture(t, "empty.html")
	detail := readFixture(t, "detail.html")

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("ageConfirmed"); err != nil || c.Value != "true" {
			t.Errorf("missing/invalid ageConfirmed cookie on %s", r.URL)
			http.Error(w, "age gate", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/watch-newest-helix-studios-clips-and-scenes.html":
			if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1" {
				_, _ = w.Write(empty)
				return
			}
			_, _ = w.Write(listing)
		case "/1756413/helix-studios-moores-little-whore-streaming-scene-video.html",
			"/1758059/helix-studios-archer-needs-moore-streaming-scene-video.html":
			_, _ = w.Write(detail)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestScraper(t *testing.T, ts *httptest.Server) *Scraper {
	t.Helper()
	s := New(sites[0]) // helixstudios
	s.Client = ts.Client()
	s.base = ts.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			t.Fatalf("scrape error: %v", res.Err)
		}
	}
	return scenes
}

func TestParseListingAndDetail(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := newTestScraper(t, ts)

	scenes := collect(t, s, ts.URL+"/", scraper.ListOpts{})
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	first := scenes[0]
	if first.ID != "1756413" {
		t.Errorf("ID = %q, want 1756413", first.ID)
	}
	if first.SiteID != "helixstudios" {
		t.Errorf("SiteID = %q, want helixstudios", first.SiteID)
	}
	if first.Studio != "Helix Studios" {
		t.Errorf("Studio = %q, want Helix Studios", first.Studio)
	}
	if first.Title != "Moore's Little Whore" {
		t.Errorf("Title = %q, want Moore's Little Whore", first.Title)
	}
	if first.URL != ts.URL+"/1756413/helix-studios-moores-little-whore-streaming-scene-video.html" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Duration != 21*60 {
		t.Errorf("Duration = %d, want %d", first.Duration, 21*60)
	}
	if got, want := first.Date.Format("2006-01-02"), "2025-09-18"; got != want {
		t.Errorf("Date = %q, want %q", got, want)
	}
	if first.Description == "" {
		t.Errorf("Description is empty")
	}
	if first.Thumbnail == "" {
		t.Errorf("Thumbnail is empty")
	}
	wantPerf := []string{"Tyler Moore", "Vincent Revero"}
	if len(first.Performers) != len(wantPerf) {
		t.Fatalf("Performers = %v, want %v", first.Performers, wantPerf)
	}
	for i, p := range wantPerf {
		if first.Performers[i] != p {
			t.Errorf("Performers[%d] = %q, want %q", i, first.Performers[i], p)
		}
	}
	wantTags := []string{"Blonde", "Twink", "Blowjob", "Smooth", "Rimming"}
	if len(first.Tags) != len(wantTags) {
		t.Fatalf("Tags = %v, want %v", first.Tags, wantTags)
	}
	for i, tag := range wantTags {
		if first.Tags[i] != tag {
			t.Errorf("Tags[%d] = %q, want %q", i, first.Tags[i], tag)
		}
	}

	if scenes[1].ID != "1758059" || scenes[1].Title == "" {
		t.Errorf("second scene unexpected: %+v", scenes[1])
	}
}

func TestKnownIDsEarlyStop(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := newTestScraper(t, ts)

	scenes := collect(t, s, ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"1756413": true},
	})
	// The known ID is first on the page, but the rest of that page is already
	// fetched and is emitted before the walk stops.
	if len(scenes) != 1 {
		t.Fatalf("expected the rest of the page (1 scene), got %d", len(scenes))
	}
}

func TestResolveListingSeriesAndChannel(t *testing.T) {
	s := New(sites[0])
	cases := []struct {
		url        string
		wantPath   string
		wantStudio string
	}{
		{"https://www.helixstudios.net/", "/watch-newest-helix-studios-clips-and-scenes.html", "Helix Studios"},
		{
			"https://www.helixstudios.net/watch-newest-helix-studios-clips-and-scenes.html?series=62682",
			"/watch-newest-helix-studios-clips-and-scenes.html?series=62682",
			"Helix Europe",
		},
		{
			"https://www.helixstudios.net/videos/studios/35/helix-latin-america",
			"/watch-newest-helix-studios-clips-and-scenes.html",
			"Helix Latin America",
		},
	}
	for _, c := range cases {
		path, studio := s.resolveListing(c.url)
		if path != c.wantPath {
			t.Errorf("resolveListing(%q) path = %q, want %q", c.url, path, c.wantPath)
		}
		if studio != c.wantStudio {
			t.Errorf("resolveListing(%q) studio = %q, want %q", c.url, studio, c.wantStudio)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	helix := newFor("helixstudios")
	eightteen := newFor("8teenboy")
	spank := newFor("spankthis")

	checks := []struct {
		s    *Scraper
		url  string
		want bool
	}{
		{helix, "https://www.helixstudios.net/", true},
		{helix, "https://www.helixstudios.com/foo.html", true},
		{helix, "https://www.8teenboy.com/", false},
		{eightteen, "https://www.8teenboy.com/", true},
		{spank, "https://spankthis.com/", true},
		{spank, "https://www.helixstudios.net/", false},
	}
	for _, c := range checks {
		if got := c.s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s.MatchesURL(%q) = %v, want %v", c.s.ID(), c.url, got, c.want)
		}
	}
}

func TestRegistered(t *testing.T) {
	for _, id := range []string{"helixstudios", "8teenboy", "spankthis"} {
		if newFor(id) == nil {
			t.Errorf("site %q not registered", id)
		}
	}
}

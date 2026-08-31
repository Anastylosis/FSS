package mmpnetwork

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

func TestSiteTable(t *testing.T) {
	ids := map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.SiteID] {
			t.Errorf("duplicate site id %q", cfg.SiteID)
		}
		ids[cfg.SiteID] = true
		if cfg.StudioName == "" || cfg.Domain == "" {
			t.Errorf("%s: incomplete config %+v", cfg.SiteID, cfg)
		}
		got, err := scraper.ForURL("https://" + cfg.Domain + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.Domain, err)
			continue
		}
		if got.ID() != cfg.SiteID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.Domain, got.ID(), cfg.SiteID)
		}
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := newFor("povbitch")
	for _, u := range []string{"https://povbitch.com/", "https://www.povbitch.com/updates", "http://povbitch.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://povbitch.net/", "https://notpovbitch.com/", "https://example.com/povbitch.com/", "https://mmpnetwork.com/"} {
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
	if first.id == "" || !strings.HasPrefix(first.path, "/video/"+first.id+"/") {
		t.Errorf("id/path = %q/%q", first.id, first.path)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if !strings.HasPrefix(first.thumbnail, "https://") || strings.Contains(first.thumbnail, "?") {
		t.Errorf("thumbnail = %q — the cache-busting query is dropped", first.thumbnail)
	}
}

// Fake Shooting writes protocol-relative hrefs where the others write a bare
// path; a pattern anchored on "/video/" matched no card there at all.
func TestParseListingProtocolRelativeHref(t *testing.T) {
	items := parseListing(readFixture(t, "listing_protocol_relative.html"))
	if len(items) != 1 {
		t.Fatalf("got %d cards, want 1", len(items))
	}
	if items[0].id != "21" {
		t.Errorf("id = %q", items[0].id)
	}
	if items[0].path != "/video/21/grandma-experiment" {
		t.Errorf("path = %q — the host must not survive into the path", items[0].path)
	}
}

func TestEnrichFromDetail(t *testing.T) {
	item := listItem{}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if item.title != "Silly doll creampie" {
		t.Errorf("title = %q", item.title)
	}
	// The card abbreviates the date ("Oct 10, 2019"); the detail spells it out.
	if item.date != "October 10, 2019" {
		t.Errorf("date = %q", item.date)
	}
	if len(item.performers) != 1 || item.performers[0] != "Tarja King" {
		t.Errorf("performers = %v", item.performers)
	}
	if !strings.HasPrefix(item.description, "Tarja is a loveliness itself") {
		t.Errorf("description = %q", item.description)
	}
	if item.duration != 16*60+49 {
		t.Errorf("duration = %d", item.duration)
	}
	if item.resolution != "1080p" {
		t.Errorf("resolution = %q", item.resolution)
	}
	if len(item.tags) == 0 {
		t.Error("tags missing")
	}
}

func TestTopResolution(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1080p, 720p, 480p", "1080p"},
		{"480p", "480p"},
		{"", ""},
		{"HD", ""},
	}
	for _, c := range cases {
		if got := topResolution(c.in); got != c.want {
			t.Errorf("topResolution(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMaxPage(t *testing.T) {
	if got := maxPage(readFixture(t, "listing_page1.html")); got < 2 {
		t.Errorf("maxPage = %d", got)
	}
	// A `p=` elsewhere in the page must not be read as a page number.
	if got := maxPage([]byte(`<html><a href="/x?p=99">x</a></html>`)); got != 0 {
		t.Errorf("maxPage without a pagination block = %d, want 0", got)
	}
}

func TestToScene(t *testing.T) {
	s := newFor("povbitch")
	item := listItem{
		id: "28", path: "/video/28/stupid-girl-creampie", title: "Silly doll creampie",
		thumbnail: "https://free.povbitch.com/028pov/cover.jpg", performers: []string{"Tarja King"},
		date: "October 10, 2019", duration: 1009, resolution: "1080p",
		description: "D", tags: []string{"pov"},
	}
	sc := s.toScene(item, "https://povbitch.com/", time.Now().UTC())
	if sc.URL != "https://povbitch.com/video/28/stupid-girl-creampie" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.SiteID != "povbitch" || sc.Studio != "PovBitch" {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}
	if sc.Date.Format("2006-01-02") != "2019-10-10" {
		t.Errorf("Date = %v", sc.Date)
	}

	// The hub leaves the date out entirely; that must stay zero, not guessed.
	item.date = ""
	if got := s.toScene(item, "https://povbitch.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/updates":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
		default:
			t.Errorf("unexpected fetch %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := newFor("povbitch")
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://povbitch.com/", scraper.ListOpts{Workers: 2})
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
		if sc.Description == "" || sc.Duration == 0 || sc.Date.IsZero() {
			t.Errorf("detail enrichment missing on %s: %+v", sc.ID, sc)
		}
		if !strings.HasPrefix(sc.URL, srv.URL) {
			t.Errorf("URL = %q, want it under the test server", sc.URL)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/video/") {
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	s := newFor("povbitch")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://povbitch.com/",
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

func TestListScenesReportsDetailFailureButKeepsScene(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/video/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	s := newFor("povbitch")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://povbitch.com/", scraper.ListOpts{})
	scenes, errs := 0, 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 2 {
		t.Errorf("scenes = %d, want 2 — a dead detail page must not drop the card", scenes)
	}
	if errs != 2 {
		t.Errorf("errors = %d, want 2", errs)
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
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), "https://povbitch.com", base)
	if strings.Contains(rewritten, "https://povbitch.com") {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

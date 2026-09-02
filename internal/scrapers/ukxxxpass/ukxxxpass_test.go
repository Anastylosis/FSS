package ukxxxpass

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
	s := newFor("splatbukkake")
	for _, u := range []string{"https://splatbukkake.xxx/", "https://www.splatbukkake.xxx/movies", "http://splatbukkake.xxx"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://ukpornparty.xxx/", "https://notsplatbukkake.xxx/", "https://example.com/splatbukkake.xxx/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestStudioPathRe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://splatbukkake.xxx/movies/studio/1/splatbukkake", "/movies/studio/1/splatbukkake"},
		{"https://splatbukkake.xxx/movies/studio/3/uk-porn-party?page=2", "/movies/studio/3/uk-porn-party"},
		{"https://splatbukkake.xxx/movies", ""},
		{"https://splatbukkake.xxx/", ""},
	}
	for _, c := range cases {
		got := ""
		if m := studioPathRe.FindStringSubmatch(c.in); m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("studioPathRe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id == "" || !strings.Contains(first.url, "/movie/"+first.id+"/") {
		t.Errorf("id/url = %q/%q", first.id, first.url)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	// The Livewire template sprinkles "<!--[if BLOCK]>" comments through the
	// cast list; none of it may survive into a performer name.
	if len(first.performers) == 0 {
		t.Error("performers missing")
	}
	for _, p := range first.performers {
		if strings.Contains(p, "[if") || strings.ContainsAny(p, "<>") {
			t.Errorf("performer %q carries template noise", p)
		}
	}
	if first.date == "" {
		t.Errorf("date = %q", first.date)
	}
}

func TestEnrichFromDetail(t *testing.T) {
	item := listItem{}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if item.title == "" {
		t.Error("title missing")
	}
	// The detail spells the date DD-MM-YYYY where the card writes DD.MM.YYYY.
	if item.date != "26.08.2026" {
		t.Errorf("date = %q", item.date)
	}
	if item.studio != "SplatBukkake" {
		t.Errorf("studio = %q", item.studio)
	}
	if !strings.HasPrefix(item.description, "It's semi-final day") {
		t.Errorf("description = %q", item.description)
	}
}

// A scene with no description must not pick up a neighbour's card text from
// the related-movies grid further down the page.
func TestEnrichFromDetailWithoutADescription(t *testing.T) {
	body := []byte(`<div class="movieTitle">T</div>
	<div>Released on: 01-02-2026 by: <a href="/movies/studio/1/x">Studio</a></div>
	</div></div>
	<div class="movieItem"><div class="title"><a href="/movie/1/x">Some neighbour title that is long</a></div></div><br>`)
	var item listItem
	enrichFromDetail(body, &item)
	if strings.Contains(item.description, "neighbour") {
		t.Errorf("description = %q — the related grid must not be read as a description", item.description)
	}
}

func TestToSceneUsesTheReleasingStudio(t *testing.T) {
	s := newFor("splatbukkake")
	item := listItem{id: "1", url: "https://splatbukkake.xxx/movie/1/x", title: "T", date: "26.08.2026"}
	if got := s.toScene(item, "https://splatbukkake.xxx/", time.Now().UTC()); got.Studio != "Splat Bukkake" {
		t.Errorf("Studio = %q, want the config default", got.Studio)
	}
	// splatbukkake.xxx hosts several brands; the detail names the real one.
	item.studio = "UK Porn Party"
	got := s.toScene(item, "https://splatbukkake.xxx/", time.Now().UTC())
	if got.Studio != "UK Porn Party" {
		t.Errorf("Studio = %q", got.Studio)
	}
	if got.Date.Format("2006-01-02") != "2026-08-26" {
		t.Errorf("Date = %v", got.Date)
	}

	item.date = "not a date"
	if bad := s.toScene(item, "https://splatbukkake.xxx/", time.Now().UTC()); !bad.Date.IsZero() {
		t.Errorf("Date = %v, want zero", bad.Date)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/movie/"):
			_, _ = w.Write(readFixture(t, "detail.html"))
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		default:
			// The server-rendered listing carries no pager; past the end it
			// renders no cards.
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		}
	}))
	defer srv.Close()

	s := newFor("splatbukkake")
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://splatbukkake.xxx/", scraper.ListOpts{Workers: 2})
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
		if sc.Description == "" {
			t.Errorf("detail enrichment missing on %s", sc.ID)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/movie/") {
			_, _ = w.Write(readFixture(t, "detail.html"))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	s := newFor("splatbukkake")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://splatbukkake.xxx/",
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
		switch {
		case strings.HasPrefix(r.URL.Path, "/movie/"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		default:
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		}
	}))
	defer srv.Close()

	s := newFor("splatbukkake")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://splatbukkake.xxx/", scraper.ListOpts{})
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
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), "https://splatbukkake.xxx", base)
	if strings.Contains(rewritten, "https://splatbukkake.xxx") {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

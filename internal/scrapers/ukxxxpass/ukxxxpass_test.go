package ukxxxpass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	var ids []string
	for _, it := range items {
		ids = append(ids, it.id)
	}
	// 32622 and 32124 sit in the "Check out these DVDs as well" strip and are
	// DVDs, not scenes.
	if got := strings.Join(ids, ","); got != "32874,32868,32784" {
		t.Fatalf("card ids = %s, want 32874,32868,32784", got)
	}
	for _, it := range items {
		if strings.Contains(it.thumbnail, "/cover/") {
			t.Errorf("%s: DVD cover %q leaked into the scene cards", it.id, it.thumbnail)
		}
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
	if first.date != "09.09.2026" {
		t.Errorf("date = %q", first.date)
	}
	if first.title != "Lovely Lola Marie gets cum faced again" {
		t.Errorf("title = %q", first.title)
	}
	if first.thumbnail != "/movie/5/6/561eaa108c9074d4c16da3186f2b9be0/thumbs/thumb.jpg" {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	if got := strings.Join(items[1].performers, ","); got != "Pascal White,Rebecca Smyth" {
		t.Errorf("performers = %q", got)
	}
}

func TestParsePager(t *testing.T) {
	p1 := parsePager(readFixture(t, "listing_page1.html"))
	if !p1.hasPager || !p1.hasNext || p1.sceneCount != 1843 {
		t.Errorf("page 1 = %+v", p1)
	}
	last := parsePager(readFixture(t, "listing_page2.html"))
	if !last.hasPager || last.hasNext {
		t.Errorf("last page = %+v, want a pager with no next button", last)
	}
	if none := parsePager([]byte(`<html></html>`)); none.hasPager || none.hasNext || none.sceneCount != -1 {
		t.Errorf("bare page = %+v", none)
	}
}

func TestStripDVDStripLeavesOtherGridCellsAlone(t *testing.T) {
	body := []byte(`<div class="grid"><div class="col-span-4"><div>keep</div></div>` +
		`<div class="col-span-4"><button wire:click="setType('movie')">DVDs</button><div class="movieItem">x</div></div>` +
		`<div class="movieItem">y</div></div>`)
	got := string(stripDVDStrip(body))
	want := `<div class="grid"><div class="col-span-4"><div>keep</div></div><div class="movieItem">y</div></div>`
	if got != want {
		t.Errorf("stripDVDStrip =\n%s\nwant\n%s", got, want)
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

// listingServer serves the two listing fixtures as pages 1 and 2 of a
// two-page catalogue, and detail for every /movie/ path. Anything else is an
// unexpected request.
func listingServer(t *testing.T, detail http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var details atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/movie/"):
			details.Add(1)
			detail(w, r)
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case r.URL.Query().Get("page") == "2":
			_, _ = w.Write(serveFixture(t, "listing_page2.html", srv.URL))
		default:
			t.Errorf("unexpected request %s — page 2 has no next button", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &details
}

func serveDetail(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(readFixture(t, "detail.html"))
	}
}

func TestListScenes(t *testing.T) {
	srv, _ := listingServer(t, serveDetail(t))
	s := newFor("splatbukkake")
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://splatbukkake.xxx/", scraper.ListOpts{Workers: 2})
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
	// Three scene cards on page 1 (the DVD strip excluded) and one on page 2.
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	if total != 1843 {
		t.Errorf("total = %d, want the listing's sceneCount", total)
	}
	for _, sc := range scenes {
		if sc.Description == "" {
			t.Errorf("detail enrichment missing on %s", sc.ID)
		}
		if !strings.HasPrefix(sc.URL, srv.URL) {
			t.Errorf("scene URL %q left the test server", sc.URL)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	srv, details := listingServer(t, serveDetail(t))
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
	// Nothing from the known card on is emitted, so its detail is not fetched.
	if n := details.Load(); n != 1 {
		t.Errorf("detail fetches = %d, want 1", n)
	}
}

func TestListScenesReportsDetailFailureButKeepsScene(t *testing.T) {
	srv, _ := listingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
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
	if scenes != 4 {
		t.Errorf("scenes = %d, want 4 — a dead detail page must not drop the card", scenes)
	}
	if errs != 4 {
		t.Errorf("errors = %d, want 4", errs)
	}
}

// A listing the site says holds scenes, but whose cards no longer parse, is a
// markup change — it must surface as a parse failure, not read as an empty
// catalogue.
func TestListScenesReportsVanishedCardsAsParseFailure(t *testing.T) {
	cases := map[string]string{
		"scene count":    `<div wire:snapshot="{&quot;data&quot;:{&quot;type&quot;:&quot;scene&quot;,&quot;sceneCount&quot;:1843}}"></div>`,
		"next button":    `<nav aria-label="Pagination Navigation"><button wire:click="nextPage('page')">Next</button></nav>`,
		"no pager/count": `<html><body><div class="grid"></div></body></html>`,
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(page))
			}))
			defer srv.Close()
			s := newFor("splatbukkake")
			s.base = srv.URL

			ch, _ := s.ListScenes(context.Background(), "https://splatbukkake.xxx/", scraper.ListOpts{})
			var errs []error
			for res := range ch {
				switch res.Kind {
				case scraper.KindScene:
					t.Errorf("unexpected scene %s", res.Scene.ID)
				case scraper.KindError:
					errs = append(errs, res.Err)
				}
			}
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly one", errs)
			}
			if kind := scraper.Classify(errs[0]); kind != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse (%v)", kind, errs[0])
			}
		})
	}
}

// A studio the site itself counts as empty is not a failure.
func TestListScenesEmptyStudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<div wire:snapshot="{&quot;data&quot;:{&quot;sceneCount&quot;:0}}"></div>`))
	}))
	defer srv.Close()
	s := newFor("splatbukkake")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://splatbukkake.xxx/movies/studio/9/empty", scraper.ListOpts{})
	for res := range ch {
		if res.Kind == scraper.KindError || res.Kind == scraper.KindScene {
			t.Errorf("unexpected result %+v", res)
		}
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

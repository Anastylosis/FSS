package cutlersden

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
	cases := []struct {
		url  string
		want bool
	}{
		{"https://cutlersden.com/", true},
		{"https://www.cutlersden.com", true},
		{"http://cutlersden.com/categories/movies_2_d.html", true},
		{"https://cutlersden.net/", false},
		{"https://notcutlersden.com/", false},
		{"https://example.com/cutlersden.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestClassifyURL(t *testing.T) {
	cases := []struct {
		url  string
		kind urlKind
		slug string
	}{
		{"https://cutlersden.com/", kindAll, ""},
		{"https://cutlersden.com/categories/movies.html", kindAll, ""},
		{"https://cutlersden.com/categories/movies_4_d.html", kindAll, ""},
		{"https://cutlersden.com/categories/categories.html", kindAll, ""},
		{"https://cutlersden.com/categories/photos.html", kindAll, ""},
		{"https://cutlersden.com/categories/anal_1_d.html", kindCategory, "anal"},
		{"https://cutlersden.com/models/BraxtonCruz.html", kindModel, "BraxtonCruz"},
		{"https://cutlersden.com/models/models.html", kindAll, ""},
	}
	for _, c := range cases {
		kind, slug := classifyURL(c.url)
		if kind != c.kind || slug != c.slug {
			t.Errorf("classifyURL(%q) = %v/%q, want %v/%q", c.url, kind, slug, c.kind, c.slug)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New()
	cases := []struct {
		kind urlKind
		slug string
		page int
		want string
	}{
		{kindAll, "", 1, "https://cutlersden.com/categories/movies_1_d.html"},
		{kindCategory, "anal", 3, "https://cutlersden.com/categories/anal_3_d.html"},
		{kindModel, "BraxtonCruz", 2, "https://cutlersden.com/models/BraxtonCruz_2_d.html"},
	}
	for _, c := range cases {
		if got := s.listingURL(c.kind, c.slug, c.page); got != c.want {
			t.Errorf("listingURL(%v,%q,%d) = %q, want %q", c.kind, c.slug, c.page, got, c.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id != "-BRAXTON-CRUZ-XAVIER-ZANE" {
		t.Errorf("id = %q", first.id)
	}
	if first.title != "BRAXTON CRUZ + XAVIER ZANE" {
		t.Errorf("title = %q", first.title)
	}
	if first.url != "https://cutlersden.com/trailers/-BRAXTON-CRUZ-XAVIER-ZANE.html" {
		t.Errorf("url = %q", first.url)
	}
	if len(first.performers) != 2 || first.performers[0] != "Braxton Cruz" {
		t.Errorf("performers = %v", first.performers)
	}
	if first.date != "August 27, 2026" {
		t.Errorf("date = %q", first.date)
	}
	if first.duration != 22*60+25 {
		t.Errorf("duration = %d", first.duration)
	}
	if !strings.HasSuffix(first.thumbnail, ".jpg") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	// The card's hover preview lives in a script block, which is where the
	// only mp4 on the card is.
	if !strings.HasSuffix(first.preview, ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
}

func TestParseDateCount(t *testing.T) {
	cases := []struct {
		in       string
		date     string
		duration int
	}{
		{`August 27, 2026 | <i class="fa fa-play"></i> 22:25 | <i class="fa fa-camera"></i> 66`, "August 27, 2026", 1345},
		{`January 1, 2020 | <i class="fa fa-play"></i> 1:02:03`, "January 1, 2020", 3723},
		// A card with only a photo count must not read the count as a runtime.
		{`March 3, 2021 | <i class="fa fa-camera"></i> 40`, "March 3, 2021", 0},
		{"", "", 0},
	}
	for _, c := range cases {
		date, dur := parseDateCount(c.in)
		if date != c.date || dur != c.duration {
			t.Errorf("parseDateCount(%q) = %q/%d, want %q/%d", c.in, date, dur, c.date, c.duration)
		}
	}
}

func TestParseDetail(t *testing.T) {
	desc, cats := parseDetail(readFixture(t, "detail.html"))
	if !strings.HasPrefix(desc, "Xavier Zane walked in with a swagger") {
		t.Errorf("description = %q", desc)
	}
	if strings.Contains(desc, "  ") || strings.Contains(desc, "\n") {
		t.Errorf("description should be collapsed to one line: %q", desc)
	}
	if len(cats) < 5 {
		t.Fatalf("categories = %v", cats)
	}
	if cats[0] != "All-Male XXX" {
		t.Errorf("categories[0] = %q", cats[0])
	}
}

func TestSceneID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://cutlersden.com/trailers/-BRAXTON-CRUZ-XAVIER-ZANE.html", "-BRAXTON-CRUZ-XAVIER-ZANE"},
		{"/trailers/SOME-SCENE.html", "SOME-SCENE"},
		{"https://cutlersden.com/models/BraxtonCruz.html", ""},
	}
	for _, c := range cases {
		if got := sceneID(c.in); got != c.want {
			t.Errorf("sceneID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	item := listItem{
		id: "X", title: "T", url: "/trailers/X.html",
		thumbnail: "content/X/1.jpg", preview: "/videothumbs/x.mp4",
		performers: []string{"A"}, date: "August 27, 2026", duration: 60,
		description: "D", categories: []string{"C"},
	}
	sc := item.toScene("https://cutlersden.com", "https://cutlersden.com/", time.Now().UTC())
	if sc.URL != "https://cutlersden.com/trailers/X.html" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Thumbnail != "https://cutlersden.com/content/X/1.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Preview != "https://cutlersden.com/videothumbs/x.mp4" {
		t.Errorf("Preview = %q", sc.Preview)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-27" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}

	// An unparseable date leaves the field zero rather than guessing.
	item.date = "not a date"
	if got := item.toScene("https://cutlersden.com", "https://cutlersden.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestHasNextPage(t *testing.T) {
	if !hasNextPage(readFixture(t, "listing_page1.html")) {
		t.Error("page 1 has a next arrow")
	}
	if hasNextPage(readFixture(t, "listing_last.html")) {
		t.Error("the last page has no next arrow")
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "movies_1_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.Contains(r.URL.Path, "movies_2_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_last.html", srv.URL))
		case strings.HasPrefix(r.URL.Path, "/trailers/"):
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
		default:
			t.Errorf("unexpected fetch %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://cutlersden.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	byID := map[string]models.Scene{}
	total := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			byID[res.Scene.ID] = res.Scene
		case scraper.KindTotal:
			total = res.Total
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(byID) != 3 {
		t.Fatalf("got %d scenes, want 3", len(byID))
	}
	if total != 3 {
		t.Errorf("Total = %d, want 3", total)
	}
	sc, ok := byID["-BRAXTON-CRUZ-XAVIER-ZANE"]
	if !ok {
		t.Fatalf("missing scene, got %v", keys(byID))
	}
	if sc.Description == "" || len(sc.Categories) == 0 {
		t.Errorf("detail enrichment missing: %q %v", sc.Description, sc.Categories)
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "movies_1_d.html") {
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
			return
		}
		_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	ch, _ := s.ListScenes(context.Background(), "https://cutlersden.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{known: true}})

	var ids []string
	stopped := false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			ids = append(ids, res.Scene.ID)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected a StoppedEarly result")
	}
	if len(ids) != 1 {
		t.Errorf("ids = %v, want just the one before the known id", ids)
	}
}

func TestListScenesReportsDetailFailureButKeepsScene(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "movies_1_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.Contains(r.URL.Path, "movies_2_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_last.html", srv.URL))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://cutlersden.com/", scraper.ListOpts{})
	scenes, errs := 0, 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 3 {
		t.Errorf("scenes = %d, want 3 — a dead detail page must not drop the card", scenes)
	}
	if errs != 3 {
		t.Errorf("errors = %d, want 3", errs)
	}
}

func keys(m map[string]models.Scene) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
// server. The site writes absolute URLs in its own markup, so a fixture served
// verbatim would send the detail fetches to production.
func serveFixture(t *testing.T, name, base string) []byte {
	t.Helper()
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), defaultURL, base)
	if strings.Contains(rewritten, defaultURL) {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

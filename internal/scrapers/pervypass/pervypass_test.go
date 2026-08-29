package pervypass

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
		got, err := scraper.ForURL("https://www." + cfg.Domain + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.Domain, err)
			continue
		}
		if got.ID() != cfg.SiteID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.Domain, got.ID(), cfg.SiteID)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := newFor("pawged")
	cases := []struct {
		url  string
		want bool
	}{
		{"https://pawged.com/", true},
		{"https://www.pawged.com/tour/categories/movies_2_d.html", true},
		// justpov.com redirects to the pawged tour, so it is an alias.
		{"https://www.justpov.com/tour/", true},
		{"https://onlybbc.com/", false},
		{"https://notpawged.com/", false},
		{"https://example.com/pawged.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestCategory(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.pawged.com/", ""},
		{"https://www.pawged.com/tour/categories/movies.html", ""},
		{"https://www.pawged.com/tour/categories/movies_3_d.html", ""},
		{"https://www.pawged.com/tour/categories/PAWG.html", "PAWG"},
		{"https://www.pawged.com/tour/categories/PAWG_2_d.html", "PAWG"},
	}
	for _, c := range cases {
		if got := category(c.in); got != c.want {
			t.Errorf("category(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := newFor("pawged")
	if got := s.listingURL("movies", 3); got != "https://www.pawged.com/tour/categories/movies_3_d.html" {
		t.Errorf("listingURL = %q", got)
	}
	if got := s.listingURL("PAWG", 1); got != "https://www.pawged.com/tour/categories/PAWG_1_d.html" {
		t.Errorf("listingURL = %q", got)
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	// The content directory is the set id; the update slug is a title and
	// changes when the title does.
	if first.id != "256pawg" {
		t.Errorf("id = %q", first.id)
	}
	if first.title != "PAWG Poses for Prick" {
		t.Errorf("title = %q", first.title)
	}
	if first.url != "https://www.pawged.com/tour/updates/PAWG-Poses-for-Prick.html" {
		t.Errorf("url = %q", first.url)
	}
	if !strings.HasSuffix(first.thumbnail, "1-4x.jpg") {
		t.Errorf("thumbnail = %q — the widest still should win", first.thumbnail)
	}
	if len(first.performers) != 1 || first.performers[0] != "Scarlett Shadows" {
		t.Errorf("performers = %v", first.performers)
	}
	if first.date != "08/28/2026" {
		t.Errorf("date = %q", first.date)
	}
}

func TestSetID(t *testing.T) {
	cases := []struct{ thumb, url, want string }{
		{"content/256pawg/1-4x.jpg", "https://x/tour/updates/Title.html", "256pawg"},
		{"", "https://x/tour/updates/Title.html", "Title"},
		{"", "/tour/updates/Other.html", "Other"},
	}
	for _, c := range cases {
		if got := setID(c.thumb, c.url); got != c.want {
			t.Errorf("setID(%q,%q) = %q, want %q", c.thumb, c.url, got, c.want)
		}
	}
}

func TestEnrichFromDetail(t *testing.T) {
	item := listItem{}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if !strings.HasPrefix(item.description, "Scarlette Shadows showcases") {
		t.Errorf("description = %q", item.description)
	}
	if len(item.tags) < 5 || item.tags[0] != "ALT" {
		t.Errorf("tags = %v", item.tags)
	}
	if !strings.HasSuffix(item.preview, ".mp4") || strings.HasPrefix(item.preview, "/") {
		t.Errorf("preview = %q — the trailer path is relative to the tour base", item.preview)
	}
	// The detail page carries the date too, for a card that lacked one.
	if item.date != "08/28/2026" {
		t.Errorf("date = %q", item.date)
	}
}

func TestEnrichDoesNotOverwriteTheCardDate(t *testing.T) {
	item := listItem{date: "01/02/2020"}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if item.date != "01/02/2020" {
		t.Errorf("date = %q, want the card's own", item.date)
	}
}

func TestAbsURL(t *testing.T) {
	base := "https://www.pawged.com"
	cases := []struct{ in, want string }{
		// The tour sets <base href=".../tour/">, so a bare path is relative to
		// /tour/, not to the site root.
		{"content/256pawg/1.jpg", base + "/tour/content/256pawg/1.jpg"},
		{"trailers/trailer-256pawg_1.mp4", base + "/tour/trailers/trailer-256pawg_1.mp4"},
		{"/members/", base + "/members/"},
		{"https://cdn.example/x.jpg", "https://cdn.example/x.jpg"},
		{"", ""},
	}
	for _, c := range cases {
		if got := absURL(base, c.in); got != c.want {
			t.Errorf("absURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToScene(t *testing.T) {
	item := listItem{
		id: "256pawg", title: "T", url: "https://www.pawged.com/tour/updates/T.html",
		thumbnail: "content/256pawg/1-4x.jpg", performers: []string{"A"},
		date: "08/28/2026", description: "D", tags: []string{"Tag"},
	}
	sc := item.toScene(sites[0], "https://www.pawged.com", "https://pawged.com/", time.Now().UTC())
	if sc.SiteID != "pawged" || sc.Studio != "PAWGED" {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}
	if sc.Thumbnail != "https://www.pawged.com/tour/content/256pawg/1-4x.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-28" {
		t.Errorf("Date = %v", sc.Date)
	}
	item.date = "not a date"
	if got := item.toScene(sites[0], "https://www.pawged.com", "https://pawged.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "movies_1_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.Contains(r.URL.Path, "movies_2_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_page2.html", srv.URL))
		case strings.Contains(r.URL.Path, "/tour/updates/"):
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
		default:
			// Page 3 and beyond: the tour clamps and repeats page 2.
			_, _ = w.Write(serveFixture(t, "listing_page2.html", srv.URL))
		}
	}))
	defer srv.Close()

	s := newFor("pawged")
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://pawged.com/", scraper.ListOpts{Workers: 2})
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
		t.Fatalf("got %d scenes, want 3 — the repeated page must end the walk: %v", len(byID), keys(byID))
	}
	if total != 3 {
		t.Errorf("Total = %d, want 3", total)
	}
	sc := byID["256pawg"]
	if sc.Description == "" || len(sc.Tags) == 0 {
		t.Errorf("detail enrichment missing: %q %v", sc.Description, sc.Tags)
	}
	if !strings.HasPrefix(sc.URL, srv.URL) {
		t.Errorf("URL = %q, want it under the test server", sc.URL)
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tour/updates/") {
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	s := newFor("pawged")
	s.base = srv.URL

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	ch, _ := s.ListScenes(context.Background(), "https://pawged.com/",
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
		if strings.Contains(r.URL.Path, "/tour/updates/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.URL.Path, "movies_1_d.html") {
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
			return
		}
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	s := newFor("pawged")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://pawged.com/", scraper.ListOpts{})
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
// server, so the detail fetches stay offline.
func serveFixture(t *testing.T, name, base string) []byte {
	t.Helper()
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), "https://www.pawged.com", base)
	if strings.Contains(rewritten, "https://www.pawged.com") {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

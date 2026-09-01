package divinebreasts

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
	for _, u := range []string{"https://divinebreasts.com/", "https://www.divinebreasts.com/tour1/", "http://divinebreasts.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://divinebreasts.net/", "https://notdivinebreasts.com/", "https://example.com/divinebreasts.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The region gate sets a cookie and redirects to the same URL; without a jar
// every page is a redirect loop and the scrape returns nothing.
func TestClientCarriesACookieJar(t *testing.T) {
	if New().client.Jar == nil {
		t.Error("client has no cookie jar")
	}
}

func TestClassifyURL(t *testing.T) {
	cases := []struct {
		url  string
		kind urlKind
		slug string
	}{
		{"https://divinebreasts.com/", kindAll, ""},
		{"https://divinebreasts.com/tour1/categories/movies_3_d.html", kindAll, ""},
		{"https://divinebreasts.com/tour1/categories/bbw_1_d.html", kindCategory, "bbw"},
		{"https://divinebreasts.com/tour1/models/Kitty.html", kindModel, "Kitty"},
		{"https://divinebreasts.com/tour1/models/models.html", kindAll, ""},
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
		{kindAll, "", 2, "https://divinebreasts.com/tour1/categories/movies_2_d.html"},
		{kindCategory, "bbw", 1, "https://divinebreasts.com/tour1/categories/bbw_1_d.html"},
		{kindModel, "Kitty", 3, "https://divinebreasts.com/tour1/models/Kitty_3_d.html"},
	}
	for _, c := range cases {
		if got := s.listingURL(c.kind, c.slug, c.page); got != c.want {
			t.Errorf("listingURL = %q, want %q", got, c.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id != "6033" {
		t.Errorf("id = %q", first.id)
	}
	if first.title != "Kitty Doggy Style Jigglers Outside" {
		t.Errorf("title = %q", first.title)
	}
	if !strings.HasSuffix(first.thumbnail, "-4x.jpg") {
		t.Errorf("thumbnail = %q — the widest still should win", first.thumbnail)
	}
	if len(first.performers) != 1 || first.performers[0] != "Kitty" {
		t.Errorf("performers = %v", first.performers)
	}
	if first.date != "26 August, 2026" {
		t.Errorf("date = %q", first.date)
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
		id: "6033", title: "T", url: "/tour1/trailers/T.html",
		thumbnail:  "/tour1/content//contentthumbs/55/69/115569-4x.jpg",
		performers: []string{"Kitty"}, date: "26 August, 2026",
	}
	sc := s.toScene(item, "https://divinebreasts.com/", time.Now().UTC())
	if sc.URL != "https://divinebreasts.com/tour1/trailers/T.html" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-26" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}

	item.date = "not a date"
	if got := s.toScene(item, "https://divinebreasts.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestAbsURL(t *testing.T) {
	base := "https://divinebreasts.com"
	cases := []struct{ in, want string }{
		{"/tour1/trailers/x.html", base + "/tour1/trailers/x.html"},
		// The tour sets <base href="…/tour1/">, so a bare path hangs off it.
		{"content//contentthumbs/1.jpg", base + "/tour1/content//contentthumbs/1.jpg"},
		{"https://cdn.example/x.jpg", "https://cdn.example/x.jpg"},
		{"", ""},
	}
	for _, c := range cases {
		if got := absURL(base, c.in); got != c.want {
			t.Errorf("absURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	var pages []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Path)
		if strings.Contains(r.URL.Path, "movies_1_d.html") {
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
			return
		}
		_, _ = w.Write([]byte(`<html><body></body></html>`))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://divinebreasts.com/", scraper.ListOpts{})
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
		if !strings.HasPrefix(sc.URL, srv.URL) {
			t.Errorf("URL = %q, want it under the test server", sc.URL)
		}
	}
}

// Cards repeat within a page and the tour re-serves its last page past the
// end, so an all-repeats page is the clamp and ends the walk — without that
// the pager's 51-page count would be walked out in full for nothing.
func TestAllRepeatsPageEndsTheWalk(t *testing.T) {
	var fetched int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched++
		// Every page serves the same two cards; the fixture's pager names 6.
		_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://divinebreasts.com/", scraper.ListOpts{})
	scenes := 0
	for res := range ch {
		if res.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 2 {
		t.Errorf("scenes = %d, want 2 — the repeats must be deduplicated", scenes)
	}
	if fetched != 2 {
		t.Errorf("fetched %d pages, want 2 — the second page was all repeats", fetched)
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
// server.
func serveFixture(t *testing.T, name, base string) []byte {
	t.Helper()
	rewritten := strings.ReplaceAll(string(readFixture(t, name)), defaultURL, base)
	if strings.Contains(rewritten, defaultURL) {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

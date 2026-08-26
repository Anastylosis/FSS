package kickass

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegisteredSitesAreDistinct(t *testing.T) {
	ids := map[string]bool{}
	domains := map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.SiteID] {
			t.Errorf("duplicate site id %q", cfg.SiteID)
		}
		if domains[cfg.Domain] {
			t.Errorf("duplicate domain %q", cfg.Domain)
		}
		ids[cfg.SiteID] = true
		domains[cfg.Domain] = true
		if cfg.StudioName == "" {
			t.Errorf("%s: empty studio name", cfg.SiteID)
		}
	}
	if len(sites) < 19 {
		t.Errorf("expected the whole network, got %d sites", len(sites))
	}
}

func TestMatchesURL(t *testing.T) {
	s := newFor("ultracuckolds")
	if s == nil {
		t.Fatal("newFor returned nil")
	}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.ultracuckolds.com/", true},
		{"https://ultracuckolds.com", true},
		{"http://www.ultracuckolds.com/guest/videos/", true},
		{"https://www.cumeatingcuckolds.com/", false},
		{"https://notultracuckolds.com/", false},
		{"https://example.com/ultracuckolds.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestHyphenatedDomainIsNotAWildcard(t *testing.T) {
	s := newFor("5guycreampie")
	if !s.MatchesURL("https://www.5-guy-cream-pie.com/") {
		t.Error("own domain should match")
	}
	if s.MatchesURL("https://www.5xguyxcreamxpie.com/") {
		t.Error("the hyphens must be escaped in the host pattern")
	}
}

func TestPatterns(t *testing.T) {
	if got := newFor("cumeatingcuckolds").Patterns(); !strings.Contains(strings.Join(got, " "), "/tour/updates") {
		t.Errorf("tour site patterns = %v", got)
	}
	if got := newFor("kickassteens").Patterns(); !strings.Contains(strings.Join(got, " "), "/guest/videos/") {
		t.Errorf("guest site patterns = %v", got)
	}
}

func TestListingURL(t *testing.T) {
	guest := newFor("ultracuckolds")
	if got := guest.listingURL(2); got != "https://www.ultracuckolds.com/guest/videos/?page=2" {
		t.Errorf("guest listingURL = %q", got)
	}
	tour := newFor("cumeatingcuckolds")
	if got := tour.listingURL(3); got != "https://www.cumeatingcuckolds.com/tour/updates?page=3" {
		t.Errorf("tour listingURL = %q", got)
	}
}

func TestParseGuestListing(t *testing.T) {
	items := parseGuestListing(readFixture(t, "guest_listing.html"), "https://base")
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id != "1656" {
		t.Errorf("id = %q", first.id)
	}
	if first.title != "Gianna" {
		t.Errorf("title = %q", first.title)
	}
	if first.url != "https://base/guest/video/?id=1656" {
		t.Errorf("url = %q", first.url)
	}
	if !strings.HasPrefix(first.thumbnail, "https://cdn01.kickass.com/") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	// The card's src carries &amp; entities that must not survive into a URL.
	if strings.Contains(first.thumbnail, "&amp;") {
		t.Errorf("thumbnail not unescaped: %q", first.thumbnail)
	}
}

func TestGuestPageInfo(t *testing.T) {
	page, last := guestPageInfo(readFixture(t, "guest_listing.html"))
	if page != 1 || last != 3 {
		t.Errorf("guestPageInfo = %d/%d, want 1/3", page, last)
	}
	if p, l := guestPageInfo([]byte("<html></html>")); p != 0 || l != 0 {
		t.Errorf("guestPageInfo without a banner = %d/%d", p, l)
	}
}

func TestEnrichGuestDetail(t *testing.T) {
	it := listItem{id: "1656", title: "Gianna"}
	enrichGuestDetail(readFixture(t, "guest_detail.html"), &it)
	if it.date.Format("2006-01-02") != "2024-02-08" {
		t.Errorf("date = %v", it.date)
	}
	if !strings.HasPrefix(it.blurb, "The saying goes that love's a two-way street.") {
		t.Errorf("blurb = %q", it.blurb)
	}
	if len(it.performers) != 1 || it.performers[0] != "Gianna" {
		t.Errorf("performers = %v", it.performers)
	}
	if !strings.HasSuffix(it.preview, "_preview.mp4") {
		t.Errorf("preview = %q", it.preview)
	}
	if !strings.HasPrefix(it.thumbnail, "https://cdn01.kickass.com/") {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
}

func TestParseTourListing(t *testing.T) {
	items := parseTourListing(readFixture(t, "tour_listing.html"), "https://base")
	// The fixture holds three cards, one of which is a photo gallery.
	if len(items) != 2 {
		t.Fatalf("got %d scenes, want 2 (the photoset must be skipped)", len(items))
	}
	first := items[0]
	if first.id == "" || first.url != "https://base/tour/updates/"+first.id {
		t.Errorf("id/url = %q/%q", first.id, first.url)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if len(first.performers) == 0 {
		t.Error("performers missing")
	}
	if !strings.HasSuffix(first.blurb, "...") {
		t.Logf("blurb = %q", first.blurb)
	}
	if !strings.Contains(first.preview, ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
}

func TestMaxTourPage(t *testing.T) {
	if got := maxTourPage(readFixture(t, "tour_listing.html")); got < 2 {
		t.Errorf("maxTourPage = %d, want the pager's highest page", got)
	}
	if got := maxTourPage([]byte("<html></html>")); got != 0 {
		t.Errorf("maxTourPage without a pager = %d", got)
	}
}

func TestEnrichTourDetail(t *testing.T) {
	it := listItem{id: "3012", blurb: "truncated..."}
	enrichTourDetail(readFixture(t, "tour_detail.html"), &it)
	if strings.HasSuffix(it.blurb, "...") {
		t.Errorf("detail blurb should replace the truncated card one: %q", it.blurb)
	}
	if len(it.performers) != 1 || it.performers[0] != "Sophie Webber" {
		t.Errorf("performers = %v", it.performers)
	}
}

func TestTourPremiereDateFromBlurb(t *testing.T) {
	body := []byte(`<p class="blurb">She is so turned on. *Original Premier 03/07/2017</p>`)
	it := listItem{}
	enrichTourDetail(body, &it)
	if it.date.Format("2006-01-02") != "2017-03-07" {
		t.Errorf("date = %v", it.date)
	}
	// Most updates name no date at all, and prose must not be mistaken for one.
	plain := listItem{}
	enrichTourDetail([]byte(`<p class="blurb">They met on 03/07/2017 by chance.</p>`), &plain)
	if !plain.date.IsZero() {
		t.Errorf("date = %v, want zero", plain.date)
	}
}

func TestEnrichKeepsCardValuesWhenDetailIsEmpty(t *testing.T) {
	it := listItem{title: "Card Title", blurb: "Card blurb", performers: []string{"A"}}
	enrichTourDetail([]byte("<html></html>"), &it)
	enrichGuestDetail([]byte("<html></html>"), &it)
	if it.title != "Card Title" || it.blurb != "Card blurb" || len(it.performers) != 1 {
		t.Errorf("empty detail overwrote card data: %+v", it)
	}
}

func TestCleanText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Gianna\n  ", "Gianna"},
		{"a &amp; b", "a & b"},
		{"<a href=\"x\"> Name </a>", "Name"},
		{"Stop or I&#039;ll Squirt", "Stop or I'll Squirt"},
	}
	for _, c := range cases {
		if got := cleanText(c.in); got != c.want {
			t.Errorf("cleanText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAnchorNamesDeduplicates(t *testing.T) {
	got := anchorNames([]byte(`<a href="1">Ann</a> , <a href="2">ann</a>, <a href="3">Bee</a>`))
	if fmt.Sprint(got) != fmt.Sprint([]string{"Ann", "Bee"}) {
		t.Errorf("anchorNames = %v", got)
	}
}

func TestListScenesGuest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/guest/videos/":
			_, _ = w.Write(readFixture(t, "guest_listing.html"))
		case "/guest/video/":
			_, _ = w.Write(readFixture(t, "guest_detail.html"))
		default:
			t.Errorf("unexpected fetch %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := newFor("ultracuckolds")
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://www.ultracuckolds.com/", scraper.ListOpts{Workers: 2})
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
	// Two cards on a "Page 1 of 3" banner.
	if total != 6 {
		t.Errorf("Total = %d, want 6", total)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	sc := scenes[0]
	if sc.SiteID != "ultracuckolds" || sc.Studio != "Ultra Cuckolds" {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}
	if sc.StudioURL != "https://www.ultracuckolds.com/" {
		t.Errorf("StudioURL = %q", sc.StudioURL)
	}
	if sc.Date.IsZero() || sc.Description == "" || len(sc.Performers) == 0 {
		t.Errorf("detail enrichment missing: %+v", sc)
	}
	if !strings.HasPrefix(sc.URL, srv.URL) {
		t.Errorf("URL = %q, want it under the test server", sc.URL)
	}
}

func TestGuestListingIgnoresKnownIDs(t *testing.T) {
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/guest/videos/" {
			pages++
			_, _ = w.Write(readFixture(t, "guest_listing.html"))
			return
		}
		_, _ = w.Write(readFixture(t, "guest_detail.html"))
	}))
	defer srv.Close()

	s := newFor("ultracuckolds")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://www.ultracuckolds.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{"1656": true}})
	scenes := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			t.Error("the guest listing is unordered — it must never stop early")
		}
	}
	// The fixture is served for every page, so the repeat guard is what ends
	// the walk; the known id must not.
	if scenes != 2 {
		t.Errorf("scenes = %d, want 2", scenes)
	}
}

func TestTourListingStopsAtKnownID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tour/updates" {
			_, _ = w.Write(readFixture(t, "tour_listing.html"))
			return
		}
		_, _ = w.Write(readFixture(t, "tour_detail.html"))
	}))
	defer srv.Close()

	s := newFor("cumeatingcuckolds")
	s.base = srv.URL

	first := parseTourListing(readFixture(t, "tour_listing.html"), srv.URL)
	ch, _ := s.ListScenes(context.Background(), "https://www.cumeatingcuckolds.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{first[1].id: true}})

	var scenes []models.Scene
	stopped := false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected a StoppedEarly result")
	}
	if len(scenes) != 1 || scenes[0].ID != first[0].id {
		t.Errorf("scenes = %v", scenes)
	}
}

func TestListScenesReportsDetailFailureButKeepsScene(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/guest/videos/" {
			_, _ = w.Write(readFixture(t, "guest_listing.html"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newFor("ultracuckolds")
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://www.ultracuckolds.com/", scraper.ListOpts{})
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

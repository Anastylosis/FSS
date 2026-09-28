package loveherfilms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	if len(items) == 0 {
		t.Fatal("no items parsed from listing")
	}
	// First item is the newest scene, taken from the payload (groupId-anchored).
	if items[0].id != "golf-lessons-koda-monroe-van-wylde-boobs" {
		t.Errorf("first item id = %q", items[0].id)
	}
	if items[0].title != "Golf Lessons" {
		t.Errorf("first item title = %q", items[0].title)
	}
	for _, it := range items {
		if it.id == "" {
			t.Error("item with empty id")
		}
	}
}

func TestListingTotal(t *testing.T) {
	if got := listingTotal(readFixture(t, "listing.html")); got != 374 {
		t.Errorf("listingTotal = %d, want 374", got)
	}
	if got := listingTotal([]byte("<html></html>")); got != 0 {
		t.Errorf("listingTotal(no payload) = %d, want 0", got)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(readFixture(t, "detail.html"))

	if d.description == "" {
		t.Error("empty description")
	}
	if !strings.HasPrefix(d.thumbnail, "https://") {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
	if d.date.IsZero() {
		t.Error("zero date")
	}
	if d.date.Year() != 2026 || d.date.Month() != 6 || d.date.Day() != 16 {
		t.Errorf("date = %v, want 2026-06-16", d.date)
	}
	if d.duration != 38*60+56 {
		t.Errorf("duration = %d, want %d", d.duration, 38*60+56)
	}
	wantPerf := map[string]bool{"Bill Bailey": true, "Mia Malkova": true}
	if len(d.performers) != len(wantPerf) {
		t.Errorf("performers = %v", d.performers)
	}
	for _, p := range d.performers {
		if !wantPerf[p] {
			t.Errorf("unexpected performer %q", p)
		}
	}
	if len(d.tags) == 0 {
		t.Fatal("no tags parsed")
	}
	if d.tags[0] != "Arches" {
		t.Errorf("first tag = %q", d.tags[0])
	}
	foundFeet := false
	for _, tag := range d.tags {
		if tag == "Feet" {
			foundFeet = true
		}
	}
	if !foundFeet {
		t.Error("expected 'Feet' tag")
	}
}

func TestRunEndToEnd(t *testing.T) {
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/tour/trailers/"):
			_, _ = w.Write(detail)
		case strings.Contains(r.URL.Path, "/tour/categories/movies/1/"):
			_, _ = w.Write(listing)
		default:
			// page 2+ : empty, ends pagination
			_, _ = w.Write([]byte("<html><body></body></html>"))
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "loveherfeet", Domain: "loveherfeet.com", StudioName: "Love Her Feet"})
	s.Client = ts.Client()
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	var total int
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			sc := res.Scene
			if sc.ID == "" || sc.Title == "" || sc.URL == "" || sc.SiteID != "loveherfeet" {
				t.Errorf("incomplete scene: %+v", sc)
			}
			if sc.Studio != "Love Her Feet" {
				t.Errorf("studio = %q", sc.Studio)
			}
			if sc.ScrapedAt.IsZero() {
				t.Error("zero ScrapedAt")
			}
			// detail-enriched fields (all detail responses are the same fixture)
			if sc.Date.IsZero() || sc.Duration == 0 || len(sc.Tags) == 0 || len(sc.Performers) == 0 {
				t.Errorf("scene not enriched: %+v", sc)
			}
			scenes++
		case scraper.KindError:
			t.Errorf("scraper error: %v", res.Err)
		case scraper.KindTotal:
			total = res.Total
		}
	}

	if scenes == 0 {
		t.Fatal("no scenes emitted")
	}
	if total != 374 {
		t.Errorf("total = %d, want 374", total)
	}
}

func TestKnownIDsEarlyStop(t *testing.T) {
	listing := readFixture(t, "listing.html")
	// detailHits is incremented from concurrent worker requests, so it must be
	// accessed atomically to stay race-free under -race.
	var detailHits atomic.Int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/tour/trailers/"):
			detailHits.Add(1)
			_, _ = w.Write(readFixture(t, "detail.html"))
		case strings.Contains(r.URL.Path, "/tour/categories/movies/1/"):
			_, _ = w.Write(listing)
		default:
			_, _ = w.Write([]byte("<html></html>"))
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "loveherfeet", Domain: "loveherfeet.com", StudioName: "Love Her Feet"})
	s.Client = ts.Client()
	s.base = ts.URL

	items := parseListing(listing)
	first := items[0].id
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  2,
		KnownIDs: map[string]bool{first: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	stoppedEarly := false
	scenes := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindScene:
			scenes++
		}
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly when first ID is known")
	}
	// The known first scene is skipped, but the rest of the page is already in
	// hand and is emitted before the walk stops.
	if scenes != len(items)-1 {
		t.Errorf("expected the other %d scenes on the page, got %d", len(items)-1, scenes)
	}
	// The known first ID skips its own detail fetch; the remaining items on the
	// page are fetched concurrently before Paginate reaches the known one.
	if int(detailHits.Load()) >= len(items) {
		t.Errorf("known first ID should skip its detail fetch, hits=%d items=%d", detailHits.Load(), len(items))
	}
}

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		siteID string
		url    string
		want   bool
	}{
		{"loveherfeet", "https://www.loveherfeet.com/tour/", true},
		{"loveherfeet", "https://loveherfeet.com/tour/categories/movies/1/latest/", true},
		{"loveherfeet", "https://www.loveherboobs.com/tour/", false},
		{"loveherboobs", "https://www.loveherboobs.com/tour/", true},
		{"loveherbutt", "http://loveherbutt.com/tour/", true},
		{"shelovesblack", "https://www.shelovesblack.com/tour/", true},
		{"loveherfilms", "https://www.loveherfilms.com/tour/", true},
	}
	for _, c := range cases {
		s := newFor(c.siteID)
		if s == nil {
			t.Fatalf("no scraper for %q", c.siteID)
		}
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s MatchesURL(%q) = %v, want %v", c.siteID, c.url, got, c.want)
		}
	}
}

func TestRegistration(t *testing.T) {
	for _, cfg := range sites {
		if newFor(cfg.SiteID) == nil {
			t.Errorf("site %q not constructible", cfg.SiteID)
		}
	}
	if len(sites) != 5 {
		t.Errorf("expected 5 sites, got %d", len(sites))
	}
}

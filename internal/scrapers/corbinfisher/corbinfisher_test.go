package corbinfisher

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

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// newTestServer serves the listing fixture for category pages (page 1 only;
// later pages 404 -> empty -> stop) and the detail fixture for trailer pages.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	listing := loadFixture(t, "listing.html")
	detail := loadFixture(t, "detail.html")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/tour/categories/guys/1/"):
			_, _ = w.Write(listing)
		case strings.HasPrefix(r.URL.Path, "/tour/categories/guys/"):
			// any later page: no cards -> Paginate stops
			_, _ = w.Write([]byte("<html><body></body></html>"))
		case strings.HasPrefix(r.URL.Path, "/tour/trailers/"):
			_, _ = w.Write(detail)
		case strings.HasPrefix(r.URL.Path, "/tour/models/"):
			_, _ = w.Write(listing)
		default:
			http.NotFound(w, r)
		}
	}))
}

func collect(t *testing.T, studioURL string, opts scraper.ListOpts, base string) []models.Scene {
	t.Helper()
	s := New()
	s.base = base
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, studioURL, opts)
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

func TestMatchesURL(t *testing.T) {
	s := New()
	good := []string{
		"https://www.corbinfisher.com/",
		"https://corbinfisher.com/tour/categories/guys/1/latest/",
		"http://www.corbinfisher.com/tour/models/Charlie-3.html",
	}
	for _, u := range good {
		if !s.MatchesURL(u) {
			t.Errorf("expected match for %q", u)
		}
	}
	bad := []string{
		"https://shopcorbinfisher.com/",
		"https://example.com/",
	}
	for _, u := range bad {
		if s.MatchesURL(u) {
			t.Errorf("unexpected match for %q", u)
		}
	}
}

func TestScrapeListing(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	scenes := collect(t, ts.URL+"/", scraper.ListOpts{}, ts.URL)
	if len(scenes) != 3 {
		t.Fatalf("expected 3 scenes, got %d", len(scenes))
	}

	// Scenes are enriched from the same detail fixture (Fucking Charlie).
	got := scenes[0]
	if got.ID != "Fucking-Charlie" {
		t.Errorf("ID = %q, want Fucking-Charlie", got.ID)
	}
	if got.SiteID != "corbinfisher" {
		t.Errorf("SiteID = %q, want corbinfisher", got.SiteID)
	}
	if got.Studio != "Corbin Fisher" {
		t.Errorf("Studio = %q, want Corbin Fisher", got.Studio)
	}
	if got.Title != "Fucking Charlie" {
		t.Errorf("Title = %q, want Fucking Charlie", got.Title)
	}
	if !strings.HasSuffix(got.URL, "/tour/trailers/Fucking-Charlie.html") {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Duration != 31*60+54 {
		t.Errorf("Duration = %d, want %d", got.Duration, 31*60+54)
	}
	wantDate := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	if !got.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", got.Date, wantDate)
	}
	if len(got.Performers) != 2 || got.Performers[0] != "Charlie" || got.Performers[1] != "Cody" {
		t.Errorf("Performers = %v, want [Charlie Cody]", got.Performers)
	}
	if !strings.Contains(got.Description, "top stud") {
		t.Errorf("Description missing expected text: %q", got.Description)
	}
	if !strings.Contains(got.Thumbnail, "contentthumbs") {
		t.Errorf("Thumbnail = %q", got.Thumbnail)
	}
	if got.ScrapedAt.IsZero() {
		t.Error("ScrapedAt is zero")
	}
}

func TestScrapeStopsOnKnownID(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	opts := scraper.ListOpts{KnownIDs: map[string]bool{"Fucking-Charlie": true}}
	scenes := collect(t, ts.URL+"/", opts, ts.URL)
	// The known ID is first in document order, but the rest of the page is
	// already in hand and is emitted before the walk stops.
	if len(scenes) != 2 {
		t.Fatalf("expected the other 2 scenes on the page, got %d", len(scenes))
	}
}

func TestParseListingPage(t *testing.T) {
	items := parseListingPage(loadFixture(t, "listing.html"))
	if len(items) != 3 {
		t.Fatalf("expected 3 list items, got %d", len(items))
	}
	if items[0].id != "Fucking-Charlie" {
		t.Errorf("item[0].id = %q", items[0].id)
	}
	if items[0].title != "Fucking Charlie" {
		t.Errorf("item[0].title = %q", items[0].title)
	}
	if items[0].duration != 31*60+54 {
		t.Errorf("item[0].duration = %d", items[0].duration)
	}
}

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage(loadFixture(t, "detail.html"))
	if d.title != "Fucking Charlie" {
		t.Errorf("title = %q", d.title)
	}
	if d.duration != 31*60+54 {
		t.Errorf("duration = %d", d.duration)
	}
	if len(d.performers) != 2 {
		t.Errorf("performers = %v", d.performers)
	}
	if d.date.IsZero() {
		t.Error("date is zero")
	}
}

func TestParseListingPageThumbnails(t *testing.T) {
	items := parseListingPage(loadFixture(t, "listing.html"))
	if len(items) != 3 {
		t.Fatalf("expected 3 list items, got %d", len(items))
	}
	// Card 0 uses the hover-preview markup, the others the stdimage markup.
	for i, it := range items {
		if !strings.Contains(it.thumbnail, "/contentthumbs/") {
			t.Errorf("item[%d].thumbnail = %q", i, it.thumbnail)
		}
	}
}

func TestHasNextPage(t *testing.T) {
	body := loadFixture(t, "listing.html")
	if !hasNextPage(body, 1) {
		t.Error("page 1 should have a next page")
	}
	if hasNextPage(body, 2) {
		t.Error("page 2 should not have a next page")
	}
}

func TestScrapeEmptyFirstPageIsParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var gotErr error
	for res := range ch {
		if res.Kind == scraper.KindError {
			gotErr = res.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error for a page with no cards")
	}
	if k := scraper.Classify(gotErr); k != scraper.FailureParse {
		t.Errorf("Classify = %v, want FailureParse", k)
	}
}

package femdomempire

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	good := []string{
		"https://femdomempire.com",
		"https://femdomempire.com/",
		"https://www.femdomempire.com/tour/categories/movies/1/latest/",
		"https://femdomempire.com/tour/trailers/Chastity-Sex-Denial.html",
	}
	for _, u := range good {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	bad := []string{
		"https://example.com/femdomempire",
		"https://kink.com/channel/foo",
	}
	for _, u := range bad {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestParseCard(t *testing.T) {
	items := parseListingPage([]byte(readFixture(t, "listing.html")))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	it := items[0]
	if it.id != "3388" {
		t.Errorf("id = %q, want 3388", it.id)
	}
	if it.path != "/tour/trailers/Chastity-Sex-Denial.html" {
		t.Errorf("path = %q", it.path)
	}
	if it.title != "Chastity Sex Denial" {
		t.Errorf("title = %q", it.title)
	}
	if it.duration != 686 { // 11:26
		t.Errorf("duration = %d, want 686", it.duration)
	}
	if len(it.performers) != 1 || it.performers[0] != "Jill Kassidy" {
		t.Errorf("performers = %v", it.performers)
	}
	if it.date.IsZero() || it.date.Format("2006-01-02") != "2026-06-19" {
		t.Errorf("date = %v, want 2026-06-19", it.date)
	}
	if !strings.Contains(it.thumbnail, "57127-1x.jpg") {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
}

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(readFixture(t, "detail.html")))
	if d.title != "Chastity Sex Denial" {
		t.Errorf("title = %q", d.title)
	}
	if !strings.Contains(d.description, "safe word") {
		t.Errorf("description = %q", d.description)
	}
	want := []string{"Brat Girls", "Brunette", "Chastity", "Tease and Denial"}
	if len(d.tags) != len(want) {
		t.Fatalf("tags = %v, want %v", d.tags, want)
	}
	for i, w := range want {
		if d.tags[i] != w {
			t.Errorf("tag[%d] = %q, want %q", i, d.tags[i], w)
		}
	}
}

// TestListScenes wires a listing page plus detail page through an httptest
// server, exercising the full pagination + worker-pool + toScene path.
func TestListScenes(t *testing.T) {
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/tour/trailers/"):
			_, _ = fmt.Fprint(w, detail)
		case strings.Contains(r.URL.Path, "/tour/categories/movies/1/"):
			_, _ = fmt.Fprint(w, listing)
		default:
			// page 2+ : empty -> Done
			_, _ = fmt.Fprint(w, "<html><body></body></html>")
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/categories/movies/1/latest/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var got int
	var first scraper.SceneResult
	for res := range ch {
		if res.Kind == scraper.KindError {
			t.Fatalf("error result: %v", res.Err)
		}
		if res.Kind == scraper.KindScene {
			if got == 0 {
				first = res
			}
			got++
		}
	}
	if got != 3 {
		t.Fatalf("got %d scenes, want 3", got)
	}

	sc := first.Scene
	if sc.SiteID != "femdomempire" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Studio != "Femdom Empire" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if sc.Title != "Chastity Sex Denial" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.ID != "3388" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.Duration != 686 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Jill Kassidy" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if !strings.Contains(sc.Description, "safe word") {
		t.Errorf("Description = %q", sc.Description)
	}
	if len(sc.Tags) != 4 {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if sc.Date.Format("2006-01-02") != "2026-06-19" {
		t.Errorf("Date = %v", sc.Date)
	}
}

func TestListScenesKnownIDEarlyStop(t *testing.T) {
	listing := readFixture(t, "listing.html")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tour/categories/movies/1/") {
			_, _ = fmt.Fprint(w, listing)
			return
		}
		_, _ = fmt.Fprint(w, "<html></html>")
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	known := map[string]bool{"3388": true} // first card
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/categories/movies/1/latest/", scraper.ListOpts{KnownIDs: known})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes int
	var stopped bool
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	// The known card is first, but the rest of the page is already fetched
	// and is emitted before the walk stops.
	if scenes == 0 {
		t.Error("the rest of the page must still be emitted")
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

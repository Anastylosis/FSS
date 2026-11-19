package girlsrimming

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://girlsrimming.com", true},
		{"https://girlsrimming.com/all/video?page=2", true},
		{"https://www.girlsrimming.com/tour/categories/movies/2/latest/", true},
		{"http://girlsrimming.com/v/487-Evas-Naughty-Obsession", true},
		{"https://auntjudys.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestID(t *testing.T) {
	if got := New().ID(); got != siteID {
		t.Errorf("ID() = %q, want %q", got, siteID)
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}

	got := items[0]
	if got.id != "487" {
		t.Errorf("id = %q, want 487", got.id)
	}
	if !strings.HasSuffix(got.url, "/v/487-Evas-Naughty-Obsession") {
		t.Errorf("url = %q", got.url)
	}
	if got.title != "Eva's Naughty Obsession" {
		t.Errorf("title = %q", got.title)
	}
	if got.duration != 33*60+18 {
		t.Errorf("duration = %d, want 1998", got.duration)
	}
	want := time.Date(2026, time.November, 7, 0, 0, 0, 0, time.UTC)
	if !got.date.Equal(want) {
		t.Errorf("date = %v, want %v", got.date, want)
	}
	if !slices.Equal(got.performers, []string{"Dean", "Eva Tender"}) {
		t.Errorf("performers = %v", got.performers)
	}
	if !slices.Contains(got.categories, "Rimming") || !slices.Contains(got.tags, "Sloppy Blowjob") {
		t.Errorf("categories = %v, tags = %v", got.categories, got.tags)
	}
	if !strings.HasPrefix(got.thumb, "https://") || !strings.HasSuffix(got.thumb, "thumbnail.webp") {
		t.Errorf("thumb = %q", got.thumb)
	}

	// Newest-first ordering underpins the KnownIDs early-stop.
	for i := 1; i < len(items); i++ {
		if items[i].date.After(items[i-1].date) {
			t.Errorf("item %d is newer than item %d; listing must be newest-first", i, i-1)
		}
	}
}

func TestParseListingEmpty(t *testing.T) {
	if items := parseListing([]byte(`<html><body>nothing</body></html>`)); len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

// ---- detail ----

func TestApplyDetail(t *testing.T) {
	scene := models.Scene{}
	applyDetail(&scene, readFixture(t, "detail.html"))

	if !strings.HasPrefix(scene.Description, "The stunning and gorgeous Eva Tender") || strings.ContainsAny(scene.Description, "<>") || strings.Contains(scene.Description, "&#039;") {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Title != "Eva's Naughty Obsession - Hot Girls Giving Rimjobs To Guys" {
		t.Errorf("Title = %q (og:title is the fallback when the card gave none)", scene.Title)
	}
}

// ---- end-to-end ----

func TestListScenes(t *testing.T) {
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")

	var detailHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/all/video" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(listing)
		case strings.HasPrefix(r.URL.Path, "/v/"):
			detailHits.Add(1)
			_, _ = w.Write(detail)
		default:
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		}
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if got := detailHits.Load(); got != 3 {
		t.Errorf("detail fetches = %d, want 3", got)
	}
	for _, sc := range scenes {
		if sc.SiteID != siteID || sc.Studio != studioName {
			t.Errorf("scene %s: SiteID=%q Studio=%q", sc.ID, sc.SiteID, sc.Studio)
		}
		if sc.Title == "" || sc.Date.IsZero() || sc.Duration == 0 {
			t.Errorf("scene %s is missing listing data: %+v", sc.ID, sc)
		}
		if len(sc.Performers) == 0 {
			t.Errorf("scene %s has no performers", sc.ID)
		}
	}
}

func TestDetailFailureIsNotFatal(t *testing.T) {
	listing := readFixture(t, "listing.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/all/video" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(listing)
		case strings.HasPrefix(r.URL.Path, "/v/"):
			w.WriteHeader(http.StatusNotFound)
		default:
			_, _ = fmt.Fprint(w, `<html></html>`)
		}
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	for _, sc := range scenes {
		if sc.Title == "" || sc.Date.IsZero() {
			t.Errorf("scene %s lost its card data", sc.ID)
		}
	}
}

func TestContextCancellation(t *testing.T) {
	listing := readFixture(t, "listing.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(listing)
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("channel did not close after context cancellation")
	}
}

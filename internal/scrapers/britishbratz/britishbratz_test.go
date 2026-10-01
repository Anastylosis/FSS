package britishbratz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func loadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/listing.html")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseListingPage(t *testing.T) {
	s := New()
	scenes := s.parseListingPage(loadFixture(t), "https://www.britishbratz.com/")

	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	sc := scenes[1]
	if sc.ID != "2384927a-da41-4c2b-9fd2-e526175c913d" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.Title != "$100 In 120 Seconds" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.URL != "https://www.britishbratz.com/updates/previews/videos/100-in-120-seconds" {
		t.Errorf("URL = %q", sc.URL)
	}
	if !strings.HasPrefix(sc.Thumbnail, "https://vz-1c30c71e-eb5.b-cdn.net/2384927a-") || strings.Contains(sc.Thumbnail, "&amp;") {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	want := time.Date(2025, 10, 23, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if len(sc.Tags) != 1 || sc.Tags[0] != "Financial Domination" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Jasmine Jones" {
		t.Errorf("Performers = %v", sc.Performers)
	}

	// The first card credits "Various Models" as plain text and has no
	// category; the footer's links after the last card must not leak in.
	if len(scenes[0].Performers) != 0 || len(scenes[0].Tags) != 0 {
		t.Errorf("card 0 performers=%v tags=%v, want none", scenes[0].Performers, scenes[0].Tags)
	}
	last := scenes[2]
	if len(last.Performers) != 1 || len(last.Tags) != 1 {
		t.Errorf("last card performers=%v tags=%v", last.Performers, last.Tags)
	}
}

func TestEstimateTotal(t *testing.T) {
	if total := estimateTotal(loadFixture(t)); total != 31*pageSize {
		t.Errorf("total = %d, want %d", total, 31*pageSize)
	}
}

func TestScrapePaginates(t *testing.T) {
	listing := loadFixture(t)
	var (
		mu    sync.Mutex
		pages []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/updates/videos" {
			http.NotFound(w, r)
			return
		}
		p := r.URL.Query().Get("updates_page")
		mu.Lock()
		pages = append(pages, p)
		mu.Unlock()
		if p == "1" {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>No updates found.</body></html>"))
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL
	scenes, errs := collect(t, s, scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(pages, ",") != "1,2" {
		t.Errorf("pages fetched = %v, want [1 2]", pages)
	}
}

func TestScrapeEmptyFirstPageIsParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer ts.Close()

	s := New()
	s.base = ts.URL
	_, errs := collect(t, s, scraper.ListOpts{})
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("Classify = %v, want FailureParse", k)
	}
}

func collect(t *testing.T, s *Scraper, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, "https://www.britishbratz.com/", opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return scenes, errs
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.britishbratz.com/", true},
		{"https://britishbratz.com/updates", true},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

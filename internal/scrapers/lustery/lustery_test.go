package lustery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/videos":
			if r.URL.Query().Get("page") != "1" {
				// no more pages
				_, _ = fmt.Fprint(w, `{"currentPagePermalinks":[],"totalCount":2143,"recommendedPagePermalinks":[]}`)
				return
			}
			_, _ = fmt.Fprint(w, readFixture(t, "videos_page1.json"))
		case strings.HasPrefix(r.URL.Path, "/api/video/"):
			permalink := strings.TrimPrefix(r.URL.Path, "/api/video/")
			_, _ = fmt.Fprint(w, readFixture(t, "video_"+permalink+".json"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func collect(t *testing.T, s *Scraper, opts scraper.ListOpts) ([]scraper.SceneResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := s.ListScenes(ctx, "https://lustery.com/", opts)
	if err != nil {
		return nil, err
	}
	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}
	return results, nil
}

func TestScrape(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := New()
	s.base = ts.URL
	s.client = ts.Client()

	results, err := collect(t, s, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes []scraper.SceneResult
	var total int
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			t.Fatalf("unexpected error result: %v", r.Err)
		}
	}

	if total != 2143 {
		t.Errorf("total = %d, want 2143", total)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	// Find the "quick-suck" scene and assert parsed fields.
	var found bool
	for _, r := range scenes {
		sc := r.Scene
		if sc.ID != "quick-suck" {
			continue
		}
		found = true

		if sc.SiteID != "lustery" {
			t.Errorf("SiteID = %q, want lustery", sc.SiteID)
		}
		if sc.Studio != "Lustery" {
			t.Errorf("Studio = %q, want Lustery", sc.Studio)
		}
		if sc.Title != "Quick Suck" {
			t.Errorf("Title = %q, want Quick Suck", sc.Title)
		}
		if sc.URL != ts.URL+"/video/quick-suck" {
			t.Errorf("URL = %q", sc.URL)
		}
		if sc.Duration != 634 {
			t.Errorf("Duration = %d, want 634", sc.Duration)
		}
		// publishAt 1781776800 -> 2026-06-18 UTC
		wantDate := time.Unix(1781776800, 0).UTC()
		if !sc.Date.Equal(wantDate) {
			t.Errorf("Date = %v, want %v", sc.Date, wantDate)
		}
		wantPerf := []string{"Iris Leon", "Jase Leon"}
		if len(sc.Performers) != len(wantPerf) {
			t.Fatalf("Performers = %v, want %v", sc.Performers, wantPerf)
		}
		for i := range wantPerf {
			if sc.Performers[i] != wantPerf[i] {
				t.Errorf("Performers[%d] = %q, want %q", i, sc.Performers[i], wantPerf[i])
			}
		}
		// categories merged ahead of tags
		if len(sc.Tags) == 0 || sc.Tags[0] != "home-sex" {
			t.Errorf("Tags = %v, want first=home-sex", sc.Tags)
		}
		if !strings.HasPrefix(sc.Thumbnail, "https://img.lustery.com/") || !strings.HasSuffix(sc.Thumbnail, ".convert.webp") {
			t.Errorf("Thumbnail = %q", sc.Thumbnail)
		}
		if sc.ScrapedAt.IsZero() {
			t.Error("ScrapedAt is zero")
		}
	}
	if !found {
		t.Fatal("quick-suck scene not found")
	}
}

func TestScrapeKnownIDsEarlyStop(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := New()
	s.base = ts.URL
	s.client = ts.Client()

	// The first listed permalink is known: it is skipped, the rest of the page
	// is emitted, and the walk stops at the end of the page.
	results, err := collect(t, s, scraper.ListOpts{
		KnownIDs: map[string]bool{"quick-suck": true},
	})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var stopped bool
	var sceneCount int
	for _, r := range results {
		switch r.Kind {
		case scraper.KindStoppedEarly:
			stopped = true
		case scraper.KindScene:
			sceneCount++
		}
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if sceneCount == 0 {
		t.Error("the rest of the page must still be emitted")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := map[string]bool{
		"https://lustery.com/":           true,
		"https://www.lustery.com/videos": true,
		"http://lustery.com/video/quick": true,
		"https://example.com/lustery":    false,
		"https://notlustery.com/":        false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

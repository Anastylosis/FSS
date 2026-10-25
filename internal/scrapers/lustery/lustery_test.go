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
		switch {
		case r.URL.Path == "/sitemap.xml":
			_, _ = fmt.Fprint(w, readFixture(t, "sitemap.xml"))
		case strings.HasPrefix(r.URL.Path, "/api/video/"):
			permalink := strings.TrimPrefix(r.URL.Path, "/api/video/")
			if permalink == "missing" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
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

	// Three distinct /video/ entries; the couple page and the repeat are not scenes.
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
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

func TestScrapeKnownIDsSkip(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := New()
	s.base = ts.URL
	s.client = ts.Client()

	// The sitemap is not in release order, so a stored scene is skipped and
	// the walk carries on; StoppedEarly reports that something was skipped.
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
	if sceneCount != 2 {
		t.Errorf("got %d scenes, want the 2 unstored ones", sceneCount)
	}
}

// A detail that will not load is reported, not dropped silently: the walk
// must count as incomplete so --full cannot delete that scene.
func TestDetailFailureIsReported(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := New()
	s.base = ts.URL
	s.client = ts.Client()

	out := make(chan scraper.SceneResult, 8)
	s.fetchDetails(context.Background(), []string{"quick-suck", "missing"}, "https://lustery.com/", scraper.ListOpts{Workers: 1}, time.Now(), out)
	close(out)
	scenes, errs := 0, 0
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 1 || errs != 1 {
		t.Errorf("scenes=%d errs=%d, want 1 and 1", scenes, errs)
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

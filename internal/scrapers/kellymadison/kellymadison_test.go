package kellymadison

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
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

// collect drains the channel into scenes, failing on any error result.
func collect(t *testing.T, ch <-chan scraper.SceneResult) []models.Scene {
	t.Helper()
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("unexpected error result: %v", r.Err)
		}
	}
	return scenes
}

// ---- Fidelity CMS ----

func TestFidelityListScenes(t *testing.T) {
	listing := readFixture(t, "fidelity_listing.html")
	detail := readFixture(t, "fidelity_detail.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/episodes" && r.URL.Query().Get("page") == "":
			// first page → serve listing
			_, _ = w.Write(listing)
		case r.URL.Path == "/episodes":
			// page=2 etc → empty page to terminate pagination
			_, _ = w.Write([]byte("<html><body></body></html>"))
		case strings.HasPrefix(r.URL.Path, "/episodes/"):
			_, _ = w.Write(detail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newFor("pornfidelity")
	s.Client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := collect(t, ch)

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	// Newest-first order: first card is episode 519065870.
	sc := scenes[0]
	if sc.ID != "519065870" {
		t.Errorf("ID = %q, want 519065870", sc.ID)
	}
	if sc.SiteID != "pornfidelity" {
		t.Errorf("SiteID = %q, want pornfidelity", sc.SiteID)
	}
	if sc.Studio != "Porn Fidelity" {
		t.Errorf("Studio = %q, want Porn Fidelity", sc.Studio)
	}
	if sc.Title != "Fill'er Up w/ Love 2" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.URL != srv.URL+"/episodes/519065870" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Duration != 81*60 {
		t.Errorf("Duration = %d, want %d", sc.Duration, 81*60)
	}
	if sc.Date.Format("2006-01-02") != "2026-06-19" {
		t.Errorf("Date = %v, want 2026-06-19", sc.Date)
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
	// Performers come from the detail page actor array.
	if got := strings.Join(sc.Performers, ","); got != "Madison,Kloe Love" {
		t.Errorf("Performers = %q, want Madison,Kloe Love", got)
	}
	// og:description (full) should win over the listing description.
	if !strings.HasPrefix(sc.Description, "What can we say? Kloe wanted more") {
		t.Errorf("Description = %q", sc.Description)
	}

	if scenes[1].ID != "803446345" {
		t.Errorf("second scene ID = %q, want 803446345", scenes[1].ID)
	}
}

func TestFidelityKnownIDsEarlyStop(t *testing.T) {
	listing := readFixture(t, "fidelity_listing.html")

	detailHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/episodes":
			_, _ = w.Write(listing)
		case strings.HasPrefix(r.URL.Path, "/episodes/"):
			detailHits++
			_, _ = w.Write([]byte("<html></html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newFor("teenfidelity")
	s.Client = srv.Client()
	s.base = srv.URL

	// Mark the first card as known. It costs no detail fetch, but the rest of
	// the page is already in hand and is emitted before the walk stops.
	ch, err := s.ListScenes(context.Background(), srv.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"519065870": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes := collect(t, ch)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (the other card on the page)", len(scenes))
	}
	if scenes[0].ID == "519065870" {
		t.Error("the known card must not be emitted")
	}
	// The known first card costs no detail fetch (it becomes a stub); only the
	// second, unknown card is enriched.
	if detailHits != 1 {
		t.Errorf("detail fetched %d times, want 1 (only the unknown card)", detailHits)
	}
}

// ---- Ultra CMS ----

func TestUltraListScenes(t *testing.T) {
	search := readFixture(t, "ultra_search.json")
	detail := readFixture(t, "ultra_detail.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/episodes/search" && r.URL.Query().Get("page") == "1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(search)
		case r.URL.Path == "/episodes/search":
			// page>=2 → empty catalogue to terminate
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","total":0,"html":""}`))
		case strings.HasPrefix(r.URL.Path, "/episodes/"):
			_, _ = w.Write(detail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newFor("8kmilfs")
	s.Client = srv.Client()
	s.base = srv.URL
	s.catalogueBase = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := collect(t, ch)

	// The fixture catalogue has 8KM and 8KT cards; 8kmilfs keeps only 8KM.
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (8KM only)", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "8KM/36" {
		t.Errorf("ID = %q, want 8KM/36", sc.ID)
	}
	if sc.SiteID != "8kmilfs" {
		t.Errorf("SiteID = %q, want 8kmilfs", sc.SiteID)
	}
	if sc.Studio != "8K MILFs" {
		t.Errorf("Studio = %q, want 8K MILFs", sc.Studio)
	}
	if sc.Title != "Sweet Swan! 🦢" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.URL != srv.URL+"/episodes/8KM/36" {
		t.Errorf("URL = %q", sc.URL)
	}
	// Runtime from listing card: 57 mins → 3420s.
	if sc.Duration != 57*60 {
		t.Errorf("Duration = %d, want %d", sc.Duration, 57*60)
	}
	if sc.Date.Format("01-02") != "06-17" {
		t.Errorf("Date = %v, want month-day 06-17", sc.Date)
	}
	if got := strings.Join(sc.Performers, ","); got != "Brandi Swan" {
		t.Errorf("Performers = %q, want Brandi Swan", got)
	}
	if !strings.HasPrefix(sc.Description, "Sexy Brandi Swan is here to show you true passion") {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
}

func TestUltraPrefixFiltering(t *testing.T) {
	search := readFixture(t, "ultra_search.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/episodes/search" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(search)
		case r.URL.Path == "/episodes/search":
			_, _ = w.Write([]byte(`{"total":0,"html":""}`))
		case strings.HasPrefix(r.URL.Path, "/episodes/"):
			_, _ = w.Write([]byte("<html></html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 8kteens keeps only the 8KT card from the same fixture.
	s := newFor("8kteens")
	s.Client = srv.Client()
	s.base = srv.URL
	s.catalogueBase = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := collect(t, ch)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (8KT only)", len(scenes))
	}
	if !strings.HasPrefix(scenes[0].ID, "8KT/") {
		t.Errorf("ID = %q, want 8KT/ prefix", scenes[0].ID)
	}
}

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		site string
		url  string
		want bool
	}{
		{"pornfidelity", "https://www.pornfidelity.com/", true},
		{"pornfidelity", "https://pornfidelity.com/episodes", true},
		{"pornfidelity", "https://www.teenfidelity.com/", false},
		{"5kporn", "https://www.5kporn.com/episodes/5KP/1", true},
		{"5kporn", "https://www.8kmilfs.com/", false},
		{"8kmilfs", "https://www.8kmilfs.com/episodes", true},
	}
	for _, c := range cases {
		s := newFor(c.site)
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s.MatchesURL(%q) = %v, want %v", c.site, c.url, got, c.want)
		}
	}
}

func TestRegistration(t *testing.T) {
	for _, cfg := range sites {
		if _, err := scraper.ForID(cfg.SiteID); err != nil {
			t.Errorf("site %q not registered: %v", cfg.SiteID, err)
		}
	}
}

package vintageflash

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/detail.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://vintageflash.com", true},
		{"http://www.vintageflash.com/", true},
		{"https://vintageflash.com/chloe-toy-a-vintage-classic_MTY3NQ==.html", true},
		{"https://nylonscash.com/", false},
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

// The scene id is a base64-encoded integer and the slug is ignored by the
// server, which is what makes enumeration possible at all.
func TestSceneURL(t *testing.T) {
	orig := siteBase
	siteBase = "https://vintageflash.com"
	defer func() { siteBase = orig }()

	got := sceneURL(1675)
	want := "https://vintageflash.com/set_" + base64.StdEncoding.EncodeToString([]byte("1675")) + ".html"
	if got != want {
		t.Errorf("sceneURL(1675) = %q, want %q", got, want)
	}
	if !strings.Contains(got, "MTY3NQ==") {
		t.Errorf("sceneURL(1675) = %q, expected the base64 of 1675", got)
	}
}

func TestFetchScene(t *testing.T) {
	detail := readFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(detail)
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	sc, ok := s.fetchScene(context.Background(), "https://vintageflash.com", 1675, time.Now())
	if !ok {
		t.Fatal("fetchScene returned not-ok")
	}
	if sc.ID != "1675" {
		t.Errorf("ID = %q", sc.ID)
	}
	// The title tag reads "Vintage Flash: Model - Title"; both parts are used.
	if sc.Title != "A Vintage Classic" {
		t.Errorf("Title = %q", sc.Title)
	}
	if !slices.Equal(sc.Performers, []string{"Chloe Toy"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
	// "161 images and 12:31 video"
	if sc.Duration != 751 {
		t.Errorf("Duration = %d, want 751", sc.Duration)
	}
	want := time.Date(2020, time.November, 27, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if !strings.HasPrefix(sc.Description, "Chloe looks truly ravishing") {
		t.Errorf("Description = %q", sc.Description)
	}
	if !strings.Contains(sc.Thumbnail, "awizicon") {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
}

// A missing id answers HTTP 500. That is a gap in the catalogue, not a
// failure, and must not surface as an error.
func TestFetchSceneMissingIDIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	if _, ok := s.fetchScene(context.Background(), "x", 999, time.Now()); ok {
		t.Error("a 500 response should report not-found")
	}
}

// A 200 page that is not a scene (no "Vintage Flash: " title) is also a miss.
func TestFetchSceneNonScenePageIsAMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><head><title>Some Other Page</title></head></html>`)
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	if _, ok := s.fetchScene(context.Background(), "x", 5, time.Now()); ok {
		t.Error("a non-scene page should report not-found")
	}
}

// ---- end-to-end ----

// idServer serves scenes for the ids in `present` and 500s for the rest, the
// way the live site does.
func idServer(t *testing.T, present map[int]bool) (*Scraper, *atomic.Int32) {
	t.Helper()
	detail := readFixture(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = fmt.Fprint(w, `<html><head><title>Vintage Flash!</title></head></html>`)
			return
		}
		hits.Add(1)
		enc := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/set_"), ".html")
		raw, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		id, err := strconv.Atoi(string(raw))
		if err != nil || !present[id] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(detail)
	}))
	t.Cleanup(srv.Close)

	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })

	s := New()
	s.Client = srv.Client()
	return s, &hits
}

func TestListScenesWalksIDsAndStopsOnGap(t *testing.T) {
	// A small catalogue with an interior gap, then nothing.
	present := map[int]bool{1: true, 2: true, 5: true, 40: true}
	s, hits := idServer(t, present)

	ch, err := s.ListScenes(context.Background(), "https://vintageflash.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != len(present) {
		t.Fatalf("got %d scenes, want %d", len(scenes), len(present))
	}
	// Interior gaps must not end the walk — id 40 is past a 34-id gap.
	ids := map[string]bool{}
	for _, sc := range scenes {
		ids[sc.ID] = true
	}
	if !ids["40"] {
		t.Error("id 40 was missed; an interior gap ended the walk early")
	}

	// The walk must stop well before maxID once the catalogue runs out.
	if got := hits.Load(); got >= int32(maxID) {
		t.Errorf("probed %d ids, want far fewer than maxID=%d", got, maxID)
	}
}

func TestListScenesEmptyCatalogue(t *testing.T) {
	s, _ := idServer(t, map[int]bool{})

	ch, err := s.ListScenes(context.Background(), "https://vintageflash.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if scenes := testutil.CollectScenes(t, ch); len(scenes) != 0 {
		t.Errorf("got %d scenes, want 0", len(scenes))
	}
}

// homeServer answers the homepage with a redirect to location and counts every
// other request, so a test can prove the id walk never started.
func homeServer(t *testing.T, location string) (*Scraper, *atomic.Int32) {
	t.Helper()
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, location, http.StatusMovedPermanently)
			return
		}
		probes.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })

	s := New()
	s.Client = srv.Client()
	return s, &probes
}

func collectErrs(t *testing.T, s *Scraper) (int, []error) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), "https://vintageflash.com", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := 0
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindTotal, scraper.KindStoppedEarly:
		}
	}
	return scenes, errs
}

// The live domain 301s to nhlpcentral.com. Following that, every id probe
// lands on a 404 and reads as a gap, so the scrape must say why it has nothing
// instead of ending quietly with zero scenes.
func TestListScenesReportsFoldedIntoNHLP(t *testing.T) {
	s, probes := homeServer(t, "https://nhlpcentral.com/")

	scenes, errs := collectErrs(t, s)
	if scenes != 0 {
		t.Errorf("got %d scenes, want 0", scenes)
	}
	if len(errs) != 1 || !errors.Is(errs[0], errFoldedIntoNHLP) {
		t.Fatalf("errors = %v, want exactly errFoldedIntoNHLP", errs)
	}
	if !strings.Contains(errs[0].Error(), "nhlpcentral.com") {
		t.Errorf("error %q does not name the new home", errs[0])
	}
	if n := probes.Load(); n != 0 {
		t.Errorf("walked %d ids after detecting the redirect, want 0", n)
	}
}

// Only the redirect to nhlpcentral.com is the known fold. A redirect anywhere
// else is still an error, but not that one.
func TestListScenesOtherRedirectIsAPlainError(t *testing.T) {
	s, probes := homeServer(t, "https://parked.example/")

	scenes, errs := collectErrs(t, s)
	if scenes != 0 || len(errs) != 1 {
		t.Fatalf("scenes=%d errs=%v, want 0 scenes and one error", scenes, errs)
	}
	if errors.Is(errs[0], errFoldedIntoNHLP) {
		t.Errorf("a redirect to parked.example was reported as the NHLP fold: %v", errs[0])
	}
	if n := probes.Load(); n != 0 {
		t.Errorf("walked %d ids after an off-site redirect, want 0", n)
	}
}

func TestIsNHLP(t *testing.T) {
	cases := map[string]bool{
		"https://nhlpcentral.com/":              true,
		"https://www.NHLPcentral.com/index.php": true,
		"https://nhlpcentral.com.evil.test/":    false,
		"https://vintageflash.com/":             false,
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := isNHLP(u); got != want {
			t.Errorf("isNHLP(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestContextCancellation(t *testing.T) {
	present := map[int]bool{}
	for i := 1; i <= 2000; i++ {
		present[i] = true
	}
	s, _ := idServer(t, present)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, "https://vintageflash.com", scraper.ListOpts{})
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
	case <-time.After(20 * time.Second):
		t.Fatal("channel did not close after context cancellation")
	}
}

package xsinsvr

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
		{"https://xsinsvr.com/", true},
		{"https://xsinsvr.com/videos", true},
		{"https://www.xsinsvr.com/videos/3", true},
		{"https://xsinsvr.com/video/morning-love", true},
		{"https://xsinsvr.com/model/olivia-sparkle", true},
		{"https://sinsvr.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// The /videos listing paginates; model, studio and tag pages do not.
func TestSinglePageDetection(t *testing.T) {
	cases := []struct {
		url    string
		single bool
	}{
		{"https://xsinsvr.com/videos", false},
		{"https://xsinsvr.com/videos/4", false},
		{"https://xsinsvr.com/", false},
		{"https://xsinsvr.com/model/olivia-sparkle", true},
		{"https://xsinsvr.com/studio/billie-star", true},
		{"https://xsinsvr.com/tag/scene/anal", true},
	}
	for _, c := range cases {
		if got := singlePageRe.MatchString(c.url); got != c.single {
			t.Errorf("singlePage(%q) = %v, want %v", c.url, got, c.single)
		}
	}
}

func TestListingPageURL(t *testing.T) {
	orig := siteBase
	siteBase = "https://xsinsvr.com"
	defer func() { siteBase = orig }()

	// Page 1 is the bare path — /videos/0 renders as the disabled prev link.
	if got, want := listingPageURL(1), "https://xsinsvr.com/videos"; got != want {
		t.Errorf("page 1 = %q, want %q", got, want)
	}
	if got, want := listingPageURL(2), "https://xsinsvr.com/videos/2"; got != want {
		t.Errorf("page 2 = %q, want %q", got, want)
	}
}

// ---- listing ----

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}

	got := items[0]
	if got.slug != "morning-love" {
		t.Errorf("slug = %q", got.slug)
	}
	if got.title != "Morning Love" {
		t.Errorf("title = %q", got.title)
	}
	// The template emits `class="main-image"src="…"` with no space; the regex
	// must tolerate that.
	if !strings.HasPrefix(got.thumb, "https://public-mojo.xsinsvr.com/") {
		t.Errorf("thumb = %q", got.thumb)
	}
	if !strings.HasSuffix(got.preview, "preview.mp4") {
		t.Errorf("preview = %q", got.preview)
	}
	if !slices.Equal(got.performers, []string{"Olivia Sparkle", "Matty Mila Perez"}) {
		t.Errorf("performers = %v", got.performers)
	}
	// XSinsVR licenses catalogues, so the card's studio is the originating
	// brand, not XSinsVR.
	if got.studio != "Olivia Sparkle" {
		t.Errorf("studio = %q", got.studio)
	}
}

// The props div holds the resolution badge and only sometimes a runtime
// ("<strong>8K</strong> <span></span>"), so a missing duration is normal and
// the badge text must never be read as one.
func TestParseListingDurationOptional(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	if len(items) < 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0].duration != 0 {
		t.Errorf("item 0 duration = %d, want 0 (the card has no runtime)", items[0].duration)
	}
	if items[1].duration != 2967 { // 49:27
		t.Errorf("item 1 duration = %d, want 2967", items[1].duration)
	}
}

// Licensed scenes carry other brands — the aggregator model must be preserved.
func TestParseListingKeepsLicensedStudios(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	var studios []string
	for _, it := range items {
		studios = append(studios, it.studio)
	}
	if !slices.Contains(studios, "Badoink VR") {
		t.Errorf("studios = %v, expected a licensed brand among them", studios)
	}
}

func TestParseListingEmpty(t *testing.T) {
	if items := parseListing([]byte(`<html><body>nothing</body></html>`)); len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

// ---- end-to-end ----

// detailSceneIDs gives each listing slug a distinct data-scene id, so the
// three fixture scenes do not collapse onto one id.
var detailSceneIDs = map[string]string{
	"morning-love":          "1507", // keeps the fixture's own id
	"anal-reward-2":         "1508",
	"the-committed-thruple": "1509",
}

func newTestSite(t *testing.T) (*Scraper, *httptest.Server, *atomic.Int32) {
	t.Helper()
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")

	var detailHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos":
			_, _ = w.Write(listing)
		case strings.HasPrefix(r.URL.Path, "/video/"):
			detailHits.Add(1)
			// Give each slug its own scene id. Serving the fixture verbatim
			// gave every scene the fixture's id (1507), which hid that the id
			// is parsed from the detail page (sceneIDRe wants digits, so the
			// substitute has to be numeric too).
			slug := strings.TrimPrefix(r.URL.Path, "/video/")
			id, ok := detailSceneIDs[slug]
			if !ok {
				id = "1507"
			}
			_, _ = w.Write([]byte(strings.ReplaceAll(string(detail), "1507", id)))
		case strings.HasPrefix(r.URL.Path, "/model/"),
			strings.HasPrefix(r.URL.Path, "/studio/"),
			strings.HasPrefix(r.URL.Path, "/tag/"):
			_, _ = w.Write(listing)
		default:
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)

	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })

	s := New()
	s.Client = srv.Client()
	return s, srv, &detailHits
}

func collect(t *testing.T, s *Scraper, studioURL string) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	return testutil.CollectScenes(t, ch)
}

// collectWithErrors returns the scenes and the reported failures, for the cases
// where a dropped scene must still be announced.
func collectWithErrors(t *testing.T, s *Scraper, studioURL string) ([]models.Scene, []error) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
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

func TestListScenes(t *testing.T) {
	s, srv, detailHits := newTestSite(t)

	scenes := collect(t, s, srv.URL+"/videos")
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	// Every scene needs a detail fetch: the id lives only there.
	if got := detailHits.Load(); got != 3 {
		t.Errorf("detail fetches = %d, want 3", got)
	}
	// Each scene must carry its own id. The store is keyed on (id, site_id),
	// so repeats would silently collapse three scenes into one at Save.
	ids := map[string]bool{}
	for _, sc := range scenes {
		if ids[sc.ID] {
			t.Errorf("scene id %q repeated", sc.ID)
		}
		ids[sc.ID] = true
	}

	sc := scenes[0]
	if sc.ID != "1507" {
		t.Errorf("ID = %q, want 1507", sc.ID)
	}
	if sc.SiteID != siteID {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Title != "Morning Love" {
		t.Errorf("Title = %q", sc.Title)
	}
	want := time.Date(2026, time.July, 22, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if !slices.Equal(sc.Performers, []string{"Olivia Sparkle", "Matty Mila Perez"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Studio != "Olivia Sparkle" {
		t.Errorf("Studio = %q, want the licensing brand", sc.Studio)
	}
	if sc.Description == "" {
		t.Error("Description is empty")
	}
	if !strings.Contains(sc.Resolution, "8K") {
		t.Errorf("Resolution = %q", sc.Resolution)
	}
	if len(sc.Tags) == 0 {
		t.Error("Tags are empty")
	}
	// Tags repeat on the page and must be deduped.
	seen := map[string]bool{}
	for _, tag := range sc.Tags {
		if seen[tag] {
			t.Errorf("duplicate tag %q in %v", tag, sc.Tags)
		}
		seen[tag] = true
	}
}

// The scene id exists only on the detail page, so a failed detail fetch means
// there is no usable scene — it must be dropped, but reported: swallowed, it
// looks like a site with nothing on it, and --full then deletes the catalogue.
func TestDetailFailureDropsSceneAndReportsIt(t *testing.T) {
	listing := readFixture(t, "listing.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos":
			_, _ = w.Write(listing)
		case strings.HasPrefix(r.URL.Path, "/video/"):
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

	scenes, errs := collectWithErrors(t, s, srv.URL+"/videos")
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes, want 0 — no id means no scene", len(scenes))
	}
	if len(errs) == 0 {
		t.Error("a detail failure must be reported, not swallowed")
	}
}

// A detail page without a data-scene attribute is not a scene, and the parse
// failure is reported so it cannot be mistaken for an empty catalogue.
func TestMissingSceneIDDropsSceneAndReportsIt(t *testing.T) {
	listing := readFixture(t, "listing.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/videos" {
			_, _ = w.Write(listing)
			return
		}
		_, _ = fmt.Fprint(w, `<html><body><p>no data-scene here</p></body></html>`)
	}))
	defer srv.Close()

	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()

	scenes, errs := collectWithErrors(t, s, srv.URL+"/videos")
	if len(scenes) != 0 {
		t.Errorf("got %d scenes, want 0", len(scenes))
	}
	if len(errs) == 0 {
		t.Fatal("a missing scene id must be reported as a parse failure")
	}
	for _, err := range errs {
		if kind := scraper.Classify(err); kind != scraper.FailureParse {
			t.Errorf("failure kind = %v, want FailureParse", kind)
		}
	}
}

func TestRunSinglePage(t *testing.T) {
	s, srv, _ := newTestSite(t)

	scenes := collect(t, s, srv.URL+"/model/olivia-sparkle")
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
}

func TestContextCancellation(t *testing.T) {
	s, srv, _ := newTestSite(t)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, srv.URL+"/videos", scraper.ListOpts{})
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

// A page whose details all fail yields no scenes, and Paginate reads an empty
// page as the end of the catalogue — so the callback must set Continue, or a
// mid-crawl block truncates the walk and --full deletes the rest.
func TestAllDetailsFailingDoesNotEndTheWalk(t *testing.T) {
	listing := readFixture(t, "listing.html")
	var listPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/video/"):
			w.WriteHeader(http.StatusTooManyRequests)
		case strings.HasPrefix(r.URL.Path, "/videos"):
			listPages++
			if listPages == 1 {
				_, _ = w.Write(listing)
				return
			}
			_, _ = fmt.Fprint(w, `<html><body>no videos</body></html>`)
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

	scenes, errs := collectWithErrors(t, s, srv.URL+"/videos")
	if len(scenes) != 0 {
		t.Errorf("got %d scenes, want 0", len(scenes))
	}
	if len(errs) == 0 {
		t.Error("the failures must be reported")
	}
	if listPages < 2 {
		t.Errorf("fetched %d listing pages — an all-failed page must not stop the walk", listPages)
	}
}

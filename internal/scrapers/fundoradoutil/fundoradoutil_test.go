package fundoradoutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

var testCfg = SiteConfig{SiteID: "rubberempire", Host: "rubber-empire.com", StudioName: "Rubber Empire", Labels: []int{749, 751}}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type recorder struct {
	mu     sync.Mutex
	paths  []string
	hdrs   http.Header
	detail map[string]bool
}

// newServer serves label 749 as two pages and label 751 as one page that
// repeats a scene from 749, so cross-list dedup is exercised.
func newServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{detail: map[string]bool{}}
	empty := []byte(`{"videos":{"current_page":1,"data":[],"last_page":1,"total":0}}`)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sitecfg", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("h") != "rubber-empire.com" {
			http.NotFound(w, r)
			return
		}
		rec.mu.Lock()
		rec.hdrs = r.Header.Clone()
		rec.mu.Unlock()
		_, _ = w.Write(fixture(t, "sitecfg.json"))
	})
	mux.HandleFunc("/api/videos/browse/", func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path+"?"+r.URL.RawQuery)
		rec.mu.Unlock()
		if r.URL.Query().Get("site_id") != "34" {
			http.Error(w, "bad site", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/api/videos/browse/labels/749", "/api/videos/browse/genres/236":
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write(fixture(t, "browse_p2.json"))
				return
			}
			_, _ = w.Write(fixture(t, "browse_p1.json"))
		case "/api/videos/browse/labels/751":
			_, _ = w.Write(fixture(t, "browse_p2.json"))
		default:
			_, _ = w.Write(empty)
		}
	})
	mux.HandleFunc("/api/videodetail/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/videodetail/")
		rec.mu.Lock()
		rec.detail[id] = true
		rec.mu.Unlock()
		b, err := os.ReadFile(filepath.Join("testdata", "detail_"+id+".json"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec
}

func newTestScraper(srv *httptest.Server) *Scraper {
	s := New(testCfg)
	s.Client = srv.Client()
	s.APIBase = srv.URL
	return s
}

type collected struct {
	scenes  []scraper.SceneResult
	errs    []error
	total   int
	stopped bool
}

func run(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) collected {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var c collected
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			c.scenes = append(c.scenes, r)
		case scraper.KindError:
			c.errs = append(c.errs, r.Err)
		case scraper.KindTotal:
			c.total = r.Total
		case scraper.KindStoppedEarly:
			c.stopped = true
		}
	}
	return c
}

func TestListScenesRoot(t *testing.T) {
	srv, rec := newServer(t)
	s := newTestScraper(srv)
	c := run(t, s, "https://rubber-empire.com/", scraper.ListOpts{Workers: 2})

	if len(c.scenes) != 4 {
		t.Fatalf("got %d scenes, want 4 (751 repeats a 749 scene)", len(c.scenes))
	}
	// The fixture reports 4 for 749 and 4 for 751, summed up front.
	if c.total != 8 {
		t.Errorf("progress total = %d, want 8", c.total)
	}
	// 496231 and 494403 have no detail fixture, so two detail errors.
	if len(c.errs) != 2 {
		t.Errorf("got %d errors, want 2: %v", len(c.errs), c.errs)
	}

	first := c.scenes[0].Scene
	if first.ID != "496235" || first.SiteID != "rubberempire" {
		t.Errorf("first scene id/site = %q/%q", first.ID, first.SiteID)
	}
	if first.Title != "Was für ein wunderbarer Tag für seine Eier" {
		t.Errorf("title = %q", first.Title)
	}
	if first.URL != "https://rubber-empire.com/video/496235/was-fur-ein-wunderbarer-tag-fur-seine-eier" {
		t.Errorf("url = %q", first.URL)
	}
	if first.StudioURL != "https://rubber-empire.com/" {
		t.Errorf("studioURL = %q", first.StudioURL)
	}
	if first.Studio != "Rubber Empire" {
		t.Errorf("studio = %q", first.Studio)
	}
	if first.Date.Format("2006-01-02") != "2026-09-21" {
		t.Errorf("date = %v, want 2026-09-21", first.Date)
	}
	if first.Duration != 689 {
		t.Errorf("duration = %d", first.Duration)
	}
	if len(first.Performers) != 2 || first.Performers[0] != "Lady Blackdiamoond" || first.Performers[1] != "Anna Petrovna" {
		t.Errorf("performers = %v", first.Performers)
	}
	if !strings.HasPrefix(first.Thumbnail, "https://img.cfmx-cd.net/") {
		t.Errorf("thumbnail = %q", first.Thumbnail)
	}
	if !strings.HasPrefix(first.Description, "Es beginnt damit") {
		t.Errorf("description = %.40q", first.Description)
	}
	if len(first.Tags) == 0 {
		t.Error("no tags from detail genres")
	}

	// A scene whose detail failed is still emitted from the listing.
	third := c.scenes[2].Scene
	if third.ID != "496231" || third.Description != "" || third.Title == "" {
		t.Errorf("detail-less scene = %+v", third)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, want := range []string{"/api/videos/browse/labels/749", "/api/videos/browse/labels/751"} {
		found := false
		for _, p := range rec.paths {
			found = found || strings.HasPrefix(p, want)
		}
		if !found {
			t.Errorf("never requested %s: %v", want, rec.paths)
		}
	}
	if rec.hdrs.Get("Origin") != "https://rubber-empire.com" || rec.hdrs.Get("Referer") != "https://rubber-empire.com/" {
		t.Errorf("origin/referer = %q/%q", rec.hdrs.Get("Origin"), rec.hdrs.Get("Referer"))
	}
}

func TestListScenesGenreMode(t *testing.T) {
	srv, rec := newServer(t)
	s := newTestScraper(srv)
	url := "https://rubber-empire.com/videos/browse/genres/236?ts=Q0JU"
	c := run(t, s, url, scraper.ListOpts{})
	if len(c.scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(c.scenes))
	}
	if c.scenes[0].Scene.StudioURL != url {
		t.Errorf("studioURL = %q, want the URL passed in", c.scenes[0].Scene.StudioURL)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, p := range rec.paths {
		if strings.Contains(p, "labels/") {
			t.Errorf("genre URL walked a label list: %s", p)
		}
	}
}

func TestListScenesKnownIDsStopsEarly(t *testing.T) {
	srv, rec := newServer(t)
	s := newTestScraper(srv)
	c := run(t, s, "https://rubber-empire.com", scraper.ListOpts{KnownIDs: map[string]bool{"496233": true}})
	if !c.stopped {
		t.Error("expected StoppedEarly")
	}
	for _, r := range c.scenes {
		if r.Scene.ID == "496233" {
			t.Error("known scene was re-emitted")
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, p := range rec.paths {
		if strings.Contains(p, "page=2") && strings.Contains(p, "labels/749") {
			t.Errorf("walked past the page holding a known scene: %s", p)
		}
	}
	if rec.detail["496233"] {
		t.Log("known scene detail was fetched; tolerated, the page is already paid for")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(testCfg)
	for _, u := range []string{
		"https://rubber-empire.com",
		"https://rubber-empire.com/",
		"https://www.rubber-empire.com/",
		"http://rubber-empire.com/?x=1",
		"https://rubber-empire.com/videos/browse/labels/751",
		"https://rubber-empire.com/videos/browse/studio/3482",
		"https://rubber-empire.com/videos/browse/genres/236?ts=Q0JU",
		"https://rubber-empire.com/videos/browse/genre/236",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://rubber-empire.com.evil.com/",
		"https://xrubber-empire.com/",
		"https://rubber-empire.com/video/496235/slug",
		"https://rubber-empire.com/videos/new",
		"https://rubber-empire.com/videos/browse/actors/12",
		"https://rubber-empireXcom/",
		"https://orgasmabuse.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestTargets(t *testing.T) {
	s := New(testCfg)
	got := s.targets("https://rubber-empire.com/")
	if len(got) != 2 || got[0] != (browseTarget{"labels", 749}) || got[1] != (browseTarget{"labels", 751}) {
		t.Errorf("root targets = %v", got)
	}
	got = s.targets("https://rubber-empire.com/videos/browse/studio/3482?ts=abc")
	if len(got) != 1 || got[0] != (browseTarget{"studio", 3482}) {
		t.Errorf("studio targets = %v", got)
	}
	got = s.targets("https://rubber-empire.com/videos/browse/genre/236")
	if len(got) != 1 || got[0] != (browseTarget{"genre", 236}) {
		t.Errorf("genre targets = %v", got)
	}
}

func TestPatterns(t *testing.T) {
	p := New(testCfg).Patterns()
	want := map[string]bool{
		"rubber-empire.com":                           true,
		"rubber-empire.com/videos/browse/labels/{id}": true,
		"rubber-empire.com/videos/browse/studio/{id}": true,
		"rubber-empire.com/videos/browse/genres/{id}": true,
		"rubber-empire.com/videos/browse/genre/{id}":  true,
	}
	if len(p) != len(want) {
		t.Fatalf("patterns = %v", p)
	}
	for _, x := range p {
		if !want[x] {
			t.Errorf("unexpected pattern %q", x)
		}
	}
}

func TestParseDate(t *testing.T) {
	cases := map[string]string{
		"2026-09-20T22:00:00.000000Z": "2026-09-21", // CEST midnight
		"2026-01-14T23:00:00.000000Z": "2026-01-15", // CET midnight
		"2026-09-20T22:00:00Z":        "2026-09-21",
		"":                            "",
		"garbage":                     "",
	}
	for in, want := range cases {
		got := parseDate(in)
		if want == "" {
			if !got.IsZero() {
				t.Errorf("parseDate(%q) = %v, want zero", in, got)
			}
			continue
		}
		if got.Format("2006-01-02") != want {
			t.Errorf("parseDate(%q) = %v, want %s", in, got, want)
		}
	}
}

func TestLangString(t *testing.T) {
	if got := (langString{"de": "Hallo", "en": "Hello"}).String(); got != "Hello" {
		t.Errorf("en preferred: %q", got)
	}
	if got := (langString{"nl": "Hoi", "de": "Hallo"}).String(); got != "Hallo" {
		t.Errorf("stable fallback: %q", got)
	}
	if got := (langString{"de": ""}).String(); got != "" {
		t.Errorf("empty: %q", got)
	}
	var nilLS langString
	if nilLS.String() != "" || nilLS.key() != "" {
		t.Error("nil langString should be empty")
	}
}

func TestDescriptionPrefersTitleLanguage(t *testing.T) {
	d := &detailResponse{}
	d.Video.Description = langString{"de": "Beschreibung"}
	d.Video.TransDescription = langString{"en": "Description", "de": "Beschreibung"}
	if got := description(d, "de"); got != "Beschreibung" {
		t.Errorf("got %q", got)
	}
	d.Video.Description = nil
	if got := description(d, "de"); got != "Beschreibung" {
		t.Errorf("trans fallback in title language: %q", got)
	}
	if got := description(d, "fr"); got != "Description" {
		t.Errorf("any-language fallback: %q", got)
	}
}

func TestSiteConfigWithoutIDIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	s := newTestScraper(srv)
	c := run(t, s, "https://rubber-empire.com", scraper.ListOpts{})
	if len(c.scenes) != 0 || len(c.errs) != 1 {
		t.Fatalf("scenes=%d errs=%v", len(c.scenes), c.errs)
	}
	if !scraper.Classify(c.errs[0]).MissingData() {
		t.Error("error should count as missing data")
	}
}

package badoink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// newTestScraper wires a badoinkvr-shaped scraper to a test server.
func newTestScraper(t *testing.T, handler http.Handler) (*Scraper, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	s := New(SiteConfig{
		SiteID:     "badoinkvr",
		Domain:     "badoinkvr.com",
		StudioName: "BaDoinkVR",
		ListPath:   "vrpornvideos",
		VideoPath:  "vrpornvideo",
	})
	s.base = ts.URL
	s.Client = ts.Client()
	return s, ts
}

func TestParseListing(t *testing.T) {
	s := newFor("badoinkvr")
	body := []byte(readFixture(t, "listing.html"))
	items := s.parseListing(body)
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}
	first := items[0]
	if first.id != "327636" {
		t.Errorf("first id = %q, want 327636", first.id)
	}
	if first.slug != "vanessa_goes_pearl_diving-327636" {
		t.Errorf("first slug = %q", first.slug)
	}
	if first.url != "/vrpornvideo/vanessa_goes_pearl_diving-327636/" {
		t.Errorf("first url = %q", first.url)
	}
	if first.title != "Vanessa Goes Pearl Diving" {
		t.Errorf("first title = %q", first.title)
	}
	// HTML-entity title is unescaped.
	if items[3].title != "Rodeo + Juliet" {
		t.Errorf("rodeo title = %q, want Rodeo + Juliet", items[3].title)
	}
	if mp := s.maxPageNum(body); mp != 4 {
		t.Errorf("maxPageNum = %d, want 4", mp)
	}
}

func TestParseDetail(t *testing.T) {
	s := newFor("badoinkvr")
	body := []byte(readFixture(t, "detail.html"))
	d := s.parseDetail(body)

	if d.title != "LUST MEMORY VI: Lustception" {
		t.Errorf("title = %q", d.title)
	}
	if d.description == "" {
		t.Error("description empty")
	}
	if strings.Contains(d.description, "<a") || strings.Contains(d.description, "&quot;") {
		t.Errorf("description not cleaned: %q", d.description)
	}
	if d.thumbnail == "" {
		t.Error("thumbnail empty")
	}
	if d.preview == "" {
		t.Error("preview empty")
	}
	// PT52M49S -> 3169 seconds
	if d.duration != 3169 {
		t.Errorf("duration = %d, want 3169", d.duration)
	}
	if d.date.IsZero() {
		t.Error("date not parsed")
	}
	if len(d.performers) == 0 || d.performers[0] != "Blake Blossom" {
		t.Errorf("performers = %v", d.performers)
	}
	if len(d.tags) == 0 || d.tags[0] != "Virtual Reality" {
		t.Errorf("tags = %v, want first Virtual Reality", d.tags)
	}
	if len(d.tags) < 3 {
		t.Errorf("expected category tags beyond Virtual Reality, got %v", d.tags)
	}
}

func TestRunEndToEnd(t *testing.T) {
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/vrpornvideo/"):
			_, _ = w.Write([]byte(detail))
		case r.URL.Path == "/vrpornvideos" && r.URL.Query().Get("order") == "newest":
			_, _ = w.Write([]byte(listing))
		default:
			// page 2+ returns empty -> Paginate stops.
			_, _ = w.Write([]byte("<html><body></body></html>"))
		}
	})

	s, _ := newTestScraper(t, handler)

	ch, err := s.ListScenes(context.Background(), s.base, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes []string
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			sc := res.Scene
			if sc.SiteID != "badoinkvr" {
				t.Errorf("SiteID = %q", sc.SiteID)
			}
			if sc.Studio != "BaDoinkVR" {
				t.Errorf("Studio = %q", sc.Studio)
			}
			if sc.Title == "" || sc.URL == "" || sc.ID == "" {
				t.Errorf("missing core fields: %+v", sc)
			}
			if sc.StudioURL != s.base {
				t.Errorf("StudioURL = %q", sc.StudioURL)
			}
			if sc.Duration == 0 {
				t.Errorf("scene %s has no duration", sc.ID)
			}
			if len(sc.Tags) == 0 || sc.Tags[0] != "Virtual Reality" {
				t.Errorf("scene %s tags = %v", sc.ID, sc.Tags)
			}
			scenes = append(scenes, sc.ID)
		case scraper.KindError:
			t.Fatalf("scrape error: %v", res.Err)
		}
	}

	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5: %v", len(scenes), scenes)
	}
}

func TestKnownIDsEarlyStop(t *testing.T) {
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/vrpornvideo/"):
			_, _ = w.Write([]byte(detail))
		case r.URL.Path == "/vrpornvideos":
			_, _ = w.Write([]byte(listing))
		default:
			_, _ = w.Write([]byte("<html><body></body></html>"))
		}
	})

	s, _ := newTestScraper(t, handler)

	// Mark the 3rd listing scene as known: Paginate finishes the page — the
	// known scene itself is skipped — and stops before the next one.
	opts := scraper.ListOpts{KnownIDs: map[string]bool{"327633": true}}
	ch, err := s.ListScenes(context.Background(), s.base, opts)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var stopped bool
	var count int
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			count++
			if res.Scene.ID == "327633" {
				t.Errorf("known ID 327633 should not be emitted")
			}
		case scraper.KindStoppedEarly:
			stopped = true
		case scraper.KindError:
			t.Fatalf("scrape error: %v", res.Err)
		}
	}
	if !stopped {
		t.Error("expected StoppedEarly signal")
	}
	if count != 4 {
		t.Errorf("emitted %d scenes before stop, want 4 — every card but the known one", count)
	}
}

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		siteID string
		url    string
		want   bool
	}{
		{"badoinkvr", "https://badoinkvr.com/vrpornvideos", true},
		{"badoinkvr", "https://www.badoinkvr.com/", true},
		{"badoinkvr", "https://18vr.com/vrpornvideos", false},
		{"vrcosplayx", "https://vrcosplayx.com/cosplaypornvideos", true},
		{"realvr", "https://realvr.com/vrpornvideos", true},
		{"18vr", "https://18vr.com/", true},
		{"babevr", "https://babevr.com/vrpornvideos", true},
	}
	for _, c := range cases {
		s := newFor(c.siteID)
		if s == nil {
			t.Fatalf("no scraper for %q", c.siteID)
		}
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s MatchesURL(%q) = %v, want %v", c.siteID, c.url, got, c.want)
		}
	}
}

func TestAllSitesRegistered(t *testing.T) {
	for _, cfg := range sites {
		s := newFor(cfg.SiteID)
		if s == nil {
			t.Errorf("site %q not constructible", cfg.SiteID)
			continue
		}
		if s.ID() != cfg.SiteID {
			t.Errorf("ID() = %q, want %q", s.ID(), cfg.SiteID)
		}
		if len(s.Patterns()) == 0 {
			t.Errorf("%s has no patterns", cfg.SiteID)
		}
	}
}

// The zero-value-slot contract, pinned.
//
// fetchDetails writes into a pre-sized []models.Scene indexed by position, and a
// goroutine cancelled before it finishes deliberately leaves its slot untouched. The
// zero value has an empty ID, and the consumer skips those — that filter is the *only*
// thing stopping a cancelled fetch from emitting a scene with no title or URL.
//
// This is easy to break in either direction, which is why it is pinned rather than left
// to a comment:
//   - remove the `ID == ""` skip and every cancelled fetch emits an empty scene;
//   - "fix" the cancel path by writing a populated stub and the same thing happens.
//
// 20 other packages share this exact shape, so the contract is worth stating once
// against a representative of it.
func TestCancelledDetailFetchContributesNoScene(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	s, ts := newTestScraper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "vrpornvideo/") {
			<-release // park every detail fetch
			return
		}
		w.Header().Set("Content-Type", "text/html")
	}))
	_ = ts

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before any goroutine runs

	items := []listItem{{id: "a", url: "/vrpornvideo/a"}, {id: "b", url: "/vrpornvideo/b"}}
	got := s.fetchDetails(ctx, items, scraper.ListOpts{Workers: 2}, time.Now().UTC())

	// fetchDetails filters the zero-value slots itself, so a fully cancelled run
	// yields nothing at all rather than a set of hollow scenes.
	if len(got) != 0 {
		for _, sc := range got {
			t.Errorf("cancelled fetch produced a scene: id=%q title=%q url=%q — a slot "+
				"left unwritten by a cancelled goroutine must not become an emittable "+
				"scene", sc.ID, sc.Title, sc.URL)
		}
	}
}

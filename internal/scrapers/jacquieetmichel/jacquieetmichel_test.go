package jacquieetmichel

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

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// detailByID maps the scene IDs present in listing_page1.html to detail
// fixtures. IDs without a fixture get a generic VideoObject so the worker
// pool still produces a scene.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	listing := readFixture(t, "listing_page1.html")
	empty := readFixture(t, "listing_empty.html")
	alice := readFixture(t, "detail_alice.html")
	julia := readFixture(t, "detail_julia.html")

	genericDetail := func(id, name string) []byte {
		return []byte(`<!doctype html><html><head>
<script type="application/ld+json">
{"@context":"https://schema.org","@graph":[
 {"@type":"VideoObject","name":"` + name + `","description":"desc","duration":"PT10M",
  "datePublished":"2026-01-01T04:00:00.000Z","uploadDate":"2026-01-01T04:00:00.000Z",
  "thumbnailUrl":"https://t2.example/thumb.jpg","contentUrl":"https://example/trailer/` + id + `",
  "keywords":["Blonde"],"actor":[{"@type":"Person","name":"Someone"}]}
]}
</script></head><body></body></html>`)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/fr/content/list", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			_, _ = fmt.Fprint(w, string(listing))
			return
		}
		_, _ = fmt.Fprint(w, string(empty))
	})
	mux.HandleFunc("/fr/content/", func(w http.ResponseWriter, r *http.Request) {
		// /fr/content/{id}/{slug}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/fr/content/"), "/")
		id := parts[0]
		switch id {
		case "6a3053774456cf816fbae3c0":
			_, _ = fmt.Fprint(w, string(alice))
		case "6a2bd3b767d8650a5c794717":
			_, _ = fmt.Fprint(w, string(julia))
		default:
			_, _ = fmt.Fprint(w, string(genericDetail(id, "Scene "+id)))
		}
	})
	return httptest.NewServer(mux)
}

func collect(t *testing.T, s *Scraper, opts scraper.ListOpts) []scraper.SceneResult {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), s.base+"/fr/content/list", opts)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var out []scraper.SceneResult
	for r := range ch {
		out = append(out, r)
	}
	return out
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := map[string]bool{
		"https://www.jacquieetmicheltv.net/":                  true,
		"https://jacquieetmicheltv.net/fr/content/list":       true,
		"http://www.jacquieetmicheltv.net/fr/content/abc/def": true,
		"https://www.example.com/":                            false,
		"https://jacquieetmichel.com/":                        false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 5 {
		t.Fatalf("parseListing: got %d items, want 5", len(items))
	}
	if items[0].id != "6a3053774456cf816fbae3c0" {
		t.Errorf("first id = %q", items[0].id)
	}
	if items[0].url != "/fr/content/6a3053774456cf816fbae3c0/alice-soumise-par-deux-mecs-est-plus-excitee-que-jamais" {
		t.Errorf("first url = %q", items[0].url)
	}
}

func TestExtractGraphVideoObject(t *testing.T) {
	vo := extractGraphVideoObject(readFixture(t, "detail_alice.html"))
	if vo == nil {
		t.Fatal("extractGraphVideoObject returned nil")
	}
	if !strings.HasPrefix(vo.Name, "Alice") {
		t.Errorf("Name = %q", vo.Name)
	}
	if got := vo.actors(); len(got) != 3 || got[0] != "Alice de Paris" {
		t.Errorf("actors = %v", got)
	}
	if len(vo.tags()) < 10 {
		t.Errorf("tags = %v (want >=10)", vo.tags())
	}
}

func TestScrapeParsesScenes(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := New()
	s.base = ts.URL

	results := collect(t, s, scraper.ListOpts{Workers: 2})

	var scenes []scraper.SceneResult
	sawTotal := false
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindTotal:
			sawTotal = true
		case scraper.KindError:
			t.Fatalf("unexpected error result: %v", r.Err)
		}
	}
	if !sawTotal {
		t.Error("expected a progress/total result")
	}
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}

	// Find Alice scene and assert full field mapping.
	var alice *scraper.SceneResult
	for i := range scenes {
		if scenes[i].Scene.ID == "6a3053774456cf816fbae3c0" {
			alice = &scenes[i]
			break
		}
	}
	if alice == nil {
		t.Fatal("Alice scene not found")
	}
	sc := alice.Scene
	if sc.SiteID != "jacquieetmichel" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Studio != "Jacquie & Michel TV" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if !strings.HasPrefix(sc.Title, "Alice, soumise") {
		t.Errorf("Title = %q", sc.Title)
	}
	if !strings.HasSuffix(sc.URL, "/fr/content/6a3053774456cf816fbae3c0/alice-soumise-par-deux-mecs-est-plus-excitee-que-jamais") {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Description == "" {
		t.Error("Description empty")
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail empty")
	}
	if sc.Preview == "" {
		t.Error("Preview empty")
	}
	if len(sc.Performers) != 3 || sc.Performers[0] != "Alice de Paris" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Tags) < 10 {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if sc.Duration != 30*60+42 {
		t.Errorf("Duration = %d, want %d", sc.Duration, 30*60+42)
	}
	if sc.Date.IsZero() || sc.Date.Year() != 2026 {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt zero")
	}
}

func TestScrapeKnownIDsEarlyStop(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := New()
	s.base = ts.URL

	// The first scene on page 1 is known. The page is already fetched, so the
	// rest of it is emitted and the walk stops at the end of the page.
	opts := scraper.ListOpts{
		Workers:  2,
		KnownIDs: map[string]bool{"6a3053774456cf816fbae3c0": true},
	}
	results := collect(t, s, opts)

	stopped := false
	scenes := 0
	for _, r := range results {
		switch r.Kind {
		case scraper.KindStoppedEarly:
			stopped = true
		case scraper.KindScene:
			if r.Scene.ID == "6a3053774456cf816fbae3c0" {
				t.Error("the known scene must not be emitted")
			}
			scenes++
		}
	}
	if scenes == 0 {
		t.Error("the rest of page 1 must still be emitted")
	}
	if !stopped {
		t.Error("expected StoppedEarly result")
	}
}

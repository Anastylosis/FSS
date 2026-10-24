package mysatin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	ms, sp := New(sites[0]), New(sites[1])
	for _, u := range []string{
		"https://www.mysatin.com/",
		"https://mysatin.com/updates",
		"https://www.mysatin.com/models/Bex-White",
	} {
		if !ms.MatchesURL(u) {
			t.Errorf("mysatin MatchesURL(%q) = false", u)
		}
		if sp.MatchesURL(u) {
			t.Errorf("satinplay MatchesURL(%q) = true", u)
		}
	}
	if !sp.MatchesURL("https://www.satinplay.com/models/Jess-West") {
		t.Error("satinplay model URL not matched")
	}
	if ms.MatchesURL("https://www.mysatinfun.com/") {
		t.Error("matched a different domain")
	}
}

func TestParseUpdates(t *testing.T) {
	refs := parseUpdates(readFixture(t, "updates.html"))
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	want := setRef{id: "bexwhitepinknightie2", path: "/free-satin-gallery/bexwhitepinknightie2.html", date: "2026-10-25", title: "Pink Satin Nightie"}
	if refs[0] != want {
		t.Errorf("refs[0] = %+v, want %+v", refs[0], want)
	}
}

func TestParseModelPages(t *testing.T) {
	slugs := parseModelIndex(readFixture(t, "models.html"))
	if strings.Join(slugs, ",") != "Bex-White,Bad-Dolly" {
		t.Errorf("slugs = %v", slugs)
	}
	refs := parseModelSets(readFixture(t, "model.html"))
	if len(refs) != 2 || refs[1].id != "bexredrobe" || refs[1].date != "2019-03-01" || refs[1].title != "Red Satin Robe" {
		t.Errorf("model sets = %+v", refs)
	}
}

func TestParseDetail(t *testing.T) {
	d, ok := parseDetail(readFixture(t, "detail.html"))
	if !ok {
		t.Fatal("parseDetail failed")
	}
	if d.title != "Pink Satin Nightie" {
		t.Errorf("title = %q", d.title)
	}
	if !strings.Contains(d.description, "nightie & purple") {
		t.Errorf("description = %q", d.description)
	}
	if strings.Join(d.performers, ",") != "Bex White,Bad Dolly" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.duration != 3600+7*60 {
		t.Errorf("duration = %d", d.duration)
	}
	if d.date != "2026-10-25" || d.thumbnail != "/gallery/2026/10/2026-10-25_bexwhitepinknightie2/preview/video-preview.jpg" {
		t.Errorf("date/thumbnail = %q %q", d.date, d.thumbnail)
	}

	d, ok = parseDetail(readFixture(t, "satinplay_detail.html"))
	if !ok || d.title != "Copper Satin Sheets" || d.duration != 540 ||
		strings.Join(d.performers, ",") != "Jess West" || d.date != "2026-10-10" ||
		!strings.HasPrefix(d.description, "Jess is looking") {
		t.Errorf("satinplay detail = %+v", d)
	}
	if _, ok := parseDetail([]byte("<html></html>")); ok {
		t.Error("parseDetail accepted an empty page")
	}
}

// A few sets carry an empty <h1> on the site itself; the listing title fills in.
func TestEmptyTitleFallsBackToListing(t *testing.T) {
	d, ok := parseDetail(readFixture(t, "empty_title.html"))
	if !ok || d.title != "" || d.description != "" || strings.Join(d.performers, ",") != "Hayley Marie" {
		t.Fatalf("detail = %+v ok=%v", d, ok)
	}
	sc := New(sites[0]).toScene(setRef{id: "hayleypurple", path: "/free-satin-gallery/hayleypurple.html", title: "Purple Satin"}, d, "https://www.mysatin.com/", time.Now())
	if sc.Title != "Purple Satin" || sc.Date.Format("2006-01-02") != "2013-09-01" {
		t.Errorf("scene = %+v", sc)
	}
}

type fixtureServer struct {
	mu   sync.Mutex
	hits []string
}

func newServer(t *testing.T) (*httptest.Server, *fixtureServer) {
	fs := &fixtureServer{}
	pages := map[string]string{
		"/updates":          "updates.html",
		"/updates2.html":    "updates2.html",
		"/models":           "models.html",
		"/models/Bex-White": "model.html",
		"/models/Bad-Dolly": "model.html",
		"/updates3.html":    "updates2.html",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		fs.hits = append(fs.hits, r.URL.Path)
		fs.mu.Unlock()
		name := pages[r.URL.Path]
		if strings.HasPrefix(r.URL.Path, "/free-satin-gallery/") {
			name = "detail.html"
		}
		if name == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(readFixture(t, name))
	}))
	t.Cleanup(srv.Close)
	return srv, fs
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, bool) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	sort.Slice(scenes, func(i, j int) bool { return scenes[i].ID < scenes[j].ID })
	return scenes, stopped
}

func TestFullCatalogue(t *testing.T) {
	srv, fs := newServer(t)
	s := New(sites[0])
	s.base = srv.URL
	s.client = srv.Client()

	const studioURL = "https://www.mysatin.com/"
	scenes, _ := collect(t, s, studioURL, scraper.ListOpts{})
	var ids []string
	for _, sc := range scenes {
		ids = append(ids, sc.ID)
		if sc.StudioURL != studioURL || sc.SiteID != "mysatin" || sc.Studio != "MySatin" {
			t.Errorf("scene %s stamped %q %q %q", sc.ID, sc.StudioURL, sc.SiteID, sc.Studio)
		}
		if !strings.HasPrefix(sc.URL, srv.URL+"/free-satin-gallery/") {
			t.Errorf("scene URL = %q", sc.URL)
		}
	}
	if got := strings.Join(ids, ","); got != "bexredrobe,bexwhitebluenightie3,bexwhitepinknightie2,dollywhitedress2" {
		t.Fatalf("ids = %s", got)
	}
	// Listing dates win over the gallery path of the (shared) detail fixture.
	if d := scenes[0].Date.Format("2006-01-02"); d != "2019-03-01" {
		t.Errorf("bexredrobe date = %s", d)
	}
	if scenes[2].Title != "Pink Satin Nightie" || len(scenes[2].Performers) != 2 {
		t.Errorf("scene = %+v", scenes[2])
	}
	for _, h := range fs.hits {
		if h == "/updates3.html" {
			t.Error("walked past the last updates page")
		}
	}
}

func TestKnownIDStopsAfterPage(t *testing.T) {
	srv, fs := newServer(t)
	s := New(sites[0])
	s.base = srv.URL
	s.client = srv.Client()

	scenes, stopped := collect(t, s, "https://www.mysatin.com/", scraper.ListOpts{
		KnownIDs: map[string]bool{"bexwhitepinknightie2": true},
	})
	if !stopped {
		t.Error("no StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].ID != "dollywhitedress2" {
		t.Errorf("scenes = %+v", scenes)
	}
	for _, h := range fs.hits {
		if h == "/updates2.html" || strings.HasPrefix(h, "/models") {
			t.Errorf("fetched %s after early stop", h)
		}
	}
}

func TestModelMode(t *testing.T) {
	srv, fs := newServer(t)
	s := New(sites[0])
	s.base = srv.URL
	s.client = srv.Client()

	scenes, _ := collect(t, s, "https://www.mysatin.com/models/Bex-White", scraper.ListOpts{})
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, h := range fs.hits {
		if strings.HasPrefix(h, "/updates") || h == "/models" {
			t.Errorf("model mode fetched %s", h)
		}
	}
}

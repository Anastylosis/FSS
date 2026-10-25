package indianaram

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

func sthjConfig() siteConfig {
	for _, c := range sites {
		if c.prefix == "sthj" {
			return c
		}
	}
	panic("no sthj config")
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fixtureServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits []string
}

func newFixtureServer(t *testing.T, files map[string]string) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		fs.hits = append(fs.hits, r.URL.Path)
		fs.mu.Unlock()
		name, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(readFixture(t, name))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func newTestScraper(srv *httptest.Server) *Scraper {
	s := newScraper(sthjConfig())
	s.Client = srv.Client()
	s.base = srv.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error, bool) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return scenes, errs, stopped
}

func catalogueServer(t *testing.T) *fixtureServer {
	return newFixtureServer(t, map[string]string{
		"/sthj_updates.htm":   "sthj_updates.htm",
		"/sthj_archive_1.htm": "sthj_archive_1.htm",
		"/sthj_archive_2.htm": "sthj_archive_2.htm",
	})
}

func TestListScenesWalksUpdatesThenArchives(t *testing.T) {
	srv := catalogueServer(t)
	s := newTestScraper(srv.Server)
	const studioURL = "https://slowteasinghandjobs.com/sthj_main.htm"

	scenes, errs, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := []string{
		"Liam Sucked to an Intense Orgasm",
		"Muscular Tanner Bound and Jerked",
		"Draining Ed - Part 3",
		"Draining Ed - Part 2",
		"Dakota",
		"Justin",
		"Bryce",
	}
	if len(scenes) != len(want) {
		var got []string
		for _, sc := range scenes {
			got = append(got, sc.Title)
		}
		t.Fatalf("got %d scenes %q, want %d", len(scenes), got, len(want))
	}
	for i, sc := range scenes {
		if sc.Title != want[i] {
			t.Errorf("scene %d title = %q, want %q", i, sc.Title, want[i])
		}
		if sc.StudioURL != studioURL {
			t.Errorf("scene %d StudioURL = %q", i, sc.StudioURL)
		}
		if sc.SiteID != "slowteasinghandjobs" || sc.Studio != "Slow Teasing Hand Jobs" {
			t.Errorf("scene %d site/studio = %q/%q", i, sc.SiteID, sc.Studio)
		}
		if !strings.HasPrefix(sc.URL, srv.URL+"/members_sthj/") {
			t.Errorf("scene %d URL = %q", i, sc.URL)
		}
		if sc.Thumbnail == "" {
			t.Errorf("scene %d has no thumbnail", i)
		}
	}

	wantHits := []string{"/sthj_updates.htm", "/sthj_archive_1.htm", "/sthj_archive_2.htm"}
	if strings.Join(srv.hits, ",") != strings.Join(wantHits, ",") {
		t.Errorf("fetched %v, want %v", srv.hits, wantHits)
	}

	liam := scenes[0]
	if liam.ID != "2026 sets/liam sucked to an intense orgasm/liam-sucked-to-an-intense-orgasm" {
		t.Errorf("ID = %q", liam.ID)
	}
	if !liam.Date.Equal(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", liam.Date)
	}
	if want := "Rich had his favorite ginger Liam naked, with his hands bound to his thighs. He knew that Liam hates what he calls “a big production”"; liam.Description != want {
		t.Errorf("description = %q", liam.Description)
	}
	if !strings.HasSuffix(liam.Preview, "/preview-liam-sucked-to-an-intense-orgasm.mp4") {
		t.Errorf("preview = %q", liam.Preview)
	}
	if !strings.HasSuffix(liam.Thumbnail, "/liam-sucked-tp-an-intense-orgasm-collage340.jpg") {
		t.Errorf("thumbnail = %q", liam.Thumbnail)
	}

	// Two parts sharing one members page are told apart by title.
	if scenes[2].ID == scenes[3].ID || !strings.HasSuffix(scenes[2].ID, "#draining-ed-part-3") {
		t.Errorf("multi-part IDs = %q, %q", scenes[2].ID, scenes[3].ID)
	}
	// An undated archive scene gets no date, not its neighbour's.
	if !scenes[4].Date.IsZero() {
		t.Errorf("undated scene got %v", scenes[4].Date)
	}
	if scenes[4].Description != "Rich tied Dakota to a chair & told him he would have to get off with a slow dick massage." {
		t.Errorf("dakota description = %q", scenes[4].Description)
	}
	if !scenes[5].Date.Equal(time.Date(2018, 6, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("justin date = %v", scenes[5].Date)
	}
	if !scenes[6].Date.Equal(time.Date(2017, 10, 27, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("bryce date = %v", scenes[6].Date)
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	srv := catalogueServer(t)
	s := newTestScraper(srv.Server)
	known := map[string]bool{"2026 sets/muscular tanner bound and jerked/muscular-tanner-bound-and-jerked": true}

	scenes, errs, stopped := collect(t, s, "https://slowteasinghandjobs.com/", scraper.ListOpts{KnownIDs: known})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].Title != "Liam Sucked to an Intense Orgasm" {
		t.Errorf("got %+v", scenes)
	}
	if len(srv.hits) != 1 {
		t.Errorf("fetched %v, want only the updates page", srv.hits)
	}
}

func TestListScenesEmptyUpdatesIsParseError(t *testing.T) {
	srv := newFixtureServer(t, map[string]string{"/sthj_updates.htm": "model_aaron.htm"})
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>Down for maintenance</body></html>"))
	})
	s := newTestScraper(srv.Server)
	_, errs, _ := collect(t, s, "https://slowteasinghandjobs.com/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v", errs)
	}
}

func TestModelPage(t *testing.T) {
	const path = "/sthj_directory/model_pages/sthj_directory_aaron.htm"
	srv := newFixtureServer(t, map[string]string{path: "model_aaron.htm"})
	s := newTestScraper(srv.Server)
	studioURL := "https://slowteasinghandjobs.com" + path

	scenes, errs, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := []string{"A Long Edging", "Hip Hop Dancer on the Rack", "Aaron's Audition"}
	if len(scenes) != len(want) {
		t.Fatalf("got %d scenes, want %d", len(scenes), len(want))
	}
	for i, sc := range scenes {
		if sc.Title != want[i] {
			t.Errorf("title %d = %q", i, sc.Title)
		}
		if len(sc.Performers) != 1 || sc.Performers[0] != "Aaron" {
			t.Errorf("performers %d = %v", i, sc.Performers)
		}
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL = %q", sc.StudioURL)
		}
	}
	// Same key as the catalogue listing, despite the ../../ relative link.
	if scenes[0].ID != "2014 sets/long_edging/long_edging" {
		t.Errorf("ID = %q", scenes[0].ID)
	}
	if len(srv.hits) != 1 || srv.hits[0] != path {
		t.Errorf("fetched %v", srv.hits)
	}
}

func TestParseListingStatsBlock(t *testing.T) {
	es := parseListing(string(readFixture(t, "husg_stats.htm")), "https://hardupstraightguys.com/husg_archive01.htm")
	if len(es) != 1 {
		t.Fatalf("got %d entries", len(es))
	}
	e := es[0]
	if e.title != "Gio - Part 1" {
		t.Errorf("title = %q", e.title)
	}
	if len(e.performers) != 1 || e.performers[0] != "Geo" {
		t.Errorf("performers = %v", e.performers)
	}
	if strings.Contains(e.description, "Age") || !strings.HasPrefix(e.description, "Very muscular with a sculpted body") {
		t.Errorf("description = %q", e.description)
	}
	if !e.date.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", e.date)
	}
	if e.id != "2015_updts/wk001_242375_pt1/wk_001_202938_pt1" {
		t.Errorf("id = %q", e.id)
	}
}

func TestParseListingSkipsBoilerplate(t *testing.T) {
	body := `<table><tr><td><a href="members_smit/2013_updts/wk114/wk114_pt1.htm"><img src="a.jpg"></a></td>
<td><p><b>Video in Standard &amp; High Definition</b></p><p>&amp; iPod version - mp4</p>
<p><b>Punks Revenge - Part 1</b></p><p>The young punk never forgot.</p></td></tr>
<tr><td><a href="members_rym/12014Sets/rym_master_with_stream.htm"><img src="b.jpg"></a></td><td><p>---</p></td></tr>
</table><p>All  images  produced are copyrighted</p>`
	es := parseListing(body, "https://straightmenintrouble.com/smit_archive12.htm")
	if len(es) != 1 {
		t.Fatalf("got %d entries: %+v", len(es), es)
	}
	if es[0].title != "Punks Revenge - Part 1" || es[0].description != "The young punk never forgot." {
		t.Errorf("entry = %+v", es[0])
	}
}

func TestDecodeWindows1252(t *testing.T) {
	if got := decode([]byte("can\x92t")); got != "can’t" {
		t.Errorf("decode = %q", got)
	}
	if got := decode([]byte("can’t")); got != "can’t" {
		t.Errorf("utf-8 decode = %q", got)
	}
}

func TestArchiveLinks(t *testing.T) {
	got := archiveLinks(`<a href="smit_archive01.htm">1</a> <a href="smit_archive16.htm">16</a> <a href="smit_updates.htm">u</a>`, "smit")
	if len(got) != 2 || got[1] != "smit_archive01.htm" || got[16] != "smit_archive16.htm" {
		t.Errorf("got %v", got)
	}
}

func TestMatchesURL(t *testing.T) {
	cases := map[string]string{
		"http://slowteasinghandjobs.com/sthj_main.htm":                                        "slowteasinghandjobs",
		"https://www.slowteasinghandjobs.com/":                                                "slowteasinghandjobs",
		"https://slowteasinghandjobs.com/sthj_directory/model_pages/sthj_directory_aaron.htm": "slowteasinghandjobs",
		"https://reluctantyoungmen.com/":                                                      "reluctantyoungmen",
		"https://straightmenintrouble.com/smit_main.htm":                                      "straightmenintrouble",
		"http://hardupstraightguys.com":                                                       "hardupstraightguys",
	}
	for u, id := range cases {
		matched := ""
		for _, cfg := range sites {
			if newScraper(cfg).MatchesURL(u) {
				matched = cfg.id
			}
		}
		if matched != id {
			t.Errorf("%s matched %q, want %q", u, matched, id)
		}
	}
	s := newScraper(sthjConfig())
	for _, u := range []string{"https://slowteasinghandjobs.com.evil.example/", "https://example.com/slowteasinghandjobs.com"} {
		if s.MatchesURL(u) {
			t.Errorf("unexpected match %s", u)
		}
	}
}

package ypptour

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
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

func site(t *testing.T, id string) siteConfig {
	t.Helper()
	for _, c := range sites {
		if c.id == id {
			return c
		}
	}
	t.Fatalf("site not found: %s", id)
	return siteConfig{}
}

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://manpuppy.com/", "manpuppy"},
		{"https://www.manpuppy.com/videos?page=2&sort=newest", "manpuppy"},
		{"https://manpuppy.com/models/16426-richard-lennox", "manpuppy"},
		{"https://vrallure.com", "vrallure"},
		{"http://www.vrallure.com/scenes?page=3", "vrallure"},
		{"https://vrallure.com/models/17822-breezy-bri", "vrallure"},
		{"https://notvrallure.com/", ""},
		{"https://manpuppy.com.example.org/", ""},
		{"https://melina-may.com/", ""},
	}
	for _, c := range cases {
		got := ""
		for _, cfg := range sites {
			if New(cfg).MatchesURL(c.url) {
				if got != "" {
					t.Errorf("%q matched both %s and %s", c.url, got, cfg.id)
				}
				got = cfg.id
			}
		}
		if got != c.want {
			t.Errorf("MatchesURL(%q) matched %q, want %q", c.url, got, c.want)
		}
	}
}

// Domain-keyed config table — see testutil.CheckSiteDomainTable.
func TestSiteTableIntegrity(t *testing.T) {
	rows := make([]testutil.DomainRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.DomainRow{ID: c.id, Domain: c.domain, Studio: c.studio})
		if c.parse == nil || c.scenePath == "" || c.listPath == "" {
			t.Errorf("%s: incomplete config row", c.id)
		}
	}
	testutil.CheckSiteDomainTable(t, rows)
}

func TestModelIDOf(t *testing.T) {
	cases := map[string]string{
		"https://vrallure.com/models/17822-breezy-bri": "17822",
		"https://manpuppy.com/models/16426":            "16426",
		"https://manpuppy.com/models/sort/newest":      "",
		"https://manpuppy.com/videos":                  "",
		"https://vrallure.com/":                        "",
	}
	for in, want := range cases {
		if got := modelIDOf(in); got != want {
			t.Errorf("modelIDOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- parsing ----

func TestParseLiverpool(t *testing.T) {
	d := parseLiverpool(readFixture(t, "manpuppy_detail.html"))

	if d.title != "Danny Bianchi & Richard Lennox" {
		t.Errorf("title = %q", d.title)
	}
	if want := []string{"Richard Lennox", "Danny Bianchi"}; !slices.Equal(d.performers(), want) {
		t.Errorf("performers = %v, want %v", d.performers(), want)
	}
	if !d.features("18073") || d.features("17822") {
		t.Errorf("cast ids = %v", d.cast)
	}
	if len(d.tags) != 12 || d.tags[0] != "Amateur" || !slices.Contains(d.tags, "Cock and Ball Torture") {
		t.Errorf("tags = %v", d.tags)
	}
	if !strings.HasPrefix(d.description, "Flashy brat Danny Bianchi saunters in") ||
		!strings.Contains(d.description, "one of Danny's college professors") ||
		strings.Contains(d.description, "SCENE INFORMATION") {
		t.Errorf("description = %q", d.description)
	}
	if want := "https://cloud-nexpectation.secure.yppcdn.com/mpy/hugethumbs/mpy0588_dannybianchi_richardlennox-c960x540.jpg"; d.thumb != want {
		t.Errorf("thumb = %q, want %q", d.thumb, want)
	}
	// The theme publishes no date and no runtime.
	if !d.date.IsZero() || d.duration != 0 {
		t.Errorf("date = %v, duration = %d; want both unset", d.date, d.duration)
	}
}

func TestParseLiverpoolWithoutTrailer(t *testing.T) {
	d := parseLiverpool([]byte(`<main>
        <div class="row">
                      <img src="//cloud-nexpectation.secure.yppcdn.com/mpy/hugethumbs/mpy0031_richardlennox-c960x540.jpg" class="img-responsive" />
                  </div>
        <div class="row item">
          <div class="col-xs-12 col-md-6">
            <h1>Richard Lennox</h1>`))
	if d.title != "Richard Lennox" {
		t.Errorf("title = %q", d.title)
	}
	if want := "https://cloud-nexpectation.secure.yppcdn.com/mpy/hugethumbs/mpy0031_richardlennox-c960x540.jpg"; d.thumb != want {
		t.Errorf("thumb = %q, want %q", d.thumb, want)
	}
}

func TestParseEastwood(t *testing.T) {
	d := parseEastwood(readFixture(t, "vrallure_detail.html"))

	// The heading is "Breezy Bri : Phone Set Aside"; only the title is kept.
	if d.title != "Phone Set Aside" {
		t.Errorf("title = %q", d.title)
	}
	if want := []string{"Breezy Bri"}; !slices.Equal(d.performers(), want) {
		t.Errorf("performers = %v, want %v", d.performers(), want)
	}
	if !d.features("17822") {
		t.Errorf("cast ids = %v", d.cast)
	}
	if want := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC); !d.date.Equal(want) {
		t.Errorf("date = %v, want %v", d.date, want)
	}
	// "1103.36" seconds.
	if d.duration != 1103 {
		t.Errorf("duration = %d, want 1103", d.duration)
	}
	// The detail page carries the full copy; the listing truncates it.
	if !strings.HasPrefix(d.description, "Breezy Bri sits with bare legs") ||
		!strings.HasSuffix(d.description, "pulls you into every intimate moment.") {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 16 || d.tags[0] != "Solo" || !slices.Contains(d.tags, "P.O.V.") {
		t.Errorf("tags = %v", d.tags)
	}
	if want := "https://cloud-nexpectation.secure.yppcdn.com/vra/hugethumbs/vra0536_breezybri_180-c1138x640.jpg"; d.thumb != want {
		t.Errorf("thumb = %q, want %q", d.thumb, want)
	}
}

func TestParseEastwoodCompilation(t *testing.T) {
	d := parseEastwood(readFixture(t, "vrallure_compilation.html"))

	if d.title != "Sexy Blowjobs Moments Vol.1" {
		t.Errorf("title = %q", d.title)
	}
	perf := d.performers()
	if len(perf) < 8 || perf[0] != "Jazmin Luv" || !slices.Contains(perf, "Anissa Kate") {
		t.Errorf("performers = %v", perf)
	}
	if d.features("17822") {
		t.Error("compilation should not feature model 17822")
	}
	if d.duration != 3588 {
		t.Errorf("duration = %d, want 3588", d.duration)
	}
}

func TestParseEastwoodHeadingWithoutCast(t *testing.T) {
	d := parseEastwood([]byte(`<h1 class="latest-scene-title">
		Title: With A Colon
	</h1>`))
	if d.title != "Title: With A Colon" {
		t.Errorf("title = %q", d.title)
	}
}

// ---- sitemap ----

func TestParseSitemap(t *testing.T) {
	var set urlset
	body := readFixture(t, "vrallure_sitemap.xml")
	if err := parseutil.DecodeXML(body, &set); err != nil {
		t.Fatal(err)
	}
	refs := parseSitemap(set, "scenes")
	var slugs []string
	for _, r := range refs {
		slugs = append(slugs, r.slug)
	}
	want := []string{"vra0537_luluchu_180", "vra0536_breezybri_180", "vra0535_aliciawilliams_180"}
	if !slices.Equal(slugs, want) {
		t.Fatalf("slugs = %v, want %v", slugs, want)
	}
	// "2026-08-11T00:00:00-04:00" is the 11th, not shifted into UTC.
	if want := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC); !refs[1].date.Equal(want) {
		t.Errorf("date = %v, want %v", refs[1].date, want)
	}
}

func TestParseSitemapSortsNewestFirstAndDedupes(t *testing.T) {
	set := urlset{}
	add := func(loc, lastmod string) {
		set.URLs = append(set.URLs, struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		}{loc, lastmod})
	}
	add("https://x.com/video/old", "2021-02-02T00:00:00-05:00")
	add("https://x.com/video/undated", "")
	add("https://x.com/video/new", "2026-09-08T00:00:00-04:00")
	add("https://x.com/video/new/", "2026-09-08T00:00:00-04:00")
	add("https://x.com/models/1-a", "2026-09-09T00:00:00-04:00")
	add("https://x.com/videos", "2026-09-09T00:00:00-04:00")

	var slugs []string
	for _, r := range parseSitemap(set, "video") {
		slugs = append(slugs, r.slug)
	}
	if want := []string{"new", "old", "undated"}; !slices.Equal(slugs, want) {
		t.Errorf("slugs = %v, want %v", slugs, want)
	}
}

// ---- end to end ----

func newTestScraper(t *testing.T, id, sitemapFixture string, detail func(path string) []byte) (*Scraper, string, *atomic.Int32) {
	t.Helper()
	cfg := site(t, id)
	var details atomic.Int32
	srv := testutil.SitemapServerFunc(t, "https://"+cfg.domain, readFixture(t, sitemapFixture), func(path string) []byte {
		details.Add(1)
		return detail(path)
	})
	s := New(cfg)
	s.Client = srv.Client()
	s.base = srv.URL
	return s, srv.URL, &details
}

func TestListScenesManPuppy(t *testing.T) {
	page := readFixture(t, "manpuppy_detail.html")
	s, base, _ := newTestScraper(t, "manpuppy", "manpuppy_sitemap.xml", func(string) []byte { return page })

	ch, err := s.ListScenes(context.Background(), "https://manpuppy.com/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "mpy0588_dannybianchi_richardlennox" || sc.SiteID != "manpuppy" || sc.Studio != "ManPuppy" {
		t.Errorf("identity = %q/%q/%q", sc.ID, sc.SiteID, sc.Studio)
	}
	if want := base + "/video/mpy0588_dannybianchi_richardlennox"; sc.URL != want {
		t.Errorf("URL = %q, want %q", sc.URL, want)
	}
	// ManPuppy's page has no date; it comes from the sitemap.
	if want := time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("date = %v, want %v", sc.Date, want)
	}
	if scenes[3].ID != "mpy0582_richardlennox" {
		t.Errorf("last scene = %q", scenes[3].ID)
	}
	for _, sc := range scenes {
		if !strings.HasPrefix(sc.URL, base) {
			t.Errorf("scene URL %q left the test server", sc.URL)
		}
		testutil.ValidateScene(t, sc)
	}
}

func TestListScenesVRAllurePrefersPageDate(t *testing.T) {
	page := readFixture(t, "vrallure_detail.html")
	s, _, _ := newTestScraper(t, "vrallure", "vrallure_sitemap.xml", func(string) []byte { return page })

	ch, err := s.ListScenes(context.Background(), "https://vrallure.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	// Every fixture scene serves the Aug 11 page, so the first scene — dated
	// Aug 14 in the sitemap — proves the page's own date wins.
	if want := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC); !scenes[0].Date.Equal(want) {
		t.Errorf("date = %v, want %v", scenes[0].Date, want)
	}
}

func TestKnownIDStopsBeforeFetchingDetails(t *testing.T) {
	page := readFixture(t, "manpuppy_detail.html")
	s, _, details := newTestScraper(t, "manpuppy", "manpuppy_sitemap.xml", func(string) []byte { return page })

	opts := scraper.ListOpts{KnownIDs: map[string]bool{"mpy0583_richardlennox": true}}
	ch, err := s.ListScenes(context.Background(), "https://manpuppy.com/", opts)
	if err != nil {
		t.Fatal(err)
	}
	scenes, stopped := testutil.CollectScenesWithStop(t, ch)
	if len(scenes) != 2 || !stopped {
		t.Errorf("got %d scenes, stoppedEarly=%v; want 2, true", len(scenes), stopped)
	}
	if n := details.Load(); n != 2 {
		t.Errorf("fetched %d detail pages, want 2 — known scenes must not be fetched", n)
	}
}

func TestModelURLFiltersTheCatalogue(t *testing.T) {
	breezy := readFixture(t, "vrallure_detail.html")
	other := readFixture(t, "vrallure_compilation.html")
	s, _, _ := newTestScraper(t, "vrallure", "vrallure_sitemap.xml", func(path string) []byte {
		if strings.HasSuffix(path, "/vra0536_breezybri_180") {
			return breezy
		}
		return other
	})

	ch, err := s.ListScenes(context.Background(), "https://vrallure.com/models/17822-breezy-bri", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 1 || scenes[0].ID != "vra0536_breezybri_180" {
		t.Fatalf("scenes = %v, want only vra0536_breezybri_180", ids(scenes))
	}
	if scenes[0].StudioURL != "https://vrallure.com/models/17822-breezy-bri" {
		t.Errorf("StudioURL = %q", scenes[0].StudioURL)
	}
}

func TestUnparseableDetailIsAParseError(t *testing.T) {
	page := readFixture(t, "manpuppy_detail.html")
	s, _, _ := newTestScraper(t, "manpuppy", "manpuppy_sitemap.xml", func(path string) []byte {
		if strings.HasSuffix(path, "/mpy0188_tristansweet_richardlennox") {
			return []byte("<html><body>redesigned</body></html>")
		}
		return page
	})

	ch, err := s.ListScenes(context.Background(), "https://manpuppy.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var (
		scenes []models.Scene
		errs   []error
	)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	if len(scenes) != 3 {
		t.Errorf("got %d scenes, want 3 — one bad page must not end the walk", len(scenes))
	}
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Errorf("errs = %v, want one parse failure", errs)
	}
}

func TestEmptySitemapIsAParseError(t *testing.T) {
	s, _, _ := newTestScraper(t, "vrallure", "vrallure_sitemap.xml", func(string) []byte { return nil })
	// The fixture has no /video/ entries, as if the site renamed its scene path.
	s.cfg.scenePath = "video"

	ch, err := s.ListScenes(context.Background(), "https://vrallure.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for r := range ch {
		if r.Kind == scraper.KindError {
			got = r.Err
		}
	}
	if got == nil || scraper.Classify(got) != scraper.FailureParse {
		t.Errorf("err = %v, want a parse failure", got)
	}
}

func TestCancellable(t *testing.T) {
	page := readFixture(t, "manpuppy_detail.html")
	s, _, _ := newTestScraper(t, "manpuppy", "manpuppy_sitemap.xml", func(string) []byte { return page })
	testutil.AssertCancellable(t, s, "https://manpuppy.com/", scraper.ListOpts{Workers: 1})
}

func ids(scenes []models.Scene) []string {
	var out []string
	for _, sc := range scenes {
		out = append(out, sc.ID)
	}
	return out
}

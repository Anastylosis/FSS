package philflash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://phil-flash.com/",
		"https://phil-flash.com",
		"https://www.phil-flash.com/girls/teen-kasia",
		"https://teenkasia.com/",
		"https://facedownassupuniversity.com",
		"http://karensdreamgirls.com/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{"https://phil-flash-cash.com/", "https://karisweets.com/", "https://example.com/phil-flash.com"} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
	k := NewKariSweets()
	if !k.MatchesURL("https://karisweets.com/") || !k.MatchesURL("https://www.karisweets.com/video/") {
		t.Error("karisweets should match its own host")
	}
	if k.MatchesURL("https://phil-flash.com/") {
		t.Error("karisweets should not match phil-flash.com")
	}
}

func TestResolveTarget(t *testing.T) {
	cases := []struct {
		url    string
		kind   targetKind
		slug   string
		studio string
	}{
		{"https://phil-flash.com/", targetAll, "", ""},
		{"https://phil-flash.com/latest-updates", targetAll, "", ""},
		{"https://phil-flash.com/girls/megan-qt", targetGirl, "megan-qt", ""},
		{"https://phil-flash.com/events/triple-shoot", targetEvent, "triple-shoot", ""},
		{"https://phil-flash.com/fetishes/dancing", targetFetish, "dancing", ""},
		{"https://teenkasia.com/", targetGirl, "teen-kasia", "Teen Kasia"},
		{"https://karensdreamgirls.com", targetFetish, "photographed-by-karen-dreams", "Karen's Dream Girls"},
	}
	for _, c := range cases {
		got := resolveTarget(c.url)
		if got.kind != c.kind || got.slug != c.slug || got.studio != c.studio {
			t.Errorf("%s: got %+v", c.url, got)
		}
	}
}

func TestParseSitemap(t *testing.T) {
	refs := parseSitemap(fixture(t, "sitemap.xml"))
	var ids []string
	for _, r := range refs {
		ids = append(ids, r.id())
	}
	want := []string{
		"triple-shoot/teen-girls-in-shiny-thigh-high-boots",
		"megan-qt/schoolgirl-stripper-video",
		"megan-qt-princess-blueyez/teen-girls-in-platform-high-heels",
		"teen-kasia/blonde-teen-fucks-herself-doggie-style",
		"tiffany-teen/white-scrunched-socks",
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v", ids)
	}
	if refs[0].section != "events" || refs[1].section != "girls" {
		t.Errorf("sections: %+v", refs[:2])
	}
}

func TestGirlRefs(t *testing.T) {
	all := []setRef{
		{section: "girls", portfolio: "rachel", slug: "a", exact: true},
		{section: "girls", portfolio: "jessie-cox-little-rachel", slug: "b", exact: true},
		{section: "girls", portfolio: "megan-qt", slug: "c", exact: true},
		{section: "events", portfolio: "rachel", slug: "d", exact: true},
	}
	got := girlRefs(all, "rachel")
	if len(got) != 2 || !got[0].exact || got[1].exact || got[1].portfolio != "jessie-cox-little-rachel" {
		t.Fatalf("got %+v", got)
	}
	// The joint portfolio is only a candidate: "Little Rachel" is not "rachel".
	if credits([]string{"Jessie Cox", "Little Rachel"}, "rachel") {
		t.Error("Little Rachel must not count as rachel")
	}
	if !credits([]string{"MeganQt", "Princess Blueyez"}, "megan-qt") {
		t.Error("MeganQt should count as megan-qt")
	}
}

func TestParseSetLegacyVideo(t *testing.T) {
	d, err := parseSet(fixture(t, "set_legacy_video.html"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.title != "Bunk Bed Bang" {
		t.Errorf("title = %q", d.title)
	}
	if d.videos != 2 || d.duration != 10*60+22 {
		t.Errorf("videos=%d duration=%d", d.videos, d.duration)
	}
	if !d.date.Equal(time.Date(2018, 9, 3, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", d.date)
	}
	if strings.Join(d.portfolioCast, ",") != "Teen Kasia" || len(d.titleCast) != 0 {
		t.Errorf("cast = %v / %v", d.portfolioCast, d.titleCast)
	}
	if !strings.HasPrefix(d.description, "Watch the pig tailed college teen Kasia") {
		t.Errorf("description = %q", d.description)
	}
	if d.thumbnail != "https://phil-flash.com/assets/content/girls/teen-kasia/024/poster.webp" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
	if d.preview != "https://phil-flash.com/assets/content/girls/teen-kasia/024/024t-540p.mp4" {
		t.Errorf("preview = %q", d.preview)
	}
}

func TestParseSetVideoWithTags(t *testing.T) {
	d, err := parseSet(fixture(t, "set_video.html"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.title != "Megan's Magic Show" {
		t.Errorf("title = %q", d.title)
	}
	if d.videos == 0 || d.duration == 0 {
		t.Errorf("videos=%d duration=%d", d.videos, d.duration)
	}
	if len(d.tags) != 8 || d.tags[0] != "Dancing" || d.tags[7] != "Twerking" {
		t.Errorf("tags = %v", d.tags)
	}
	if strings.Join(d.titleCast, ",") != "MeganQt" {
		t.Errorf("titleCast = %v", d.titleCast)
	}
}

func TestParseSetPhotoOnly(t *testing.T) {
	d, err := parseSet(fixture(t, "set_photo.html"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.videos != 0 || d.duration != 0 {
		t.Errorf("photo set reported video: %+v", d)
	}
	if d.title != "An Angel in White—Scrunched Socks" {
		t.Errorf("title = %q", d.title)
	}
	// The footer's site-wide "about" video is not this set's media.
	if !strings.Contains(d.thumbnail, "/tiffany-teen/white-scrunched-socks/poster/") || d.preview != "" {
		t.Errorf("thumbnail=%q preview=%q", d.thumbnail, d.preview)
	}
}

func TestParseSetDropsServerTimeDate(t *testing.T) {
	// A set with no stored date is rendered with the server's clock.
	now := time.Date(2018, 9, 3, 5, 0, 0, 0, time.UTC)
	d, err := parseSet(fixture(t, "set_legacy_video.html"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !d.date.IsZero() {
		t.Errorf("date = %v, want zero", d.date)
	}
}

func TestParseKariPhotoSetIgnoresNetworkTotals(t *testing.T) {
	_, ok, err := parseKariSet(fixture(t, "kari_photo.html"))
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v: a photo set must be skipped despite the network Videos row", ok, err)
	}
}

func TestParseSetEvent(t *testing.T) {
	d, err := parseSet(fixture(t, "set_event.html"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.title != "Stacked Ass" || d.portfolioName != "Triple Shoot" {
		t.Errorf("title=%q portfolio=%q", d.title, d.portfolioName)
	}
	if len(d.portfolioCast) != 0 {
		t.Errorf("event crumb must not be a performer: %v", d.portfolioCast)
	}
	if strings.Join(d.titleCast, "|") != "MeganQt|Tiffany Teen|Princess Blueyez" {
		t.Errorf("titleCast = %v", d.titleCast)
	}
	if d.duration != 23*60+43 {
		t.Errorf("duration = %d", d.duration)
	}
}

func TestParseSetMissingTitle(t *testing.T) {
	if _, err := parseSet([]byte("<html><body>nothing</body></html>"), time.Now()); err == nil {
		t.Fatal("expected an error for a page with no title")
	}
}

func newTestServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	pages := map[string]string{
		"/sitemap.xml": "sitemap.xml",
		"/events/triple-shoot/teen-girls-in-shiny-thigh-high-boots":          "set_event.html",
		"/girls/megan-qt/schoolgirl-stripper-video":                          "set_video.html",
		"/girls/megan-qt-princess-blueyez/teen-girls-in-platform-high-heels": "set_joint.html",
		"/girls/teen-kasia/blonde-teen-fucks-herself-doggie-style":           "set_legacy_video.html",
		"/girls/tiffany-teen/white-scrunched-socks":                          "set_photo.html",
		"/fetishes/schoolgirl-uniform":                                       "fetish_sets.html",
		"/fetishes/photographed-by-karen-dreams":                             "fetish_portfolios.html",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), ageCookie) {
			t.Errorf("%s fetched without the age cookie", r.URL.Path)
		}
		if hits != nil {
			hits.Add(1)
		}
		name, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fixture(t, name))
	}))
}

func collect(t *testing.T, s scraper.StudioScraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error, bool) {
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
	sort.Slice(scenes, func(i, j int) bool { return scenes[i].ID < scenes[j].ID })
	return scenes, errs, stopped
}

func testScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.client = srv.Client()
	s.base = srv.URL
	return s
}

func ids(scenes []models.Scene) string {
	var out []string
	for _, sc := range scenes {
		out = append(out, sc.ID)
	}
	return strings.Join(out, ",")
}

func TestListScenesAll(t *testing.T) {
	srv := newTestServer(t, nil)
	defer srv.Close()
	const studioURL = "https://phil-flash.com/"
	scenes, errs, _ := collect(t, testScraper(srv), studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	// The photo-only Tiffany Teen set is skipped.
	want := "megan-qt-princess-blueyez/teen-girls-in-platform-high-heels,megan-qt/schoolgirl-stripper-video,teen-kasia/blonde-teen-fucks-herself-doggie-style,triple-shoot/teen-girls-in-shiny-thigh-high-boots"
	if ids(scenes) != want {
		t.Fatalf("ids = %s", ids(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL || sc.SiteID != siteID || sc.Title == "" || sc.ScrapedAt.IsZero() {
			t.Errorf("bad scene %+v", sc)
		}
		if !strings.HasPrefix(sc.URL, siteBase+"/") {
			t.Errorf("url = %s", sc.URL)
		}
	}
	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}
	if sc := byID["teen-kasia/blonde-teen-fucks-herself-doggie-style"]; sc.Studio != "Teen Kasia" {
		t.Errorf("solo portfolio studio = %q", sc.Studio)
	}
	ev := byID["triple-shoot/teen-girls-in-shiny-thigh-high-boots"]
	if ev.Studio != networkName || ev.Series != "Triple Shoot" || len(ev.Performers) != 3 {
		t.Errorf("event scene = %+v", ev)
	}
	joint := byID["megan-qt-princess-blueyez/teen-girls-in-platform-high-heels"]
	if strings.Join(joint.Performers, "|") != "MeganQt|Princess Blueyez" || joint.Studio != networkName {
		t.Errorf("joint scene = %+v", joint)
	}
}

func TestListScenesChildDomainGirl(t *testing.T) {
	srv := newTestServer(t, nil)
	defer srv.Close()
	const studioURL = "https://meganqt.com/"
	scenes, errs, _ := collect(t, testScraper(srv), studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if ids(scenes) != "megan-qt-princess-blueyez/teen-girls-in-platform-high-heels,megan-qt/schoolgirl-stripper-video" {
		t.Fatalf("ids = %s", ids(scenes))
	}
	for _, sc := range scenes {
		if sc.Studio != "MeganQt" || sc.StudioURL != studioURL {
			t.Errorf("scene %s studio=%q studioURL=%q", sc.ID, sc.Studio, sc.StudioURL)
		}
	}
}

func TestListScenesFetishSets(t *testing.T) {
	srv := newTestServer(t, nil)
	defer srv.Close()
	scenes, errs, _ := collect(t, testScraper(srv), "https://facedownassupuniversity.com/", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if ids(scenes) != "megan-qt/schoolgirl-stripper-video,triple-shoot/teen-girls-in-shiny-thigh-high-boots" {
		t.Fatalf("ids = %s", ids(scenes))
	}
	if scenes[0].Studio != "Face Down Ass Up University" {
		t.Errorf("studio = %q", scenes[0].Studio)
	}
}

func TestListScenesFetishPortfolios(t *testing.T) {
	srv := newTestServer(t, nil)
	defer srv.Close()
	scenes, errs, _ := collect(t, testScraper(srv), "https://phil-flash.com/fetishes/photographed-by-karen-dreams", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	// Both linked portfolios expand from the sitemap; Tiffany's set is photos only.
	if ids(scenes) != "teen-kasia/blonde-teen-fucks-herself-doggie-style" {
		t.Fatalf("ids = %s", ids(scenes))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	var hits atomic.Int32
	srv := newTestServer(t, &hits)
	defer srv.Close()
	known := map[string]bool{
		"megan-qt/schoolgirl-stripper-video":                          true,
		"megan-qt-princess-blueyez/teen-girls-in-platform-high-heels": true,
		"tiffany-teen/white-scrunched-socks":                          true,
		"triple-shoot/teen-girls-in-shiny-thigh-high-boots":           true,
	}
	scenes, errs, stopped := collect(t, testScraper(srv), "https://phil-flash.com/", scraper.ListOpts{KnownIDs: known})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if ids(scenes) != "teen-kasia/blonde-teen-fucks-herself-doggie-style" || !stopped {
		t.Fatalf("ids = %s stopped=%v", ids(scenes), stopped)
	}
	if hits.Load() != 2 {
		t.Errorf("expected sitemap + one set fetch, got %d requests", hits.Load())
	}
}

func TestListScenesSitemapError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<urlset></urlset>"))
	}))
	defer srv.Close()
	scenes, errs, _ := collect(t, testScraper(srv), "https://phil-flash.com/", scraper.ListOpts{})
	if len(scenes) != 0 || len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("scenes=%d errs=%v", len(scenes), errs)
	}
}

func TestKariSweets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), ageCookie) {
			t.Errorf("%s fetched without the age cookie", r.URL.Path)
		}
		switch {
		case r.URL.Path == kariListPath:
			_, _ = w.Write(fixture(t, "kari_listing.html"))
		case r.URL.Path == "/latest-updates/261/twerking-on-a-stripper-pole.php":
			_, _ = w.Write(fixture(t, "kari_video.html"))
		case strings.HasPrefix(r.URL.Path, "/latest-updates/"):
			_, _ = w.Write(fixture(t, "kari_photo.html"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := NewKariSweets()
	s.client = srv.Client()
	s.base = srv.URL
	const studioURL = "https://karisweets.com/"
	scenes, errs, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "261" || sc.Title != "Club Twerk" || sc.StudioURL != studioURL || sc.SiteID != kariSiteID {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Duration != 14*60+35 || !sc.Date.Equal(time.Date(2020, 4, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("duration=%d date=%v", sc.Duration, sc.Date)
	}
	if strings.Join(sc.Performers, ",") != "Kari Sweets" || sc.Studio != kariStudio {
		t.Errorf("performers=%v studio=%q", sc.Performers, sc.Studio)
	}
	if sc.URL != "https://karisweets.com/latest-updates/261/twerking-on-a-stripper-pole.php" {
		t.Errorf("url = %s", sc.URL)
	}
	if sc.Thumbnail != "https://phil-flash.com/assets/content/girls/kari-sweets/261/poster.jpg" ||
		!strings.HasSuffix(sc.Preview, "261t-540p.mp4") || sc.Description == "" {
		t.Errorf("media/description: %+v", sc)
	}
}

func TestSplitCast(t *testing.T) {
	for in, want := range map[string]string{
		"MeganQt And Princess Blueyez":              "MeganQt|Princess Blueyez",
		"DawnAvril, Janessa Brazila, and Mia Heart": "DawnAvril|Janessa Brazila|Mia Heart",
		"Brandy&#39;s Box":                          "Brandy's Box",
	} {
		if got := strings.Join(splitCast(in), "|"); got != want {
			t.Errorf("splitCast(%q) = %q", in, got)
		}
	}
	if got := slugTitle("homemade-schoolgirl-uniform"); got != "Homemade Schoolgirl Uniform" {
		t.Errorf("slugTitle = %q", got)
	}
}

func TestKariTitleDropsSubtitle(t *testing.T) {
	page := []byte(`<h1 class="card-title h3 text-center"> The Graveyard<br><small class="">Cute and Creepy</small><br><small class="text-muted">Kari Sweets</small> </h1>` +
		`<h5 class="card-title">Set Data</h5><table><tr><td>Videos</td><td>1</td></tr></table>`)
	sc, ok, err := parseKariSet(page)
	if err != nil || !ok || sc.Title != "The Graveyard" {
		t.Fatalf("title=%q ok=%v err=%v", sc.Title, ok, err)
	}
}

func TestListScenesSitemapDeadPageIsAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			_, _ = w.Write([]byte(`<urlset><url><loc>https://phil-flash.com/girls/kendall-blaze/gone</loc></url></urlset>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, errs, _ := collect(t, testScraper(srv), "https://phil-flash.com/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureAbsent {
		t.Fatalf("errs = %v", errs)
	}
}

package erikalust

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func cfgFor(t *testing.T, id string) SiteConfig {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			return c
		}
	}
	t.Fatalf("no site %q", id)
	return SiteConfig{}
}

func TestSiteCount(t *testing.T) {
	if len(sites) != 3 {
		t.Errorf("expected 3 storefronts, got %d", len(sites))
	}
	seen := map[string]bool{}
	for _, c := range sites {
		if seen[c.SiteID] {
			t.Errorf("duplicate SiteID: %s", c.SiteID)
		}
		seen[c.SiteID] = true
	}
}

func TestMatchesURL(t *testing.T) {
	xc := New(cfgFor(t, "xconfessions"))
	for _, u := range []string{
		"https://xconfessions.com",
		"https://xconfessions.com/",
		"https://www.xconfessions.com/film/my-pregnant-desire",
		"http://xconfessions.com/categories/romance",
	} {
		if !xc.MatchesURL(u) {
			t.Errorf("expected match: %s", u)
		}
	}
	// Sibling storefronts share a platform but not a scraper — each keys its
	// own studio, so they must not answer for each other.
	for _, u := range []string{
		"https://erikalust.com/",
		"https://lustcinema.com/movies/bike-smut",
		"https://notxconfessions.com/film/x",
		"https://xconfessions.com.evil.test/",
	} {
		if xc.MatchesURL(u) {
			t.Errorf("expected no match: %s", u)
		}
	}
	if lc := New(cfgFor(t, "lustcinema")); !lc.MatchesURL("https://lustcinema.com/movies/bike-smut") {
		t.Error("lustcinema should match its own movie URL")
	}
}

// The sitemaps mix scenes with performers, directors and confessions, and each
// scene carries `/watch/...` sub-pages for trailers and chapters.
func TestScenePath(t *testing.T) {
	xc := New(cfgFor(t, "xconfessions"))
	cases := map[string]string{
		"https://xconfessions.com/film/my-pregnant-desire": "/film/my-pregnant-desire",
		"https://xconfessions.com/film/bike-smut/":         "/film/bike-smut",
		"/film/relative": "/film/relative",
		"https://xconfessions.com/performers/diosa-mor":        "",
		"https://xconfessions.com/confessions/my-pregnant":     "",
		"https://xconfessions.com/collaborators/directors/x":   "",
		"https://xconfessions.com/film/lipstick/watch/bhs/214": "",
		"https://xconfessions.com/":                            "",
		"https://xconfessions.com/movies/bike-smut":            "",
	}
	for in, want := range cases {
		got, ok := xc.scenePath(in)
		if want == "" {
			if ok {
				t.Errorf("scenePath(%q) = %q, want not a scene", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("scenePath(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}

	// Lust Cinema uses a different segment for the same thing.
	lc := New(cfgFor(t, "lustcinema"))
	if got, ok := lc.scenePath("https://lustcinema.com/movies/bike-smut"); !ok || got != "/movies/bike-smut" {
		t.Errorf("lustcinema scenePath = %q,%v", got, ok)
	}
	if _, ok := lc.scenePath("https://lustcinema.com/film/bike-smut"); ok {
		t.Error("lustcinema should not accept /film/")
	}
}

// `/sitemap.xml` is gzip served without a `.gz` extension or a
// `Content-Encoding` header, so nothing upstream unwraps it.
func TestMaybeGunzip(t *testing.T) {
	if got := string(maybeGunzip([]byte("<urlset/>"))); got != "<urlset/>" {
		t.Errorf("plain body was altered: %q", got)
	}
	if got := string(maybeGunzip(gz("<urlset/>"))); got != "<urlset/>" {
		t.Errorf("gzip body not decompressed: %q", got)
	}
	// A body that starts like gzip but is not must come back untouched rather
	// than empty — reading it verbatim beats losing the page.
	broken := append([]byte{0x1f, 0x8b}, "not really gzip"...)
	if got := maybeGunzip(broken); !bytes.Equal(got, broken) {
		t.Errorf("undecodable body was not passed through: %q", got)
	}
}

func TestParseFilmTemplate(t *testing.T) {
	d := parseScene(filmPage, "XConfessions")

	if d.title != "My Pregnant Desire" {
		t.Errorf("title = %q", d.title)
	}
	if !strings.HasPrefix(d.description, "Nobody told her pregnancy") {
		t.Errorf("description = %q", d.description)
	}
	if d.director != "Diosa Mor" {
		t.Errorf("director = %q", d.director)
	}
	if want := []string{"Real Couples", "Romance", "Heterosexual"}; !eq(d.categories, want) {
		t.Errorf("categories = %v, want %v", d.categories, want)
	}
	// The payload's `length` is exact; the page rounds it to "12 min".
	if d.duration != 755 {
		t.Errorf("duration = %d, want 755 (00:12:35, not the rounded 12 min)", d.duration)
	}
	if got := d.date.Format("2006-01-02"); got != "2026-08-13" {
		t.Errorf("date = %s", got)
	}
	// The first <img> on the page is the site logo; the poster comes from the
	// payload, with its escaped slashes undone.
	if d.thumb != "https://img.xconfessions.com/poster.jpg?auto=compress" {
		t.Errorf("thumb = %q", d.thumb)
	}
	if want := []string{"Diosa Mor"}; !eq(d.performers, want) {
		t.Errorf("performers = %v, want %v", d.performers, want)
	}
}

// Lust Cinema renders no <h1>, links its cast by image, and blurs the synopsis
// rather than withholding it.
func TestParseMovieTemplate(t *testing.T) {
	d := parseScene(moviePage, "Lust Cinema")

	if d.title != "CUM WITH ME (A Month in the Life) Ep.1" {
		t.Errorf("title = %q", d.title)
	}
	if !strings.HasPrefix(d.description, "What does a month") {
		t.Errorf("description = %q", d.description)
	}
	if d.director != "King Noire" {
		t.Errorf("director = %q", d.director)
	}
	if d.duration != 920 {
		t.Errorf("duration = %d, want 920", d.duration)
	}
	if got := d.date.Format("2006-01-02"); got != "2026-03-05" {
		t.Errorf("date = %s", got)
	}
	// The site lists a duo entity next to both of its members.
	if want := []string{"King Noire", "Jet Setting Jasmine"}; !eq(d.performers, want) {
		t.Errorf("performers = %v, want %v", d.performers, want)
	}
}

// An empty placeholder block precedes the real synopsis on both templates.
func TestParseSceneSkipsEmptyDescriptionBlock(t *testing.T) {
	page := strings.Replace(filmPage, `<div class="description-block">`,
		`<div class="description-block"></div><div class="description-block">`, 1)
	if d := parseScene(page, "XConfessions"); !strings.HasPrefix(d.description, "Nobody told her") {
		t.Errorf("description = %q", d.description)
	}
}

// Films without linked cast name them in the line under the title.
func TestParseSceneCastFallback(t *testing.T) {
	page := strings.Replace(filmPage,
		`<a href="/performers/diosa-mor" class="x"> Diosa Mor </a>`, "", 1)
	d := parseScene(page, "XConfessions")
	if want := []string{"Emma C", "Agatha Vega"}; !eq(d.performers, want) {
		t.Errorf("performers = %v, want %v", d.performers, want)
	}
}

// A stage name containing "&" must survive; only a duo whose every member is
// separately credited is dropped.
func TestDropCompositeCredits(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{
			[]string{"Jet Setting Jasmine & King Noire", "King Noire", "Jet Setting Jasmine"},
			[]string{"King Noire", "Jet Setting Jasmine"},
		},
		{
			// Only one half is credited: this is a name, not a duo entity.
			[]string{"Sam & Max", "Sam", "Someone Else"},
			[]string{"Sam & Max", "Sam", "Someone Else"},
		},
		{[]string{"Sam & Max", "Sam"}, []string{"Sam & Max", "Sam"}},
		{[]string{"Diosa Mor"}, []string{"Diosa Mor"}},
	}
	for _, c := range cases {
		if got := dropCompositeCredits(append([]string(nil), c.in...)); !eq(got, c.want) {
			t.Errorf("dropCompositeCredits(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The release date is lifted from a minified payload by shape, not by name, so
// the rendered year is the guard that keeps that safe.
func TestReleaseDate(t *testing.T) {
	const payload = `x="2026-08-13 00:00:00";`
	if got := releaseDate(payload, "2026").Format("2006-01-02"); got != "2026-08-13" {
		t.Errorf("matching year: got %s", got)
	}
	if got := releaseDate(payload, ""); got.IsZero() {
		t.Error("no rendered year: the literal should still be accepted")
	}
	if got := releaseDate(payload, "2019"); !got.IsZero() {
		t.Errorf("mismatched year: got %s, want no date", got)
	}
	if got := releaseDate("no dates here", "2026"); !got.IsZero() {
		t.Errorf("no literal: got %s, want no date", got)
	}
}

// docTitle strips only the storefront's own trailing name — the site spells it
// without a space — and leaves a scene title that contains a dash alone.
func TestDocTitle(t *testing.T) {
	cases := []struct{ raw, studio, want string }{
		{"Panties On —  LustCinema", "Lust Cinema", "Panties On"},
		{"Sex Ed - Part Two —  LustCinema", "Lust Cinema", "Sex Ed - Part Two"},
		{"Something Unbranded", "Lust Cinema", "Something Unbranded"},
	}
	for _, c := range cases {
		if got := docTitle(c.raw, c.studio); got != c.want {
			t.Errorf("docTitle(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestParseSceneMissingPayload(t *testing.T) {
	// Strip the payload: the rounded minutes on the page are the fallback, and
	// the date goes away with it.
	page := scriptRe.ReplaceAllString(filmPage, "")
	d := parseScene(page, "XConfessions")
	if d.duration != 12*60 {
		t.Errorf("duration = %d, want the rounded 720", d.duration)
	}
	if !d.date.IsZero() {
		t.Errorf("date = %s, want none without the payload", d.date)
	}
	if d.title != "My Pregnant Desire" {
		t.Errorf("title = %q", d.title)
	}
}

func TestListScenesEndToEnd(t *testing.T) {
	srv := newTestSite(t)
	defer srv.Close()

	s := newTestScraper(t, "xconfessions", srv)
	scenes, errs := collect(t, s, srv.URL+"/", scraper.ListOpts{Workers: 2})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (performers, confessions and /watch/ sub-pages are not scenes)", len(scenes))
	}

	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}
	got, ok := byID["my-pregnant-desire"]
	if !ok {
		t.Fatalf("missing scene, got %v", byID)
	}
	if got.SiteID != "xconfessions" || got.Studio != "XConfessions" {
		t.Errorf("siteID/studio = %q/%q", got.SiteID, got.Studio)
	}
	if got.URL != srv.URL+"/film/my-pregnant-desire" {
		t.Errorf("URL = %q, want the test server (the sitemap's live host must be rebased)", got.URL)
	}
	if got.Duration != 755 || got.Title != "My Pregnant Desire" {
		t.Errorf("scene = %+v", got)
	}
}

// A sitemap that lists no scenes (xconfessions.com, 2026-11) falls back to the
// paginated listing, which ends at the first page adding nothing new.
func TestListScenesListingFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<urlset><url><loc>https://xconfessions.com</loc></url></urlset>`)
	})
	mux.HandleFunc("/film", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = fmt.Fprint(w, `<a href="/film/my-pregnant-desire">x</a><a href="/film/my-pregnant-desire/watch/t/1">t</a><a href="/categories/romance">c</a>`)
		case "2":
			_, _ = fmt.Fprint(w, `<a href="/film/bike-smut">x</a><a href="/film/my-pregnant-desire">x</a>`)
		default:
			_, _ = fmt.Fprint(w, `<a href="/film/bike-smut">x</a>`)
		}
	})
	mux.HandleFunc("/film/my-pregnant-desire", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, filmPage)
	})
	mux.HandleFunc("/film/bike-smut", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.ReplaceAll(filmPage, "My Pregnant Desire", "Bike Smut"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	scenes, errs := collect(t, newTestScraper(t, "xconfessions", srv), srv.URL+"/", scraper.ListOpts{Workers: 2})
	if len(errs) != 0 || len(scenes) != 2 {
		t.Fatalf("got %d scenes, errors %v; want 2 scenes", len(scenes), errs)
	}
}

// Pointing at one scene must not read the sitemaps at all.
func TestListScenesSingleScene(t *testing.T) {
	srv := newTestSite(t)
	defer srv.Close()

	s := newTestScraper(t, "xconfessions", srv)
	scenes, errs := collect(t, s, srv.URL+"/film/my-pregnant-desire", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 || scenes[0].ID != "my-pregnant-desire" {
		t.Fatalf("got %+v", scenes)
	}
}

// A scene page that no longer parses must be reported, not silently dropped —
// an authoritative Save would otherwise delete the scene.
func TestUnparseableSceneIsReported(t *testing.T) {
	srv := newTestSite(t)
	defer srv.Close()

	s := newTestScraper(t, "xconfessions", srv)
	scenes, errs := collect(t, s, srv.URL+"/film/broken", scraper.ListOpts{})
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes, want none", len(scenes))
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified %v, want FailureParse", k)
	}
}

// ---- helpers ----

func newTestScraper(t *testing.T, id string, srv *httptest.Server) *Scraper {
	t.Helper()
	s := New(cfgFor(t, id))
	s.baseOverride = srv.URL
	s.Client = srv.Client()
	return s
}

func collect(t *testing.T, s *Scraper, u string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ch, err := s.ListScenes(ctx, u, opts)
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

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func gz(s string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return buf.Bytes()
}

func newTestSite(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Both the index and its children are gzip, served with no Content-Encoding.
	serveGz := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(gz(body))
		}
	}
	mux.HandleFunc("/sitemap.xml", serveGz(`<?xml version="1.0"?><sitemapindex>
<sitemap><loc>https://xconfessions.com/generic1.xml.gz</loc></sitemap>
<sitemap><loc>https://xconfessions.com/generic2.xml.gz</loc></sitemap>
<sitemap><loc>https://xconfessions.com/missing.xml.gz</loc></sitemap>
</sitemapindex>`))
	mux.HandleFunc("/generic1.xml.gz", serveGz(`<urlset>
<url><loc>https://xconfessions.com/film/my-pregnant-desire</loc></url>
<url><loc>https://xconfessions.com/film/my-pregnant-desire/watch/trailer/12</loc></url>
<url><loc>https://xconfessions.com/performers/diosa-mor</loc></url>
<url><loc>https://xconfessions.com/confessions/my-pregnant-desire</loc></url>
</urlset>`))
	mux.HandleFunc("/generic2.xml.gz", serveGz(`<urlset>
<url><loc>https://xconfessions.com/film/bike-smut</loc></url>
<url><loc>https://xconfessions.com/collaborators/directors/erika-lust</loc></url>
<url><loc>https://xconfessions.com/film/my-pregnant-desire</loc></url>
</urlset>`))

	mux.HandleFunc("/film/my-pregnant-desire", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, filmPage)
	})
	mux.HandleFunc("/film/bike-smut", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.ReplaceAll(filmPage, "My Pregnant Desire", "Bike Smut"))
	})
	mux.HandleFunc("/film/broken", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>redesigned, no scene block</body></html>")
	})
	// A child sitemap that 404s must not take the rest of the catalogue with it.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	})
	return httptest.NewServer(mux)
}

const filmPage = `<!doctype html><html><head>
<title>XConfessions | Watch My Pregnant Desire</title>
<script>window.__NUXT__=(function(a,b,c){t.length="00:12:35";t.release_date=N;` +
	`t.poster_picture="https://img.xconfessions.com/poster.jpg?auto=compress";` +
	`return {x:"2026-08-13 00:00:00"}}(void 0,null,!1));</script>
</head><body>
<img src="https://img.xconfessions.com/logo.svg">
<h1 class="hidden leading-tight xl:block movie-details-title">My Pregnant Desire</h1></div>
<div class="flex"><p class="text-lg">Emma C, Agatha Vega</p>
<div class="flex"><a href="/categories/real-couples" class="mr-1">Real Couples</a><a href="/categories/romance" class="mr-1">Romance</a><a href="/categories/heterosexual" class="mr-1">Heterosexual</a></div></div>
<div class="flex items-center text-sm gap-x-4"><div><span class="uppercase">movie</span></div>
<div><span class="uppercase">12 min</span></div> <span class="text-neutral-30">2026</span></div>
<p><a href="/performers/diosa-mor" class="x"> Diosa Mor </a></p>
<div class="description-block"><p>Nobody told her pregnancy could make her this horny.</p><p>A personal erotic short.</p></div>
<p><span class="text-neutral-30">Director:</span> <span><a href="/collaborators/directors/diosa-mor" class="hover:text-link"> Diosa Mor </a></span></p>
</body></html>`

const moviePage = `<!doctype html><html><head>
<title>CUM WITH ME (A Month in the Life) Ep.1 —  LustCinema</title>
<script>window.__NUXT__=(function(a,b,c){length:"00:15:20",performers:[{name:"Jet Setting Jasmine & King Noire"}],` +
	`cover_picture:"https://img.lustcinema.com/cover.jpg";return {r:"2026-03-05 00:00:00"}}(void 0,null,!1));</script>
</head><body>
<img src="https://img.lustcinema.com/logo.svg">
<div id="detailsContainer"><p class="mb-4 text-base text-neutral"> 2026 </p></div>
<div class="text-sm blur-sm tablet:text-base"> What does a month in the life of a porn performer really look like? </div>
<div class="text-sm description-block tablet:text-base"></div>
<h3 class="secondary-heading">Cast &amp; Crew</h3>
<a href="/directors/king-noire" title="King Noire" class="w-full"><img src="x"></a>
<a href="/cast/jasmine-king-noire" title="Jet Setting Jasmine &amp; King Noire "><img src="x"></a>
<a href="/cast/king-noire" title="King Noire"><img src="x"></a>
<a href="/cast/jet-setting-jasmine" title="Jet Setting Jasmine"><img src="x"></a>
</body></html>`

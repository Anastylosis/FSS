package beefcakehunter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://beefcakehunter.com/home/", true},
		{"https://beefcakehunter.com", true},
		{"https://www.beefcakehunter.com/video-category/arturo/", true},
		{"https://beefcakehunter.com/video-category/keywords/feet/", true},
		{"https://beefcakehunterx.com/", false},
		{"https://example.com/beefcakehunter.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestPageURL(t *testing.T) {
	cases := []struct {
		studio string
		page   int
		want   string
	}{
		{"https://beefcakehunter.com/home/", 1, siteBase + "/?post_type=video"},
		{"https://beefcakehunter.com/home/", 3, siteBase + "/page/3/?post_type=video"},
		{"https://beefcakehunter.com/video-category/arturo/", 1, siteBase + "/video-category/arturo/"},
		{"https://beefcakehunter.com/video-category/keywords/feet", 2, siteBase + "/video-category/keywords/feet/page/2/"},
	}
	for _, c := range cases {
		if got := pageURL(c.studio, c.page); got != c.want {
			t.Errorf("pageURL(%q, %d) = %q, want %q", c.studio, c.page, got, c.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	var last time.Time
	items := parseListing(fixture(t, "listing1.html"), &last)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	it := items[1]
	if it.id != "341" || it.url != "https://beefcakehunter.com/video/8-of-sweet-cock-for-the-holidays/" {
		t.Errorf("item = %+v", it)
	}
	if it.title != "8″ of sweet cock for the holidays .." {
		t.Errorf("title = %q", it.title)
	}
	// A blank date is the_date() suppressing a repeat: it inherits the previous card's.
	if want := time.Date(2014, 3, 28, 0, 0, 0, 0, time.UTC); !it.date.Equal(want) {
		t.Errorf("date = %v, want %v", it.date, want)
	}
	if strings.Contains(it.excerpt, "Read More") || it.excerpt == "" {
		t.Errorf("excerpt = %q", it.excerpt)
	}
	if !hasNextPage(fixture(t, "listing1.html")) || hasNextPage(fixture(t, "listing2.html")) {
		t.Error("hasNextPage wrong")
	}

	// The carry crosses the page boundary.
	items = parseListing(fixture(t, "listing2.html"), &last)
	if want := time.Date(2014, 3, 26, 0, 0, 0, 0, time.UTC); len(items) == 0 || !items[0].date.Equal(want) {
		t.Errorf("page 2 first date = %v, want %v", items[0].date, want)
	}
}

func TestClassify(t *testing.T) {
	perf, tags := classify([]string{"arturo", "face-fucking", "keywords", "hairy", "hairy-2", "michael-s", "bbc-keywords"})
	if !slices.Equal(perf, []string{"Arturo", "Michael S"}) {
		t.Errorf("performers = %v", perf)
	}
	if !slices.Equal(tags, []string{"Face Fucking", "Hairy", "BBC"}) {
		t.Errorf("tags = %v", tags)
	}
}

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, fixture(t, "detail.html"))
	if sc.Title != "Thick dick Arturo" {
		t.Errorf("title = %q", sc.Title)
	}
	if want := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Thumbnail != "https://beefcakehunter.com/wp-content/uploads/ARTURO-1.1.00_16_03_15.Still2417.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if !strings.HasSuffix(sc.Preview, "ARTUROPreview.mp4") {
		t.Errorf("preview = %q", sc.Preview)
	}
	if !strings.HasPrefix(sc.Description, "Thick dick Arturo, a new Beefcake, has arrived") ||
		!strings.HasSuffix(sc.Description, "land, Beefcake Arturo!") || strings.Contains(sc.Description, "<") {
		t.Errorf("description = %q", sc.Description)
	}
}

func newServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case r.URL.Path == "/" && r.URL.Query().Get("post_type") == "video":
			_, _ = w.Write([]byte(fixture(t, "listing1.html")))
		case r.URL.Path == "/page/2/":
			_, _ = w.Write([]byte(fixture(t, "listing2.html")))
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = w.Write([]byte(fixture(t, "detail.html")))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })
	return srv
}

// The fixture cards link to the live host, so detail URLs are rewritten to the
// test server through a transport that redirects every request there.
type rewrite struct {
	target string
	rt     http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(r.target, "http://")
	return r.rt.RoundTrip(req)
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

func TestListScenes(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, &hits)
	s := New()
	s.Client = &http.Client{Transport: rewrite{target: srv.URL, rt: http.DefaultTransport}}

	const studio = "https://beefcakehunter.com/home/"
	scenes, errs, _ := collect(t, s, studio, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != studio || sc.SiteID != siteID || sc.Studio != studioName {
			t.Errorf("scene %s: studio fields %q %q %q", sc.ID, sc.StudioURL, sc.SiteID, sc.Studio)
		}
		// Every detail request serves the same fixture.
		if sc.Title != "Thick dick Arturo" || sc.Preview == "" {
			t.Errorf("scene %s not enriched: %+v", sc.ID, sc)
		}
	}
	if got := scenes[0]; got.ID != "306" || len(got.Performers) == 0 {
		t.Errorf("first scene = %+v", got)
	}
	// 2 listing pages + 5 details.
	if n := hits.Load(); n != 7 {
		t.Errorf("requests = %d, want 7", n)
	}
}

func TestListScenesKnownIDsSkipsDetail(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, &hits)
	s := New()
	s.Client = &http.Client{Transport: rewrite{target: srv.URL, rt: http.DefaultTransport}}

	scenes, errs, stopped := collect(t, s, "https://beefcakehunter.com/home/",
		scraper.ListOpts{KnownIDs: map[string]bool{"341": true}})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !stopped || len(scenes) != 2 {
		t.Fatalf("stopped=%v scenes=%d, want early stop with 2", stopped, len(scenes))
	}
	// 1 listing page + 2 details; the known scene's detail is not fetched.
	if n := hits.Load(); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

func TestListScenesDetailErrorReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(fixture(t, "listing2.html")))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })

	s := New()
	s.Client = &http.Client{Transport: rewrite{target: srv.URL, rt: http.DefaultTransport}}
	scenes, errs, _ := collect(t, s, "https://beefcakehunter.com/", scraper.ListOpts{})
	if len(scenes) != 2 || len(errs) != 2 {
		t.Fatalf("scenes=%d errs=%d, want 2 and 2", len(scenes), len(errs))
	}
	if scenes[0].Description == "" {
		t.Error("listing excerpt should survive a failed detail fetch")
	}
}

func TestEmptyFirstPageIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	t.Cleanup(srv.Close)
	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })

	s := New()
	s.Client = srv.Client()
	_, errs, _ := collect(t, s, "https://beefcakehunter.com/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v", errs)
	}
}

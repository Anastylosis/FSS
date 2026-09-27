package arx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
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

func siteByID(t *testing.T, id string) siteConfig {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			return c
		}
	}
	t.Fatalf("no site %q", id)
	return siteConfig{}
}

func TestSites(t *testing.T) {
	if len(sites) != 10 {
		t.Fatalf("got %d sites, want 10", len(sites))
	}
	ids := map[string]bool{}
	for _, c := range sites {
		if c.SiteID == "" || c.Domain == "" || c.StudioName == "" {
			t.Errorf("incomplete config: %+v", c)
		}
		if ids[c.SiteID] {
			t.Errorf("duplicate SiteID %q", c.SiteID)
		}
		ids[c.SiteID] = true
	}
}

func TestMatchesURL(t *testing.T) {
	s := newScraper(siteByID(t, "honeytrans"))
	cases := []struct {
		url   string
		match bool
	}{
		{"https://honeytrans.com/", true},
		{"https://www.honeytrans.com/scenes", true},
		{"http://honeytrans.com/scenes/1310/x", true},
		{"https://japanlust.com/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestNoCrossMatching(t *testing.T) {
	for _, a := range sites {
		s := newScraper(a)
		for _, b := range sites {
			if a.SiteID == b.SiteID {
				continue
			}
			if u := "https://" + b.Domain + "/"; s.MatchesURL(u) {
				t.Errorf("%s wrongly matches %s", a.SiteID, u)
			}
		}
	}
}

// ---- sitemap ----

// The sitemap lists model and category pages alongside scenes; only the scene
// entries are enumerated.
func TestFetchSitemapKeepsOnlyScenes(t *testing.T) {
	sm := readFixture(t, "sitemap.xml")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(sm)
	}))
	defer srv.Close()

	s := newScraper(siteByID(t, "honeytrans"))
	s.Client = srv.Client()
	s.base = srv.URL

	refs, err := s.fetchSitemap(context.Background())
	if err != nil {
		t.Fatalf("fetchSitemap: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3 (the model entry must be dropped)", len(refs))
	}
	if refs[0].id != "1310" {
		t.Errorf("refs[0].id = %q, want 1310", refs[0].id)
	}
	for _, r := range refs {
		if !strings.Contains(r.url, "/scenes/") {
			t.Errorf("non-scene url kept: %q", r.url)
		}
	}
}

func TestFetchSitemapDedupes(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>https://honeytrans.com/scenes/1/a</loc></url>
<url><loc>https://honeytrans.com/scenes/1/a</loc></url>
<url><loc>https://honeytrans.com/scenes/2/b</loc></url>
</urlset>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	s := newScraper(siteByID(t, "honeytrans"))
	s.Client = srv.Client()
	s.base = srv.URL

	refs, err := s.fetchSitemap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Errorf("got %d refs, want 2", len(refs))
	}
}

// ---- detail ----

func newDetailScraper(t *testing.T, body []byte) (*Scraper, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	s := newScraper(siteByID(t, "honeytrans"))
	s.Client = srv.Client()
	return s, srv
}

func TestToScene(t *testing.T) {
	s, srv := newDetailScraper(t, readFixture(t, "detail.html"))
	now := time.Now().UTC()

	sc, ok := s.toScene(context.Background(), "https://honeytrans.com", sceneRef{id: "1310", url: srv.URL + "/scenes/1310/x"}, now)
	if !ok {
		t.Fatal("toScene returned not-ok")
	}

	if sc.ID != "1310" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "honeytrans" || sc.Studio != "Honey Trans" {
		t.Errorf("SiteID = %q, Studio = %q", sc.SiteID, sc.Studio)
	}
	if sc.Title != "Latina Transsexual Returns To Toy Ass And Stroke Cock" {
		t.Errorf("Title = %q", sc.Title)
	}
	// Entities must be decoded — og:description carries &#x27;.
	if strings.Contains(sc.Description, "&#") {
		t.Errorf("Description still holds entities: %q", sc.Description)
	}
	want := time.Date(2021, time.January, 8, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if !slices.Equal(sc.Performers, []string{"Guilhermina Johansen"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
	wantCats := []string{"Shemale Latina", "Shemale Lingerie", "Shemale Masturbation", "Shemale Toys"}
	if !slices.Equal(sc.Categories, wantCats) {
		t.Errorf("Categories = %v, want %v", sc.Categories, wantCats)
	}
}

// The page renders a related-scenes rail whose cards carry their own
// /models/ and /categories/ links. Reading those document-wide would attribute
// other scenes' casts to this one.
func TestToSceneExcludesRelatedRail(t *testing.T) {
	detail := `<html><head><meta property="og:title" content="The Scene" /></head><body>
<h1 class="tracking-tight">THE SCENE</h1><span>Jan 8, 2021</span>
<div class="grid grid-cols-[100px_1fr]"><span class="font-semibold">Models:</span><div>
  <a href="/models/1/real-performer"><div class="chip"><span>Real Performer</span></div></a>
</div></div>
<div class="related-rail">
  <a class="text-default-600" href="/models/2/other-performer"><h3>Other Performer</h3></a>
  <a class="text-default-600" href="/categories/other-category"><h3>Other</h3></a>
</div></body></html>`

	s, srv := newDetailScraper(t, []byte(detail))
	sc, ok := s.toScene(context.Background(), "x", sceneRef{id: "9", url: srv.URL + "/scenes/9/x"}, time.Now())
	if !ok {
		t.Fatal("toScene returned not-ok")
	}
	if slices.Contains(sc.Performers, "Other Performer") {
		t.Errorf("Performers = %v; the related rail leaked in", sc.Performers)
	}
	if !slices.Equal(sc.Performers, []string{"Real Performer"}) {
		t.Errorf("Performers = %v, want [Real Performer]", sc.Performers)
	}
}

// A page with no og:title is not a scene.
func TestToSceneDropsUntitledPage(t *testing.T) {
	s, srv := newDetailScraper(t, []byte(`<html><body>nothing</body></html>`))
	if _, ok := s.toScene(context.Background(), "x", sceneRef{id: "1", url: srv.URL + "/scenes/1/x"}, time.Now()); ok {
		t.Error("a page with no og:title should be dropped")
	}
}

func TestTitleCaseSlug(t *testing.T) {
	cases := map[string]string{
		"guilhermina-johansen": "Guilhermina Johansen",
		"shemale-latina":       "Shemale Latina",
		"solo":                 "Solo",
		"":                     "",
	}
	for in, want := range cases {
		if got := titleCaseSlug(in); got != want {
			t.Errorf("titleCaseSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- end-to-end ----

func TestListScenes(t *testing.T) {
	// fetchSitemap follows each <loc> verbatim, so the fixture's live host must
	// be rewritten to this server. Setting s.base only redirects the sitemap
	// request; the detail fetches would still go to honeytrans.com.
	srv := testutil.SitemapServer(t, "https://honeytrans.com",
		readFixture(t, "sitemap.xml"), readFixture(t, "detail.html"))

	s := newScraper(siteByID(t, "honeytrans"))
	s.Client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	for _, sc := range scenes {
		// scene.URL is the URL that was actually fetched — if it is not on the
		// test server, this test just scraped the live site.
		if !strings.HasPrefix(sc.URL, srv.URL) {
			t.Errorf("scene %s fetched %q, which is not the test server", sc.ID, sc.URL)
		}
		if sc.Title == "" || sc.Date.IsZero() || len(sc.Performers) == 0 {
			t.Errorf("scene %s incomplete: %+v", sc.ID, sc)
		}
	}
}

func TestSitemapErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newScraper(siteByID(t, "honeytrans"))
	s.Client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sawErr := false
	for res := range ch {
		if res.Kind == scraper.KindError {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("a sitemap failure produced no error result")
	}
}

func TestContextCancellation(t *testing.T) {
	sitemap := readFixture(t, "sitemap.xml")
	detail := readFixture(t, "detail.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			_, _ = w.Write(sitemap)
			return
		}
		_, _ = w.Write(detail)
	}))
	defer srv.Close()

	s := newScraper(siteByID(t, "honeytrans"))
	s.Client = srv.Client()
	s.base = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, srv.URL, scraper.ListOpts{})
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

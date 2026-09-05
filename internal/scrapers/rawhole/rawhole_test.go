package rawhole

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.rawhole.com", true},
		{"https://rawhole.com/free-videos.html", true},
		{"https://www.rawhole.com/bareback/free-videos.html", true},
		{"https://www.rawhole.com/free-video/2nd-times-better.html", true},
		{"https://rawhole.com.evil.org/", false},
		{"https://notrawhole.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestCategoryPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.rawhole.com/bareback/free-videos.html", "/bareback/free-videos.html"},
		{"https://www.rawhole.com/free-videos.html", ""},
		{"https://www.rawhole.com/", ""},
		{"https://www.rawhole.com/free-video/x.html", ""},
	}
	for _, c := range cases {
		if got := categoryPath(c.in); got != c.want {
			t.Errorf("categoryPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The tour renders dates through Django's AP-style month filter, so four
// months arrive in a form no Go layout parses — "Sept." above all.
func TestParseAPDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"May 11, 2026", "2026-05-11", true},
		{"Aug. 11, 2025", "2025-08-11", true},
		{"Sept. 3, 2024", "2024-09-03", true},
		{"March 1, 2020", "2020-03-01", true},
		{"December 25, 2019", "2019-12-25", true},
		{"Smarch 4, 2020", "", false},
		{"11 August 2025", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := parseAPDate(c.in)
		if ok != c.ok {
			t.Errorf("parseAPDate(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got.Format("2006-01-02") != c.want {
			t.Errorf("parseAPDate(%q) = %v, want %s", c.in, got, c.want)
		}
	}
}

func TestSlugOf(t *testing.T) {
	if got := slugOf("https://www.rawhole.com/free-video/2nd-times-better.html"); got != "2nd-times-better" {
		t.Errorf("slugOf = %q", got)
	}
}

const detailPage = `<html><head>
<meta property="og:image" content="https://www.rawhole.com/content/abc/sample-pg_site3.jpg?v=1">
</head><body>
<h1>Latin Lust In The Library</h1>
<div class="description"><p>Hanging by the pool, Derek dangles his feet in.</p></div>
<ul class="list-group sidebar-menu mb-4">
	<li class="list-group-item clearfix">&nbsp;&nbsp;Added: Sept. 11, 2026</li>
	<li class="list-group-item clearfix">&nbsp;&nbsp;Length: 15:08</li>
	<li class="list-group-item clearfix cat">
		<i class="fa fa-tags"></i>
		<a class="mr-2" href="/anal/free-videos.html">#Anal</a>
		<a class="mr-2" href="/bareback/free-videos.html">#Bareback</a>
		<a class="mr-2" href="/anal/free-videos.html">#Anal</a>
	</li>
</ul>
<div class="model-v"><div class="model-photo"></div><h1 class="text-truncate">Derek</h1></div>
<div class="model-v"><div class="model-photo"></div><h1 class="text-truncate">ToyDaddy</h1></div>
</body></html>`

func TestParseScene(t *testing.T) {
	sc, err := parseScene([]byte(detailPage),
		"https://www.rawhole.com/free-video/latin-lust-in-the-library.html", "https://www.rawhole.com")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "latin-lust-in-the-library" || sc.SiteID != siteID {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	// The cast is rendered as one profile card per performer, each with its
	// own <h1>, so the title must come from the first <h1> only.
	if sc.Title != "Latin Lust In The Library" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Description != "Hanging by the pool, Derek dangles his feet in." {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Date.Format("2006-01-02") != "2026-09-11" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Duration != 15*60+8 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if sc.Thumbnail != "https://www.rawhole.com/content/abc/sample-pg_site3.jpg?v=1" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if !slices.Equal(sc.Performers, []string{"Derek", "ToyDaddy"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !slices.Equal(sc.Categories, []string{"Anal", "Bareback"}) {
		t.Errorf("categories = %v", sc.Categories)
	}
}

// Not every scene credits anyone, and a page with no profile cards must still
// produce a scene rather than borrowing a name from elsewhere.
func TestParseSceneWithNoCast(t *testing.T) {
	page := `<html><body><h1>2nd Time&#x27;s Better</h1>
	<div class="description"><p>Ricky and Amone swap.</p></div>
	<li class="list-group-item clearfix">Added: Aug. 11, 2025</li>
	<li class="list-group-item clearfix">Length: 18:59</li></body></html>`

	sc, err := parseScene([]byte(page), "https://www.rawhole.com/free-video/x.html", "https://www.rawhole.com")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Title != "2nd Time's Better" {
		t.Errorf("title = %q", sc.Title)
	}
	if len(sc.Performers) != 0 {
		t.Errorf("performers = %v, want none", sc.Performers)
	}
	if sc.Duration != 18*60+59 {
		t.Errorf("duration = %d", sc.Duration)
	}
}

func TestParseSceneWithoutATitleIsAParseError(t *testing.T) {
	_, err := parseScene([]byte(`<html><body><p>nothing</p></body></html>`),
		"https://www.rawhole.com/free-video/x.html", "https://www.rawhole.com")
	if err == nil {
		t.Fatal("want an error")
	}
	if k := scraper.Classify(err); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	slugs   []string
	sitemap string

	mu   sync.Mutex
	hits []string
}

func (f *fakeSite) hit(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, p)
}

func (f *fakeSite) hitList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hits)
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.hit(r.URL.Path)
		switch {
		case r.URL.Path == "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, f.sitemap)
		case strings.HasPrefix(r.URL.Path, "/free-video/"):
			slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/free-video/"), ".html")
			_, _ = fmt.Fprint(w, strings.Replace(detailPage,
				"Latin Lust In The Library", "Scene "+slug, 1))
		case strings.HasSuffix(r.URL.Path, "/free-videos.html"):
			var b strings.Builder
			for _, s := range f.slugs[:min(2, len(f.slugs))] {
				fmt.Fprintf(&b, `<a href="/free-video/%s.html">x</a>`, s)
			}
			_, _ = fmt.Fprint(w, "<html><body>"+b.String()+"</body></html>")
		default:
			http.NotFound(w, r)
		}
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	fmt.Fprintf(&b, `<url><loc>%s/free-videos.html</loc></url>`, ts.URL)
	for _, s := range f.slugs {
		fmt.Fprintf(&b, `<url><loc>%s/free-video/%s.html</loc></url>`, ts.URL, s)
	}
	// A repeated <loc> must not become a second scene.
	if len(f.slugs) > 0 {
		fmt.Fprintf(&b, `<url><loc>%s/free-video/%s.html</loc></url>`, ts.URL, f.slugs[0])
	}
	b.WriteString(`</urlset>`)
	f.sitemap = b.String()
	return &Scraper{Client: ts.Client(), base: ts.URL}
}

func collect(t *testing.T, s *Scraper, studioURL string) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 500)
	go s.run(context.Background(), studioURL, scraper.ListOpts{}, out)
	var ids []string
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return ids, errs
}

func TestRunWalksTheSitemap(t *testing.T) {
	f := &fakeSite{slugs: []string{"one", "two", "three"}}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://www.rawhole.com")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"one", "three", "two"}) {
		t.Errorf("ids = %v", ids)
	}
	// The sitemap lists non-scene URLs too; only /free-video/ pages are fetched.
	if slices.Contains(f.hitList(), "/free-videos.html") {
		t.Error("fetched the listing page, which the sitemap walk has no use for")
	}
}

// `/free-videos.html` shows 24 cards and ignores `?page=`, so a category page
// is the whole of that category's public listing — not page one of it.
func TestRunCategoryReadsTheSinglePage(t *testing.T) {
	f := &fakeSite{slugs: []string{"one", "two", "three"}}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, s.base+"/bareback/free-videos.html")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"one", "two"}) {
		t.Errorf("ids = %v", ids)
	}
	if slices.Contains(f.hitList(), "/sitemap.xml") {
		t.Error("a category walk must not fall back to the whole catalogue")
	}
}

func TestRunReportsASitemapWithNoScenes(t *testing.T) {
	f := &fakeSite{}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://www.rawhole.com")
	if len(ids) != 0 {
		t.Errorf("got %d scenes, want none", len(ids))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

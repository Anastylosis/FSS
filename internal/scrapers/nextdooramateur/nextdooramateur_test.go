package nextdooramateur

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range sites {
		if seen[c.SiteID] {
			t.Errorf("duplicate site id %q", c.SiteID)
		}
		seen[c.SiteID] = true
		if c.Host == "" || c.StudioName == "" || len(c.IndexPaths) == 0 {
			t.Errorf("%s: incomplete config %+v", c.SiteID, c)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(sites[2]) // creampieebony
	cases := []struct {
		url  string
		want bool
	}{
		{"http://www.creampieebony.com/", true},
		{"https://creampieebony.com/tour/thegirls.htm", true},
		{"http://creampieebony.com", true},
		{"http://www.creampiesquad.com/", false},
		{"http://www.creampieebony.com.evil.org/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// Every site in the network must claim its own host and no other's, or
// scraper.ForURL routes a URL into whichever scraper registered first.
func TestEachSiteMatchesOnlyItsOwnHost(t *testing.T) {
	for _, mine := range sites {
		s := New(mine)
		for _, other := range sites {
			u := "http://" + other.Host + "/"
			want := other.Host == mine.Host
			if got := s.MatchesURL(u); got != want {
				t.Errorf("%s.MatchesURL(%q) = %v, want %v", mine.SiteID, u, got, want)
			}
		}
	}
}

func TestSlugOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://x.com/previews/Alina.htm", "Alina"},
		{"http://x.com/preview/01012014_dillan.html", "01012014_dillan"},
		{"http://x.com/previews/adara/index.html", "adara"},
		{"http://x.com/previews/cynara/cynara.html", "cynara"},
	}
	for _, c := range cases {
		if got := slugOf(c.in); got != c.want {
			t.Errorf("slugOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrettifySlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"mistystone", "Mistystone"},
		{"lauren_phoenix", "Lauren Phoenix"},
		{"hot-wife", "Hot Wife"},
		{"", ""},
	}
	for _, c := range cases {
		if got := prettifySlug(c.in); got != c.want {
			t.Errorf("prettifySlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStoryDropsChromeAndKeepsEveryParagraph(t *testing.T) {
	nodes := []string{
		"Home",
		strings.Repeat("First paragraph about the shoot. ", 5),
		"JOIN NOW",
		strings.Repeat("Second paragraph continuing the story. ", 5),
		"Registered with Cyber Patrol and Netnanny on June 22, 2003, and a long enough tail to clear the prose threshold entirely.",
	}
	got := story(nodes)
	if !strings.HasPrefix(got, "First paragraph") {
		t.Errorf("story lost its opening: %q", got)
	}
	if !strings.Contains(got, "Second paragraph") {
		t.Error("story kept only one paragraph")
	}
	if strings.Contains(got, "Cyber Patrol") {
		t.Error("story kept the certification boilerplate")
	}
}

func TestThumbnailPrefersTheShootStill(t *testing.T) {
	cases := []struct {
		name, page, slug, want string
	}{
		{
			name: "path naming the shoot",
			page: `<img src="../tour/images/csheader_preview.jpg"><img src="images/dakodabrooks_0630/1.jpg">`,
			slug: "dakodabrooks_0630",
			want: "http://x.com/preview/images/dakodabrooks_0630/1.jpg",
		},
		{
			name: "known gallery directory",
			page: `<img src="../images/members.jpg"><img src="../pic/adrianna/1.jpg">`,
			slug: "adrianna-shoot",
			want: "http://x.com/pic/adrianna/1.jpg",
		},
		{
			name: "bare filename beside the page",
			page: `<img src="../../wcgbhtml/images/header_01.jpg"><img src="DSC01599.jpg">`,
			slug: "cynara",
			want: "http://x.com/preview/DSC01599.jpg",
		},
		{
			name: "nothing but chrome",
			page: `<img src="../images/join.jpg"><img src="../banners/logo.jpg">`,
			slug: "nobody",
			want: "",
		},
	}
	for _, c := range cases {
		got := thumbnail(c.page, "http://x.com/preview/page.htm", c.slug)
		if got != c.want {
			t.Errorf("%s: thumbnail = %q, want %q", c.name, got, c.want)
		}
	}
}

const previewPage = `<html><head><title>Creampie Squad</title></head><body>
<script>var x = 1;</script>
<table><tr><td><img src="../tour/images/csheader_preview.jpg"></td></tr>
<tr><td>Name: Dakoda Brooks&nbsp;&nbsp; Age: 21&nbsp; From: Alabama</td></tr>
<tr><td><img src="images/dakodabrooks_0630/1.jpg"></td></tr>
<tr><td>Dakoda is a cute girl from Alabama a friend of ours referred to us. She came over to meet us and we all had a great afternoon together.</td></tr>
<tr><td>She stayed for the weekend and we shot two more scenes with her before she flew home again, which is why there is so much footage.</td></tr>
<tr><td>JOIN</td></tr>
</table></body></html>`

func TestParsePreview(t *testing.T) {
	s := New(sites[3]) // creampiesquad
	ref := previewRef{id: "dakodabrooks_0630", url: "http://creampiesquad.com/preview/dakodabrooks_0630.htm"}

	sc, err := s.parsePreview([]byte(previewPage), ref, "http://creampiesquad.com/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "dakodabrooks_0630" || sc.SiteID != "creampiesquad" {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.Title != "Dakoda Brooks" {
		t.Errorf("title = %q", sc.Title)
	}
	if !slices.Equal(sc.Performers, []string{"Dakoda Brooks"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !strings.HasPrefix(sc.Description, "Dakoda is a cute girl") ||
		!strings.Contains(sc.Description, "She stayed for the weekend") {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Thumbnail != "http://creampiesquad.com/preview/images/dakodabrooks_0630/1.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	// The suffix is a month and day with no year attached, so there is nothing
	// to store as a date.
	if !sc.Date.IsZero() {
		t.Errorf("date = %v, want zero", sc.Date)
	}
}

// One site dates its preview filenames; the title then falls back to the name
// part of the slug when the page carries no "Name:" line.
func TestParsePreviewDatedSlug(t *testing.T) {
	s := New(sites[1]) // amateurcreampies
	ref := previewRef{id: "01132014_carmen", url: "http://www.amateurcreampies.com/preview/01132014_carmen.html"}

	sc, err := s.parsePreview([]byte(`<html><body><p>`+
		strings.Repeat("Carmen came by for the afternoon. ", 6)+`</p></body></html>`), ref, "http://www.amateurcreampies.com/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Date.Format("2006-01-02") != "2014-01-13" {
		t.Errorf("date = %v, want 2014-01-13", sc.Date)
	}
	if sc.Title != "Carmen" {
		t.Errorf("title = %q", sc.Title)
	}
	if len(sc.Performers) != 0 {
		t.Errorf("performers = %v, want none — the page names nobody", sc.Performers)
	}
}

// The trailing month-day is stripped from a slug used as a title, but never
// read as a date: there is no year in it.
func TestParsePreviewUndatedSlug(t *testing.T) {
	s := New(sites[2]) // creampieebony
	ref := previewRef{id: "mistystone_0610", url: "http://www.creampieebony.com/previews/mistystone_0610.htm"}

	sc, err := s.parsePreview([]byte(`<html><body><p>`+
		strings.Repeat("Misty Stone was first featured on my site. ", 6)+`</p></body></html>`), ref, "http://www.creampieebony.com/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Title != "Mistystone" {
		t.Errorf("title = %q", sc.Title)
	}
	if !sc.Date.IsZero() {
		t.Errorf("date = %v, want zero", sc.Date)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	index    map[string]string // path -> html
	previews map[string]string // path -> html

	mu   sync.Mutex
	hits []string
}

func (f *fakeSite) hit(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, path)
}

func (f *fakeSite) hitList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hits)
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.hit(r.URL.Path)
		if body, ok := f.index[r.URL.Path]; ok {
			_, _ = fmt.Fprint(w, body)
			return
		}
		if body, ok := f.previews[r.URL.Path]; ok {
			_, _ = fmt.Fprint(w, body)
			return
		}
		http.NotFound(w, r)
	}
}

func newTestScraper(t *testing.T, cfg SiteConfig, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	s := New(cfg)
	s.Client = ts.Client()
	s.base = ts.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 200)
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
	sort.Strings(ids)
	return ids, errs
}

// A shoot linked twice under different filenames is one scene, not two.
func TestRunDeduplicatesShootsLinkedTwice(t *testing.T) {
	f := &fakeSite{
		index: map[string]string{"/tour/": `<html><body>
			<a href="previews/cynara/index.html">a</a>
			<a href="previews/cynara/cynara.html">a again</a>
			<a href="previews/adara/index.html">b</a>
		</body></html>`},
		previews: map[string]string{
			"/previews/cynara/index.html": previewPage,
			"/previews/adara/index.html":  previewPage,
		},
	}
	cfg := sites[6] // westcoastgangbangs: index at /tour/, links resolve from root
	s := newTestScraper(t, cfg, f)

	ids, errs := collect(t, s, "http://westcoastgangbangs.example/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"adara", "cynara"}) {
		t.Errorf("ids = %v", ids)
	}
	if slices.Contains(f.hitList(), "/previews/cynara/cynara.html") {
		t.Error("fetched the same shoot twice")
	}
}

// The tour usually lists a subset of the girls index; the walk covers the
// union rather than whichever page was tried first.
func TestRunMergesEveryIndexPage(t *testing.T) {
	f := &fakeSite{
		index: map[string]string{
			"/tour/thegirls.htm": `<a href="../preview/alina.htm">a</a><a href="../preview/beth.htm">b</a>`,
			"/tour/":             `<a href="../preview/beth.htm">b</a><a href="../preview/cara.htm">c</a>`,
		},
		previews: map[string]string{
			"/preview/alina.htm": previewPage,
			"/preview/beth.htm":  previewPage,
			"/preview/cara.htm":  previewPage,
		},
	}
	s := newTestScraper(t, sites[3], f) // creampiesquad: two index paths

	ids, errs := collect(t, s, "http://creampiesquad.example/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"alina", "beth", "cara"}) {
		t.Errorf("ids = %v, want the union of both index pages", ids)
	}
}

// A tour that links no previews at all is a redesign, and must not look like
// an empty catalogue to an authoritative --full Save.
func TestRunReportsAnIndexWithNoPreviewsAsAParseError(t *testing.T) {
	f := &fakeSite{index: map[string]string{
		"/tour/thegirls.htm": `<html><body><a href="/join.htm">Join</a></body></html>`,
		"/tour/":             `<html><body></body></html>`,
	}}
	s := newTestScraper(t, sites[3], f)

	ids, errs := collect(t, s, "http://creampiesquad.example/")
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

// A preview the index promised but the server no longer serves is a known
// scene that went uncollected, so it is reported rather than swallowed — that
// is what keeps --full from deleting the rest of the catalogue around it.
func TestRunReportsADeadPreviewLink(t *testing.T) {
	f := &fakeSite{
		index: map[string]string{
			"/tour/thegirls.htm": `<a href="../preview/alina.htm">a</a><a href="../preview/gone.htm">b</a>`,
			"/tour/":             ``,
		},
		previews: map[string]string{"/preview/alina.htm": previewPage},
	}
	s := newTestScraper(t, sites[3], f)

	ids, errs := collect(t, s, "http://creampiesquad.example/")
	if !slices.Equal(ids, []string{"alina"}) {
		t.Errorf("ids = %v", ids)
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "gone") {
		t.Errorf("error does not name the dead preview: %v", errs[0])
	}
}

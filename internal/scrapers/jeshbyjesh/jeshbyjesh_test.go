package jeshbyjesh

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
		{"https://www.jeshbyjesh.com/", true},
		{"https://jeshbyjesh.com/tour/categories/movies_3_d.html", true},
		{"https://www.jeshbyjesh.com/tour/series/season-1.html", true},
		{"https://jeshbyjesh.com.evil.org/", false},
		{"https://jesh.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestFilterPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.jeshbyjesh.com/", ""},
		{"https://www.jeshbyjesh.com/tour/categories/movies.html", ""},
		{"https://www.jeshbyjesh.com/tour/categories/movies_4_d.html", ""},
		{"https://www.jeshbyjesh.com/tour/series/season-1.html", "/tour/series/season-1.html"},
		{"https://www.jeshbyjesh.com/tour/categories/bonus.html", "/tour/categories/bonus.html"},
		{"https://www.jeshbyjesh.com/tour/models/Veronika-Vengeance.html", "/tour/models/Veronika-Vengeance.html"},
	}
	for _, c := range cases {
		if got := filterPath(c.in); got != c.want {
			t.Errorf("filterPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugOf(t *testing.T) {
	if got := slugOf("https://www.jeshbyjesh.com/tour/trailers/20260815-S07-VeronikaVengeance.html"); got != "20260815-S07-VeronikaVengeance" {
		t.Errorf("slugOf = %q", got)
	}
	if got := slugOf("https://www.jeshbyjesh.com/tour/categories/movies.html"); got != "" {
		t.Errorf("slugOf = %q, want empty for a non-scene URL", got)
	}
}

const detailPage = `<html><head>
<meta property="og:title" content="SEASON 7 &bull;&nbsp;VERONIKA VENGEANCE">
</head><body>
<h1>SEASON 7 &bull;&nbsp;VERONIKA VENGEANCE</h1>
<img id="set-target-459" class="video_placeholder stdimage" src="/tour/content//contentthumbs/44/94/4494-1x.jpg" />
<div class="trailer_overlay"><div class="trailer_box"><h3>Like what you see?</h3>
	<p> You must be a member to view this video.<br/> <a href="/members2/">Log In</a> or Join Now to get full access to this scene.</p>
</div></div>
<div class="blog-content">
	<p>EPISODE 3 &bull; VENDETTA</p><p>SCENE DESCRIPTION: Veronika has a thing for facials.</p>
</div>
<div class="video-meta"><div class="video-meta-left">
	<div class="update-info-block"><span class="update-info-value text-uppercase">
		<ul class="tags-list"><li><a href="https://www.jeshbyjesh.com/tour/series/season-7.html" title="Season 7">Season 7</a></li></ul>
	</span></div>
	<div class="update-info-block"><span class="update-info-title">RELEASE DATE:</span>
		<span class="update-info-value text-uppercase">August 15, 2026</span></div>
	<div class="update-info-block"><span class="update-info-title">SCENE LENGTH:</span>
		<span class="update-info-value text-uppercase">27:03</span></div>
	<div class="update-info-block"><span class="update-info-title">TAGS:</span>
		<span class="update-info-value"><ul class="tags-list">
		<li><a href="https://www.jeshbyjesh.com/tour/categories/alt.html">Alt</a></li>
		<li><a href="https://www.jeshbyjesh.com/tour/categories/pawg.html">PAWG</a></li>
		<li><a href="https://www.jeshbyjesh.com/tour/categories/alt.html">Alt</a></li>
		</ul></span></div>
</div><div class="video-meta-right">
	<ul class="model-list"><li><a href="https://www.jeshbyjesh.com/tour/models/Veronika-Vengeance.html">
		<span class="model-list-name">Veronika Vengeance</span></a></li></ul>
</div></div>
</body></html>`

func TestParseScene(t *testing.T) {
	s := New()
	sc, err := s.parseScene([]byte(detailPage),
		"https://www.jeshbyjesh.com/tour/trailers/20260815-S07-VeronikaVengeance.html",
		"https://www.jeshbyjesh.com/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "20260815-S07-VeronikaVengeance" || sc.SiteID != siteID {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.Title != "SEASON 7 VERONIKA VENGEANCE" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Date.Format("2006-01-02") != "2026-08-15" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Duration != 27*60+3 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if sc.Series != "Season 7" {
		t.Errorf("series = %q", sc.Series)
	}
	if !slices.Equal(sc.Tags, []string{"Alt", "PAWG"}) {
		t.Errorf("tags = %v — a repeat must not be stored twice", sc.Tags)
	}
	if !slices.Equal(sc.Performers, []string{"Veronika Vengeance"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Thumbnail != "https://www.jeshbyjesh.com/tour/content//contentthumbs/44/94/4494-1x.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	// The members-only overlay is a long paragraph too; anchoring on
	// blog-content is what keeps it out of the copy.
	want := "EPISODE 3 VENDETTA\n\nSCENE DESCRIPTION: Veronika has a thing for facials."
	if sc.Description != want {
		t.Errorf("description = %q,\nwant %q", sc.Description, want)
	}
	if strings.Contains(sc.Description, "must be a member") {
		t.Error("the join overlay leaked into the description")
	}
}

func TestParseSceneWithoutATitleIsAParseError(t *testing.T) {
	s := New()
	_, err := s.parseScene([]byte(`<html><body><p>nothing</p></body></html>`),
		"https://www.jeshbyjesh.com/tour/trailers/x.html", "https://www.jeshbyjesh.com/")
	if err == nil {
		t.Fatal("want an error")
	}
	if k := scraper.Classify(err); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// ---- end-to-end ----

// The grid, the hero slider and the "you may also like" carousel all use
// card-link, so a page carries more links than its grid holds and consecutive
// pages overlap. Past the end the listing clamps back to a page already walked
// rather than 404ing, so the only end marker is a page with nothing new on it.
type fakeSite struct {
	// gridPages is how many pages of fresh cards the listing really has.
	gridPages int
	perPage   int

	mu    sync.Mutex
	pages []int
}

func (f *fakeSite) note(p int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages = append(f.pages, p)
}

func (f *fakeSite) noted() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pages)
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/trailers/") {
			_, _ = fmt.Fprint(w, detailPage)
			return
		}
		var page int
		switch {
		case strings.Contains(r.URL.Path, "/series/"):
			page = 1
		default:
			if _, err := fmt.Sscanf(r.URL.Path, "/tour/categories/movies_%d_d.html", &page); err != nil {
				http.NotFound(w, r)
				return
			}
			f.note(page)
			// Past the end the site clamps: it keeps serving the last real page.
			if page > f.gridPages {
				page = f.gridPages
			}
		}

		var b strings.Builder
		for i := range f.perPage {
			slug := fmt.Sprintf("p%ds%d", page, i)
			fmt.Fprintf(&b, `<div class="content-card"><a href="/tour/trailers/%s.html" class="card-link" title="x"></a></div>`, slug)
		}
		// The carousel repeats page 1's first card on every page — but a site
		// with no scenes at all renders neither.
		if f.perPage > 0 {
			fmt.Fprintf(&b, `<div class="content-card"><a href="/tour/trailers/p1s0.html" class="card-link" title="x"></a></div>`)
		}
		_, _ = fmt.Fprint(w, b.String())
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
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
	slices.Sort(ids)
	return ids, errs
}

func TestRunStopsWhenTheListingClamps(t *testing.T) {
	f := &fakeSite{gridPages: 3, perPage: 2}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://www.jeshbyjesh.com/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	want := []string{"p1s0", "p1s1", "p2s0", "p2s1", "p3s0", "p3s1"}
	if !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	// It probes one page past the end to discover the clamp, and no further.
	pages := f.noted()
	if !slices.Contains(pages, 4) {
		t.Errorf("pages = %v, want the clamp probed", pages)
	}
	if slices.Contains(pages, 5) {
		t.Errorf("pages = %v, want the walk to stop at the first clamped page", pages)
	}
}

// A series or model page lists its whole set at once, so it is one request,
// not page one of a paged walk.
func TestRunFilterPageIsASingleRequest(t *testing.T) {
	f := &fakeSite{gridPages: 3, perPage: 2}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, s.base+"/tour/series/season-1.html")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"p1s0", "p1s1"}) {
		t.Errorf("ids = %v", ids)
	}
	if len(f.noted()) != 0 {
		t.Errorf("a series walk requested catalogue pages: %v", f.noted())
	}
}

func TestRunReportsAnEmptyFirstPage(t *testing.T) {
	f := &fakeSite{gridPages: 0, perPage: 0}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://www.jeshbyjesh.com/")
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

package ladyfyre

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = New()
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.ladyfyre.com/tour/categories/movies.html", true},
		{"https://ladyfyre.com/tour/updates/Some-Scene.html", true},
		{"http://www.ladyfyre.com/tour/models/LadyOliviaFyre.html", true},
		{"https://www.ladyfyre.com/", true},
		{"https://www.example.com/", false},
		{"https://www.houseofyre.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

const listingHTML = `<html><body>
<li class="active"><a href="https://www.ladyfyre.com/tour/categories/movies_1_d.html">1</a></li>
<li><a href="https://www.ladyfyre.com/tour/categories/movies_2_d.html">2</a></li>
<li><a href="https://www.ladyfyre.com/tour/categories/movies_3_d.html">3</a></li>
<div class="updateItem">
	<a href="https://www.ladyfyre.com/tour/updates/Ridding-Yourself-of-Masculine-Mannerisms.html">
		<img class="stdimage " src="https://cdn.example.com/tour/content/LadyFyreRidding/1.jpg" />
	</a>
	<div class="updateDetails">
	<div class="buy_button">Buy $8.99</div>
		<h4>
			<a href="https://www.ladyfyre.com/tour/updates/Ridding-Yourself-of-Masculine-Mannerisms.html">
				Ridding Yourself of Masculine Mannerisms
			</a>
		</h4>
		<p>
	<span class="tour_update_models">
			<a href="https://www.ladyfyre.com/tour/models/LadyOliviaFyre.html">Lady Fyre</a>
	</span>
 <span>05/16/2026</span></p>
	</div>
</div>
<div class="updateItem">
	<a href="https://www.ladyfyre.com/tour/updates/Therapist-Part-3.html">
		<img class="stdimage " src="https://cdn.example.com/tour/content/Therapist3/1.jpg" />
	</a>
	<div class="updateDetails">
	<div class="buy_button">Buy $12.99</div>
		<h4>
			<a href="https://www.ladyfyre.com/tour/updates/Therapist-Part-3.html">
				Therapist Part 3
			</a>
		</h4>
		<p>
	<span class="tour_update_models">
			<a href="https://www.ladyfyre.com/tour/models/LadyOliviaFyre.html">Lady Fyre</a>, <a href="https://www.ladyfyre.com/tour/models/AnotherModel.html">Another Model</a>
	</span>
 <span>05/09/2026</span></p>
	</div>
</div>
</body></html>`

func TestParseListingEntries(t *testing.T) {
	entries := parseListingEntries([]byte(listingHTML))
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	e := entries[0]
	if e.slug != "Ridding-Yourself-of-Masculine-Mannerisms" {
		t.Errorf("slug = %q", e.slug)
	}
	if e.title != "Ridding Yourself of Masculine Mannerisms" {
		t.Errorf("title = %q", e.title)
	}
	if len(e.performers) != 1 || e.performers[0] != "Lady Fyre" {
		t.Errorf("performers = %v", e.performers)
	}
	if e.date != "05/16/2026" {
		t.Errorf("date = %q", e.date)
	}
	if e.thumbnail != "https://cdn.example.com/tour/content/LadyFyreRidding/1.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}
	if e.price != 8.99 {
		t.Errorf("price = %f", e.price)
	}

	e2 := entries[1]
	if e2.slug != "Therapist-Part-3" {
		t.Errorf("entry 2 slug = %q", e2.slug)
	}
	if len(e2.performers) != 2 {
		t.Errorf("entry 2 performers = %v", e2.performers)
	}
	if e2.price != 12.99 {
		t.Errorf("entry 2 price = %f", e2.price)
	}
}

func TestParseMaxPage(t *testing.T) {
	if got := parseMaxPage([]byte(listingHTML)); got != 3 {
		t.Errorf("parseMaxPage = %d, want 3", got)
	}
}

func TestHasNextPage(t *testing.T) {
	if !hasNextPage([]byte(listingHTML), 1) {
		t.Error("expected hasNextPage(1) = true")
	}
	if hasNextPage([]byte(listingHTML), 3) {
		t.Error("expected hasNextPage(3) = false")
	}
}

const detailHTML = `<html><head>
<meta property="og:description" content="A great scene description here"/>
</head><body>
<span class="update_tags">
Tags:
  <a href="https://www.ladyfyre.com/tour/categories/femdom.html">Femdom</a>
  <a href="https://www.ladyfyre.com/tour/categories/SissyTraining.html">Sissy Training</a>
</span>
</body></html>`

func TestParseDetail(t *testing.T) {
	entry := listEntry{
		slug:       "Test-Scene",
		url:        "https://www.ladyfyre.com/tour/updates/Test-Scene.html",
		title:      "Test Scene",
		performers: []string{"Lady Fyre"},
		date:       "05/16/2026",
		thumbnail:  "https://cdn.example.com/thumb.jpg",
		price:      8.99,
	}

	scene := parseDetail([]byte(detailHTML), entry, "https://www.ladyfyre.com"+entry.url)

	if scene.Title != "Test Scene" {
		t.Errorf("title = %q", scene.Title)
	}
	if scene.Description != "A great scene description here" {
		t.Errorf("description = %q", scene.Description)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "Femdom" {
		t.Errorf("tags = %v", scene.Tags)
	}
	if scene.Date.Format("2006-01-02") != "2026-05-16" {
		t.Errorf("date = %v", scene.Date)
	}
	if scene.PriceHistory == nil || scene.PriceHistory[0].Regular != 8.99 {
		t.Errorf("price = %v", scene.PriceHistory)
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case strings.Contains(r.URL.Path, "movies_1_d") || strings.Contains(r.URL.Path, "movies.html"):
			_, _ = fmt.Fprint(w, strings.ReplaceAll(listingHTML, "https://www.ladyfyre.com", ""))
		case strings.Contains(r.URL.Path, "movies_2_d"):
			_, _ = fmt.Fprint(w, `<html><body>
<div class="updateItem">
	<a href="/tour/updates/Last-Scene.html">
		<img class="stdimage " src="https://cdn.example.com/tour/content/Last/1.jpg" />
	</a>
	<div class="updateDetails">
		<h4><a href="/tour/updates/Last-Scene.html">Last Scene</a></h4>
		<p><span class="tour_update_models"><a href="/tour/models/Model.html">Model</a></span>
 <span>01/01/2020</span></p>
	</div>
</div>
</body></html>`)
		case strings.Contains(r.URL.Path, "movies_3_d"):
			_, _ = fmt.Fprint(w, "<html></html>")
		case strings.Contains(r.URL.Path, "/updates/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3", scenes)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case strings.Contains(r.URL.Path, "movies_"):
			_, _ = fmt.Fprint(w, strings.ReplaceAll(listingHTML, "https://www.ladyfyre.com", ""))
		case strings.Contains(r.URL.Path, "/updates/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"Therapist-Part-3": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	var stoppedEarly bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

// Markup copied from a live /tour/models/{slug}.html page (trimmed). The card
// container, title span and date span all differ from the main listing — that
// mismatch is why the /models/ mode silently returned nothing, so the fixture
// keeps those three shapes verbatim rather than being hand-simplified.
const modelPageHTML = `<html><body>
<div class="modelUpdates">
<div class="update_block">
<div class="update_table_left">
	<div class="update_block_info">
		<span class="update_title">Annoyed, Hot Teacher Milks You</span>
		<br />
	<span class="tour_update_models">
			<a href="https://www.ladyfyre.com/tour/models/LadyOliviaFyre.html">Lady Fyre</a>
	</span>
		<br />
		<span class="availdate">07/25/2026									<br />Available to Members Now</span>
		<hr class="update_hr" />
		<span class="latest_update_description">You&rsquo;ve not been paying attention to the lessons.</span>
		<span class="update_tags">
	  <a href="https://www.ladyfyre.com/tour/categories/big-tits.html">Big Tits</a>
		</span>
	</div>
	</div>
	<div class="update_table_right">
		<div class="update_image">
			<a  href="https://www.ladyfyre.com/tour/updates/Annoyed-Hot-Teacher-Milks-You.html">
			<img alt="LadyFyreAnnoyed" class="stdimage large_update_thumb left thumbs" src="https://cdn.example.com/tour/content/LadyFyreAnnoyed/0.jpg" />
			</a>
			<div class="buy_button">Buy $12.99</div>
		</div>
	</div>
</div>
<div class="update_block">
<div class="update_table_left">
	<div class="update_block_info">
		<span class="update_title">Stepmom&#039;s Sloppy Seconds</span>
		<br />
	<span class="tour_update_models">
			<a href="https://www.ladyfyre.com/tour/models/LadyOliviaFyre.html">Lady Fyre</a>
			<a href="https://www.ladyfyre.com/tour/models/LazFyre.html">Laz Fyre</a>
	</span>
		<br />
		<span class="availdate">06/13/2026									<br />Available to Members Now</span>
	</div>
	</div>
	<div class="update_table_right">
		<div class="update_image">
			<a  href="https://www.ladyfyre.com/tour/updates/Stepmoms-Sloppy-Seconds.html">
			<img alt="LadyFyreStepmoms" class="stdimage large_update_thumb left thumbs" src="https://cdn.example.com/tour/content/LadyFyreStepmoms/0.jpg" />
			</a>
			<div class="buy_button">Buy $8.99</div>
		</div>
	</div>
</div>
</div>
</body></html>`

func TestParseModelEntries(t *testing.T) {
	entries := parseModelEntries([]byte(modelPageHTML))
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	e := entries[0]
	if e.slug != "Annoyed-Hot-Teacher-Milks-You" {
		t.Errorf("slug = %q", e.slug)
	}
	if e.title != "Annoyed, Hot Teacher Milks You" {
		t.Errorf("title = %q", e.title)
	}
	if e.date != "07/25/2026" {
		t.Errorf("date = %q — the model page uses <span class=\"availdate\">, not a bare <span>", e.date)
	}
	if len(e.performers) != 1 || e.performers[0] != "Lady Fyre" {
		t.Errorf("performers = %v", e.performers)
	}
	if e.thumbnail != "https://cdn.example.com/tour/content/LadyFyreAnnoyed/0.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}
	if e.price != 12.99 {
		t.Errorf("price = %v", e.price)
	}

	e2 := entries[1]
	if e2.title != "Stepmom's Sloppy Seconds" {
		t.Errorf("entry 2 title = %q (HTML entities should be unescaped)", e2.title)
	}
	if len(e2.performers) != 2 {
		t.Errorf("entry 2 performers = %v, want both models", e2.performers)
	}
}

// The two templates must not be interchangeable: parsing a model page with the
// listing parser is what produced zero scenes, so this pins that they really do
// need separate delimiters rather than one lenient pattern.
func TestListingParserDoesNotParseModelPage(t *testing.T) {
	if got := parseListingEntries([]byte(modelPageHTML)); len(got) != 0 {
		t.Fatalf("the listing parser found %d entries on a model page; if the "+
			"templates converged, collapse the two parsers instead of keeping both", len(got))
	}
	if got := parseModelEntries([]byte(listingHTML)); len(got) != 0 {
		t.Fatalf("the model parser found %d entries on a listing page", len(got))
	}
}

// Drives the /models/ URL end to end, so the dispatch in run() and the parser
// runSinglePage picks are both pinned. Asserting the parsers in isolation is not
// enough: pointing runSinglePage back at the listing parser is exactly the bug
// that shipped, and it leaves parseModelEntries passing its own unit test.
func TestListScenesModelPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case strings.Contains(r.URL.Path, "/models/"):
			_, _ = fmt.Fprint(w, strings.ReplaceAll(modelPageHTML, "https://www.ladyfyre.com", ""))
		case strings.Contains(r.URL.Path, "/updates/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/models/LadyOliviaFyre.html", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	var titles []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			titles = append(titles, r.Scene.Title)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Fatalf("got %d scenes from a model page, want 2", scenes)
	}
	if titles[0] == "" {
		t.Error("model-page scene has an empty Title")
	}
}

// The silent-zero path is what hid this mode being broken, so the error is
// asserted rather than assumed.
func TestListScenesModelPageReportsUnparseablePage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body>no cards here</body></html>")
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/models/Nobody.html", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
		}
	}
	if errs == 0 {
		t.Error("a model page with no parseable cards reported success with zero scenes; " +
			"that silence is what let this mode ship broken")
	}
}

// The listing links are site-relative; a scene stored with "/tour/updates/x.html"
// as its URL resolves against nothing outside the scraper.
func TestParseDetailStoresAnAbsoluteURL(t *testing.T) {
	entry := listEntry{
		slug:  "abc",
		title: "A Scene",
		url:   "/tour/updates/abc.html",
	}
	scene := parseDetail([]byte(`<html><body></body></html>`), entry, "https://www.ladyfyre.com"+entry.url)
	if scene.URL != "https://www.ladyfyre.com/tour/updates/abc.html" {
		t.Errorf("URL = %q, want the absolute form", scene.URL)
	}
}

func TestResolveURL(t *testing.T) {
	s := New()
	cases := []struct{ in, want string }{
		{"/tour/updates/a.html", "https://www.ladyfyre.com/tour/updates/a.html"},
		{"https://www.ladyfyre.com/tour/updates/a.html", "https://www.ladyfyre.com/tour/updates/a.html"},
	}
	for _, c := range cases {
		if got := s.resolveURL(c.in); got != c.want {
			t.Errorf("resolveURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

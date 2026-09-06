package darkreachmodernutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

// Fixtures derived from real mybestsexlife.com markup.

const listingHTML = `<html><body>
<div class="row-col-padding-10"><div class="row">

<div class="col-md-4 col-sm-6 col-12">
  <div class="item item-update item-video">
    <div class="img-div">
      <a href="https://www.mybestsexlife.com/trailers/MyBestSexLifeWorshippingPrincess.html" title="MyBestSexLife Worshipping Princess">
        <img id="set-target-269" class="update_thumb thumbs stdimage" src0_1x="/content//contentthumbs/05/37/537-1x.jpg" />
        <span class="item-icon"><i class="fa fa-play-circle"></i></span>
      </a>
    </div>
    <div class="content-div">
      <h4>
        <a href="https://www.mybestsexlife.com/trailers/MyBestSexLifeWorshippingPrincess.html" title="MyBestSexLife Worshipping Princess">
          MyBestSexLife Worshipping Princess
        </a>
      </h4>
      <div class="more-info-div">
         | <i class="fa fa-calendar"></i> Jan 23, 2026
      </div>
    </div>
  </div>
</div>

<div class="col-md-4 col-sm-6 col-12">
  <div class="item item-update item-video">
    <div class="img-div">
      <a href="https://www.mybestsexlife.com/trailers/StunningAsianBrunetteNatashaTy.html" title="Stunning Asian Brunette Natasha Ty">
        <img id="set-target-239" class="update_thumb thumbs stdimage" src0_1x="/content//contentthumbs/04/96/496-1x.jpg" />
      </a>
    </div>
    <div class="content-div">
      <h4>
        <a href="https://www.mybestsexlife.com/trailers/StunningAsianBrunetteNatashaTy.html" title="Stunning Asian Brunette Natasha Ty">
          Stunning Asian Brunette Natasha Ty
        </a>
      </h4>
      <div class="more-info-div">
         | <i class="fa fa-calendar"></i> Dec 15, 2025
      </div>
    </div>
  </div>
</div>

</div></div>

<ul class="pagination">
  <li><a href="categories/movies_1_d.html" class="active">1</a></li>
  <li><a href="categories/movies_2_d.html">2</a></li>
  <li><a href="categories/movies_10_d.html">10</a></li>
</ul>
</body></html>`

const emptyHTML = `<html><body><div class="row"></div></body></html>`

func testConfig(base string) SiteConfig {
	return SiteConfig{
		ID:       "mybestsexlife",
		SiteBase: base,
		Studio:   "My Best Sex Life",
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?mybestsexlife\.com`),
	}
}

func TestParseListing_extractsCards(t *testing.T) {
	items := parseListing([]byte(listingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.id != "MyBestSexLifeWorshippingPrincess" {
		t.Errorf("ID = %q", first.id)
	}
	if first.title != "MyBestSexLife Worshipping Princess" {
		t.Errorf("Title = %q", first.title)
	}
	if first.url != "https://www.mybestsexlife.com/trailers/MyBestSexLifeWorshippingPrincess.html" {
		t.Errorf("URL = %q", first.url)
	}
	if first.thumb != "/content//contentthumbs/05/37/537-1x.jpg" {
		t.Errorf("Thumb (raw) = %q", first.thumb)
	}
	if first.date.Year() != 2026 || first.date.Month() != 1 || first.date.Day() != 23 {
		t.Errorf("Date = %v, want 2026-01-23", first.date)
	}

	second := items[1]
	if second.id != "StunningAsianBrunetteNatashaTy" {
		t.Errorf("Second ID = %q", second.id)
	}
	if second.date.Year() != 2025 || second.date.Month() != 12 || second.date.Day() != 15 {
		t.Errorf("Second date = %v", second.date)
	}
}

func TestParseListing_dedupes(t *testing.T) {
	doubled := listingHTML + listingHTML
	items := parseListing([]byte(doubled))
	if len(items) != 2 {
		t.Errorf("got %d after dedup, want 2", len(items))
	}
}

func TestEstimateTotal(t *testing.T) {
	got := estimateTotal([]byte(listingHTML), 2)
	if got != 20 {
		t.Errorf("estimateTotal = %d, want 20", got)
	}
}

func TestListingURL(t *testing.T) {
	tests := []struct {
		tourPrefix string
		page       int
		want       string
	}{
		{"", 1, "https://example.com/categories/movies_1_d.html"},
		{"", 5, "https://example.com/categories/movies_5_d.html"},
		{"/tour", 1, "https://example.com/tour/categories/movies_1_d.html"},
		{"/tour", 3, "https://example.com/tour/categories/movies_3_d.html"},
	}
	for _, c := range tests {
		s := New(SiteConfig{SiteBase: "https://example.com", TourPrefix: c.tourPrefix, MatchRe: regexp.MustCompile(`.*`)})
		got := s.listingURL(c.page)
		if got != c.want {
			t.Errorf("tourPrefix=%q page=%d → %q, want %q", c.tourPrefix, c.page, got, c.want)
		}
	}
}

func TestToScene_resolvesRelativeURLs(t *testing.T) {
	item := sceneItem{id: "Foo", url: "/trailers/Foo.html", thumb: "/content/foo.jpg"}
	scene := item.toScene("mybestsexlife", "https://example.com", "Studio", item.date)
	if scene.URL != "https://example.com/trailers/Foo.html" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Thumbnail != "https://example.com/content/foo.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(testConfig("https://www.mybestsexlife.com"))
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.mybestsexlife.com/", true},
		{"http://mybestsexlife.com/categories/movies_2_d.html", true},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v", c.url, got)
		}
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			_, _ = fmt.Fprint(w, emptyHTML)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "mybestsexlife",
		SiteBase: ts.URL,
		Studio:   "My Best Sex Life",
		MatchRe:  regexp.MustCompile(`.*`),
	})

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.Studio != "My Best Sex Life" {
				t.Errorf("Studio = %q", r.Scene.Studio)
			}
			if r.Scene.Date.IsZero() {
				t.Errorf("Date is zero for %q", r.Scene.Title)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/categories/movies_1_d.html" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "mybestsexlife",
		SiteBase: ts.URL,
		Studio:   "My Best Sex Life",
		MatchRe:  regexp.MustCompile(`.*`),
	})

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"StunningAsianBrunetteNatashaTy": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var (
		scenes       int
		stoppedEarly bool
	)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

// Ensure strings import is used even when fixtures don't.
var _ = strings.Contains

func TestLastPage(t *testing.T) {
	body := []byte(`<ul class="pagination">
		<li><a href="/t1/categories/movies_1_d.html">1</a></li>
		<li><a href="/t1/categories/movies_2_d.html">2</a></li>
		<li><a href="/t1/categories/movies_11_d.html">11</a></li>
		<li><a href="/t1/categories/movies_30_d.html">30</a></li>
	</ul>`)
	if got := lastPage(body); got != 30 {
		t.Errorf("lastPage = %d, want 30", got)
	}
	if got := lastPage([]byte(`<div>no pager</div>`)); got != 1 {
		t.Errorf("lastPage = %d, want 1", got)
	}
}

// The pager names the last page, so the walk can stop on purpose. Without that
// the only end marker is an empty page, which costs a wasted request every run
// and — on a site that 404s past the end — turns the normal end of the
// catalogue into a reported error that marks the run incomplete.
func TestRunStopsAtTheLastPageThePagerNames(t *testing.T) {
	const pages = 3
	var requested []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/categories/movies_%d_d.html", &n); err != nil {
			http.NotFound(w, r)
			return
		}
		requested = append(requested, n)
		if n > pages {
			http.NotFound(w, r)
			return
		}
		var b strings.Builder
		for i := range 2 {
			fmt.Fprintf(&b, `<div class="item item-update item-video"><div class="img-div">
				<a href="/trailers/Scene%d%d.html" title="Scene %d%d">
				<img id="set-target-%d%d" class="update_thumb thumbs stdimage" src0_1x="/content/x%d%d.jpg" /></a>
				</div><div class="content-div"><h4>
				<a href="/trailers/Scene%d%d.html" title="Scene %d%d">Scene %d%d</a></h4>
				<div class="more-info-div"><i class="fa fa-calendar"></i> Jan 23, 2026</div></div></div>`,
				n, i, n, i, n, i, n, i, n, i, n, i, n, i)
		}
		for p := 1; p <= pages; p++ {
			fmt.Fprintf(&b, `<a href="/categories/movies_%d_d.html">%d</a>`, p, p)
		}
		_, _ = fmt.Fprint(w, b.String())
	}))
	defer srv.Close()

	s := New(SiteConfig{
		ID: "test", SiteBase: srv.URL, Studio: "Test",
		MatchRe: regexp.MustCompile(`^` + regexp.QuoteMeta(srv.URL)),
	})
	s.client = srv.Client()

	out := make(chan scraper.SceneResult, 100)
	go s.run(context.Background(), srv.URL, scraper.ListOpts{}, out)
	var n int
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != pages*2 {
		t.Errorf("got %d scenes, want %d", n, pages*2)
	}
	if slices.Contains(requested, pages+1) {
		t.Errorf("requested page %d past the pager's last page: %v", pages+1, requested)
	}
}

// A site whose pager is missing has no last page to read, so the walk must
// still fall back to stopping on the first page with no cards rather than
// stopping after page one.
func TestRunFallsBackToAnEmptyPageWhenThereIsNoPager(t *testing.T) {
	const pages = 2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/categories/movies_%d_d.html", &n); err != nil {
			http.NotFound(w, r)
			return
		}
		if n > pages {
			_, _ = fmt.Fprint(w, `<div class="empty"></div>`)
			return
		}
		_, _ = fmt.Fprintf(w, `<div class="item item-update item-video"><div class="img-div">
			<a href="/trailers/Scene%d.html" title="Scene %d">
			<img id="set-target-%d" class="update_thumb thumbs stdimage" src0_1x="/content/x%d.jpg" /></a>
			</div><div class="content-div"><h4>
			<a href="/trailers/Scene%d.html" title="Scene %d">Scene %d</a></h4>
			<div class="more-info-div"><i class="fa fa-calendar"></i> Jan 23, 2026</div></div></div>`,
			n, n, n, n, n, n, n)
	}))
	defer srv.Close()

	s := New(SiteConfig{
		ID: "test", SiteBase: srv.URL, Studio: "Test",
		MatchRe: regexp.MustCompile(`^` + regexp.QuoteMeta(srv.URL)),
	})
	s.client = srv.Client()

	out := make(chan scraper.SceneResult, 100)
	go s.run(context.Background(), srv.URL, scraper.ListOpts{}, out)
	var n int
	for r := range out {
		if r.Kind == scraper.KindScene {
			n++
		}
	}
	if n != pages {
		t.Errorf("got %d scenes, want %d — the empty-page fallback did not run", n, pages)
	}
}

// A page past the end that 404s is the normal end of the catalogue on some of
// these sites, not a failure: reporting it would mark every full run incomplete
// and block the authoritative Save.
func TestRunTreatsA404PastPageOneAsDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/categories/movies_1_d.html" {
			http.NotFound(w, r)
			return
		}
		// No pager at all, so the walk must probe page 2 and meet the 404.
		_, _ = fmt.Fprint(w, `<div class="item item-update item-video"><div class="img-div">
			<a href="/trailers/Only.html" title="Only">
			<img id="set-target-1" class="update_thumb thumbs stdimage" src0_1x="/content/x.jpg" /></a>
			</div><div class="content-div"><h4>
			<a href="/trailers/Only.html" title="Only">Only</a></h4>
			<div class="more-info-div"><i class="fa fa-calendar"></i> Jan 23, 2026</div></div></div>`)
	}))
	defer srv.Close()

	s := New(SiteConfig{
		ID: "test", SiteBase: srv.URL, Studio: "Test",
		MatchRe: regexp.MustCompile(`^` + regexp.QuoteMeta(srv.URL)),
	})
	s.client = srv.Client()

	out := make(chan scraper.SceneResult, 100)
	go s.run(context.Background(), srv.URL, scraper.ListOpts{}, out)
	var n int
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	if n != 1 {
		t.Errorf("got %d scenes, want 1", n)
	}
	if len(errs) != 0 {
		t.Errorf("the end of the catalogue was reported as an error: %v", errs)
	}
}

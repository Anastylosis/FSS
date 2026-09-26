package nookies

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const testListingPage = `<!DOCTYPE html><html><body>
<div class="video-card">
    <div class="video-card-img img-hover-zoom">
        <a href="/video/3439/desiree-edens-milk-cookies" class="v-thumb" data-src="BASEURL/tour-content/gilfaf/preview.mp4">
            <img src="BASEURL/tour-content/gilfaf/main.jpg" alt="Desiree Eden&#39;s Milk &amp; Cookies" class="main-video-img ">
            <div class="video-logo">
                <a href="/site/gilfaf"><img src="BASEURL/tour/images/overlays/gilfaf.png" /></a>
            </div>
        </a>
    </div>
    <div class="video-card-text">
        <h4 class="title">
            <a href="/video/3439/desiree-edens-milk-cookies" title="">
                Desiree Eden's Milk &amp; Cook...
            </a>
        </h4>
        <div class="meta">
            <div>
                <a href="/model/desiree-eden" class="tag-btn">Desiree Eden</a>
            </div>
            <span class="date">2026-05-22</span>
        </div>
    </div>
</div><!-- End video-card-->
<div class="video-card">
    <div class="video-card-img img-hover-zoom">
        <a href="/video/3438/mia-river-makes-him-rain-cum" class="v-thumb">
            <img src="BASEURL/tour-content/mylked/main.jpg" alt="Mia River Makes Him Rain Cum" class="main-video-img ">
            <div class="video-logo">
                <a href="/site/mylked"><img src="BASEURL/tour/images/overlays/mylked.png" /></a>
            </div>
        </a>
    </div>
    <div class="video-card-text">
        <h4 class="title">
            <a href="/video/3438/mia-river-makes-him-rain-cum" title="">
                Mia River Makes Him Rain Cum
            </a>
        </h4>
        <div class="meta">
            <div>
                <a href="/model/mia-river" class="tag-btn">Mia River</a>
            </div>
            <span class="date">2026-05-21</span>
        </div>
    </div>
</div><!-- End video-card-->
<ul class="pagination">
<li><a href="?page=1">«</a></li>
<li class="active"><span>1</span></li>
<li><a href="?page=2">2</a></li>
<li><a href="?page=3">3</a></li>
<li><a href="?page=2">»</a></li>
</ul>
</body></html>`

const testDetailPage1 = `<!DOCTYPE html><html><head>
<meta name="description" content="Desiree Eden detail description." />
</head><body>
<h1 style="padding-bottom: 5px; padding-top: 0px;">Desiree Eden&#39;s Milk &amp; Cookies, and GILF Nookie</h1>
<h3>Update Details</h3>
<p>
    <i class="fa-regular fa-calendar"></i> Release Date: May 22, 2026
    <br />
    <i class="fa-solid fa-video"></i>

    12:34

    &nbsp;/&nbsp; <i class="fa-regular fa-images"></i> 48 Photos
</p>
<div class="video-details-block">
    <h3>Description:</h3>
    <p>Desiree Eden detail description.</p>
</div>
<div class="video-details-block video-details-tags-list">
    <h3>Tags:</h3>
    <a class="pill-link" href="/tag/gilf">GILF</a>
    <a class="pill-link" href="/tag/big-tits"> big tits</a>
    <a class="pill-link" href="/tag/handjob"> handjob</a>
</div>
<h1 class="head_line">Nookies Sites</h1>
</body></html>`

const testDetailPage2 = `<!DOCTYPE html><html><head>
<meta name="description" content="Second scene description." />
</head><body>
<h1 style="padding-bottom: 5px; padding-top: 0px;">Mia River Makes Him Rain Cum</h1>
<h3>Update Details</h3>
<p>
    <i class="fa-regular fa-calendar"></i> Release Date: May 21, 2026
    <br />
    <i class="fa-solid fa-video"></i>

    07:52
</p>
<div class="video-details-block">
    <h3>Description:</h3>
    <p>Second scene description.</p>
</div>
<div class="video-details-block video-details-tags-list">
    <h3>Tags:</h3>
    <a class="pill-link" href="/tag/petite">Petite</a>
    <a class="pill-link" href="/tag/cumshot"> cumshot</a>
</div>
</body></html>`

func testListingWithBase(base string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html><body>
<div class="video-card">
    <div class="video-card-img img-hover-zoom">
        <a href="/video/3439/desiree-edens-milk-cookies" class="v-thumb">
            <img src="%s/tour-content/gilfaf/main.jpg" alt="Desiree Eden&#39;s Milk &amp; Cookies" class="main-video-img ">
            <div class="video-logo">
                <a href="/site/gilfaf"><img src="%s/tour/images/overlays/gilfaf.png" /></a>
            </div>
        </a>
    </div>
    <div class="video-card-text">
        <h4 class="title">
            <a href="/video/3439/desiree-edens-milk-cookies" title="">Desiree Eden's Milk &amp; Cook...</a>
        </h4>
        <div class="meta">
            <div><a href="/model/desiree-eden" class="tag-btn">Desiree Eden</a></div>
            <span class="date">2026-05-22</span>
        </div>
    </div>
</div><!-- End video-card-->
<div class="video-card">
    <div class="video-card-img img-hover-zoom">
        <a href="/video/3438/mia-river-makes-him-rain-cum" class="v-thumb">
            <img src="%s/tour-content/mylked/main.jpg" alt="Mia River Makes Him Rain Cum" class="main-video-img ">
            <div class="video-logo">
                <a href="/site/mylked"><img src="%s/tour/images/overlays/mylked.png" /></a>
            </div>
        </a>
    </div>
    <div class="video-card-text">
        <h4 class="title">
            <a href="/video/3438/mia-river-makes-him-rain-cum" title="">Mia River Makes Him Rain Cum</a>
        </h4>
        <div class="meta">
            <div><a href="/model/mia-river" class="tag-btn">Mia River</a></div>
            <span class="date">2026-05-21</span>
        </div>
    </div>
</div><!-- End video-card-->
<ul class="pagination">
<li><a href="?page=1">«</a></li>
<li class="active"><span>1</span></li>
<li><a href="?page=2">2</a></li>
<li><a href="?page=3">3</a></li>
<li><a href="?page=2">»</a></li>
</ul>
</body></html>`, base, base, base, base)
}

func TestParseListingPage(t *testing.T) {
	items := parseListingPage([]byte(testListingPage))
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	it := items[0]
	if it.id != "3439" {
		t.Errorf("id = %q, want 3439", it.id)
	}
	if it.url != "/video/3439/desiree-edens-milk-cookies" {
		t.Errorf("url = %q", it.url)
	}
	if it.title != "Desiree Eden's Milk & Cookies" {
		t.Errorf("title = %q", it.title)
	}
	if it.subSite != "gilfaf" {
		t.Errorf("subSite = %q, want gilfaf", it.subSite)
	}
	want := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	if !it.date.Equal(want) {
		t.Errorf("date = %v, want %v", it.date, want)
	}
	if len(it.performers) != 1 || it.performers[0] != "Desiree Eden" {
		t.Errorf("performers = %v", it.performers)
	}

	it2 := items[1]
	if it2.id != "3438" {
		t.Errorf("id = %q, want 3438", it2.id)
	}
	if it2.title != "Mia River Makes Him Rain Cum" {
		t.Errorf("title = %q", it2.title)
	}
	if it2.subSite != "mylked" {
		t.Errorf("subSite = %q, want mylked", it2.subSite)
	}
}

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(testDetailPage1))

	if d.title != "Desiree Eden's Milk & Cookies, and GILF Nookie" {
		t.Errorf("title = %q", d.title)
	}
	if d.duration != 754 {
		t.Errorf("duration = %d, want 754", d.duration)
	}
	if d.description != "Desiree Eden detail description." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 3 {
		t.Errorf("tags = %v, want 3", d.tags)
	}
}

func TestEstimateTotal(t *testing.T) {
	body := []byte(testListingPage)
	total := estimateTotal(body, 24)
	if total != 72 {
		t.Errorf("estimateTotal = %d, want 72 (3 pages * 24)", total)
	}
}

func TestHasNextPage(t *testing.T) {
	if !hasNextPage([]byte(testListingPage)) {
		t.Error("expected next page")
	}
	if hasNextPage([]byte(`<html><body>no pagination</body></html>`)) {
		t.Error("expected no next page")
	}
}

func TestPagination(t *testing.T) {
	page1Called := false
	page2Called := false
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos":
			if r.URL.Query().Get("page") == "2" {
				page2Called = true
				w.WriteHeader(200)
				return
			}
			page1Called = true
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/video/3439/desiree-edens-milk-cookies":
			_, _ = fmt.Fprint(w, testDetailPage1)
		case "/video/3438/mia-river-makes-him-rain-cum":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if !page1Called || !page2Called {
		t.Errorf("page1=%v page2=%v, both should be true", page1Called, page2Called)
	}
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
	if len(scenes) >= 1 && scenes[0] != "3439" {
		t.Errorf("scene[0] = %q", scenes[0])
	}
}

func TestDetailEnrichment(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos":
			if r.URL.Query().Get("page") != "" {
				w.WriteHeader(200)
				return
			}
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/video/3439/desiree-edens-milk-cookies":
			_, _ = fmt.Fprint(w, testDetailPage1)
		case "/video/3438/mia-river-makes-him-rain-cum":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			w.WriteHeader(200)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var got []scraper.SceneResult
	for r := range ch {
		if r.Kind == scraper.KindScene {
			got = append(got, r)
		}
	}

	if len(got) < 1 {
		t.Fatal("expected at least 1 scene")
	}

	scene := got[0].Scene
	if scene.Title != "Desiree Eden's Milk & Cookies, and GILF Nookie" {
		t.Errorf("title = %q, want full title from detail", scene.Title)
	}
	if scene.Duration != 754 {
		t.Errorf("duration = %d, want 754", scene.Duration)
	}
	if scene.Description != "Desiree Eden detail description." {
		t.Errorf("description = %q", scene.Description)
	}
	if len(scene.Tags) != 3 {
		t.Errorf("tags = %v, want 3", scene.Tags)
	}
	if scene.SiteID != "gilfaf" {
		t.Errorf("siteID = %q, want gilfaf", scene.SiteID)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Desiree Eden" {
		t.Errorf("performers = %v", scene.Performers)
	}
}

func TestEarlyStop(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos":
			if r.URL.Query().Get("page") != "" {
				w.WriteHeader(200)
				return
			}
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/video/3439/desiree-edens-milk-cookies":
			_, _ = fmt.Fprint(w, testDetailPage1)
		case "/video/3438/mia-river-makes-him-rain-cum":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			w.WriteHeader(200)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	known := map[string]bool{"3438": true}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{KnownIDs: known})
	if err != nil {
		t.Fatal(err)
	}

	var scenes []string
	stoppedEarly := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		}
	}

	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://nookies.com/", true},
		{"https://www.nookies.com/videos", true},
		{"https://nookies.com/site/clubtug", true},
		{"https://nookies.com/model/jessica-ryan", true},
		{"https://nookies.com/tag/handjob", true},
		{"https://example.com/", false},
		// New-CMS standalone domains.
		{"https://www.milfaf.com/", true},
		{"https://milfaf.com/videos", true},
		{"https://www.gilfaf.com/", true},
		{"https://www.breedme.com/tag/creampie", true},
		{"https://www.shadyspa.com/models/london-river", true},
		{"https://www.over40handjobs.com/", true},
		{"https://over40handjobs.com/videos", true},
		{"https://teentugs.com/", false}, // legacy domain — scraped via nookies hub only
	}
	for _, tc := range tests {
		if got := s.MatchesURL(tc.url); got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// ---- new CMS (own-domain VideoObject) ----

const testNewListingPage = `<!DOCTYPE html><html><body>
<a href="https://www.milfaf.com/video/3458"><img src="x.jpg"></a>
<h2><a href="https://www.milfaf.com/video/3458">Title A</a></h2>
<a href="https://www.milfaf.com/video/3453"><img src="y.jpg"></a>
<a href="https://www.milfaf.com/video/3453" class="watch">Watch</a>
<a href="/videos?page=2">2</a>
<a href="/videos?page=3">3</a>
</body></html>`

const testNewDetailPage = `<!DOCTYPE html><html><head>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[]}
</script>
<script type="application/ld+json">
{
  "@context":"https://schema.org",
  "@type":"VideoObject",
  "name":"MILF Ms. Amanda Teaches Young Cub",
  "description":"A long description here.",
  "thumbnailUrl":"https://nookies.com/tour-content/milfaf/Amanda-MFAF/main.jpg",
  "url":"https://www.milfaf.com/video/3458",
  "uploadDate":"2026-06-16T00:00:00+00:00",
  "duration":"PT25M21S",
  "contentUrl":"https://nookies.com/tour-trailers/3458.mp4",
  "actor":[{"@type":"Person","name":"Ms Amanda"}],
  "genre":["hardcore","blonde","big tits","milf"]
}
</script>
</head><body></body></html>`

func TestNewListVideoIDs(t *testing.T) {
	ids := newListVideoIDs([]byte(testNewListingPage))
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want 2", ids)
	}
	if ids[0] != "3458" || ids[1] != "3453" {
		t.Errorf("ids = %v, want [3458 3453] in order", ids)
	}
}

func TestMaxPageNum(t *testing.T) {
	if n := maxPageNum([]byte(testNewListingPage)); n != 3 {
		t.Errorf("maxPageNum = %d, want 3", n)
	}
	if n := maxPageNum([]byte(`<html>no pages</html>`)); n != 1 {
		t.Errorf("maxPageNum = %d, want 1", n)
	}
}

func TestParseGenre(t *testing.T) {
	tags := parseGenre([]byte(testNewDetailPage))
	want := []string{"hardcore", "blonde", "big tits", "milf"}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Errorf("tags[%d] = %q, want %q", i, tags[i], want[i])
		}
	}
}

func TestNewScene(t *testing.T) {
	sc, ok := newScene([]byte(testNewDetailPage), "3458", "milfaf",
		"https://www.milfaf.com", "https://www.milfaf.com/", time.Now().UTC())
	if !ok {
		t.Fatal("newScene returned ok=false")
	}
	if sc.ID != "3458" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "milfaf" {
		t.Errorf("SiteID = %q, want milfaf", sc.SiteID)
	}
	if sc.Studio != "MilfAF" {
		t.Errorf("Studio = %q, want MilfAF", sc.Studio)
	}
	if sc.Title != "MILF Ms. Amanda Teaches Young Cub" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.URL != "https://www.milfaf.com/video/3458" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Duration != 25*60+21 {
		t.Errorf("Duration = %d, want 1521", sc.Duration)
	}
	if sc.Preview != "https://nookies.com/tour-trailers/3458.mp4" {
		t.Errorf("Preview = %q", sc.Preview)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Ms Amanda" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 4 {
		t.Errorf("Tags = %v, want 4", sc.Tags)
	}
	want := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
}

// testNewDetailPageNoGenre mirrors a brand (over40handjobs) whose VideoObject
// JSON-LD omits "genre" — tags must come from the visible tag pills instead.
const testNewDetailPageNoGenre = `<!DOCTYPE html><html><head>
<script type="application/ld+json">
{
  "@context":"https://schema.org",
  "@type":"VideoObject",
  "name":"Blonde MILF Bella Bare Awesome Tugjob",
  "description":"A description.",
  "thumbnailUrl":"https://nookies.com/tour-content/over40handjobs/main.jpg",
  "uploadDate":"2026-05-15T00:00:00+00:00",
  "duration":"PT9M54S",
  "actor":[{"@type":"Person","name":"Bella Bare"}]
}
</script>
</head><body>
<a href="/tag/blonde">blonde</a>
<a href="/tag/handjob">Handjob</a>
<a href="/tag/handjob">Handjob</a>
</body></html>`

func TestSceneTagsFallsBackToTagPills(t *testing.T) {
	tags := sceneTags([]byte(testNewDetailPageNoGenre))
	want := []string{"blonde", "Handjob"}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Errorf("tags[%d] = %q, want %q", i, tags[i], want[i])
		}
	}
}

func TestSceneTagsPrefersGenre(t *testing.T) {
	// testNewDetailPage has a genre[] array and no tag pills at all — the
	// genre path must be used, not silently fall through.
	tags := sceneTags([]byte(testNewDetailPage))
	if len(tags) != 4 {
		t.Errorf("tags = %v, want 4 genre tags", tags)
	}
}

func TestNewSceneNoGenreUsesTagPills(t *testing.T) {
	sc, ok := newScene([]byte(testNewDetailPageNoGenre), "3430", "over40handjobs",
		"https://www.over40handjobs.com", "https://www.over40handjobs.com/videos", time.Now().UTC())
	if !ok {
		t.Fatal("newScene returned ok=false")
	}
	if sc.Studio != "Over 40 Handjobs" {
		t.Errorf("Studio = %q, want %q", sc.Studio, "Over 40 Handjobs")
	}
	want := []string{"blonde", "Handjob"}
	if len(sc.Tags) != len(want) {
		t.Fatalf("Tags = %v, want %v", sc.Tags, want)
	}
	for i := range want {
		if sc.Tags[i] != want[i] {
			t.Errorf("Tags[%d] = %q, want %q", i, sc.Tags[i], want[i])
		}
	}
}

func TestNewCMSBaseAndPath(t *testing.T) {
	tests := []struct {
		url, slug, wantBase, wantPath string
	}{
		{"https://www.milfaf.com/", "milfaf", "https://www.milfaf.com", "/videos"},
		{"https://www.milfaf.com/videos", "milfaf", "https://www.milfaf.com", "/videos"},
		{"https://www.gilfaf.com/tag/gilf", "gilfaf", "https://www.gilfaf.com", "/tag/gilf"},
		{"https://www.milfaf.com/models/london-river", "milfaf", "https://www.milfaf.com", "/models/london-river"},
	}
	for _, tc := range tests {
		base, path := newCMSBaseAndPath(tc.url, tc.slug)
		if base != tc.wantBase || path != tc.wantPath {
			t.Errorf("newCMSBaseAndPath(%q) = (%q, %q), want (%q, %q)", tc.url, base, path, tc.wantBase, tc.wantPath)
		}
	}
}

func TestSiteMode(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/site/clubtug":
			if r.URL.Query().Get("page") != "" {
				w.WriteHeader(200)
				return
			}
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/video/3439/desiree-edens-milk-cookies":
			_, _ = fmt.Fprint(w, testDetailPage1)
		case "/video/3438/mia-river-makes-him-rain-cum":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			w.WriteHeader(200)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/site/clubtug", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			count++
		}
	}

	if count != 2 {
		t.Errorf("got %d scenes, want 2", count)
	}
}

func TestModelMode(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/model/jessica-ryan":
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/video/3439/desiree-edens-milk-cookies":
			_, _ = fmt.Fprint(w, testDetailPage1)
		case "/video/3438/mia-river-makes-him-rain-cum":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/model/jessica-ryan", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			count++
		}
	}

	if count != 2 {
		t.Errorf("got %d scenes, want 2", count)
	}
}

// The new CMS's only end-of-listing signal used to be an empty page, which
// costs one request past the end and — on an out-of-range page — a pager that
// names the page you asked for. Reading the pager ends the walk on the last
// real page instead.
func TestRunNewCMSStopsOnTheLastPagerPage(t *testing.T) {
	listing := func(page, maxPage int, ids ...string) string {
		var b strings.Builder
		b.WriteString(`<html><body>`)
		for _, id := range ids {
			fmt.Fprintf(&b, `<a href="/video/%s">x</a>`, id)
		}
		for p := 1; p <= maxPage; p++ {
			fmt.Fprintf(&b, `<a href="/videos?page=%d">%d</a>`, p, p)
		}
		b.WriteString(`</body></html>`)
		return b.String()
	}

	var listingPages []int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/video/") {
			id := strings.TrimPrefix(r.URL.Path, "/video/")
			_, _ = fmt.Fprintf(w, `<html><head><script type="application/ld+json">`+
				`{"@type":"VideoObject","name":"Scene %s","description":"d"}`+
				`</script></head><body></body></html>`, id)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		listingPages = append(listingPages, page)
		switch page {
		case 1:
			_, _ = fmt.Fprint(w, listing(1, 2, "101", "102"))
		case 2:
			_, _ = fmt.Fprint(w, listing(2, 2, "201"))
		default:
			// Past the end the CMS serves a pager that names the page asked
			// for and no cards at all.
			_, _ = fmt.Fprint(w, listing(page, page))
		}
	}))
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	out := make(chan scraper.SceneResult, 32)
	s.runNewCMS(context.Background(), ts.URL+"/videos", "over40handjobs", scraper.ListOpts{Workers: 1}, out)
	close(out)

	var scenes int
	for r := range out {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3", scenes)
	}
	if len(listingPages) != 2 {
		t.Errorf("fetched listing pages %v, want just 1 and 2", listingPages)
	}
}

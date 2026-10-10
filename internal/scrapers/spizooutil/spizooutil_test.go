package spizooutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const testListingPage = `<!DOCTYPE html><html><body>
<p>Page 1 of 3</p>
<a href="movies_1_d.html">1</a> <a href="movies_2_d.html">2</a> <a href="movies_3_d.html">3</a>
<div class="thumb-pic">
	<a href="https://www.spizoo.com/updates/Scene-One-Title.html">
		<img src="https://contentsmall.spizoo.com/tour/content/scene1/2.jpg?w=570" alt="Scene One Title" class="large_update_thumb img-responsive thumbs" />
	</a>
</div>
<div class="thumb-data">
	<div class="thumb-info">
		<div class="title-label">
			<a title="" href="">Scene One Title</a>
		</div>
		<div class="pornstar-label">
			<span class="update_models">
				<span class="tour_update_models">
					<a href="https://www.spizoo.com/models/Jane-Doe.html" title="Jane Doe">Jane Doe</a>
				</span>
			</span>
		</div>
	</div>
</div>
</div>
</div>
<div class="thumb-pic">
	<a href="https://www.spizoo.com/updates/Scene-Two-Title.html">
		<img src="https://contentsmall.spizoo.com/tour/content/scene2/2.jpg?w=570" alt="Scene Two" class="large_update_thumb img-responsive thumbs" />
	</a>
</div>
<div class="thumb-data">
	<div class="thumb-info">
		<div class="title-label">
			<a title="" href="">Scene Two &amp; More</a>
		</div>
		<div class="pornstar-label">
			<span class="update_models">
				<span class="tour_update_models">
					<a href="https://www.spizoo.com/models/Alice.html" title="Alice">Alice</a>
				</span>
				<span class="tour_update_models">
					<a href="https://www.spizoo.com/models/Bob.html" title="Bob">Bob</a>
				</span>
			</span>
		</div>
	</div>
</div>
</div>
</div>
</body></html>`

const testDetailPage = `<!DOCTYPE html><html><head>
<meta name="description" content="An amazing scene with great performers." />
</head><body>
<h1>Scene One Title</h1>
<div class="row line data-others">
	<div class="col-3">
		<h3>Pornstars:</h3>
		<a href='/models/Jane-Doe.html' title="Jane Doe">Jane Doe. </a>
		<a href='/models/John-Smith.html' title="John Smith">John Smith. </a>
	</div>
	<div class="col-3">
		<h3>Release Date:</h3>
		<p class="date">2026-05-20</p>
	</div>
	<div class="col-3">
		<h4>Length:</h4>
		<p>
			34:39
		</p>
	</div>
</div>
<div class="row">
	<div class="col-12">
		<h3>Categories:</h3>
		<a href="#" title="Big Tits" class="  category-tag">Big Tits</a>
		<a href="#" title="Blowjob" class="  category-tag">Blowjob</a>
		<a href="#" title="4k" class="  category-tag">4k</a>
	</div>
</div>
</body></html>`

const testDetailPage2 = `<!DOCTYPE html><html><head>
<meta name="description" content="Second scene description." />
</head><body>
<h1>Scene Two &amp; More</h1>
<div class="row line data-others">
	<div class="col-3">
		<h3>Pornstars:</h3>
		<a href='/models/Alice.html' title="Alice">Alice. </a>
		<a href='/models/Bob.html' title="Bob">Bob. </a>
	</div>
	<div class="col-3">
		<h3>Release Date:</h3>
		<p class="date">2026-05-15</p>
	</div>
	<div class="col-3">
		<h4>Length:</h4>
		<p>22:10</p>
	</div>
</div>
<div class="row">
	<div class="col-12">
		<h3>Categories:</h3>
		<a href="#" title="Threesome" class="  category-tag">Threesome</a>
	</div>
</div>
</body></html>`

func TestParseListingPage(t *testing.T) {
	items := parseListingPage([]byte(testListingPage))
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	it := items[0]
	if it.slug != "Scene-One-Title" {
		t.Errorf("slug = %q, want Scene-One-Title", it.slug)
	}
	if it.url != "https://www.spizoo.com/updates/Scene-One-Title.html" {
		t.Errorf("url = %q", it.url)
	}
	if it.title != "Scene One Title" {
		t.Errorf("title = %q", it.title)
	}
	if it.thumbnail != "https://contentsmall.spizoo.com/tour/content/scene1/2.jpg?w=570" {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
	if len(it.performers) != 1 || it.performers[0] != "Jane Doe" {
		t.Errorf("performers = %v", it.performers)
	}

	it2 := items[1]
	if it2.slug != "Scene-Two-Title" {
		t.Errorf("slug = %q, want Scene-Two-Title", it2.slug)
	}
	if it2.title != "Scene Two & More" {
		t.Errorf("title = %q", it2.title)
	}
	if len(it2.performers) != 2 {
		t.Errorf("performers = %v, want 2", it2.performers)
	}
}

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(testDetailPage))

	want := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	if !d.date.Equal(want) {
		t.Errorf("date = %v, want %v", d.date, want)
	}
	if d.duration != 2079 {
		t.Errorf("duration = %d, want 2079", d.duration)
	}
	if len(d.performers) != 2 || d.performers[0] != "Jane Doe" || d.performers[1] != "John Smith" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.description != "An amazing scene with great performers." {
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

func TestExtractSlug(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.spizoo.com/updates/Scene-One-Title.html", "Scene-One-Title"},
		{"https://www.firstclasspov.com/updates/Some-Scene.html", "Some-Scene"},
		{"https://www.spizoo.com/models/Jane.html", ""},
	}
	for _, tc := range tests {
		if got := extractSlug(tc.url); got != tc.want {
			t.Errorf("extractSlug(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func testListingWithBase(base string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html><body>
<p>Page 1 of 3</p>
<a href="movies_1_d.html">1</a> <a href="movies_2_d.html">2</a> <a href="movies_3_d.html">3</a>
<div class="thumb-pic">
	<a href="%s/updates/Scene-One-Title.html">
		<img src="https://contentsmall.spizoo.com/tour/content/scene1/2.jpg?w=570" alt="Scene One Title" class="large_update_thumb img-responsive thumbs" />
	</a>
</div>
<div class="thumb-data">
	<div class="thumb-info">
		<div class="title-label">
			<a title="" href="">Scene One Title</a>
		</div>
		<div class="pornstar-label">
			<span class="update_models">
				<span class="tour_update_models">
					<a href="%s/models/Jane-Doe.html" title="Jane Doe">Jane Doe</a>
				</span>
			</span>
		</div>
	</div>
</div>
</div>
</div>
<div class="thumb-pic">
	<a href="%s/updates/Scene-Two-Title.html">
		<img src="https://contentsmall.spizoo.com/tour/content/scene2/2.jpg?w=570" alt="Scene Two" class="large_update_thumb img-responsive thumbs" />
	</a>
</div>
<div class="thumb-data">
	<div class="thumb-info">
		<div class="title-label">
			<a title="" href="">Scene Two &amp; More</a>
		</div>
		<div class="pornstar-label">
			<span class="update_models">
				<span class="tour_update_models">
					<a href="%s/models/Alice.html" title="Alice">Alice</a>
				</span>
				<span class="tour_update_models">
					<a href="%s/models/Bob.html" title="Bob">Bob</a>
				</span>
			</span>
		</div>
	</div>
</div>
</div>
</div>
</body></html>`, base, base, base, base, base)
}

func TestPagination(t *testing.T) {
	page1Called := false
	page2Called := false
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			page1Called = true
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/categories/movies_2_d.html":
			page2Called = true
			w.WriteHeader(200)
		case "/updates/Scene-One-Title.html":
			_, _ = fmt.Fprint(w, testDetailPage)
		case "/updates/Scene-Two-Title.html":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		cfg:    SiteConfig{SiteID: "test", Domain: "test.com", StudioName: "Test"},
		client: ts.Client(),
		base:   ts.URL,
	}

	studioURL := ts.URL + "/studio/"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
			if r.Scene.StudioURL != studioURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, studioURL)
			}
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
	if len(scenes) >= 1 && scenes[0] != "Scene-One-Title" {
		t.Errorf("scene[0] = %q", scenes[0])
	}
}

func TestDetailEnrichment(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/categories/movies_2_d.html":
			w.WriteHeader(200)
		case "/updates/Scene-One-Title.html":
			_, _ = fmt.Fprint(w, testDetailPage)
		case "/updates/Scene-Two-Title.html":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		cfg:    SiteConfig{SiteID: "test", Domain: "test.com", StudioName: "Test"},
		client: ts.Client(),
		base:   ts.URL,
	}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
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
	if scene.Duration != 2079 {
		t.Errorf("duration = %d, want 2079", scene.Duration)
	}
	want := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(want) {
		t.Errorf("date = %v, want %v", scene.Date, want)
	}
	if len(scene.Performers) != 2 {
		t.Errorf("performers = %v, want 2", scene.Performers)
	}
	if scene.Description != "An amazing scene with great performers." {
		t.Errorf("description = %q", scene.Description)
	}
	if len(scene.Tags) != 3 {
		t.Errorf("tags = %v, want 3", scene.Tags)
	}
}

func TestEarlyStop(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, testListingWithBase(ts.URL))
		case "/updates/Scene-One-Title.html":
			_, _ = fmt.Fprint(w, testDetailPage)
		case "/updates/Scene-Two-Title.html":
			_, _ = fmt.Fprint(w, testDetailPage2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		cfg:    SiteConfig{SiteID: "test", Domain: "test.com", StudioName: "Test"},
		client: ts.Client(),
		base:   ts.URL,
	}

	known := map[string]bool{"Scene-Two-Title": true}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{KnownIDs: known})
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
	s := New(SiteConfig{SiteID: "spizoo", Domain: "spizoo.com", StudioName: "Spizoo"})
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.spizoo.com/", true},
		{"https://spizoo.com/categories/movies_1_d.html", true},
		{"https://www.spizoo.com/models/Jane.html", true},
		{"https://example.com/", false},
	}
	for _, tc := range tests {
		if got := s.MatchesURL(tc.url); got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

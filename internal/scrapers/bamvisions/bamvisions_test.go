package bamvisions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

const testEpisodeHTML = `<div class="item-episode">
	<div class="item-thumbs">
		<span class="left">
			<a href="https://tour.bamvisions.com/trailers/Test-Scene-Title.html" title="Test Scene Title">
				<img id="set-target-922" width="765" height="430" alt="" class="mainThumb thumbs stdimage" src0_1x="/content/contentthumbs/89/15/8915-1x.jpg" src0_2x="/content/contentthumbs/89/15/8915-2x.jpg" src0_3x="/content/contentthumbs/89/15/8915-3x.jpg" />
			</a>
		</span>
	</div>
	<div class="item-info">
		<div class="item-title-row">
			<div class="right">
				<h3>
					<a href="https://tour.bamvisions.com/trailers/Test-Scene-Title.html" title="Test Scene Title">
						Test Scene Title					</a>
				</h3>
				<div class="fake-h5">
			<a href="https://tour.bamvisions.com/models/JaneDoe.html">Jane Doe</a>
</div>
			</div>
		</div>
		<p class="description"></p>
		<ul class="item-meta">
			<li><i class="fa fa-calendar"></i> <strong>Release Date: </strong> April 20, 2026</li>
			<li><i class="fa fa-play"></i> <strong>Length: </strong> 37:02</li>
		</ul>
	</div>
</div><!--//item-episode-->`

const testEpisodeMultiPerf = `<div class="item-episode">
	<div class="item-thumbs">
		<span class="left">
			<a href="https://tour.bamvisions.com/trailers/Multi-Perf.html" title="Multi Perf">
				<img id="set-target-800" src0_1x="/content/contentthumbs/80/00/8000-1x.jpg" />
			</a>
		</span>
	</div>
	<div class="item-info">
		<div class="item-title-row">
			<div class="right">
				<h3>
					<a href="https://tour.bamvisions.com/trailers/Multi-Perf.html" title="Multi Perf">Multi Perf</a>
				</h3>
				<div class="fake-h5">
			<a href="/models/Alice.html">Alice</a>, <a href="/models/Bob.html">Bob</a>
</div>
			</div>
		</div>
		<ul class="item-meta">
			<li><strong>Release Date: </strong> March 15, 2025</li>
			<li><strong>Length: </strong> 25:30</li>
		</ul>
	</div>
</div><!--//item-episode-->`

func TestParseListingPage(t *testing.T) {
	body := []byte(testEpisodeHTML + testEpisodeMultiPerf)
	scenes := parseListingPage(body, "https://tour.bamvisions.com")
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "922" {
		t.Errorf("id = %q, want 922", s.id)
	}
	if s.title != "Test Scene Title" {
		t.Errorf("title = %q, want %q", s.title, "Test Scene Title")
	}
	if s.date.Format("2006-01-02") != "2026-04-20" {
		t.Errorf("date = %v, want 2026-04-20", s.date)
	}
	if s.duration != 2222 {
		t.Errorf("duration = %d, want 2222", s.duration)
	}
	if len(s.performers) != 1 || s.performers[0] != "Jane Doe" {
		t.Errorf("performers = %v, want [Jane Doe]", s.performers)
	}
	if s.thumb != "https://tour.bamvisions.com/content/contentthumbs/89/15/8915-1x.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
	if s.url != "https://tour.bamvisions.com/trailers/Test-Scene-Title.html" {
		t.Errorf("url = %q", s.url)
	}

	s2 := scenes[1]
	if s2.id != "800" {
		t.Errorf("id = %q, want 800", s2.id)
	}
	if len(s2.performers) != 2 || s2.performers[0] != "Alice" || s2.performers[1] != "Bob" {
		t.Errorf("performers = %v, want [Alice Bob]", s2.performers)
	}
	if s2.duration != 1530 {
		t.Errorf("duration = %d, want 1530", s2.duration)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"37:02", 2222},
		{"5:00", 300},
		{"0:30", 30},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseLastPage(t *testing.T) {
	body := []byte(`<a href="https://tour.bamvisions.com/categories/movies/50/latest/">&gt;&gt;</a>`)
	if got := parseLastPage(body); got != 50 {
		t.Errorf("parseLastPage = %d, want 50", got)
	}
}

const episodeTpl = `<div class="item-episode">
	<div class="item-thumbs">
		<span class="left">
			<a href="/trailers/Scene-%d.html" title="Scene %d">
				<img id="set-target-%d" src0_1x="/content/contentthumbs/%d-1x.jpg" />
			</a>
		</span>
	</div>
	<div class="item-info">
		<div class="item-title-row">
			<div class="right">
				<h3><a href="/trailers/Scene-%d.html" title="Scene %d">Scene %d</a></h3>
				<div class="fake-h5"><a href="/models/test.html">Test</a></div>
			</div>
		</div>
		<ul class="item-meta">
			<li><strong>Release Date: </strong> January 1, 2025</li>
			<li><strong>Length: </strong> 10:00</li>
		</ul>
	</div>
</div><!--//item-episode-->`

func buildTestPage(ids []int, lastPage int) []byte {
	var sb string
	for _, id := range ids {
		sb += fmt.Sprintf(episodeTpl, id, id, id, id, id, id, id)
	}
	pager := ""
	if lastPage > 0 {
		pager = fmt.Sprintf(`<a href="/categories/movies/%d/latest/">&gt;&gt;</a>`, lastPage)
	}
	return []byte(sb + pager)
}

func newTestServer(pages [][]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		var pageNum int
		_, _ = fmt.Sscanf(r.URL.Path, "/categories/movies/%d/latest/", &pageNum)
		if pageNum == 0 {
			pageNum = 1
		}
		idx := pageNum - 1
		if idx >= 0 && idx < len(pages) {
			_, _ = w.Write(buildTestPage(pages[idx], len(pages)))
		} else {
			_, _ = fmt.Fprint(w, `<div>empty</div>`)
		}
	}))
}

func TestListScenes(t *testing.T) {
	ts := newTestServer([][]int{{100, 200}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Title != "Scene 100" {
		t.Errorf("title = %q, want Scene 100", results[0].Title)
	}
	if results[0].SiteID != siteID {
		t.Errorf("siteID = %q, want %q", results[0].SiteID, siteID)
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := newTestServer([][]int{{1, 2, 3, 4}})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stopped := testutil.CollectScenesWithStop(t, ch)
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3 — the page is finished before the stop", len(results))
	}
}

func TestListScenesModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(buildTestPage([]int{100, 200, 300}, 0))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	modelURL := ts.URL + "/models/JaneDoe.html"
	ch, err := s.ListScenes(context.Background(), modelURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3", len(results))
	}
	if results[0].StudioURL != modelURL {
		t.Errorf("StudioURL = %q, want %q", results[0].StudioURL, modelURL)
	}
}

func TestListScenesModelKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(buildTestPage([]int{100, 200, 300}, 0))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/JaneDoe.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"200": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2 (skip known ID 200)", len(results))
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := []int{10, 20, 30}
	page2 := []int{40, 50}

	ts := newTestServer([][]int{page1, page2})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 5 {
		t.Fatalf("got %d scenes, want 5", len(results))
	}
}

package rawalphamales

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		{"https://www.rawalphamales.com", true},
		{"https://rawalphamales.com/video/some-scene/", true},
		{"http://www.rawalphamales.com/category/feet/", true},
		{"https://www.lucasentertainment.com", false},
		{"https://www.example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

const listingHTML = `<html><body>
<div class="video-container featured-video">
	<h1>
		<small>Apr 15th, 2026</small>
		<a href="/video/zac-covington-rides-michael-lucas/">Zac Covington Rides Michael Lucas&#8217;s Uncut Cock</a>
	</h1>
	<div class="row">
		<img class="lazy a1" data-original="/content/movies/RAM327/RAM327.jpg">
	</div>
</div>
<div class="video-container post-content">
	<h3>
		<small>Mar 25th, 2026</small>
		<a href="/video/jake-morgan-bottoms-for-michael-lucas/" title="Jake Morgan Bottoms For Michael Lucas">Jake Morgan Bottoms For Michael Lucas</a>
	</h3>
	<div class="single-image">
		<img class="lazy sceneimage0" data-original="/content/movies/RAM326/RAM326-A_video.jpg">
	</div>
</div>
<div class="video-container post-content">
	<h3>
		<small>Mar 4th, 2026</small>
		<a href="/video/kosta-viking-slams-steven-angel-fully-edited/" title="Kosta Viking Slams Steven Angel | Fully Edited">Kosta Viking Slams Steven Angel | Fully Edited</a>
	</h3>
	<div class="single-image">
		<img class="lazy sceneimage0" data-original="/content/movies/RVD003-05/RVD003-05.jpg">
	</div>
</div>
<div class='wp-pagenavi'>
<span class='pages'>Page 1 of 3</span>
<span class='current'>1</span>
<a class="page larger" href="/page/2/">2</a>
<a class="nextpostslink" rel="next" href="/page/2/">»</a>
<a class="last" href="/page/3/">Last »</a>
</div>
</body></html>`

func TestParseListingEntries(t *testing.T) {
	entries := parseListingEntries([]byte(listingHTML), "https://www.rawalphamales.com")
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	e := entries[0]
	if e.slug != "zac-covington-rides-michael-lucas" {
		t.Errorf("slug = %q", e.slug)
	}
	if e.title != "Zac Covington Rides Michael Lucas’s Uncut Cock" {
		t.Errorf("title = %q", e.title)
	}
	if e.date != "Apr 15th, 2026" {
		t.Errorf("date = %q", e.date)
	}
	if e.thumbnail != "https://www.rawalphamales.com/content/movies/RAM327/RAM327.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}

	e2 := entries[1]
	if e2.slug != "jake-morgan-bottoms-for-michael-lucas" {
		t.Errorf("slug = %q", e2.slug)
	}
	if e2.thumbnail != "https://www.rawalphamales.com/content/movies/RAM326/RAM326-A_video.jpg" {
		t.Errorf("thumbnail = %q", e2.thumbnail)
	}

	e3 := entries[2]
	if e3.slug != "kosta-viking-slams-steven-angel-fully-edited" {
		t.Errorf("slug = %q", e3.slug)
	}
}

func TestParseTotalPages(t *testing.T) {
	if got := parseTotalPages([]byte(listingHTML)); got != 3 {
		t.Errorf("parseTotalPages = %d, want 3", got)
	}
}

func TestHasNextPage(t *testing.T) {
	if !hasNextPage([]byte(listingHTML), 1) {
		t.Error("expected hasNextPage(1) = true")
	}
	noNext := `<div class='wp-pagenavi'><span class='pages'>Page 3 of 3</span></div>`
	if hasNextPage([]byte(noNext), 3) {
		t.Error("expected hasNextPage(3) = false")
	}
}

func TestParseOrdinalDate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Apr 15th, 2026", "2026-04-15"},
		{"Mar 25th, 2026", "2026-03-25"},
		{"Sep 3rd, 2025", "2025-09-03"},
		{"Jan 1st, 2020", "2020-01-01"},
		{"Jun 22nd, 2019", "2019-06-22"},
	}
	for _, tt := range tests {
		got := parseOrdinalDate(tt.input)
		if got.IsZero() {
			t.Errorf("parseOrdinalDate(%q) returned zero", tt.input)
			continue
		}
		if got.Format("2006-01-02") != tt.want {
			t.Errorf("parseOrdinalDate(%q) = %s, want %s", tt.input, got.Format("2006-01-02"), tt.want)
		}
	}
}

func TestExtractPerformers(t *testing.T) {
	tests := []struct {
		title string
		want  []string
	}{
		{"Jake Morgan Bottoms For Michael Lucas", []string{"Jake Morgan", "Michael Lucas"}},
		{"Kosta Viking Slams Steven Angel | Fully Edited", []string{"Kosta Viking", "Steven Angel"}},
		{"Manuel Skye Pounds Drew Dixon | Fully Edited", []string{"Manuel Skye", "Drew Dixon"}},
		{"Zac Covington Rides Michael Lucas’s Uncut Cock", []string{"Zac Covington", "Michael Lucas"}},
		{"Dylan James Plows Bottom Twink Nil Roma | Fully Edited", []string{"Dylan James", "Nil Roma"}},
	}
	for _, tt := range tests {
		got := extractPerformers(tt.title)
		if len(got) != len(tt.want) {
			t.Errorf("extractPerformers(%q) = %v, want %v", tt.title, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("extractPerformers(%q)[%d] = %q, want %q", tt.title, i, got[i], tt.want[i])
			}
		}
	}
}

const detailHTML = `<html><head>
<meta property="article:tag" content="RAM326" />
<meta property="article:section" content="Bareback Sex" />
<script type='application/ld+json'>{"@graph":[{"@type":"WebPage","datePublished":"2026-03-25T17:00:24+00:00","description":"Jake Morgan description"}]}</script>
</head><body>
<div class="video-container highlighted">
	<h1>
		<small>Mar 25th, 2026</small>
		Jake Morgan Bottoms For Michael Lucas
	</h1>
	<p class="p1">Jake Morgan didn&#8217;t just wander into Puerto Vallarta. He was personally invited by Michael Lucas.</p>
</div>
</body></html>`

func TestParseDetail(t *testing.T) {
	entry := listEntry{
		slug:      "jake-morgan-bottoms-for-michael-lucas",
		url:       "https://www.rawalphamales.com/video/jake-morgan-bottoms-for-michael-lucas/",
		title:     "Jake Morgan Bottoms For Michael Lucas",
		date:      "Mar 25th, 2026",
		thumbnail: "https://www.rawalphamales.com/content/movies/RAM326/RAM326-A_video.jpg",
	}

	scene := parseDetail([]byte(detailHTML), entry, "https://www.rawalphamales.com/category/x/")

	if scene.StudioURL != "https://www.rawalphamales.com/category/x/" {
		t.Errorf("StudioURL = %q", scene.StudioURL)
	}

	if scene.ID != "jake-morgan-bottoms-for-michael-lucas" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "rawalphamales" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "Jake Morgan Bottoms For Michael Lucas" {
		t.Errorf("title = %q", scene.Title)
	}
	if scene.Description != "Jake Morgan didn’t just wander into Puerto Vallarta. He was personally invited by Michael Lucas." {
		t.Errorf("description = %q", scene.Description)
	}
	if scene.Date.Format("2006-01-02") != "2026-03-25" {
		t.Errorf("date = %v", scene.Date)
	}
	if len(scene.Tags) != 1 || scene.Tags[0] != "Bareback Sex" {
		t.Errorf("tags = %v", scene.Tags)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Jake Morgan" || scene.Performers[1] != "Michael Lucas" {
		t.Errorf("performers = %v", scene.Performers)
	}
	if scene.Thumbnail != "https://www.rawalphamales.com/content/movies/RAM326/RAM326-A_video.jpg" {
		t.Errorf("thumbnail = %q", scene.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/page/1/":
			_, _ = fmt.Fprint(w, listingHTML)
		case "/page/2/":
			_, _ = fmt.Fprint(w, `<html><body>
<div class="video-container post-content">
	<h3>
		<small>Feb 1st, 2026</small>
		<a href="/video/fourth-scene/" title="Fourth Scene">Fourth Scene</a>
	</h3>
	<div class="single-image">
		<img class="lazy sceneimage0" data-original="/content/movies/RAM320/RAM320-A_video.jpg">
	</div>
</div>
<div class='wp-pagenavi'><span class='pages'>Page 2 of 3</span>
<a class="nextpostslink" href="/page/3/">»</a></div>
</body></html>`)
		case "/page/3/":
			_, _ = fmt.Fprint(w, "<html></html>")
		case "/video/", "/":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			_, _ = fmt.Fprint(w, detailHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.StudioURL != ts.URL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, ts.URL)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 4 {
		t.Errorf("got %d scenes, want 4", scenes)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/page/1/":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			_, _ = fmt.Fprint(w, detailHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"jake-morgan-bottoms-for-michael-lucas": true},
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

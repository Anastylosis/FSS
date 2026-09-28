package titanmen

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/parseutil"
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
		{"https://www.titanmen.com/category.php?id=5&s=d", true},
		{"https://titanmen.com/category.php?id=5&s=d", true},
		{"http://www.titanmen.com/sets.php?id=8", true},
		{"https://www.titanmen.com/dvds.php?id=244&sceneid=3321", true},
		{"https://www.titanmen.com/dvds.php?id=244", true},
		{"https://www.titanmen.com/", true},
		{"https://www.example.com/", false},
		{"https://titanmenstore.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestDetectMode(t *testing.T) {
	tests := []struct {
		url  string
		want urlMode
	}{
		{"https://www.titanmen.com/category.php?id=5&s=d", modeCategory},
		{"https://www.titanmen.com/category.php?id=13&s=d", modeCategory},
		{"https://www.titanmen.com/sets.php?id=8", modeModel},
		{"https://www.titanmen.com/dvds.php?id=244", modeDVD},
		{"https://www.titanmen.com/dvds.php?id=244&sceneid=3321", modeScenes},
		{"https://www.titanmen.com/", modeScenes},
	}
	for _, tt := range tests {
		if got := detectMode(tt.url); got != tt.want {
			t.Errorf("detectMode(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

const listingHTML = `<html><body>
<div class="nav-page-container"><span class="mobile-hide">Page </span>1 of 3</div>
<div id="scene-grid-item-3321" class="col span_3_of_12 scene-grid-item scene-grid-item-1">
<div class="scene-grid-image-container scene-grid-image-container-3321">
<a href="dvds.php?id=244&sceneid=3321" class="scene-grid-link"><img src="https://cdn.example.com/contentthumbs/73/64/57364-1x.jpg" class="scene-grid-image scene-image scene-image-3321"></a>
</div>
<strong><a href="dvds.php?id=244&sceneid=3321" class="scene-link-3321 scene-link">Road To Redneck Hollow: Scene 1</a></strong>
<div class="overlay-stars">Dean Flynn, Riley Scott, Rodney Steele</div>
<div class="overlay-dates-time">
<strong>Released:</strong> May 19, 2026 | <strong>Length:</strong> 29:14
</div>
</div><!-- end scene-grid-item -->
<div id="scene-grid-item-3143" class="col span_3_of_12 scene-grid-item scene-grid-item-2">
<div class="scene-grid-image-container scene-grid-image-container-3143">
<a href="dvds.php?id=301&sceneid=3143" class="scene-grid-link"><img src="https://cdn.example.com/contentthumbs/73/43/57343-1x.jpg" class="scene-grid-image scene-image scene-image-3143"></a>
</div>
<strong><a href="dvds.php?id=301&sceneid=3143" class="scene-link-3143 scene-link">Deep Water: Scene 2</a></strong>
<div class="overlay-stars">Derrick Hanson, Ray Dragon</div>
<div class="overlay-dates-time">
<strong>Released:</strong> May 18, 2026 | <strong>Length:</strong> 13:55
</div>
</div><!-- end scene-grid-item -->
</body></html>`

func TestParseListingEntries(t *testing.T) {
	entries := parseListingEntries([]byte(listingHTML))
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	e := entries[0]
	if e.sceneID != "3321" {
		t.Errorf("sceneID = %q, want 3321", e.sceneID)
	}
	if e.dvdID != "244" {
		t.Errorf("dvdID = %q, want 244", e.dvdID)
	}
	if e.title != "Road To Redneck Hollow: Scene 1" {
		t.Errorf("title = %q", e.title)
	}
	if len(e.performers) != 3 || e.performers[0] != "Dean Flynn" {
		t.Errorf("performers = %v", e.performers)
	}
	if e.date != "May 19, 2026" {
		t.Errorf("date = %q", e.date)
	}
	if e.duration != "29:14" {
		t.Errorf("duration = %q", e.duration)
	}
	if e.thumbnail != "https://cdn.example.com/contentthumbs/73/64/57364-1x.jpg" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}

	e2 := entries[1]
	if e2.sceneID != "3143" {
		t.Errorf("entry 2 sceneID = %q, want 3143", e2.sceneID)
	}
}

func TestParseTotalPages(t *testing.T) {
	if got := parseTotalPages([]byte(listingHTML)); got != 3 {
		t.Errorf("parseTotalPages = %d, want 3", got)
	}
	if got := parseTotalPages([]byte("<html></html>")); got != 0 {
		t.Errorf("parseTotalPages empty = %d, want 0", got)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"29:14", 29*60 + 14},
		{"1:30", 90},
		{"0:45", 45},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

const detailHTML = `<html><body>
<h1 class="scene-header-title">Road To Redneck Hollow: Dean Flynn, Rodney Steele, Riley Scott &amp; Trojan Rock</h1>
<div class="rating_box" data-voted="0" data-rating="8.7" data-type="set" data-id="3321">
</div>
<p><strong>Featuring: </strong><a href="sets.php?id=62">Dean Flynn</a>, <a href="sets.php?id=1381">Riley Scott</a>, <a href="sets.php?id=1383">Rodney Steele</a></p>
<div class="col span_6_of_12"><h2>Description</h2><p>Logan pays a visit to his old buddy Ryann Wood.</p></div>
<div class="col span_6_of_12"><h2>Scene Details</h2>
<p><strong>Released: </strong> May 19, 2026</p>
<p><strong>Movie Title: </strong><a href="dvds.php?id=244">Road to Redneck Hollow</a></p>
<p><strong>Director: </strong><a href="category.php?id=60">Joe Gage</a></p>
<p><strong>Categories: </strong> <a href="category.php?id=13&amp;s=d">3-Way/Spit Roasting</a>, <a href="category.php?id=7&amp;s=d">Anal Sex</a>, <a href="category.php?id=5&amp;s=d">Scenes</a>, <a href="category.php?id=56&amp;s=d">TitanMen</a></p>
<p><strong>Length:</strong> 29:14</p>
</div>
<a href="dvds.php?id=244&sceneid=3321" class="scene-grid-link"><img src="https://cdn.example.com/contentthumbs/73/64/57364-1x.jpg" class="scene-grid-image scene-image scene-image-3321"></a>
</body></html>`

func TestParseDetail(t *testing.T) {
	entry := listEntry{
		sceneID:    "3321",
		dvdID:      "244",
		title:      "Placeholder",
		performers: []string{"Placeholder"},
		date:       "May 19, 2026",
		duration:   "29:14",
		thumbnail:  "https://old.example.com/thumb.jpg",
	}

	scene := parseDetail([]byte(detailHTML), entry, "https://www.titanmen.com")

	if scene.Title != "Road To Redneck Hollow: Dean Flynn, Rodney Steele, Riley Scott & Trojan Rock" {
		t.Errorf("title = %q", scene.Title)
	}
	if len(scene.Performers) != 3 || scene.Performers[0] != "Dean Flynn" {
		t.Errorf("performers = %v", scene.Performers)
	}
	if scene.Description != "Logan pays a visit to his old buddy Ryann Wood." {
		t.Errorf("description = %q", scene.Description)
	}
	if scene.Duration != 29*60+14 {
		t.Errorf("duration = %d", scene.Duration)
	}
	if scene.Studio != "Road to Redneck Hollow" {
		t.Errorf("studio (movie title) = %q", scene.Studio)
	}
	if scene.Director != "Joe Gage" {
		t.Errorf("director = %q", scene.Director)
	}
	if len(scene.Tags) != 3 {
		t.Errorf("tags = %v (want 3, got %d — 'Scenes' should be filtered)", scene.Tags, len(scene.Tags))
	}
	if scene.Thumbnail != "https://cdn.example.com/contentthumbs/73/64/57364-1x.jpg" {
		t.Errorf("thumbnail = %q", scene.Thumbnail)
	}
	wantDate := "2026-05-19"
	if scene.Date.Format("2006-01-02") != wantDate {
		t.Errorf("date = %v, want %s", scene.Date, wantDate)
	}
}

func TestListScenes(t *testing.T) {
	page1 := listingHTML
	page2 := `<html><body>
<div class="nav-page-container"><span class="mobile-hide">Page </span>2 of 2</div>
<div id="scene-grid-item-100" class="col span_3_of_12 scene-grid-item scene-grid-item-1">
<div class="scene-grid-image-container scene-grid-image-container-100">
<a href="dvds.php?id=10&sceneid=100" class="scene-grid-link"><img src="https://cdn.example.com/contentthumbs/10/00/10000-1x.jpg" class="scene-grid-image scene-image scene-image-100"></a>
</div>
<strong><a href="dvds.php?id=10&sceneid=100" class="scene-link-100 scene-link">Last Scene</a></strong>
<div class="overlay-stars">Actor One</div>
<div class="overlay-dates-time">
<strong>Released:</strong> Jan 01, 2020 | <strong>Length:</strong> 10:00
</div>
</div><!-- end scene-grid-item -->
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/category.php":
			page := r.URL.Query().Get("page")
			switch page {
			case "", "1":
				_, _ = fmt.Fprint(w, page1)
			case "2":
				_, _ = fmt.Fprint(w, page2)
			default:
				_, _ = fmt.Fprint(w, "<html></html>")
			}
		case "/dvds.php":
			_, _ = fmt.Fprint(w, detailHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/category.php?id=5&s=d", scraper.ListOpts{Workers: 1})
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
		switch r.URL.Path {
		case "/category.php":
			_, _ = fmt.Fprint(w, listingHTML)
		case "/dvds.php":
			_, _ = fmt.Fprint(w, detailHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/category.php?id=5&s=d", scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"3143": true},
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
		t.Errorf("got %d scenes, want 1 (should stop at known ID)", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

func TestModelPage(t *testing.T) {
	modelHTML := `<html><body>
<h1>Scotch Inkom</h1>
<div id="scene-grid-item-218" class="col span_3_of_12 scene-grid-item scene-grid-item-0">
<a href="dvds.php?id=37&sceneid=218" class="scene-grid-link"><img src="https://cdn.example.com/contentthumbs/89/36/58936-1x.jpg" class="scene-grid-image scene-image scene-image-218"></a>
<strong><a href="dvds.php?id=37&sceneid=218" class="scene-link-218 scene-link">Speechless: Performer Interviews</a></strong>
<div class="overlay-stars">Marco Wilson, Scotch Inkom</div>
<div class="overlay-dates-time">
<strong>Released:</strong> Oct 16, 2020 | <strong>Length:</strong> 6:41
</div>
</div><!-- end scene-grid-item -->
</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/sets.php":
			_, _ = fmt.Fprint(w, modelHTML)
		case "/dvds.php":
			_, _ = fmt.Fprint(w, detailHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/sets.php?id=8", scraper.ListOpts{Workers: 1})
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
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
}

// A DVD page lists its scenes by disc position, so a bonus scene appended to a
// disc sits behind scenes that shipped with it. Stopping at the first stored
// scene hid those forever; the walk must skip and carry on.
func TestDVDPageKnownIDSkipsRatherThanTruncates(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/dvds.php":
			if r.URL.Query().Get("sceneid") != "" {
				_, _ = fmt.Fprint(w, detailHTML)
				return
			}
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			t.Errorf("unexpected fetch %s", r.URL)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseOverride: ts.URL}

	// 3321 is the first of the two entries; the second must still be reached.
	ch, err := s.ListScenes(context.Background(), ts.URL+"/dvds.php?id=244", scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"3321": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	var stoppedEarly bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(ids) != 1 || ids[0] != "3143" {
		t.Errorf("scene IDs = %v, want [3143]", ids)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly to report the skipped scene")
	}
}

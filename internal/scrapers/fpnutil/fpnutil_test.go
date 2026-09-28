package fpnutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const sampleCard = `<div class="flex flex-col justify-between h-full p-1 mt-auto md:m-0" data-setid="10803">
  <div>
    <a class="flex-none lg:min-h-28" title="Naomi&#039;s Ass Filled With Cum" href="/trailers/Naomis-Ass-Filled-With-Cum">
      <div class="video-thumbnail-link thumbnail relative latest_scene_item video_preview_div">
        <img class="videos-preload lazyLoad" data-src="https://c7669ab0a8.mjedge.net/content/contentthumbs/29/28/42928-3x.jpg?expires=123&l=41&token=abc" />
        <div class="absolute bottom-0 right-0 rating-date text-xs p-1 bg-black">
          <div class="video-data">23 min</div>
        </div>
      </div>
    </a>
  </div>
  <div class="h-full">
    <h2 class="text-sm truncate pt-1">
      <a class="text-accent-hover" href="/trailers/Naomis-Ass-Filled-With-Cum">
        Naomi's Ass Filled With Cum
      </a>
    </h2>
  </div>
  <div class="flex flex-row">
    <span class="text-accent text-xs">
      <a href="/models/Naomi-Russell.html">Naomi Russell</a>
    </span>
    <span class="text-accent text-xs">
      <a href="/models/RodSteele.html">Rod Steele</a>
    </span>
  </div>
</div>`

const sampleCard2 = `<div class="flex flex-col justify-between h-full p-1 mt-auto md:m-0" data-setid="11010">
  <div>
    <a class="flex-none lg:min-h-28" title="Second Scene Title" href="/trailers/Second-Scene-Slug">
      <div class="video-thumbnail-link thumbnail relative latest_scene_item video_preview_div">
        <img class="videos-preload lazyLoad" data-src="https://c7669ab0a8.mjedge.net/content/contentthumbs/55/90/45590-3x.jpg?expires=123&l=41&token=def" />
        <div class="absolute bottom-0 right-0 rating-date text-xs p-1 bg-black">
          <div class="video-data">31 min</div>
        </div>
      </div>
    </a>
  </div>
  <div class="h-full">
    <h2 class="text-sm truncate pt-1">
      <a class="text-accent-hover" href="/trailers/Second-Scene-Slug">Second Scene Title</a>
    </h2>
  </div>
  <div class="flex flex-row">
    <span class="text-accent text-xs">
      <a href="/models/Alice.html">Alice</a>
    </span>
  </div>
</div>`

func TestParseListingPage(t *testing.T) {
	body := []byte(sampleCard + sampleCard2)
	scenes := ParseListingPage(body)
	if len(scenes) != 2 {
		t.Fatalf("expected 2 scenes, got %d", len(scenes))
	}

	sc := scenes[0]
	if sc.ID != "10803" {
		t.Errorf("ID = %q, want 10803", sc.ID)
	}
	if sc.Title != "Naomi's Ass Filled With Cum" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Slug != "Naomis-Ass-Filled-With-Cum" {
		t.Errorf("Slug = %q", sc.Slug)
	}
	if sc.Duration != 23*60 {
		t.Errorf("Duration = %d, want %d", sc.Duration, 23*60)
	}
	if sc.Thumb == "" {
		t.Error("Thumb is empty")
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Naomi Russell" {
		t.Errorf("Performers = %v", sc.Performers)
	}

	sc2 := scenes[1]
	if sc2.ID != "11010" {
		t.Errorf("ID = %q, want 11010", sc2.ID)
	}
	if sc2.Duration != 31*60 {
		t.Errorf("Duration = %d, want %d", sc2.Duration, 31*60)
	}
}

func TestParseListingPageEmpty(t *testing.T) {
	body := []byte(`<div class="content category_listing_block clear p-2">
  <div class="mb-2 gap-1 grid grid-cols-2 lg:grid-cols-5 upcoming">
  </div>
</div>`)
	scenes := ParseListingPage(body)
	if len(scenes) != 0 {
		t.Errorf("expected 0 scenes, got %d", len(scenes))
	}
}

func TestClassifyURL(t *testing.T) {
	tests := []struct {
		url      string
		wantKind filterKind
		wantVal  string
	}{
		{"https://analized.com/", filterAll, ""},
		{"https://analized.com/porn-categories/movies/?page=1&sort=most-recent", filterAll, ""},
		{"https://analized.com/models/Naomi-Russell.html", filterModel, "Naomi-Russell"},
		{"https://analized.com/porn-categories/anal/", filterCategory, "anal"},
		{"https://analized.com/channels/analamateur/", filterCategory, "analamateur"},
		{"https://fullpornnetwork.com/channels/pornforce/", filterCategory, "pornforce"},
	}
	for _, tt := range tests {
		kind, val := classifyURL(tt.url)
		if kind != tt.wantKind || val != tt.wantVal {
			t.Errorf("classifyURL(%q) = (%v, %q), want (%v, %q)", tt.url, kind, val, tt.wantKind, tt.wantVal)
		}
	}
}

func TestListingURL(t *testing.T) {
	base := "https://analized.com"
	tests := []struct {
		kind filterKind
		val  string
		page int
		want string
	}{
		{filterAll, "", 1, "https://analized.com/porn-categories/movies/?page=1&sort=most-recent"},
		{filterAll, "", 3, "https://analized.com/porn-categories/movies/?page=3&sort=most-recent"},
		{filterModel, "Naomi-Russell", 1, "https://analized.com/models/Naomi-Russell.html"},
		{filterCategory, "anal", 2, "https://analized.com/porn-categories/anal/?page=2&sort=most-recent"},
	}
	for _, tt := range tests {
		got := listingURL(base, tt.kind, tt.val, tt.page)
		if got != tt.want {
			t.Errorf("listingURL(%v, %q, %d) = %q, want %q", tt.kind, tt.val, tt.page, got, tt.want)
		}
	}
}

func TestExtractMaxPage(t *testing.T) {
	body := []byte(`
<a href="/porn-categories/movies/?page=2&sort=most-recent">2</a>
<a href="/porn-categories/movies/?page=3&sort=most-recent">3</a>
<a href="/porn-categories/movies/?page=10&sort=most-recent">10</a>
<a href="/porn-categories/movies/?page=2&sort=most-recent">&gt;</a>
`)
	maxPage := extractMaxPage(body)
	if maxPage != 10 {
		t.Errorf("extractMaxPage = %d, want 10", maxPage)
	}
}

func TestHasNextPage(t *testing.T) {
	body := []byte(`
<a href="?page=1&sort=most-recent">1</a>
<a href="?page=2&sort=most-recent">2</a>
<a href="?page=3&sort=most-recent">3</a>
`)
	if !hasNextPage(body, 1) {
		t.Error("expected hasNextPage(1) = true")
	}
	if !hasNextPage(body, 2) {
		t.Error("expected hasNextPage(2) = true")
	}
	if hasNextPage(body, 3) {
		t.Error("expected hasNextPage(3) = false")
	}
}

const sampleDetail = `<html>
<h1 class="title_bar trailer_title text-center text-3xl text-accent">
  Naomi's Ass Filled With Cum
</h1>
<div class="flex flex-wrap gap-2 my-2">
  <label class="text-white">Starring:</label>
  <div class="text-base">
    <a href="/models/Naomi-Russell.html">
      <span class="text-accent hover:text-white flex pr-1 overflow-x-hidden truncate">
        <svg class="min-w-4 min-h-4 pr-.5" xmlns="http://www.w3.org/2000/svg" fill="currentColor" viewBox="0 0 16 16">
          <path d="M8 8a3 3 0 1 0 0-6"></path>
        </svg>
        Naomi Russell
      </span>
    </a>
  </div>
</div>
<div class="flex flex-wrap gap-1 mb-2">
  <div class="py-2">
    <a class="border-accent border hover:border-white rounded-md text-sm p-2" href="/porn-categories/anal/">Anal</a>
  </div>
  <div class="py-2">
    <a class="border-accent border hover:border-white rounded-md text-sm p-2" href="/porn-categories/Interracial-Porn/">Interracial</a>
  </div>
</div>
<p id="description" class="max-h-48 leading-7 overflow-hidden">
  A test description with &amp; entities.
</p>
<div class="my-2 flex">
  <label class="pr-1">
    Date Added:
  </label>
  2026-04-20
</div>
</html>`

func TestFetchDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/trailers/Test-Slug/":
			_, _ = fmt.Fprint(w, sampleDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test Studio"},
	}

	ls := listingScene{
		ID:         "42",
		Slug:       "Test-Slug",
		Title:      "Test Title",
		Thumb:      "https://example.com/thumb.jpg",
		Duration:   1380,
		Performers: []string{"Naomi Russell"},
	}

	scene, err := s.fetchDetail(context.Background(), ls, 0)
	if err != nil {
		t.Fatalf("fetchDetail error: %v", err)
	}

	if scene.ID != "42" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Test Title" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Description != "A test description with & entities." {
		t.Errorf("Description = %q", scene.Description)
	}
	wantDate := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "Anal" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Studio != "Test Studio" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Naomi Russell" {
		t.Errorf("Performers = %v", scene.Performers)
	}
}

func TestFetchDetailFallbackPerformers(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, sampleDetail)
	}))
	defer ts.Close()

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"},
	}

	ls := listingScene{
		ID:   "42",
		Slug: "Test-Slug",
	}

	scene, err := s.fetchDetail(context.Background(), ls, 0)
	if err != nil {
		t.Fatalf("fetchDetail error: %v", err)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Naomi Russell" {
		t.Errorf("Performers = %v, want [Naomi Russell]", scene.Performers)
	}
}

func TestRunPagination(t *testing.T) {
	page := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/porn-categories/movies/":
			page++
			if page == 1 {
				_, _ = fmt.Fprint(w, sampleCard+sampleCard2+`
<div class="pagination pt-2">
  <a href="?page=2&sort=most-recent">2</a>
</div>`)
			} else {
				_, _ = fmt.Fprint(w, "")
			}
		case "/trailers/Naomis-Ass-Filled-With-Cum/":
			_, _ = fmt.Fprint(w, sampleDetail)
		case "/trailers/Second-Scene-Slug/":
			_, _ = fmt.Fprint(w, sampleDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"},
	}

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL+"/porn-categories/movies/?page=1&sort=most-recent", scraper.ListOpts{Workers: 2}, out)

	var scenes []string
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}
	if len(scenes) != 2 {
		t.Errorf("expected 2 scenes, got %d: %v", len(scenes), scenes)
	}
}

func TestRunKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/porn-categories/movies/":
			_, _ = fmt.Fprint(w, sampleCard+sampleCard2)
		case "/trailers/Naomis-Ass-Filled-With-Cum/":
			_, _ = fmt.Fprint(w, sampleDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"},
	}

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"11010": true},
		Workers:  1,
	}, out)

	var scenes []string
	var stoppedEarly bool
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		}
	}
	if len(scenes) != 1 || scenes[0] != "10803" {
		t.Errorf("expected [10803] before known ID, got %v", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

// The model page was rebuilt as a swimlane whose cards leave data-setid empty
// and carry the id in data-scene-id instead, and whose performer link has
// attributes before its href. Both spellings must parse.
const sampleSwimlaneCard = `<div data-scene-id="5414" class="rounded-xl flex-shrink-0 w-10/12 md:w-auto">
  <div class="flex flex-col justify-between h-full p-1 mt-auto md:m-0" data-setid="">
    <div>
      <a data-swimlane-scene-index="1" class="swimlane-scene-thumbnail-link flex-none lg:min-h-28" title="Swimlane Scene" href="/trailers/Swimlane-Scene-Slug" >
        <div class="video-thumbnail-link thumbnail relative latest_scene_item video_preview_div">
          <img class="videos-preload lazyLoad" data-src="https://c7669ab0a8.mjedge.net/content/contentthumbs/91/08/49108-3x.jpg?expires=1&l=41&token=x" />
        </div>
      </a>
    </div>
    <div class="flex flex-row">
      <span class="text-accent text-xs">
        <a data-swimlane-scene-index="2" class="swimlane-scene-performer-title-link text-accent-hover" href="/models/jessica-starling.html">Jessica Starling</a>
      </span>
    </div>
  </div>
</div>`

func TestParseListingPageSwimlaneCard(t *testing.T) {
	scenes := ParseListingPage([]byte(sampleSwimlaneCard))
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "5414" {
		t.Errorf("ID = %q, want 5414", sc.ID)
	}
	if sc.Slug != "Swimlane-Scene-Slug" || sc.Title != "Swimlane Scene" {
		t.Errorf("slug/title = %q/%q", sc.Slug, sc.Title)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Jessica Starling" {
		t.Errorf("performers = %v", sc.Performers)
	}
}

// A model page is one un-paginated listing in no date order at all — live,
// Jessica Starling's ran 2026-09, 2023-12, 2022-10, 2024-11 — so a stored scene
// must be skipped rather than end the walk.
func TestRunModelPageKnownIDsSkipRatherThanStop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/naomi.html":
			_, _ = fmt.Fprint(w, sampleCard+sampleCard2)
		case "/trailers/Second-Scene-Slug/":
			_, _ = fmt.Fprint(w, sampleDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test"},
	}

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL+"/models/naomi.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"10803": true},
		Workers:  1,
	}, out)

	var scenes []string
	var stoppedEarly bool
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		}
	}
	if len(scenes) != 1 || scenes[0] != "11010" {
		t.Errorf("scene IDs = %v, want [11010] — the card after the known one", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly to report the skipped scene")
	}
}

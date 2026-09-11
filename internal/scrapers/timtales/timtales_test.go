package timtales

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

// Mirrors the 2026 markup: a featured top-video box (no card, no link), the
// category sidebar, then video-row grids of video-item cards whose anchors carry
// no trailing slash. The second card keeps the older trailing-slash form.
func listingHTML() string {
	return `<html><body>
<div class="video-box top-video cf">
  <div class="text"><h1>Latest Video Update</h1><h2>Featured Scene</h2></div>
  <div id="video-9" class="video player no-volume play-button" data-uid="9"
       style="background-image:url(&#039;https://assets.timtales.com/featured.jpg&#039;)"></div>
</div>
<a href="/videos/huge-cocks/latest">Huge Cocks</a>
<a class="number" href="/videos/latest/page-2">2</a>
<div class="video-row cf">
    <div class="video-item video-item-odd">
        
    <h2>Hot Scene &amp; More</h2>
    
            <div id="video-8" class="video player flowplayer is-splash"
                 style="background-image: url(&#039;https://assets.timtales.com/splash1.jpg&#039;)">
                <div class="fp-ratio"></div>
                <a href="/videos/hot-scene">
                    <div class="fp-ui"></div>
                </a>
            </div>
    </div>
    <div class="video-item video-item-even">
    <h2>Second One</h2>
            <div id="video-7" class="video player flowplayer is-splash"
                 style="background-image: url(&#39;https://assets.timtales.com/splash2.jpg&#39;)">
                <a href="/videos/second-one/"><div class="fp-ui"></div></a>
            </div>
    </div>
</div>
</body></html>`
}

func detailHTML(title, date, runtime, desc string) string {
	return fmt.Sprintf(`<html><body>
<div class="video-box video-single cf">
    <div class="text">
        <h1>%s</h1>

<p class="date">
    %s
    – Runtime:
    %s
</p>

        <p class="bodytext">%s</p>
            <p class="categories">Categories:
    
        <a class="emphasized" href="/videos/huge-cocks">Huge Cocks</a>, 
    
        <a class="emphasized" href="/videos/bareback">Bareback</a>
    
</p>
            <p class="categories">Men:
    
                <a class="emphasized" href="/the-men/josh">Josh</a>, 
            
                <a class="emphasized" href="/the-men/pietro-vanz">Pietro Vanz</a>
    
</p>
    </div>
</div>
</body></html>`, title, date, runtime, desc)
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.timtales.com/videos/latest/", true},
		{"https://timtales.com/videos/foo/", true},
		{"https://example.com/x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestCleanText ----

func TestCleanText(t *testing.T) {
	if got := cleanText(`  <b>A&amp;B</b>  c  `); got != "A&B c" {
		t.Errorf("cleanText = %q, want %q", got, "A&B c")
	}
}

// ---- TestFetchListing ----

func TestFetchListing(t *testing.T) {
	orig := baseURL
	defer func() { baseURL = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listingHTML())
	}))
	defer ts.Close()
	baseURL = ts.URL

	s := &Scraper{client: ts.Client()}
	items, err := s.fetchListing(context.Background(), ts.URL+"/videos/latest")
	if err != nil {
		t.Fatalf("fetchListing error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	if items[0].id != "hot-scene" || items[0].title != "Hot Scene & More" {
		t.Errorf("item0 = %+v", items[0])
	}
	if items[0].thumbnail != "https://assets.timtales.com/splash1.jpg" {
		t.Errorf("item0.thumbnail = %q", items[0].thumbnail)
	}
	if items[0].url != ts.URL+"/videos/hot-scene" {
		t.Errorf("item0.url = %q", items[0].url)
	}
	if items[1].id != "second-one" || items[1].thumbnail != "https://assets.timtales.com/splash2.jpg" {
		t.Errorf("item1 = %+v", items[1])
	}
	if items[1].url != ts.URL+"/videos/second-one" {
		t.Errorf("item1.url = %q", items[1].url)
	}
}

// ---- TestToScene (detail parse) ----

func TestToScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML("Hot Scene Full Title", "June 01, 2024", "24:13", "A great<br />\ndescription here."))
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	it := listItem{id: "hot-scene", url: ts.URL + "/videos/hot-scene", title: "Hot Scene", thumbnail: "thumb.jpg"}
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	sc := s.toScene(context.Background(), "studioURL", it, now)

	if sc.ID != "hot-scene" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != siteID {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Title != "Hot Scene Full Title" {
		t.Errorf("Title = %q (h1 should override listing title)", sc.Title)
	}
	if sc.URL != ts.URL+"/videos/hot-scene" {
		t.Errorf("URL = %q", sc.URL)
	}
	wantDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", sc.Date, wantDate)
	}
	if sc.Duration != 1453 {
		t.Errorf("Duration = %d, want 1453 (24:13)", sc.Duration)
	}
	if sc.Description != "A great description here." {
		t.Errorf("Description = %q", sc.Description)
	}
	if want := []string{"Josh", "Pietro Vanz"}; !slices.Equal(sc.Performers, want) {
		t.Errorf("Performers = %q, want %q", sc.Performers, want)
	}
	if want := []string{"Huge Cocks", "Bareback"}; !slices.Equal(sc.Categories, want) {
		t.Errorf("Categories = %q, want %q", sc.Categories, want)
	}
}

// ---- TestListScenes (end-to-end) ----

func TestListScenes(t *testing.T) {
	orig := baseURL
	defer func() { baseURL = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos/latest":
			_, _ = fmt.Fprint(w, listingHTML())
		case "/videos/hot-scene":
			_, _ = fmt.Fprint(w, detailHTML("Hot Scene Full Title", "June 1, 2024", "24:13", "Desc one."))
		case "/videos/second-one":
			_, _ = fmt.Fprint(w, detailHTML("Second One Full", "July 2, 2024", "18:00", "Desc two."))
		default:
			// page-2 etc. repeats the listing -> dedup empties -> Done.
			_, _ = fmt.Fprint(w, listingHTML())
		}
	}))
	defer ts.Close()
	baseURL = ts.URL

	s := &Scraper{client: ts.Client()}
	// Empty studioURL -> defaults to baseURL + "/videos/latest".
	ch, err := s.ListScenes(context.Background(), "", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	got := map[string]string{}
	for r := range ch {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
			continue
		}
		if r.Kind == scraper.KindScene {
			got[r.Scene.ID] = r.Scene.Title
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(got), got)
	}
	if got["hot-scene"] != "Hot Scene Full Title" || got["second-one"] != "Second One Full" {
		t.Errorf("scenes = %v", got)
	}
}

// ---- TestListScenesNoCards ----

// A listing that loads but matches no cards is a redesign, not an empty studio.
func TestListScenesNoCards(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><div class="redesigned">nothing here</div></body></html>`)
	}))
	defer ts.Close()

	orig := baseURL
	defer func() { baseURL = orig }()
	baseURL = ts.URL

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	var errs []error
	for r := range ch {
		if r.Kind == scraper.KindScene {
			t.Errorf("unexpected scene %q", r.Scene.ID)
		}
		if r.Err != nil {
			errs = append(errs, r.Err)
		}
	}
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errors = %v, want one parse failure", errs)
	}
}

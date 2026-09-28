package empirestoreutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestParseListingPage(t *testing.T) {
	body := []byte(`
<article class="scene-widget member-view" data-scene-id="130414" data-master-id="1517194">
  <a class="scene-title" href="/130414/test-scene-one-streaming-scene-video.html">
    <h6>Test Scene One</h6>
  </a>
  <p class="scene-performer-names">Jane Doe, John Smith</p>
  <p class="scene-length">32 min</p>
  <img class="screenshot" data-src="https://caps1cdn.adultempire.com/200/1517194_220.jpg" />
</article>
<article class="scene-widget member-view" data-scene-id="130415" data-master-id="1517195">
  <a class="scene-title" href="/130415/test-scene-two-streaming-scene-video.html">
    <h6>Test Scene Two</h6>
  </a>
  <p class="scene-performer-names">Alice</p>
  <p class="scene-length">20 min</p>
  <img class="screenshot" data-src="https://caps1cdn.adultempire.com/200/1517195_110.jpg" />
</article>
`)
	scenes := ParseListingPage(body, "https://test.local")
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.ID != "130414" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.URL != "https://test.local/130414/test-scene-one-streaming-scene-video.html" {
		t.Errorf("URL = %q", s.URL)
	}
	if s.Title != "Test Scene One" {
		t.Errorf("Title = %q", s.Title)
	}
	if len(s.Performers) != 2 || s.Performers[0] != "Jane Doe" || s.Performers[1] != "John Smith" {
		t.Errorf("Performers = %v", s.Performers)
	}
	if s.Duration != 1920 {
		t.Errorf("Duration = %d, want 1920", s.Duration)
	}
	if s.Thumb != "https://caps1cdn.adultempire.com/200/1517194_220.jpg" {
		t.Errorf("Thumb = %q", s.Thumb)
	}
}

func TestParseDetailPage(t *testing.T) {
	body := []byte(`
<div class="release-date"><span class="font-weight-bold mr-2">Released:</span>May 12, 2011</div>
<div class="studio"><span class="font-weight-bold mr-2">Studio:</span><span>Vouyer Media</span></div>
<div class="series"><span class="font-weight-bold mr-2">Series:</span><a href="/series/123">There's No Place Like Mom</a></div>
<div><strong>Attributes: </strong><a href="?scene_attribute=742">Cum Shot</a> <a href="?scene_attribute=743">MILF</a></div>
<h3 class="price mb-1">$1.49</h3>
<span>Buy This Scene</span>
`)
	d := ParseDetailPage(body)
	if d.Date.Year() != 2011 || d.Date.Month() != 5 || d.Date.Day() != 12 {
		t.Errorf("Date = %v", d.Date)
	}
	if d.Studio != "Vouyer Media" {
		t.Errorf("Studio = %q", d.Studio)
	}
	if d.Series != "There's No Place Like Mom" {
		t.Errorf("Series = %q", d.Series)
	}
	if len(d.Tags) != 2 || d.Tags[0] != "Cum Shot" || d.Tags[1] != "MILF" {
		t.Errorf("Tags = %v", d.Tags)
	}
	if d.Price != 1.49 {
		t.Errorf("Price = %f, want 1.49", d.Price)
	}
}

func TestParseDetailPageTagsLabel(t *testing.T) {
	body := []byte(`
<div class="release-date"><span class="font-weight-bold mr-2">Released:</span>Jan 5, 2020</div>
<div><span>Tags:</span><a href="?tag=1">Blonde</a> <a href="?tag=2">Hardcore</a></div>
`)
	d := ParseDetailPage(body)
	if len(d.Tags) != 2 || d.Tags[0] != "Blonde" {
		t.Errorf("Tags = %v", d.Tags)
	}
}

func TestExtractTotal(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"h4 format", `<h4>825 Results</h4>`, 825},
		{"span format", `<span class="font-weight-bold">825</span> Results`, 825},
		{"span with commas", `<span class="font-weight-bold">7,510</span> Results`, 7510},
		{"no match", `<div>no results</div>`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTotal([]byte(tt.body)); got != tt.want {
				t.Errorf("ExtractTotal = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveListingURL(t *testing.T) {
	tests := []struct {
		name    string
		studio  string
		base    string
		listing string
		want    string
	}{
		{"domain root", "https://www.elegantangel.com/", "https://www.elegantangel.com", "/listing.html", "https://www.elegantangel.com/listing.html"},
		{"scenes path", "https://www.reaganfoxx.com/scenes/673608/test.html", "https://www.reaganfoxx.com", "/default.html", "https://www.reaganfoxx.com/scenes/673608/test.html"},
		{"scene suffix", "https://www.elegantangel.com/watch-streaming-video-by-scene.html?studio=94000", "https://www.elegantangel.com", "/default.html", "https://www.elegantangel.com/watch-streaming-video-by-scene.html?studio=94000"},
		// A `/{id}/studio/…` URL is a shop page, not a listing: on the store
		// domains it redirects to the store's front page, so passing it
		// through scraped the store's newest scenes under the studio's name.
		{"studio filter", "https://www.elegantangel.com/93560/studio/club-59-elegant-angel-studios.html", "https://www.elegantangel.com", "/default.html", "https://www.elegantangel.com/watch-streaming-video-by-scene.html?studio=93560"},
		{"studio filter on a store subdomain", "https://bobsvideos.empirestores.co/2050/studio/club-59-streaming-porn-movies.html", "https://bobsvideos.empirestores.co", "/shop-streaming-video-by-scene.html", "https://bobsvideos.empirestores.co/watch-streaming-video-by-scene.html?studio=2050"},
		// A studio id already in query form is left alone.
		{"studio query", "https://jsi.empirestores.co/watch-streaming-video-by-scene.html?studio=5461", "https://jsi.empirestores.co", "/default.html", "https://jsi.empirestores.co/watch-streaming-video-by-scene.html?studio=5461"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveListingURL(tt.studio, tt.base, tt.listing); got != tt.want {
				t.Errorf("resolveListingURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasPagination(t *testing.T) {
	if HasPagination([]byte(`<div>no pages</div>`)) {
		t.Error("expected false for no pagination")
	}
	if !HasPagination([]byte(`<ul class="pagination">`)) {
		t.Error("expected true for pagination")
	}
}

func TestHasNextPage(t *testing.T) {
	body := []byte(`<a href="?page=2">2</a><a href="?page=3">3</a>`)
	if !HasNextPage(body, 1) {
		t.Error("expected next page from 1")
	}
	if HasNextPage(body, 3) {
		t.Error("expected no next page from 3")
	}
}

const itemTpl = `<article class="scene-widget member-view" data-scene-id="%s" data-master-id="100">
  <a class="scene-title" href="/%s/test-streaming-scene-video.html"><h6>Test Title</h6></a>
  <p class="scene-performer-names">Model A</p>
  <p class="scene-length">20 min</p>
  <img class="screenshot" data-src="https://caps1cdn.adultempire.com/200/100_110.jpg" />
</article>`

const detailTpl = `
<div class="release-date"><span class="font-weight-bold mr-2">Released:</span>Jan 1, 2026</div>
<div class="studio"><span class="font-weight-bold mr-2">Studio:</span><span>Test Studio</span></div>
<div><strong>Attributes: </strong><a href="?a=1">Tag1</a></div>
<h3 class="price mb-1">$2.99</h3>
<span>Buy This Scene</span>
`

func newTestServer(codes []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		switch {
		case r.URL.Path == "/shop-streaming-video-by-scene.html":
			pg := r.URL.Query().Get("page")
			if pg != "" && pg != "1" {
				_, _ = fmt.Fprint(w, `<div>empty</div>`)
				return
			}
			var items string
			for _, code := range codes {
				items += fmt.Sprintf(itemTpl, code, code)
			}
			_, _ = fmt.Fprint(w, items)

		case len(r.URL.Path) > 1 && r.URL.Path[1] >= '0' && r.URL.Path[1] <= '9':
			_, _ = fmt.Fprint(w, detailTpl)

		default:
			_, _ = fmt.Fprint(w, `<div>empty</div>`)
		}
	}))
}

func TestRun(t *testing.T) {
	ts := newTestServer([]string{"100", "200"})
	defer ts.Close()

	cfg := SiteConfig{
		SiteID:     "test",
		Domain:     "test.local",
		StudioName: "Test Studio",
		ListingURL: "/shop-streaming-video-by-scene.html",
	}
	s := New(cfg)
	s.Client = ts.Client()

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL+"/shop-streaming-video-by-scene.html", scraper.ListOpts{}, out)

	results := testutil.CollectScenes(t, out)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Studio != "Test Studio" && results[1].Studio != "Test Studio" {
		t.Error("expected studio from detail page")
	}
}

func TestRunKnownIDs(t *testing.T) {
	ts := newTestServer([]string{"100", "200", "300"})
	defer ts.Close()

	cfg := SiteConfig{
		SiteID:     "test",
		Domain:     "test.local",
		StudioName: "Test",
		ListingURL: "/shop-streaming-video-by-scene.html",
	}
	s := New(cfg)
	s.Client = ts.Client()

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL+"/shop-streaming-video-by-scene.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"200": true},
	}, out)

	results, stoppedEarly := testutil.CollectScenesWithStop(t, out)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(results) != 1 {
		t.Fatalf("got %d scenes, want 1", len(results))
	}
}

// The per-scene synopsis is the only prose these stores publish, and it went
// unread until now — every Jodi West scene came back with an empty Description
// while the page carried one.
func TestParseDetailPageSynopsis(t *testing.T) {
	body := []byte(`
<div class="synopsis"><p>I prefer masturbating outdoors, with the sun kissing my
fevered flesh. Today I came so hard &amp; my panties are soaked!</p></div>
<div class="release-date"><span class="font-weight-bold mr-2">Released:</span>Aug 14, 2026</div>
`)
	d := ParseDetailPage(body)
	want := "I prefer masturbating outdoors, with the sun kissing my fevered flesh. " +
		"Today I came so hard & my panties are soaked!"
	if d.Description != want {
		t.Errorf("Description = %q, want %q", d.Description, want)
	}
}

func TestParseDetailPageSynopsisMultipleParagraphs(t *testing.T) {
	body := []byte(`<div class="synopsis"><p>First para.</p><p>Second para.</p></div>`)
	d := ParseDetailPage(body)
	if d.Description != "First para. Second para." {
		t.Errorf("Description = %q, want both paragraphs joined", d.Description)
	}
}

// Most of the network publishes no scene text at all — the block is absent
// rather than empty — so a miss must stay an empty string, not an error or a
// scrap of surrounding markup.
func TestParseDetailPageNoSynopsis(t *testing.T) {
	body := []byte(`
<div class="release-date"><span class="font-weight-bold mr-2">Released:</span>Jan 5, 2020</div>
<div class="studio"><span class="font-weight-bold mr-2">Studio:</span><span>Bob's Videos</span></div>
`)
	d := ParseDetailPage(body)
	if d.Description != "" {
		t.Errorf("Description = %q, want empty when the site publishes none", d.Description)
	}
	if d.Studio != "Bob's Videos" {
		t.Errorf("Studio = %q — the other fields must still parse", d.Studio)
	}
}

// Page 2 is a query string appended to the listing URL, and the separator has
// to follow whichever the listing already uses. A studio-filtered listing is
// `…?studio={id}`, so a hard-coded "?" would produce a second one and the walk
// would silently re-fetch page 1 forever.
func TestRunPaginatesOntoAnExistingQueryString(t *testing.T) {
	for _, tt := range []struct {
		name    string
		listing string
		want    string
	}{
		{"bare path", "/shop-streaming-video-by-scene.html", "/shop-streaming-video-by-scene.html?page=2"},
		{"already has a query", "/watch-streaming-video-by-scene.html?studio=427", "/watch-streaming-video-by-scene.html?studio=427&page=2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var asked []string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if len(r.URL.Path) > 1 && r.URL.Path[1] >= '0' && r.URL.Path[1] <= '9' {
					_, _ = fmt.Fprint(w, detailTpl)
					return
				}
				asked = append(asked, r.URL.RequestURI())
				const pager = `<ul class="pagination"><a href="?page=2">2</a></ul>`
				if r.URL.Query().Get("page") == "" {
					_, _ = fmt.Fprintf(w, itemTpl, "100", "100")
					_, _ = fmt.Fprint(w, pager)
					return
				}
				_, _ = fmt.Fprint(w, `<div>empty</div>`)
			}))
			defer ts.Close()

			s := New(SiteConfig{SiteID: "test", Domain: "test.local", StudioName: "Test Studio",
				ListingURL: "/shop-streaming-video-by-scene.html"})
			s.Client = ts.Client()

			out := make(chan scraper.SceneResult)
			go s.Run(context.Background(), ts.URL+tt.listing, scraper.ListOpts{}, out)
			testutil.CollectScenes(t, out)

			if len(asked) < 2 {
				t.Fatalf("only asked for %v — page 2 was never requested", asked)
			}
			if asked[1] != tt.want {
				t.Errorf("page 2 = %q, want %q", asked[1], tt.want)
			}
		})
	}
}

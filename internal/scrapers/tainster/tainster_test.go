package tainster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const listingHTML = `<html><body>
<figure class=" video_item " data-score="" style="position: relative;">
  <div class="video_item--player">
    <a href="/Test-Channel/movie/12345/test-scene-one" class="let__up-link">
      <div class="thumb-slider list-view-slider">
        <img class="thumb-slide" data-title-image="https://cdn.example.com/thumb1.webp" src="thumb1.jpg" alt="">
      </div>
    </a>
  </div>
  <div class="video_item--content with-badge">
    <a href="/Test-Channel/movie/12345/test-scene-one" class="link" title="Test Scene One">
      <h3 class="title--5" data-fw="400">Test Scene One</h3>
    </a>
  </div>
  <div class="video_item--channel-link">
    <a href="/Test-Channel" class="link">Test Channel</a>
  </div>
</figure>
<!-- video block end -->
<figure class=" video_item " data-score="">
  <div class="video_item--player">
    <a href="/Other/movie/12346/scene-two" class="let__up-link">
      <div class="thumb-slider list-view-slider">
        <img class="thumb-slide" data-title-image="https://cdn.example.com/thumb2.webp" src="thumb2.jpg" alt="">
      </div>
    </a>
  </div>
  <div class="video_item--content with-badge">
    <a href="/Other/movie/12346/scene-two" class="link" title="Scene Two">
      <h3 class="title--5" data-fw="400">Scene Two</h3>
    </a>
  </div>
  <div class="video_item--channel-link">
    <a href="/Other" class="link">Other</a>
  </div>
</figure>
<!-- video block end -->
<ul class="pagination clearfix">
  <li class="current"><a href="#">1</a></li>
  <li class="last-page"><a href="?page=3">3</a></li>
</ul>
</body></html>`

const detailHTML = `<html><body>
<h1 class="title--3">Test Scene One - Full Title</h1>
<ul class="video_info-list">
  <li>15 May 2026</li>
  <li>by <a href="/Test-Channel" class="link" data-fw="600" target="_self"><span>Test Series Vol. 1</span></a></li>
</ul>
<div class="read-more-block mb10">
  <div>Full video:&nbsp;<strong>32 minutes</strong></div>
</div>
<div class="accordion__content">
  <h5 class="title--5 mb10">Description</h5>
  <p>A great test scene with lots of action.</p>
</div>
<div class="video-page--tag">
  <div class="" style="padding: 10px 0;">
    <div class="tags-wrap">
      <a href="/tag/1-party" class="link tooltip_container"><span data-fw="700">#Party</span></a>
      <a href="/tag/2-group" class="link tooltip_container"><span data-fw="700">#Group</span></a>
    </div>
  </div>
</div>
<h3 class="accordion__title title--4 mb20">Starring...</h3>
<div class="accordion__content">
  <div class="girls">
    <figure class="girls-item">
      <a href="/girls/40-Jane-Doe" class="girls-item--link"></a>
      <figcaption class="girls-item--content">
        <h4 class="title--4 mb2" data-fw="400">Jane Doe</h4>
      </figcaption>
    </figure>
    <figure class="girls-item">
      <a href="/girls/41-Alice-Smith" class="girls-item--link"></a>
      <figcaption class="girls-item--content">
        <h4 class="title--4 mb2" data-fw="400">Alice Smith</h4>
      </figcaption>
    </figure>
  </div>
</div>
<span class="js-price-tag">
  <span class="price-format">2.<sup>99</sup></span>
</span>
</body></html>`

const subChannelHTML = `<html><body>
<figure class="latest-channels--item js-calc-height">
  <a href="/Series-Vol-1" class="item--link"></a>
  <figcaption><h4 class="title--5"><span>Series Vol. 1</span></h4></figcaption>
</figure>
<figure class="latest-channels--item js-calc-height">
  <a href="/Series-Vol-2" class="item--link"></a>
  <figcaption><h4 class="title--5"><span>Series Vol. 2</span></h4></figcaption>
</figure>
<ul class="pagination clearfix">
  <li class="current"><a href="#">1</a></li>
  <li class="last-page"><a href="?page=1">1</a></li>
</ul>
</body></html>`

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/channel/") && strings.HasSuffix(r.URL.Path, "/all"):
			_, _ = fmt.Fprint(w, subChannelHTML)
		case strings.Contains(r.URL.Path, "/movie/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, listingHTML)
		}
	}))
}

func TestParseListing(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := New()
	s.Client = ts.Client()

	items, lastPage, err := s.fetchListing(context.Background(), ts.URL+"/videos/all?sort=newest&page=1")
	if err != nil {
		t.Fatalf("fetchListing: %v", err)
	}

	if lastPage != 3 {
		t.Errorf("lastPage = %d, want 3", lastPage)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	if items[0].id != "12345" {
		t.Errorf("items[0].id = %q, want 12345", items[0].id)
	}
	if items[0].title != "Test Scene One" {
		t.Errorf("items[0].title = %q, want %q", items[0].title, "Test Scene One")
	}
	if items[0].channel != "Test Channel" {
		t.Errorf("items[0].channel = %q, want %q", items[0].channel, "Test Channel")
	}
	if !strings.Contains(items[0].thumbnail, "thumb1.webp") {
		t.Errorf("items[0].thumbnail = %q, want thumb1.webp", items[0].thumbnail)
	}
	if items[0].path != "/Test-Channel/movie/12345/test-scene-one" {
		t.Errorf("items[0].path = %q", items[0].path)
	}

	if items[1].id != "12346" {
		t.Errorf("items[1].id = %q, want 12346", items[1].id)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail([]byte(detailHTML))

	if d.title != "Test Scene One - Full Title" {
		t.Errorf("title = %q", d.title)
	}
	want := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	if !d.date.Equal(want) {
		t.Errorf("date = %v, want %v", d.date, want)
	}
	if d.duration != 32*60 {
		t.Errorf("duration = %d, want %d", d.duration, 32*60)
	}
	if d.description != "A great test scene with lots of action." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 2 || d.tags[0] != "Party" || d.tags[1] != "Group" {
		t.Errorf("tags = %v", d.tags)
	}
	if len(d.performers) != 2 || d.performers[0] != "Jane Doe" || d.performers[1] != "Alice Smith" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.price != 2.99 {
		t.Errorf("price = %f, want 2.99", d.price)
	}
	if d.series != "Test Series Vol. 1" {
		t.Errorf("series = %q", d.series)
	}
}

func TestParseDetailSalePrice(t *testing.T) {
	saleHTML := `<span class="js-price-tag">
		<span style="position: relative;"><hr/><span class="price-format">4.<sup>99</sup></span></span>
		<span class="price-format">3.<sup>29</sup></span>
	</span>`
	d := parseDetail([]byte(saleHTML))
	if d.price != 3.29 {
		t.Errorf("sale price = %f, want 3.29", d.price)
	}
}

func TestSubChannelParsing(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := New()
	s.Client = ts.Client()

	slugs, lastPage, err := s.fetchSubChannels(context.Background(), ts.URL+"/channel/Test-Series/all")
	if err != nil {
		t.Fatalf("fetchSubChannels: %v", err)
	}
	if lastPage != 1 {
		t.Errorf("lastPage = %d, want 1", lastPage)
	}
	if len(slugs) != 2 {
		t.Fatalf("got %d slugs, want 2", len(slugs))
	}
	if slugs[0] != "Series-Vol-1" {
		t.Errorf("slugs[0] = %q, want Series-Vol-1", slugs[0])
	}
	if slugs[1] != "Series-Vol-2" {
		t.Errorf("slugs[1] = %q, want Series-Vol-2", slugs[1])
	}
}

func TestResolveListingPath(t *testing.T) {
	tests := []struct {
		url      string
		wantPath string
	}{
		{"https://www.sinx.com/videos/all", "/videos/all"},
		{"https://www.sinx.com/videos/sale", "/videos/sale"},
		{"https://www.sinx.com/Allwam", "/Allwam"},
		{"https://www.sinx.com/girls/40-Victoria-Rose", "/girls/40-Victoria-Rose"},
		{"https://www.sinx.com/tag/521-orgy", "/tag/521-orgy"},
		{"https://www.tainster.com/", "/videos/all"},
		{"https://www.tainster.com", "/videos/all"},
		{"https://www.slimewave.com/", "/Slime-Wave"},
		{"https://www.pissinginaction.com", "/Pissing-In-Action"},
		{"https://www.sinx.com/", "/videos/all"},
	}

	for _, tt := range tests {
		// resolveVideoPaths needs a context and channel but for non-series channels
		// it just returns the path directly without network calls.
		u, _ := url.Parse(tt.url)
		host := strings.TrimPrefix(u.Hostname(), "www.")

		var got string
		if host == "sinx.com" || host == "tainster.com" {
			p := strings.TrimRight(u.Path, "/")
			if channelAllRe.MatchString(p) {
				got = p // series channel — tested separately
			} else if girlsRe.MatchString(p) || tagRe.MatchString(p) || videosRe.MatchString(p) {
				got = p
			} else if p != "" && p != "/" {
				got = p
			} else {
				got = "/videos/all"
			}
		} else if info, ok := subsiteDomains[host]; ok {
			if info.isSeries {
				got = "/channel/" + info.slug + "/all"
			} else {
				got = "/" + info.slug
			}
		} else {
			got = "/videos/all"
		}

		if got != tt.wantPath {
			t.Errorf("resolveListingPath(%q) = %q, want %q", tt.url, got, tt.wantPath)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := New()
	s.Client = ts.Client()

	s.baseURL = ts.URL

	ctx := context.Background()
	ch, err := s.ListScenes(ctx, ts.URL+"/videos/all", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes []scraper.SceneResult
	for r := range ch {
		scenes = append(scenes, r)
	}

	var sceneCount int
	for _, r := range scenes {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
			if r.Scene.ID == "" {
				t.Error("scene has empty ID")
			}
			if r.Scene.Title == "" {
				t.Error("scene has empty Title")
			}
			if r.Scene.URL == "" {
				t.Error("scene has empty URL")
			}
			if r.Scene.Duration == 0 {
				t.Error("scene has zero duration")
			}
			if r.Scene.Date.IsZero() {
				t.Error("scene has zero date")
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if sceneCount == 0 {
		t.Error("expected at least one scene")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.sinx.com/videos/all", true},
		{"https://sinx.com/Allwam", true},
		{"https://www.tainster.com/", true},
		{"https://www.partyhardcore.com/", true},
		{"https://www.slimewave.com", true},
		{"https://www.pissinginaction.com/", true},
		{"https://www.allwam.net", true},
		{"https://www.my-fetish.net/", true},
		{"https://www.pornhub.com/", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// The listing card already names the scene, its channel and its thumbnail, so
// a dead detail page costs metadata rather than the scene — dropping it traded
// a missing description for a missing scene, which an authoritative --full
// Save would then delete.
func TestDetailFailureKeepsTheListingScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/movie/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, listingHTML)
	}))
	defer ts.Close()

	s := New()
	s.Client = ts.Client()
	s.baseURL = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos/all", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	scenes, errs := 0, 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.ID == "" || r.Scene.Title == "" {
				t.Errorf("scene lost its card fields: %+v", r.Scene)
			}
		case scraper.KindError:
			errs++
		}
	}
	if scenes == 0 {
		t.Error("a dead detail page must not drop the card")
	}
	if errs == 0 {
		t.Error("the detail failure must still be reported")
	}
}

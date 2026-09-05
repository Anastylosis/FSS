package scoregrouputil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

const listingHTML = `<html><body>
<div class="li-item compact video">
<div class="box pos-rel">
<div class="item-img pos-rel of-h ">
<a href="https://www.example.com/videos/Performer-A/10001/?nats=abc123" >
<div class="lazyload-wrap" style="padding-bottom:56.25%;"><img src="https://cdn.example.com/posting_10001_xl.jpg" alt="Performer A - video" class="lazyload w-100" /></div></a>
<div class="video-icons pos-abs pab pal par pat">
<div class="time-ol sm pos-abs pab par">16:28 mins</div>
</div></div>
<div class="info p-2">
<div class="t-trunc mb-1">
<a href="https://www.example.com/videos/Performer-A/10001/?nats=abc123" class="i-title accent-text"> Scene Title One </a>
</div>
<div class="t-trunc mb-1">
<small class="i-date f-r pt-1">7 Days Ago</small><small class="i-model">Performer A, Performer B</small>
</div></div></div></div>
<div class="li-item compact video">
<div class="box pos-rel">
<div class="item-img pos-rel of-h ">
<a href="https://www.example.com/videos/Performer-C/10002/?nats=abc123" >
<div class="lazyload-wrap" style="padding-bottom:56.25%;"><img src="https://cdn.example.com/posting_10002_xl.jpg" alt="Performer C - video" class="lazyload w-100" /></div></a>
<div class="video-icons pos-abs pab pal par pat">
<div class="time-ol sm pos-abs pab par">22:08 mins</div>
</div></div>
<div class="info p-2">
<div class="t-trunc mb-1">
<a href="https://www.example.com/videos/Performer-C/10002/?nats=abc123" class="i-title accent-text"> Scene Title Two </a>
</div>
<div class="t-trunc mb-1">
<small class="i-date f-r pt-1">2 Weeks Ago</small><small class="i-model">Performer C</small>
</div></div></div></div>
<a href="?page=1">1</a>
<a href="?page=2">2</a>
<a href="?page=3">3</a>
</body></html>`

func TestParseListingPage(t *testing.T) {
	scenes := parseListingPage([]byte(listingHTML), "https://www.example.com")
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	s := scenes[0]
	if s.id != "10001" {
		t.Errorf("id = %q", s.id)
	}
	if s.title != "Scene Title One" {
		t.Errorf("title = %q", s.title)
	}
	if len(s.performers) != 2 || s.performers[0] != "Performer A" || s.performers[1] != "Performer B" {
		t.Errorf("performers = %v", s.performers)
	}
	if s.duration != 16*60+28 {
		t.Errorf("duration = %d", s.duration)
	}
	if s.thumb != "https://cdn.example.com/posting_10001_xl.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
	if strings.Contains(s.url, "nats=") {
		t.Errorf("url still has nats tracking: %q", s.url)
	}
}

func TestExtractMaxPage(t *testing.T) {
	maxPage := extractMaxPage([]byte(listingHTML))
	if maxPage != 3 {
		t.Errorf("maxPage = %d, want 3", maxPage)
	}
}

func TestHasNextPage(t *testing.T) {
	if !hasNextPage([]byte(listingHTML), 1) {
		t.Error("should have next page from page 1")
	}
	if hasNextPage([]byte(listingHTML), 3) {
		t.Error("should not have next page from page 3")
	}
}

const detailHTML = `<html><head>
<meta itemprop="uploadDate" content="2026-04-20T16:00:00+00:00" />
<meta property="og:description" content="Featuring Performer A at Studio. Description text here.&hellip;" />
</head><body>
<a href="/updates-tag/Big-Tits/2/?page=1&nats=abc" class="btn btn-ol-2 btn-sm mb-2 mr-2">Big Tits</a>
<a href="/updates-tag/Blonde/32/?page=1&nats=abc" class="btn btn-ol-2 btn-sm mb-2 mr-2">Blonde</a>
<a href="/updates-tag/MILF/64/?page=1&nats=abc" class="btn btn-ol-2 btn-sm mb-2 mr-2">MILF</a>
</body></html>`

func TestFetchDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML)
	}))
	defer ts.Close()

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test Studio", VideosPath: "/videos/"},
	}

	scene, err := s.fetchDetail(context.Background(), listingScene{
		id:         "10001",
		url:        ts.URL + "/videos/Performer-A/10001/",
		title:      "Scene Title",
		performers: []string{"Performer A"},
		duration:   988,
		thumb:      "https://cdn.example.com/thumb.jpg",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	if scene.ID != "10001" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Scene Title" {
		t.Errorf("Title = %q", scene.Title)
	}
	wantDate := time.Date(2026, 4, 20, 16, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if scene.Description != "Featuring Performer A at Studio. Description text here." {
		t.Errorf("Description = %q", scene.Description)
	}
	if len(scene.Tags) != 3 {
		t.Errorf("Tags = %v, want 3 tags", scene.Tags)
	}
	if scene.Duration != 988 {
		t.Errorf("Duration = %d", scene.Duration)
	}
}

func TestRun(t *testing.T) {
	singlePageListing := strings.ReplaceAll(listingHTML, "\n<a href=\"?page=2\">2</a>\n<a href=\"?page=3\">3</a>", "")
	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/10001/") {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		if strings.Contains(r.URL.Path, "/10002/") {
			_, _ = fmt.Fprint(w, strings.ReplaceAll(detailHTML, "2026-04-20", "2026-04-15"))
			return
		}
		_, _ = fmt.Fprint(w, strings.ReplaceAll(singlePageListing, "https://www.example.com", tsURL))
	}))
	defer ts.Close()
	tsURL = ts.URL

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test", VideosPath: "/videos/"},
	}

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL, scraper.ListOpts{Workers: 2}, out)

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	singlePageListing := strings.ReplaceAll(listingHTML, "\n<a href=\"?page=2\">2</a>\n<a href=\"?page=3\">3</a>", "")
	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/10001/") {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		_, _ = fmt.Fprint(w, strings.ReplaceAll(singlePageListing, "https://www.example.com", tsURL))
	}))
	defer ts.Close()
	tsURL = ts.URL

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test", VideosPath: "/videos/"},
	}

	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"10002": true},
	}, out)

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, out)
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].ID != "10001" {
		t.Errorf("got %d scenes, want [10001]", len(scenes))
	}
}

func TestModelURL(t *testing.T) {
	singlePageListing := strings.ReplaceAll(listingHTML, "\n<a href=\"?page=2\">2</a>\n<a href=\"?page=3\">3</a>", "")
	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/10001/") {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		if strings.Contains(r.URL.Path, "/10002/") {
			_, _ = fmt.Fprint(w, strings.ReplaceAll(detailHTML, "2026-04-20", "2026-04-15"))
			return
		}
		_, _ = fmt.Fprint(w, strings.ReplaceAll(singlePageListing, "https://www.example.com", tsURL))
	}))
	defer ts.Close()
	tsURL = ts.URL

	s := &Scraper{
		Client: ts.Client(),
		cfg:    SiteConfig{SiteID: "test", SiteBase: ts.URL, StudioName: "Test", VideosPath: "/videos/", ModelsPath: "/models/"},
	}

	modelURL := tsURL + "/models/Performer-A/123/"
	out := make(chan scraper.SceneResult)
	go s.Run(context.Background(), modelURL, scraper.ListOpts{Workers: 2}, out)

	var scenes []string
	var total int
	for r := range out {
		if r.Total > 0 {
			total = r.Total
			continue
		}
		if r.Kind == scraper.KindStoppedEarly {
			continue
		}
		if r.Err != nil {
			t.Logf("error: %v", r.Err)
			continue
		}
		scenes = append(scenes, r.Scene.ID)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
}

func TestStripNATS(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://example.com/models/A/1/?nats=abc123", "https://example.com/models/A/1/"},
		{"https://example.com/models/A/1/", "https://example.com/models/A/1/"},
		{"https://example.com/page?foo=bar&nats=abc", "https://example.com/page?foo=bar"},
	}
	for _, c := range cases {
		if got := stripNATS(c.in); got != c.want {
			t.Errorf("stripNATS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Tag buttons carry `?page=1` and model links can carry higher numbers; both
// used to be counted as pager links, inflating the scene total the progress
// line reports.
func TestExtractMaxPageIgnoresNonPagerLinks(t *testing.T) {
	body := []byte(`
<a href="/updates-tag/Big-Tits/2/?page=1" class="btn btn-ol-2">Big Tits</a>
<a href="/model/jane/?page=97" class="model">Jane</a>
<nav><a href="?page=1">1</a><a href="?page=2">2</a><a href="?page=3">3</a></nav>`)

	if got := extractMaxPage(body); got != 3 {
		t.Errorf("extractMaxPage = %d, want 3 — the pager, not the tag and model links", got)
	}
	if hasNextPage(body, 3) {
		t.Error("hasNextPage said there was a page after the last one")
	}
	if !hasNextPage(body, 2) {
		t.Error("hasNextPage missed the real next page")
	}
}

// A listing with no bare-query pager falls back to the loose match rather than
// concluding there is a single page: ending every walk after page one would be
// a silent, total data loss, and an over-counted progress total is not.
func TestExtractMaxPageFallsBackWhenThePagerIsAbsolute(t *testing.T) {
	body := []byte(`<nav>
		<a href="/scenes/?page=1">1</a>
		<a href="/scenes/?page=2">2</a>
	</nav>`)

	if got := extractMaxPage(body); got != 2 {
		t.Errorf("extractMaxPage = %d, want 2 via the fallback", got)
	}
	if !hasNextPage(body, 1) {
		t.Error("the fallback must still find the next page")
	}
}

func TestExtractMaxPageOnASinglePageListing(t *testing.T) {
	body := []byte(`<div class="listing"><a href="/scene/1">one</a></div>`)
	if got := extractMaxPage(body); got != 1 {
		t.Errorf("extractMaxPage = %d, want 1", got)
	}
	if hasNextPage(body, 1) {
		t.Error("a listing with no pager has no next page")
	}
}

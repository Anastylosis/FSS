package kink

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.kink.com", true},
		{"https://kink.com", true},
		{"https://www.kink.com/shoots", true},
		{"https://www.kink.com/channel/sex-and-submission", true},
		{"https://www.kink.com/shoots?channelIds=sexandsubmission", true},
		{"https://www.kink.com/shoot/108031", true},
		{"https://www.kinkmen.com", true},
		{"https://kinkmen.com", true},
		{"https://www.kinkmen.com/channel/bound-gods", true},
		{"https://www.kinkmen.com/shoot/108095", true},
		{"https://www.brazzers.com", false},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestResolveListingConfig(t *testing.T) {
	cases := []struct {
		url      string
		wantMode listingMode
		wantBase string
	}{
		{"https://www.kink.com", modeShootsAPI, "https://www.kink.com/shoots?sort=published"},
		{"https://kink.com", modeShootsAPI, "https://kink.com/shoots?sort=published"},
		{"https://www.kink.com/channel/sex-and-submission", modeShootsAPI, "https://www.kink.com/shoots?channelIds=sexandsubmission&sort=published"},
		{"https://www.kink.com/channel/divine-bitches", modeShootsAPI, "https://www.kink.com/shoots?channelIds=divinebitches&sort=published"},
		{"https://www.kink.com/model/43807/Syren-de-Mer", modeShootsAPI, "https://www.kink.com/shoots?performerIds=43807&sort=published"},
		{"https://www.kink.com/model/101391/Melissa-Stratton", modeShootsAPI, "https://www.kink.com/shoots?performerIds=101391&sort=published"},
		{"https://www.kink.com/shoots?performerIds=43807", modeShootsAPI, "https://www.kink.com/shoots?performerIds=43807&sort=published"},
		{"https://www.kink.com/shoots?channelIds=sexandsubmission&sort=published", modeShootsAPI, "https://www.kink.com/shoots?channelIds=sexandsubmission&sort=published"},
		{"https://www.kink.com/shoots?channelIds=whippedass", modeShootsAPI, "https://www.kink.com/shoots?channelIds=whippedass&sort=published"},
		{"https://www.kink.com/tag/first-anal-bdsm", modeDirectPage, "https://www.kink.com/tag/first-anal-bdsm"},
		{"https://www.kink.com/series/security-risk", modeSeries, "https://www.kink.com/series/security-risk"},
		{"https://www.kinkmen.com", modeShootsAPI, "https://www.kinkmen.com/shoots?sort=published"},
		{"https://www.kinkmen.com/channel/bound-gods", modeShootsAPI, "https://www.kinkmen.com/shoots?channelIds=boundgods&sort=published"},
		{"https://www.kinkmen.com/model/12345/Test", modeShootsAPI, "https://www.kinkmen.com/shoots?performerIds=12345&sort=published"},
		{"https://www.kinkmen.com/tag/bondage", modeDirectPage, "https://www.kinkmen.com/tag/bondage"},
		{"https://www.kinkmen.com/series/test", modeSeries, "https://www.kinkmen.com/series/test"},
	}
	for _, c := range cases {
		lc := resolveListingConfig(c.url)
		if lc.mode != c.wantMode {
			t.Errorf("resolveListingConfig(%q).mode = %d, want %d", c.url, lc.mode, c.wantMode)
		}
		if lc.baseURL != c.wantBase {
			t.Errorf("resolveListingConfig(%q).baseURL\n  got  %q\n  want %q", c.url, lc.baseURL, c.wantBase)
		}
	}
}

const listingHTML = `<html><body>
<div class="paginator d-flex justify-content-center align-items-center my-4" aria-label="pagination">
    <ul class="pagination justify-content-center mb-0">
		<li class="page-item disabled">
			<div class="page-link cursor-pointer" data-page="1"><<</div>
		</li>
		<li class="page-item disabled active">
			<div class="page-link cursor-pointer" data-page="1">1</div>
		</li>
		<li class="page-item">
			<div class="page-link cursor-pointer" data-page="5">5</div>
		</li>
		<li class="page-item">
			<div class="page-link cursor-pointer " data-page="2">>></div>
		</li>
    </ul>
</div>

<div class="card shoot-thumbnail ">
    <div class="card-img-top position-relative rounded-0">
        <div class="favorite-button icon mx-2 "
			data-id="108031"
			data-type="shoot">
		</div>
        <a href="/shoot/108031" class="d-block position-relative overflow-hidden" aria-label="Rent is Paid in Full">
            <span class="d-block ratio ratio-thumbnail">
                <img fetchpriority="high" data-src="https://imgopt02.kink.com/shoots/108031/thumb.png" alt="Video shoot" data-sfw="false" data-trailer-url="https://cdnp.kink.com/shoots/108031/trailer.mp4" class="has-kink-spinner"/>
            </span>
        </a>
    </div>
    <div class="card-body px-0 pb-1 pt-0 d-block text-start">
        <span class="card-body-title mt-2">
            <a href="/shoot/108031" title="Rent is Paid in Full" class="d-block overflow-hidden text-elipsis h5">
                Rent is Paid in Full
            </a>
            <span class="icon-quality"> 4K </span>
        </span>
        <small class="col-12 mt-2 fs-6 h2 text-truncate d-block text-primary no-blur">
            <a href="/model/101391/Melissa-Stratton" title="Melissa Stratton , Nade Nasty ">Melissa Stratton</a>
                    , <a href="/model/100937/Nade-Nasty" title="Melissa Stratton , Nade Nasty ">Nade Nasty</a>
        </small>
        <div class="shoot-thumbnail-footer fs-6 d-flex flex-wrap gap-3 mt-2 no-blur"><a href="/channel/sexandsubmission" class="channel-tag">
                    <small>
                        Sex And Submission</small>
                </a><small class="text-muted d-flex gap-3 no-blur">
                        <span class="no-blur">Apr 24, 2026</span>
                    <div class="thumb-ratings no-blur">
                            <span class="thumb-up no-blur">93%</span>
					</div></small></div>
	</div>
</div>

<div class="card shoot-thumbnail ">
    <div class="card-img-top position-relative rounded-0">
        <div class="favorite-button icon mx-2 "
			data-id="107986"
			data-type="shoot">
		</div>
        <a href="/shoot/107986" class="d-block position-relative overflow-hidden" aria-label="Second Scene Title">
            <span class="d-block ratio ratio-thumbnail">
                <img fetchpriority="high" data-src="https://imgopt02.kink.com/shoots/107986/thumb.png" alt="Video shoot" data-sfw="false" class="has-kink-spinner"/>
            </span>
        </a>
    </div>
    <div class="card-body px-0 pb-1 pt-0 d-block text-start">
        <span class="card-body-title mt-2">
            <a href="/shoot/107986" title="Second Scene Title" class="d-block overflow-hidden text-elipsis h5">
                Second Scene Title
            </a>
        </span>
        <small class="col-12 mt-2 fs-6 h2 text-truncate d-block text-primary no-blur">
            <a href="/model/56865/Carol-Test" title="Carol Test">Carol Test</a>
        </small>
        <div class="shoot-thumbnail-footer fs-6 d-flex flex-wrap gap-3 mt-2 no-blur"><a href="/channel/divinebitches" class="channel-tag">
                    <small>
                        Divine Bitches</small>
                </a><small class="text-muted d-flex gap-3 no-blur">
                        <span class="no-blur">Apr 20, 2026</span>
                    <div class="thumb-ratings no-blur">
                            <span class="thumb-up no-blur">88%</span>
					</div></small></div>
	</div>
</div>
</body></html>`

const detailHTML = `<html><head>
<script type="application/ld+json">{"@context":"https://schema.org/","@type":"VideoObject","name":"Rent is Paid in Full","thumbnailUrl":"https://imgopt02.kink.com/shoots/108031/og_thumb.png","uploadDate":"2026-04-24T06:00:00.000Z","contentUrl":"https://cdnp.kink.com/shoots/108031/trailer_high.mp4","description":"A detailed description of the scene.","duration":"PT1M31S","actor":[{"@type":"Person","name":"Melissa Stratton","url":"https://kink.com/model/101391/Melissa-Stratton"},{"@type":"Person","name":"Nade Nasty","url":"https://kink.com/model/100937/Nade-Nasty"}],"director":{"@type":"Person","name":"The Pope","url":"https://kink.com/director/60"},"inLanguage":"en"}</script>
</head><body>
<div class="kvjs-container bg-black ratio ratio-16x9" data-setup="{&quot;id&quot;:108031,&quot;channelName&quot;:&quot;sexandsubmission&quot;,&quot;title&quot;:&quot;Rent is Paid in Full&quot;,&quot;resolutions&quot;:{&quot;2160p&quot;:true,&quot;1080p&quot;:true,&quot;720p&quot;:true},&quot;duration&quot;:3079376,&quot;posterUrl&quot;:&quot;https://imgopt02.kink.com/shoots/108031/poster.png&quot;,&quot;thumbnailUrl&quot;:&quot;https://imgopt02.kink.com/shoots/108031/thumb.png&quot;,&quot;trackingData&quot;:{&quot;tagIds&quot;:[&quot;bdsm&quot;,&quot;domination&quot;,&quot;submission&quot;,&quot;rope-bondage&quot;],&quot;modelIds&quot;:[101391,100937],&quot;modelNames&quot;:[&quot;Melissa Stratton&quot;,&quot;Nade Nasty&quot;]}}"></div>
</body></html>`

func TestFetchListing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listingHTML)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	listBase := ts.URL + "/shoots?sort=published"
	entries, totalPages, err := s.fetchListing(context.Background(), listBase+"&page=1", listBase)
	if err != nil {
		t.Fatal(err)
	}

	if totalPages != 5 {
		t.Errorf("totalPages = %d, want 5", totalPages)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	e := entries[0]
	if e.id != "108031" {
		t.Errorf("id = %q, want 108031", e.id)
	}
	if e.title != "Rent is Paid in Full" {
		t.Errorf("title = %q", e.title)
	}
	if e.thumbnail != "https://imgopt02.kink.com/shoots/108031/thumb.png" {
		t.Errorf("thumbnail = %q", e.thumbnail)
	}
	if e.preview != "https://cdnp.kink.com/shoots/108031/trailer.mp4" {
		t.Errorf("preview = %q", e.preview)
	}
	if len(e.performers) != 2 || e.performers[0] != "Melissa Stratton" {
		t.Errorf("performers = %v", e.performers)
	}
	if e.channel != "Sex And Submission" {
		t.Errorf("channel = %q", e.channel)
	}
	if e.date != "Apr 24, 2026" {
		t.Errorf("date = %q", e.date)
	}

	e2 := entries[1]
	if e2.id != "107986" {
		t.Errorf("e2.id = %q, want 107986", e2.id)
	}
	if e2.channel != "Divine Bitches" {
		t.Errorf("e2.channel = %q", e2.channel)
	}
}

func TestResolveSiteID(t *testing.T) {
	cases := []struct {
		url        string
		wantSiteID string
	}{
		{"https://www.kink.com", "kink"},
		{"https://kink.com/channel/hogtied", "kink"},
		{"https://www.kinkmen.com", "kinkmen"},
		{"https://kinkmen.com/channel/bound-gods", "kinkmen"},
		{"https://www.kinkmen.com/shoot/108095", "kinkmen"},
	}
	for _, c := range cases {
		if siteID := resolveSiteID(c.url); siteID != c.wantSiteID {
			t.Errorf("resolveSiteID(%q) = %q, want %q", c.url, siteID, c.wantSiteID)
		}
	}
}

func TestFetchDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	entry := listEntry{
		id: "108031", title: "Rent is Paid in Full",
		url:        ts.URL + "/shoot/108031",
		thumbnail:  "https://imgopt02.kink.com/shoots/108031/thumb.png",
		preview:    "https://cdnp.kink.com/shoots/108031/trailer.mp4",
		performers: []string{"Melissa Stratton", "Nade Nasty"},
		channel:    "Sex And Submission",
		date:       "Apr 24, 2026",
	}

	scene, err := s.fetchDetail(context.Background(), entry, "kink", "https://www.kink.com")
	if err != nil {
		t.Fatal(err)
	}

	if scene.SiteID != "kink" {
		t.Errorf("SiteID = %q, want kink", scene.SiteID)
	}
	if scene.StudioURL != "https://www.kink.com" {
		t.Errorf("StudioURL = %q", scene.StudioURL)
	}
	if scene.Description != "A detailed description of the scene." {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Duration != 3079 {
		t.Errorf("Duration = %d, want 3079", scene.Duration)
	}
	if scene.Thumbnail != "https://imgopt02.kink.com/shoots/108031/og_thumb.png" {
		t.Errorf("Thumbnail = %q (should be from JSON-LD)", scene.Thumbnail)
	}
	if scene.Date.Format("2006-01-02") != "2026-04-24" {
		t.Errorf("Date = %v", scene.Date)
	}
	if scene.Studio != "Sex And Submission" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if len(scene.Tags) < 4 {
		t.Errorf("Tags = %v, want at least 4", scene.Tags)
	}
	has4k := false
	for _, tag := range scene.Tags {
		if tag == "4K" {
			has4k = true
		}
	}
	if !has4k {
		t.Errorf("Tags missing 4K: %v", scene.Tags)
	}
}

func TestFetchDetailKinkMen(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	entry := listEntry{
		id:  "108095",
		url: ts.URL + "/shoot/108095",
	}

	scene, err := s.fetchDetail(context.Background(), entry, "kinkmen", "https://www.kinkmen.com/channel/bound-gods")
	if err != nil {
		t.Fatal(err)
	}

	if scene.SiteID != "kinkmen" {
		t.Errorf("SiteID = %q, want kinkmen", scene.SiteID)
	}
	if scene.StudioURL != "https://www.kinkmen.com/channel/bound-gods" {
		t.Errorf("StudioURL = %q, want the requested URL", scene.StudioURL)
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/shoot/") {
			_, _ = fmt.Fprint(w, detailHTML)
		} else if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, listingHTML)
		} else {
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.StudioURL != ts.URL {
			t.Errorf("StudioURL = %q, want %q", sc.StudioURL, ts.URL)
		}
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/shoot/") {
			_, _ = fmt.Fprint(w, detailHTML)
		} else {
			_, _ = fmt.Fprint(w, listingHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"107986": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)

	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 || scenes[0].ID != "108031" {
		t.Errorf("got ids %v, want [108031]", scenes)
	}
}

const seriesHTML = `<html><body>
<div id="seriesPage">
	<video poster='https://imgopt02.kink.com/imagedb/102670/indexImage/full.png'></video>
	<video poster='https://imgopt02.kink.com/imagedb/102671/indexImage/full.png'></video>
	<source src='https://cdnp.kink.com/imagedb/102670/trailer/trailer.mp4'>
	<source src='https://cdnp.kink.com/imagedb/102671/trailer/trailer.mp4'>
</div>
</body></html>`

func TestListScenesSeries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/shoot/") {
			_, _ = fmt.Fprint(w, detailHTML)
		} else {
			_, _ = fmt.Fprint(w, seriesHTML)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/series/test-series", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if want := ts.URL + "/series/test-series"; sc.StudioURL != want {
			t.Errorf("StudioURL = %q, want %q", sc.StudioURL, want)
		}
	}
}

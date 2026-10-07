package adultprimeutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const fixtureListing = `<!DOCTYPE html>
<html><head>
<script>
var params = {"total":50,"pageSize":24,"niche":"","year":"","sort":"","q":"","website":"TestStudio","type":"","page":%d}
</script>
</head><body>
<div class="model-wrapper portal-video-wrapper">
    <div class="overlay-wrapper">
        <div class="overlay inline-preview" data-id="1001">
            <a href="/studios/video/1001?site=teststudio" style="display:block;">
            </a>
        </div>
        <div class="ratio-16-9" style="background-image: url(https://cdn.example.com/1001/thumb.jpg); position:relative; background-size:cover;">
            <a class="absolute" href="/studios/video/1001?site=teststudio"></a>
        </div>
    </div>
    <div class="video-description video-description-front">
        <div class="description-wrapper">
            <div class="video-description-front-text-container">
                <span class="description-title ellipsis">First Scene Title </span>
                <span class="description-releasedate"><i class="fa fa-calendar"></i> May 06, 2026</span>
                <span class="description-duration"><i class="fa fa-clock-o"></i> 31:00</span>
            </div>
        </div>
    </div>
</div>
<div class="model-wrapper portal-video-wrapper">
    <div class="overlay-wrapper">
        <div class="overlay inline-preview" data-id="1002">
            <a href="/studios/video/1002?site=teststudio" style="display:block;">
            </a>
        </div>
        <div class="ratio-16-9" style="background-image: url(https://cdn.example.com/1002/thumb.jpg); position:relative; background-size:cover;">
            <a class="absolute" href="/studios/video/1002?site=teststudio"></a>
        </div>
    </div>
    <div class="video-description video-description-front">
        <div class="description-wrapper">
            <div class="video-description-front-text-container">
                <span class="description-title ellipsis">Second Scene &amp; More </span>
                <span class="description-releasedate"><i class="fa fa-calendar"></i> Apr 30, 2026</span>
                <span class="description-duration"><i class="fa fa-clock-o"></i> 18:30</span>
            </div>
        </div>
    </div>
</div>
</body></html>`

const fixtureDetail = `<!DOCTYPE html>
<html><head></head><body>
<div class="video-description">
    <div class="description-wrapper">
        <span class="description-releasedate"><i class="fa fa-calendar"></i> 06-05-2026</span>
        <span class="description-duration hidden-xs"><i class="fa fa-clock-o"></i> 31 min</span>
    </div>
</div>
<div class="update-info-container">
    <h1 class=" update-info-title">
        First Scene Title                                     Full video by Test Studio
    </h1>
    <p class="update-info-line regular">
        <b>Studio: <a href="/studios/studio/TestStudio">Test Studio</a></b>
    </p>
    <p class="update-info-line regular">
        <b>Niches: </b>
        <a href='/studios/videos?niche=Big+Ass' class='site-link'>Big Ass</a>,
        <a href='/studios/videos?niche=Brunette' class='site-link'>Brunette</a>
    </p>
    <p class="update-info-line regular">
        <b>Performers:</b>
        <a href="/pornstar/jane+doe" class="">Jane Doe</a>,
        <a href="/pornstar/john+smith" class="">John Smith</a>
    </p>
    <p class="update-info-line ap-limited-description-text regular hidden-xs">
        A great scene description here.
    </p>
</div>
</body></html>`

func newTestScraper(ts *httptest.Server) *Scraper {
	s := &Scraper{
		client: ts.Client(),
		base:   ts.URL,
		cfg: SiteConfig{
			SiteID:     "test-studio",
			Slug:       "TestStudio",
			StudioName: "Test Studio",
		},
	}
	return s
}

func TestParseListingPage(t *testing.T) {
	body := []byte(fmt.Sprintf(fixtureListing, 1))
	items := parseListingPage(body)

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	if items[0].id != "1001" {
		t.Errorf("item[0].id = %q, want %q", items[0].id, "1001")
	}
	if items[0].title != "First Scene Title" {
		t.Errorf("item[0].title = %q, want %q", items[0].title, "First Scene Title")
	}
	if items[0].date != "May 06, 2026" {
		t.Errorf("item[0].date = %q, want %q", items[0].date, "May 06, 2026")
	}
	if items[0].duration != "31:00" {
		t.Errorf("item[0].duration = %q, want %q", items[0].duration, "31:00")
	}

	if items[1].id != "1002" {
		t.Errorf("item[1].id = %q, want %q", items[1].id, "1002")
	}
	if items[1].title != "Second Scene & More" {
		t.Errorf("item[1].title = %q, want %q", items[1].title, "Second Scene & More")
	}
}

func TestParseParams(t *testing.T) {
	body := []byte(fmt.Sprintf(fixtureListing, 1))
	p := parseParams(body)

	if p.Total != 50 {
		t.Errorf("total = %d, want 50", p.Total)
	}
	if p.PageSize != 24 {
		t.Errorf("pageSize = %d, want 24", p.PageSize)
	}
	if p.Page != 1 {
		t.Errorf("page = %d, want 1", p.Page)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"31:00", 1860},
		{"18:30", 1110},
		{"1:05", 65},
		{"", 0},
		{"bad", 0},
	}
	for _, tc := range tests {
		got := parseutil.ParseDurationColon(tc.in)
		if got != tc.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/studios/videos":
			page := r.URL.Query().Get("page")
			if page == "" || page == "1" {
				_, _ = fmt.Fprintf(w, fixtureListing, 1)
			} else {
				_, _ = fmt.Fprint(w, `<html><body></body></html>`)
			}
		case "/studios/video/1001":
			_, _ = fmt.Fprint(w, fixtureDetail)
		case "/studios/video/1002":
			_, _ = fmt.Fprint(w, fixtureDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/studios/studio/TestStudio", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
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
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if sceneCount != 2 {
		t.Errorf("got %d scenes, want 2", sceneCount)
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/studios/videos":
			_, _ = fmt.Fprintf(w, fixtureListing, 1)
		case "/studios/video/1002":
			_, _ = fmt.Fprint(w, fixtureDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/studios/studio/TestStudio", scraper.ListOpts{
		KnownIDs: map[string]bool{"1001": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotStoppedEarly bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			t.Error("should not have received a scene")
		case scraper.KindStoppedEarly:
			gotStoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if !gotStoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestDetailParsing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/studios/videos":
			_, _ = fmt.Fprintf(w, fixtureListing, 1)
		case "/studios/video/1001":
			_, _ = fmt.Fprint(w, fixtureDetail)
		case "/studios/video/1002":
			_, _ = fmt.Fprint(w, fixtureDetail)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/studios/studio/TestStudio", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	for r := range ch {
		if r.Kind != scraper.KindScene {
			continue
		}
		sc := r.Scene

		if sc.Title != "First Scene Title" {
			t.Errorf("title = %q, want %q", sc.Title, "First Scene Title")
		}
		if len(sc.Performers) != 2 {
			t.Errorf("performers = %v, want 2", sc.Performers)
		} else {
			if sc.Performers[0] != "Jane Doe" {
				t.Errorf("performers[0] = %q, want %q", sc.Performers[0], "Jane Doe")
			}
		}
		if len(sc.Tags) != 2 {
			t.Errorf("tags = %v, want 2", sc.Tags)
		}
		if sc.Description != "A great scene description here." {
			t.Errorf("description = %q", sc.Description)
		}
		if sc.Duration != 1860 {
			t.Errorf("duration = %d, want 1860", sc.Duration)
		}
		if sc.Date.IsZero() {
			t.Error("date is zero")
		}
		break
	}
}

func TestListScenesNicheFilter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/studios/videos":
			if r.URL.Query().Get("niche") != "Blowjob" {
				t.Errorf("expected niche=Blowjob, got niche=%s", r.URL.Query().Get("niche"))
			}
			page := r.URL.Query().Get("page")
			if page == "" || page == "1" {
				_, _ = fmt.Fprintf(w, fixtureListing, 1)
			} else {
				_, _ = fmt.Fprint(w, `<html><body></body></html>`)
			}
		case "/studios/video/1001", "/studios/video/1002":
			_, _ = fmt.Fprint(w, fixtureDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	studioURL := ts.URL + "/studios/videos?website=TestStudio&niche=Blowjob"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var sceneCount int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
			if r.Scene.StudioURL != studioURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, studioURL)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if sceneCount != 2 {
		t.Errorf("got %d scenes, want 2", sceneCount)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		SiteID:     "test-studio",
		Slug:       "Clubsweethearts",
		StudioName: "Club SweetHearts",
	})

	tests := []struct {
		url  string
		want bool
	}{
		{"https://adultprime.com/studios/studio/Clubsweethearts", true},
		{"https://adultprime.com/studios/videos?website=Clubsweethearts", true},
		{"https://adultprime.com/studios/studio/clubsweethearts", true},
		{"https://adultprime.com/studios/studio/OtherStudio", false},
		{"https://example.com/studios/studio/Clubsweethearts", false},
	}

	for _, tc := range tests {
		got := s.MatchesURL(tc.url)
		if got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

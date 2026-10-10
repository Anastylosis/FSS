package tmwutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

const fixtureListing = `<!DOCTYPE html>
<html><head></head><body>
<div class="content-block">
    <div class="list-page-heading d-flex">
        <span class="list-page-heading__count clr-main">42</span>
    </div>
    <div class="thumb-list">
        <a class="thumb thumb-video thumb--loading" href="%s/trailers/First-Test-Scene.html">
            <div class="thumb__image__container d-block" data-thumb-scroller>
                <picture class="thumb__picture thumb__picture--set d-block rounding">
                    <img class="ani-th thumb__image" srcset="content/contentthumbs/10/01/100101-1x.jpg 1x,content/contentthumbs/10/01/100101-2x.jpg 2x" />
                </picture>
            </div>
            <div class="thumb__info">
                <header class="thumb__heading d-flex">
                    <h2 class="thumb__title">
                        <span class="thumb__title-link" href="%s/trailers/First-Test-Scene.html" title="First Test Scene">First Test Scene</span>
                    </h2>
                </header>
                <ul class="thumb__actor-list actor__list d-flex">
                    <li class="actor__element">
                        <span class="actor__name" title="Jane Doe">Jane Doe</span>
                    </li>
                    <li class="actor__element">
                        <span class="actor__name" title="John Smith">John Smith</span>
                    </li>
                </ul>
                <div class="thumb__detail d-flex">
                    <span class="thumb__detail__site-link clr-grey">Test Studio</span>
                    <time class="thumb__detail__datetime clr-grey" datetime="2026-05-01T12:00:00">May 01, 2026</time>
                </div>
            </div>
        </a>
        <a class="thumb thumb-video thumb--loading" href="%s/trailers/Second-Scene-Here.html">
            <div class="thumb__image__container d-block" data-thumb-scroller>
                <picture class="thumb__picture thumb__picture--set d-block rounding">
                    <img class="ani-th thumb__image" srcset="content/contentthumbs/20/02/200202-1x.jpg 1x,content/contentthumbs/20/02/200202-2x.jpg 2x" />
                </picture>
            </div>
            <div class="thumb__info">
                <header class="thumb__heading d-flex">
                    <h2 class="thumb__title">
                        <span class="thumb__title-link" href="%s/trailers/Second-Scene-Here.html" title="Second Scene &amp; More">Second Scene &amp; More</span>
                    </h2>
                </header>
                <ul class="thumb__actor-list actor__list d-flex">
                    <li class="actor__element">
                        <span class="actor__name" title="Alice Wonder">Alice Wonder</span>
                    </li>
                </ul>
                <div class="thumb__detail d-flex">
                    <span class="thumb__detail__site-link clr-grey">Test Studio</span>
                    <time class="thumb__detail__datetime clr-grey" datetime="2026-04-28T10:30:00">April 28, 2026</time>
                </div>
            </div>
        </a>
    </div>
</div>
</body></html>`

const fixtureDetail = `<!DOCTYPE html>
<html><head>
<meta property="og:title" content="First Test Scene - Test Studio">
<meta property="og:description" content="A great test scene description.">
<meta property="video:duration" content="1860">
<meta property="video:tag" content="Hardcore">
<meta property="video:tag" content="Blowjob">
<meta property="video:tag" content="Brunette">
<meta property="video:actor:username" content="Jane Doe">
</head><body>
<div class="player-wrapper">
    <h1 class="video-title" id="video-title">First Test Scene</h1>
    <span class="video-info-time">31 min</span>
    <time class="video-info-date" datetime="2026-05-01T12:00:00">May 01, 2026</time>
</div>
<div class="video-info-actors">
    <a class="video-actor-link actor__link" href="/models/Jane-Doe.html" title="Jane Doe">Jane Doe</a>,
    <a class="video-actor-link actor__link" href="/models/John-Smith.html" title="John Smith">John Smith</a>
</div>
<p class="video-description-text" data-video-desc-text>
    A great test scene description with more detail.
</p>
<div class="video-tags">
    <a class="video-tag-link" data-id="1" href="/categories/hardcore.html">Hardcore</a>
    <a class="video-tag-link" data-id="2" href="/categories/blowjob.html">Blowjob</a>
</div>
</body></html>`

func listingHTML(base string) string {
	return fmt.Sprintf(fixtureListing, base, base, base, base)
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{
		client: ts.Client(),
		base:   ts.URL,
		cfg: SiteConfig{
			SiteID:     "tmw-test-studio",
			Slug:       "teststudio",
			Domain:     "teststudio.com",
			StudioName: "Test Studio",
		},
	}
}

func TestParseListingPage(t *testing.T) {
	base := "https://teststudio.com"
	body := []byte(listingHTML(base))
	items := parseListingPage(body, base)

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	if items[0].id != "First-Test-Scene" {
		t.Errorf("item[0].id = %q, want %q", items[0].id, "First-Test-Scene")
	}
	if items[0].title != "First Test Scene" {
		t.Errorf("item[0].title = %q, want %q", items[0].title, "First Test Scene")
	}
	if items[0].date.IsZero() {
		t.Error("item[0].date is zero")
	}
	if len(items[0].performers) != 2 {
		t.Errorf("item[0].performers = %v, want 2", items[0].performers)
	} else if items[0].performers[0] != "Jane Doe" {
		t.Errorf("item[0].performers[0] = %q, want %q", items[0].performers[0], "Jane Doe")
	}
	if items[0].thumb != base+"/content/contentthumbs/10/01/100101-2x.jpg" {
		t.Errorf("item[0].thumb = %q", items[0].thumb)
	}

	if items[1].id != "Second-Scene-Here" {
		t.Errorf("item[1].id = %q, want %q", items[1].id, "Second-Scene-Here")
	}
	if items[1].title != "Second Scene & More" {
		t.Errorf("item[1].title = %q, want %q", items[1].title, "Second Scene & More")
	}
	if len(items[1].performers) != 1 {
		t.Errorf("item[1].performers = %v, want 1", items[1].performers)
	}
}

func TestParseTotal(t *testing.T) {
	body := []byte(listingHTML("https://example.com"))
	total := parseTotal(body)
	if total != 42 {
		t.Errorf("total = %d, want 42", total)
	}
}

func TestParseTotalWithComma(t *testing.T) {
	body := []byte(`<span class="list-page-heading__count clr-main">6,786</span>`)
	total := parseTotal(body)
	if total != 6786 {
		t.Errorf("total = %d, want 6786", total)
	}
}

func TestListScenes(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, listingHTML(ts.URL))
		case "/categories/movies_2_d.html":
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		case "/trailers/First-Test-Scene.html":
			_, _ = fmt.Fprint(w, fixtureDetail)
		case "/trailers/Second-Scene-Here.html":
			_, _ = fmt.Fprint(w, fixtureDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	studioURL := ts.URL + "/categories/movies.html"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
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

func TestListScenesKnownIDs(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, listingHTML(ts.URL))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"First-Test-Scene": true},
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
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/movies_1_d.html":
			_, _ = fmt.Fprint(w, listingHTML(ts.URL))
		case "/categories/movies_2_d.html":
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		case "/trailers/First-Test-Scene.html":
			_, _ = fmt.Fprint(w, fixtureDetail)
		case "/trailers/Second-Scene-Here.html":
			_, _ = fmt.Fprint(w, fixtureDetail)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	for r := range ch {
		if r.Kind != scraper.KindScene {
			continue
		}
		sc := r.Scene

		if sc.Title != "First Test Scene" && sc.Title != "Second Scene & More" {
			t.Errorf("unexpected title = %q", sc.Title)
		}
		if sc.Title == "First Test Scene" {
			if len(sc.Performers) != 2 {
				t.Errorf("performers = %v, want 2", sc.Performers)
			} else if sc.Performers[0] != "Jane Doe" {
				t.Errorf("performers[0] = %q, want %q", sc.Performers[0], "Jane Doe")
			}
		}
		if len(sc.Tags) != 3 {
			t.Errorf("tags = %v, want 3", sc.Tags)
		}
		if sc.Description != "A great test scene description with more detail." {
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

const fixtureHubListing = `<!DOCTYPE html>
<html><head></head><body>
<div class="content-block">
    <div class="list-page-heading d-flex">
        <span class="list-page-heading__count clr-main">18</span>
    </div>
    <div class="thumb-list">
        <a class="thumb thumb-photo thumb--loading" href="%s/trailers/Hub-Scene-One.html">
            <div class="thumb__image__container d-block" data-thumb-scroller>
                <picture class="thumb__picture thumb__picture--set d-block rounding">
                    <img class="ani-th thumb__image" srcset="content/contentthumbs/30/03/300303-1x.jpg 1x,content/contentthumbs/30/03/300303-2x.jpg 2x" />
                </picture>
            </div>
            <div class="thumb__info">
                <header class="thumb__heading d-flex">
                    <h2 class="thumb__title">
                        <span class="thumb__title-link" href="%s/trailers/Hub-Scene-One.html" title="Hub Scene One">Hub Scene One</span>
                    </h2>
                </header>
                <ul class="thumb__actor-list actor__list d-flex">
                    <li class="actor__element">
                        <span class="actor__name" title="Hub Actor">Hub Actor</span>
                    </li>
                </ul>
                <div class="thumb__detail d-flex">
                    <span class="thumb__detail__site-link clr-grey">Hub Studio</span>
                    <time class="thumb__detail__datetime clr-grey" datetime="2026-04-15T08:00:00">April 15, 2026</time>
                </div>
            </div>
        </a>
    </div>
</div>
</body></html>`

func hubListingHTML(base string) string {
	return fmt.Sprintf(fixtureHubListing, base, base)
}

func TestListScenesHub(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/categories/hubstudio_1_d.html":
			_, _ = fmt.Fprint(w, hubListingHTML(ts.URL))
		case "/categories/hubstudio_2_d.html":
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		case "/trailers/Hub-Scene-One.html":
			_, _ = fmt.Fprint(w, fixtureDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client: ts.Client(),
		base:   ts.URL,
		cfg: SiteConfig{
			SiteID:     "tmw-hubstudio",
			Slug:       "hubstudio",
			Domain:     "hubstudio.com",
			StudioName: "Hub Studio",
			Hub:        true,
		},
	}
	studioURL := ts.URL + "/categories/hubstudio_1_d.html"
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
			if r.Scene.ID != "Hub-Scene-One" {
				t.Errorf("scene ID = %q, want Hub-Scene-One", r.Scene.ID)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if sceneCount != 1 {
		t.Errorf("got %d scenes, want 1", sceneCount)
	}
}

func TestModelPage(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/Jane-Doe.html":
			_, _ = fmt.Fprint(w, listingHTML(ts.URL))
		case "/trailers/First-Test-Scene.html", "/trailers/Second-Scene-Here.html":
			_, _ = fmt.Fprint(w, fixtureDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	studioURL := ts.URL + "/models/Jane-Doe.html"
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
		SiteID:     "tmw-anal-angels",
		Slug:       "anal-angels",
		Domain:     "anal-angels.com",
		StudioName: "Anal Angels",
	})

	tests := []struct {
		url  string
		want bool
	}{
		{"https://anal-angels.com/categories/movies.html", true},
		{"https://anal-angels.com/trailers/Some-Scene.html", true},
		{"https://www.anal-angels.com/categories/movies.html", true},
		{"https://teenmegaworld.net/categories/anal-angels_1_d.html", true},
		{"https://teenmegaworld.net/sites/anal-angels", true},
		{"https://teenmegaworld.net/categories/beauty4k_1_d.html", false},
		{"https://other-site.com/categories/movies.html", false},
	}

	for _, tc := range tests {
		got := s.MatchesURL(tc.url)
		if got != tc.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

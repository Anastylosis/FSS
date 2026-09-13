package paysiteutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const listingPage = `<html><head>
<script id="__NEXT_DATA__" type="application/json">
{
  "props": {
    "pageProps": {
      "contents": {
        "total": 2,
        "page": "1",
        "per_page": "8",
        "total_pages": 1,
        "data": [
          {
            "id": 100,
            "title": "Test Scene",
            "slug": "test-scene",
            "publish_date": "2026/05/15 12:00:00",
            "seconds_duration": 872,
            "videos_duration": "14:32",
            "thumb": "https://cdn.example.com/thumb.jpg",
            "models": ["Jane"],
            "models_slugs": [{"name": "Jane", "slug": "jane"}],
            "tags": ["Solo"],
            "description": "A test.",
            "content_price": 0,
            "site": "Test Site"
          },
          {
            "id": 99,
            "title": "Scene Two",
            "slug": "scene-two",
            "publish_date": "2026/04/01 08:00:00",
            "seconds_duration": 600,
            "thumb": "https://cdn.example.com/thumb2.jpg",
            "models": [],
            "models_slugs": [],
            "tags": [],
            "description": "",
            "content_price": 15,
            "site": "Test Site"
          }
        ]
      }
    }
  }
}
</script></head><body></body></html>`

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listingPage)
	}))
}

func TestFetchListing(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "test.com", StudioName: "Test Studio"})
	s.client = ts.Client()

	items, total, totalPages, err := s.fetchListing(context.Background(), ts.URL+"/videos?page=1")
	if err != nil {
		t.Fatalf("fetchListing: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if totalPages != 1 {
		t.Errorf("totalPages = %d, want 1", totalPages)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Title != "Test Scene" {
		t.Errorf("title = %q", items[0].Title)
	}
	if items[0].SecondsDuration != 872 {
		t.Errorf("duration = %d", items[0].SecondsDuration)
	}
}

func TestToScene(t *testing.T) {
	s := New(SiteConfig{SiteID: "testsite", Domain: "test.com", StudioName: "Test Studio"})
	item := contentItem{
		ID:              100,
		Title:           "Test Scene",
		Slug:            "test-scene",
		PublishDate:     "2026/05/15 12:00:00",
		SecondsDuration: 872,
		Thumb:           "https://cdn.example.com/thumb.jpg",
		ModelsSlugs:     []modelSlug{{Name: "Jane", Slug: "jane"}},
		Tags:            []string{"Solo"},
		Description:     "A test.",
		Site:            "Test Studio",
	}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	scene := s.toScene(item, "https://test.com", now)

	if scene.ID != "100" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.URL != "https://test.com/videos/test-scene" {
		t.Errorf("URL = %q", scene.URL)
	}
	wantDate := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	if !scene.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", scene.Date, wantDate)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Jane" {
		t.Errorf("Performers = %v", scene.Performers)
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "test.com", StudioName: "Test Studio"})
	s.client = ts.Client()

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var sceneCount int
	for r := range ch {
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

func TestListPathDefaultsToVideos(t *testing.T) {
	if got := (SiteConfig{}).listPath(); got != "videos" {
		t.Errorf("listPath() = %q, want videos", got)
	}
	if got := (SiteConfig{ListPath: "scenes"}).listPath(); got != "scenes" {
		t.Errorf("listPath() = %q, want scenes", got)
	}
}

// A site serving the template at /scenes must have every derived URL follow —
// the listing it walks, the patterns it advertises and the scene URLs it
// stores.
func TestScenesListPathThreadsThrough(t *testing.T) {
	var asked []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		_, _ = fmt.Fprint(w, listingPage)
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "yesgirlz", Domain: "yesgirlz.com", StudioName: "Yes Girlz", ListPath: "scenes"})
	s.client = ts.Client()

	ch, err := s.ListScenes(context.Background(), ts.URL+"/scenes", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var urls []string
	for r := range ch {
		if r.Kind == scraper.KindScene {
			urls = append(urls, r.Scene.URL)
		}
	}
	if len(asked) == 0 || asked[0] != "/scenes" {
		t.Errorf("fetched %v, want /scenes first", asked)
	}
	if len(urls) == 0 || urls[0] != "https://yesgirlz.com/scenes/test-scene" {
		t.Errorf("scene URLs = %v", urls)
	}
	if got := s.Patterns()[1]; got != "yesgirlz.com/scenes" {
		t.Errorf("Patterns()[1] = %q", got)
	}
}

func TestMatchesURLIsDomainAnchored(t *testing.T) {
	s := New(SiteConfig{SiteID: "melinamay", Domain: "melina-may.com", StudioName: "Melina-May"})
	for _, u := range []string{"https://melina-may.com/", "https://www.melina-may.com/videos", "http://melina-may.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://melinaxmayxcom.example/", "https://notmelina-may.com/", "https://example.com/melina-may.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestModelPatternOverride(t *testing.T) {
	s := New(SiteConfig{SiteID: "vrallure", Domain: "vrallure.com", ModelPattern: "vrallure.com/models/{id}-{slug}"})
	if got := s.Patterns()[2]; got != "vrallure.com/models/{id}-{slug}" {
		t.Errorf("Patterns()[2] = %q", got)
	}
	plain := New(SiteConfig{SiteID: "x", Domain: "x.com"})
	if got := plain.Patterns()[2]; got != "x.com/models/{slug}" {
		t.Errorf("Patterns()[2] = %q", got)
	}
}

func TestAliasesWidenMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		SiteID: "povperv", Domain: "tour.povperv.com", StudioName: "POV Perv",
		ListPath: "scenes", Aliases: []string{"povperv.com"},
	})
	for _, u := range []string{
		"https://tour.povperv.com/scenes",
		"https://povperv.com",
		"https://www.povperv.com/",
		"http://povperv.com/models/monica-minx",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://povperv.com.example/", "https://notpovperv.com/", "https://example.com/povperv.com"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// An alias only redirects to the real host; nothing is served under the listing
// path there, so the scrape has to go to the configured domain. POV Perv's apex
// answers 404 for /scenes and 301 for /.
func TestAliasURLFetchesTheConfiguredDomain(t *testing.T) {
	s := New(SiteConfig{
		SiteID: "povperv", Domain: "tour.povperv.com",
		ListPath: "scenes", Aliases: []string{"povperv.com"},
	})
	cases := []struct{ in, want string }{
		{"https://povperv.com", "https://tour.povperv.com"},
		{"https://www.povperv.com/scenes", "https://tour.povperv.com"},
		{"https://tour.povperv.com/scenes", "https://tour.povperv.com"},
		{"http://tour.povperv.com/scenes", "http://tour.povperv.com"},
		{"https://www.tour.povperv.com/", "https://www.tour.povperv.com"},
		{"not a url at all", "https://tour.povperv.com"},
		// Anything not named as an alias is kept as given — that is what points
		// the offline tests at their httptest server.
		{"http://127.0.0.1:8080/scenes", "http://127.0.0.1:8080"},
	}
	for _, c := range cases {
		if got := s.baseFor(c.in); got != c.want {
			t.Errorf("baseFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNoAliasesKeepsTheOperatorsHost(t *testing.T) {
	s := New(SiteConfig{SiteID: "bjraw", Domain: "bjraw.com"})
	if got := s.baseFor("http://www.bjraw.com/videos"); got != "http://www.bjraw.com" {
		t.Errorf("baseFor = %q, want the operator's own host", got)
	}
}

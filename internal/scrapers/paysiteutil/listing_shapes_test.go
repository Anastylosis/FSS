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

func nextPage(props string) string {
	return `<html><head><script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":` + props + `}}</script></head></html>`
}

func collect(t *testing.T, s *Scraper, url string) []scraper.SceneResult {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []scraper.SceneResult
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	return scenes
}

// Jamie Young's /videos ships the listing under `pageProps.videos`, not
// `contents`, and its home page ignores ?page=.
func TestListingUnderVideosKey(t *testing.T) {
	pages := map[string]string{
		"1": `{"videos":{"total":2,"page":"1","per_page":"1","total_pages":2,"data":[{"id":1208,"title":"Solo One","slug":"solo-one","publish_date":"2025/12/12 12:12:00","seconds_duration":760,"thumb":"https://thumbs.example.com/a.jpg","models":["Jamie Young"],"site":"Jamie-Young"}]},"order_by":"publish_date"}`,
		"2": `{"videos":{"total":2,"page":"2","per_page":"1","total_pages":2,"data":[{"id":1100,"title":"Solo Two","slug":"solo-two","publish_date":"2025/11/01 10:00:00","seconds_duration":300,"models":["Jamie Young"],"site":"Jamie-Young"}]}}`,
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/videos" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, nextPage(pages[r.URL.Query().Get("page")]))
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "jamieyoung", Domain: "jamie-young.com", StudioName: "Jamie Young"})
	s.client = ts.Client()

	got := collect(t, s, ts.URL+"/videos")
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	sc := got[0].Scene
	if sc.ID != "1208" || sc.Title != "Solo One" || sc.StudioURL != ts.URL+"/videos" {
		t.Errorf("scene = %+v", sc)
	}
	if sc.URL != "https://jamie-young.com/videos/solo-one" || sc.Studio != "Jamie-Young" {
		t.Errorf("URL/Studio = %q / %q", sc.URL, sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Jamie Young" || sc.Duration != 760 {
		t.Errorf("performers/duration = %v / %d", sc.Performers, sc.Duration)
	}
	if got[1].Scene.ID != "1100" {
		t.Errorf("second scene ID = %q", got[1].Scene.ID)
	}
}

// `contents` wins when both keys exist, and a `videos` that is not a listing
// object (an array, say) must not break parsing.
func TestVideosKeyDoesNotShadowContents(t *testing.T) {
	item := `{"id":7,"title":"Real","slug":"real"}`
	for name, props := range map[string]string{
		"both":        `{"contents":{"total":1,"data":[` + item + `]},"videos":{"total":1,"data":[{"id":8,"title":"Other"}]}}`,
		"videosArray": `{"contents":{"total":1,"data":[` + item + `]},"videos":[{"id":8}]}`,
	} {
		nd, err := parseNextData([]byte(nextPage(props)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		l := nd.Props.PageProps.listing()
		if len(l.Data) != 1 || l.Data[0].ID != 7 {
			t.Errorf("%s: listing = %+v", name, l)
		}
	}
}

// Lollipop Twinks serves the listing from the home page and has no detail
// pages, so each scene's URL is its join redirect.
func TestRootListPath(t *testing.T) {
	const props = `{"contents":{"total":1,"page":1,"per_page":32,"total_pages":1,"data":[{"id":564,"title":"A Little Ohio Queer Boy","slug":"a-little-ohio-queer-boy","publish_date":"2026/10/08 00:00:00","videos_duration":"375.44","link":"https://www2.lollipoptwinks.com/track/abc/scenes/lptint050_joshbensan","thumbnail":"//cloud.example.com/lpt/largethumbs/lptint050_joshbensan.jpg","models":["Josh Bensan"],"models_slugs":[{"name":"Josh Bensan","slug":"josh-bensan"}],"site":"LollipopTwinks"}]}}`
	var asked []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, nextPage(props))
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "lollipoptwinks", Domain: "lollipoptwinks.com", StudioName: "Lollipop Twinks", ListPath: RootListPath})
	s.client = ts.Client()

	got := collect(t, s, ts.URL+"/")
	if len(asked) == 0 || asked[0] != "/?page=1" {
		t.Errorf("fetched %v, want /?page=1 first", asked)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scenes, want 1", len(got))
	}
	sc := got[0].Scene
	if sc.ID != "564" || sc.Title != "A Little Ohio Queer Boy" || sc.Duration != 375 {
		t.Errorf("scene = %+v", sc)
	}
	if sc.URL != "https://www2.lollipoptwinks.com/track/abc/scenes/lptint050_joshbensan" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Thumbnail != "https://cloud.example.com/lpt/largethumbs/lptint050_joshbensan.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if want := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v", sc.Date)
	}
	pats := s.Patterns()
	if len(pats) != 2 || pats[0] != "lollipoptwinks.com" || pats[1] != "lollipoptwinks.com/models/{slug}" {
		t.Errorf("Patterns = %v", pats)
	}
	if !s.MatchesURL("https://www.lollipoptwinks.com/?page=2") {
		t.Error("home-page URL with ?page= not matched")
	}
}

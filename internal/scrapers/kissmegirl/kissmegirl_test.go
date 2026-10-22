package kissmegirl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func serve(t *testing.T, routes map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		f, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("scrape error: %v", r.Err)
		}
	}
	return scenes
}

func testScraper(cfg SiteConfig, base string) *Scraper {
	cfg.Base = base
	s := New(cfg)
	return s
}

func TestListScenesPaginates(t *testing.T) {
	srv, hits := serve(t, map[string]string{
		"/vod.html":      "vod.html",
		"/vodpage2.html": "vodpage2.html",
	})
	s := testScraper(sites[0], srv.URL)
	const studioURL = "https://www.kissmegirl.com/welcome.html"
	// KnownIDs must not stop the walk: the listing is not in release order.
	scenes := collect(t, s, studioURL, scraper.ListOpts{KnownIDs: map[string]bool{"saralyla": true}})

	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if got := strings.Join(*hits, ","); got != "/vod.html,/vodpage2.html" {
		t.Errorf("requests = %s", got)
	}

	sc := scenes[0]
	if sc.ID != "saralyla" || sc.Title != "Sara & Lyla" {
		t.Errorf("id/title = %q/%q", sc.ID, sc.Title)
	}
	if sc.StudioURL != studioURL || sc.SiteID != "kissmegirl" || sc.Studio != "KissMe Girl" {
		t.Errorf("studio fields = %q %q %q", sc.StudioURL, sc.SiteID, sc.Studio)
	}
	if sc.URL != srv.URL+"/scenepages/sara-lyla.html" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Thumbnail != srv.URL+"/images/saralylasmall.jpg" {
		t.Errorf("thumb = %q", sc.Thumbnail)
	}
	if sc.Duration != 44*60 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if strings.Join(sc.Performers, "|") != "Sara|Lyla" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Width != 1920 || sc.Height != 1080 || sc.Format != "MP4-H264" {
		t.Errorf("tech = %dx%d %q", sc.Width, sc.Height, sc.Format)
	}
	if sc.Description == "" || strings.Contains(sc.Description, "<") {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.PriceHistory) != 0 {
		t.Errorf("members-site scene carries a price: %v", sc.PriceHistory)
	}

	// No scene page: the URL falls back to the listing with a fragment.
	if scenes[1].ID != "lunarilynn" || scenes[1].Title != "Luna & Rilynn" {
		t.Errorf("scene 2 = %q %q", scenes[1].ID, scenes[1].Title)
	}
	if scenes[1].URL != srv.URL+"/vod.html#lunarilynn" {
		t.Errorf("scene 2 url = %q", scenes[1].URL)
	}
	if scenes[2].ID != "dillionash" || scenes[2].Title != "Dillion & Ash" {
		t.Errorf("scene 3 = %q %q", scenes[2].ID, scenes[2].Title)
	}
}

func TestExplicitPrice(t *testing.T) {
	srv, _ := serve(t, map[string]string{"/scenes.html": "explicit.html"})
	s := testScraper(sites[1], srv.URL)
	scenes := collect(t, s, "https://www.kissmegirlexplicit.com/", scraper.ListOpts{})
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	sc := scenes[0]
	if sc.Title != "Jada & Cameron" || sc.Duration != 53*60 || sc.Studio != "KissMe Girl Explicit" {
		t.Errorf("scene = %q %d %q", sc.Title, sc.Duration, sc.Studio)
	}
	if sc.LowestPrice != 10.95 || len(sc.PriceHistory) != 1 {
		t.Errorf("price = %v %v", sc.LowestPrice, sc.PriceHistory)
	}
	if strings.Contains(sc.Description, "Watch Trailer") {
		t.Errorf("description kept the trailer link: %q", sc.Description)
	}
}

func TestModelPage(t *testing.T) {
	srv, hits := serve(t, map[string]string{"/models/aidrafox/aidrafox.html": "model.html"})
	s := testScraper(sites[0], srv.URL)
	const studioURL = "https://www.kissmegirl.com/models/aidrafox/aidrafox.html"
	scenes := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	if scenes[0].ID != "breeaidra" || scenes[0].StudioURL != studioURL {
		t.Errorf("scene = %q %q", scenes[0].ID, scenes[0].StudioURL)
	}
	if scenes[0].URL != "https://www.kissmegirl.com/scenepages/bree-aidra.html" {
		t.Errorf("url = %q", scenes[0].URL)
	}
	if len(*hits) != 1 {
		t.Errorf("requests = %v", *hits)
	}
}

func TestEmptyPageIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer srv.Close()
	s := testScraper(sites[2], srv.URL)
	ch, _ := s.ListScenes(context.Background(), "https://www.sloppygirl.com/", scraper.ListOpts{})
	var gotErr error
	for r := range ch {
		if r.Kind == scraper.KindError {
			gotErr = r.Err
		}
	}
	if gotErr == nil || scraper.Classify(gotErr) != scraper.FailureParse {
		t.Errorf("err = %v", gotErr)
	}
}

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		id, url string
		want    bool
	}{
		{"kissmegirl", "https://www.kissmegirl.com/welcome.html", true},
		{"kissmegirl", "http://kissmegirl.com", true},
		{"kissmegirl", "https://www.kissmegirl.com/models/aidrafox/aidrafox.html", true},
		{"kissmegirl", "https://www.kissmegirlexplicit.com/welcome.html", false},
		{"kissmegirlexplicit", "https://www.kissmegirlexplicit.com/scenes.html", true},
		{"sloppygirl", "https://www.sloppygirl.com/welcome.html", true},
		{"sloppygirl", "https://www.kissmegirl.com/", false},
	}
	byID := map[string]*Scraper{}
	for _, cfg := range sites {
		byID[cfg.ID] = New(cfg)
	}
	for _, c := range cases {
		if got := byID[c.id].MatchesURL(c.url); got != c.want {
			t.Errorf("%s.MatchesURL(%q) = %v", c.id, c.url, got)
		}
	}
}

func TestPrettyTitle(t *testing.T) {
	cases := map[string]string{
		"KISSME GIRL - AJ & MISCHA":        "AJ & Mischa",
		"SLOPPYGIRL - LILY, SAMMIE & LYLA": "Lily, Sammie & Lyla",
		"LUNA & RILYNN":                    "Luna & Rilynn",
		"Kiss Me Girl Vol. 4":              "Kiss Me Girl Vol. 4",
	}
	perfs := []string{"AJ", "Mischa"}
	for in, want := range cases {
		if got := prettyTitle(in, perfs); got != want {
			t.Errorf("prettyTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

package hitzefrei

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newTestServer(t *testing.T, requests *[]string) *httptest.Server {
	listing, model, detail := fixture(t, "listing.html"), fixture(t, "model.html"), fixture(t, "detail.html")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests = append(*requests, r.URL.RequestURI())
		switch {
		case strings.HasPrefix(r.URL.Path, "/view/"):
			_, _ = fmt.Fprint(w, detail)
		case strings.HasPrefix(r.URL.Path, "/model/"):
			_, _ = fmt.Fprint(w, model)
		case r.URL.Path == "/videos" && r.URL.Query().Get("page") == "1":
			_, _ = fmt.Fprint(w, listing)
		default:
			_, _ = fmt.Fprint(w, `<div class="row row-contents"></div>`)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), base: ts.URL}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		}
	}
	return scenes, errs
}

func TestListScenes(t *testing.T) {
	var reqs []string
	ts := newTestServer(t, &reqs)
	defer ts.Close()

	const studioURL = "https://tour.hitzefrei.com/videos"
	ch, err := newTestScraper(ts).ListScenes(context.Background(), studioURL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs := collect(ch)
	if errs != 0 || len(scenes) != 2 {
		t.Fatalf("scenes=%d errs=%d", len(scenes), errs)
	}
	byID := map[string]models.Scene{}
	for _, s := range scenes {
		byID[s.ID] = s
	}
	a := byID["363"]
	if a.Title != "Wet fun in the sun with Bodo and Mini - Part 1" || a.Studio != "City Check" {
		t.Errorf("a = %+v", a)
	}
	if a.StudioURL != studioURL || a.SiteID != "hitzefrei" || !strings.HasPrefix(a.URL, ts.URL+"/view/363/") {
		t.Errorf("a ids = %+v", a)
	}
	if a.Date.Format("2006-01-02") != "2025-07-23" || a.Duration != 14*60+41 {
		t.Errorf("a date/duration = %v %d", a.Date, a.Duration)
	}
	if len(a.Performers) != 1 || a.Performers[0] != "Mini Hotcore" {
		t.Errorf("a performers = %v", a.Performers)
	}
	if !strings.HasSuffix(a.Thumbnail, "ctc0051_minihotcore_bodo_pt1_01.jpg") {
		t.Errorf("a thumb = %q", a.Thumbnail)
	}
	if a.Description != "Gino Gemelli is visiting Berlin to meet horny Jana & more." {
		t.Errorf("a description = %q", a.Description)
	}
	b := byID["362"]
	if b.Studio != "Fuck On Arrival" || b.Duration != 3723 || len(b.Performers) != 2 {
		t.Errorf("b = %+v", b)
	}
}

func TestBrandFilterRequest(t *testing.T) {
	var reqs []string
	ts := newTestServer(t, &reqs)
	defer ts.Close()

	for _, u := range []string{"https://tour.hitzefrei.com/videos?site=10", "https://unleashed.hitzefrei.com/"} {
		reqs = nil
		ch, _ := newTestScraper(ts).ListScenes(context.Background(), u, scraper.ListOpts{Workers: 1})
		collect(ch)
		if len(reqs) == 0 || !strings.Contains(reqs[0], "site=10") {
			t.Errorf("%s: first request %v lacks site=10", u, reqs)
		}
	}
}

func TestModelMode(t *testing.T) {
	var reqs []string
	ts := newTestServer(t, &reqs)
	defer ts.Close()

	const studioURL = "https://tour.hitzefrei.com/model/75/mini-hotcore"
	ch, _ := newTestScraper(ts).ListScenes(context.Background(), studioURL, scraper.ListOpts{Workers: 1})
	scenes, errs := collect(ch)
	if errs != 0 || len(scenes) != 1 {
		t.Fatalf("scenes=%d errs=%d", len(scenes), errs)
	}
	s := scenes[0]
	if s.ID != "365" || s.Studio != "City Check" || s.StudioURL != studioURL {
		t.Errorf("scene = %+v", s)
	}
	if s.Date.Format("2006-01-02") != "2025-08-02" {
		t.Errorf("date = %v", s.Date)
	}
	// The model card has no runtime; the detail page fills it in.
	if s.Duration != 24*60+49 || s.Description == "" {
		t.Errorf("detail not applied: %+v", s)
	}
	if len(reqs) != 2 || !strings.HasPrefix(reqs[0], "/model/75/mini-hotcore") {
		t.Errorf("requests = %v", reqs)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://hitzefrei.com/", true},
		{"https://tour.hitzefrei.com/videos?_lang=en&site=3", true},
		{"https://tour.hitzefrei.com/model/75/mini-hotcore", true},
		{"https://citycheck.hitzefrei.com", true},
		{"https://pattisanals.hitzefrei.com/videos", true},
		{"https://hitzefrei.com.evil.net/", false},
		{"https://example.com/hitzefrei.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestResolveListing(t *testing.T) {
	tests := []struct {
		url  string
		want listing
	}{
		{"https://tour.hitzefrei.com/videos", listing{}},
		{"https://hitzefrei.com/", listing{}},
		{"https://tour.hitzefrei.com/videos?_lang=en&site=9", listing{site: "9"}},
		{"https://tour.hitzefrei.com/videos?site=99", listing{}},
		{"https://cuffemall.hitzefrei.com/videos", listing{site: "8"}},
		{"https://tour.hitzefrei.com/model/75/mini-hotcore", listing{model: "75/mini-hotcore"}},
	}
	for _, tt := range tests {
		if got := resolveListing(tt.url); got != tt.want {
			t.Errorf("resolveListing(%q) = %+v, want %+v", tt.url, got, tt.want)
		}
	}
}

func TestEveryBrandHostHasName(t *testing.T) {
	for host, id := range brandHosts {
		if brands[id] == "" {
			t.Errorf("%s maps to unknown site %s", host, id)
		}
	}
}

package showybeauty

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"http://showybeauty.com/", true},
		{"https://www.showybeauty.com/videos.html", true},
		{"http://showybeauty.com/model_734.html", true},
		{"http://showybeauty.com", true},
		{"http://amourangels.com/", false},
		{"https://showybeauty.com.evil.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func collect(t *testing.T, srvURL, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	orig := siteBase
	siteBase = srvURL
	t.Cleanup(func() { siteBase = orig })
	ch, err := New().ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return scenes, errs
}

func TestListingPagination(t *testing.T) {
	listing := fixture(t, "videos.html")
	// Page 1 is padded to a full page so pagination continues; page 2 holds
	// three cards (< pageSize) and must end the walk without a page-3 request.
	var page1 strings.Builder
	page1.WriteString(listing)
	for i := 0; i < pageSize-3; i++ {
		page1.WriteString(`<div class="item"><a href="/cover_9` + string(rune('a'+i)) + `.html" class="title">X</a></div>`)
	}
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		switch r.URL.Path {
		case "/videos.html":
			_, _ = w.Write([]byte(page1.String()))
		case "/videos_2.html":
			_, _ = w.Write([]byte(strings.ReplaceAll(listing, "cover_26", "cover_16")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	studio := "http://showybeauty.com/"
	scenes, errs := collect(t, srv.URL, studio, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 6 {
		t.Fatalf("got %d scenes, want 6", len(scenes))
	}
	if len(hits) != 2 {
		t.Errorf("requests = %v, want exactly two pages", hits)
	}
	sc := scenes[0]
	if sc.ID != "2624" || sc.Title != "SWEET BLOSSOM" || sc.StudioURL != studio {
		t.Errorf("scene = %+v", sc)
	}
	if sc.URL != srv.URL+"/cover_2624.html" || sc.Thumbnail != srv.URL+"/cm_cvl/z_cover2_2624.jpg" {
		t.Errorf("url/thumb = %q %q", sc.URL, sc.Thumbnail)
	}
	if !sc.Date.Equal(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Duration != 14*60+6 || sc.Director != "WART" || sc.Studio != "Showy Beauty" {
		t.Errorf("duration/director/studio = %d %q %q", sc.Duration, sc.Director, sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Sandrine" {
		t.Errorf("performers = %v", sc.Performers)
	}
}

func TestListingEndsOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/videos.html" {
			var sb strings.Builder
			for i := 0; i < pageSize; i++ {
				sb.WriteString(`<div class="item"><a href="/cover_` + strconv.Itoa(100+i) + `.html" title="" class="title">T VIDEO</a><span class="resolution">Video</span></div>`)
			}
			_, _ = w.Write([]byte(sb.String()))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, errs := collect(t, srv.URL, "http://showybeauty.com/", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("a 404 past the last page must not be an error: %v", errs)
	}
}

func TestModelPageVideosOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/model_734.html" {
			_, _ = w.Write([]byte(fixture(t, "model.html")))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	studio := "http://showybeauty.com/model_734.html"
	scenes, errs := collect(t, srv.URL, studio, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (photo set filtered)", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1245" || sc.Title != "CENTER OF GRAVITY" || sc.StudioURL != studio {
		t.Errorf("scene = %+v", sc)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Eva" || sc.Duration != 630 {
		t.Errorf("performers/duration = %v %d", sc.Performers, sc.Duration)
	}
}

func TestKnownIDsStopsAfterPage(t *testing.T) {
	listing := fixture(t, "videos.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/videos.html" {
			_, _ = w.Write([]byte(listing))
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	scenes, _ := collect(t, srv.URL, "http://showybeauty.com/", scraper.ListOpts{KnownIDs: map[string]bool{"2625": true}})
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
}

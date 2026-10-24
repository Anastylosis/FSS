package clubfilly

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.clubfilly.com/home.php", true},
		{"https://clubfilly.com/home.html", true},
		{"https://clubfilly.com/", true},
		{"https://clubfilly.com", true},
		{"https://clubfilly.com/scenes.html", true},
		{"https://clubfilly.com/scenes-3.html", true},
		{"https://clubfilly.com/pornstarsfocus-1448.html", false},
		{"https://clubfilly.com/dvdfocus-21761.html", false},
		{"https://tour.fillyfilms.com/", false},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func newServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	sitemap := read("sitemap.html")
	details := map[string][]byte{
		"/scenefocus-V95343.html": read("detail_95343.html"),
		"/scenefocus-V94654.html": read("detail_94654.html"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.html" {
			_, _ = w.Write(sitemap)
			return
		}
		if hits != nil {
			hits.Add(1)
		}
		if b, ok := details[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(`<html><body><p>no detail</p></body></html>`))
	}))
	t.Cleanup(srv.Close)
	old := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = old })
	return srv
}

func collect(t *testing.T, opts scraper.ListOpts) (map[string]models.Scene, []error, bool) {
	t.Helper()
	ch, err := New().ListScenes(context.Background(), "https://www.clubfilly.com/home.php", opts)
	if err != nil {
		t.Fatal(err)
	}
	scenes := map[string]models.Scene{}
	var errs []error
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes[r.Scene.ID] = r.Scene
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return scenes, errs, stopped
}

func TestListScenes(t *testing.T) {
	newServer(t, nil)
	scenes, errs, stopped := collect(t, scraper.ListOpts{})
	if stopped {
		t.Error("unexpected StoppedEarly")
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
	// 79626 serves a page without a scene block: reported as a parse error.
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v, want one parse error", errs)
	}

	sc := scenes["95343"]
	if sc.Title != "Real Fucking Lesbians #2: Under Our Roof Scene 1" {
		t.Errorf("title = %q", sc.Title)
	}
	if want := time.Date(2019, 10, 22, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("date = %v", sc.Date)
	}
	if strings.Join(sc.Performers, "|") != "Nikki Hearts|Hadley Viscara" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !strings.HasPrefix(sc.Description, "Nikki Hearts stars in all four scenes") {
		t.Errorf("description = %q", sc.Description)
	}
	if !strings.HasSuffix(sc.Thumbnail, "/images/V95343/crop/345x235/nt/2.jpg") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.StudioURL != "https://www.clubfilly.com/home.php" || sc.Studio != "Club Filly" || sc.SiteID != "clubfilly" {
		t.Errorf("studio fields = %q %q %q", sc.StudioURL, sc.Studio, sc.SiteID)
	}
	if !strings.HasSuffix(sc.URL, "/scenefocus-V95343.html") {
		t.Errorf("url = %q", sc.URL)
	}

	// The gallery of 94654 shows only other scenes' stills.
	if th := scenes["94654"].Thumbnail; th != "" {
		t.Errorf("thumbnail for 94654 = %q, want empty", th)
	}
}

func TestKnownIDsSkipDetailFetch(t *testing.T) {
	var hits atomic.Int32
	newServer(t, &hits)
	scenes, _, stopped := collect(t, scraper.ListOpts{KnownIDs: map[string]bool{"95343": true, "79626": true}})
	if !stopped {
		t.Error("want StoppedEarly when known scenes were skipped")
	}
	if len(scenes) != 1 || scenes["94654"].ID == "" {
		t.Fatalf("scenes = %v", scenes)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("detail requests = %d, want 1", n)
	}
}

func TestSitemapOrderAndSidebarIgnored(t *testing.T) {
	newServer(t, nil)
	ids, err := New().fetchIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids, ","); got != "95343,94654,79626" {
		t.Errorf("ids = %s", got)
	}
}

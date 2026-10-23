package studsvr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.studsvr.com",
		"https://studsvr.com/",
		"https://www.studsvr.com/scenes?page=2",
		"https://www.studsvr.com/pornstar/casey-everett",
		"https://www.studsvr.com/category/bareback-porn-videos",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://www.richard.xxx/",
		"https://example.com/studsvr.com",
		"https://notstudsvr.com/",
		"https://members.studsvr.com/login",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListingPath(t *testing.T) {
	cases := map[string]string{
		"https://www.studsvr.com/":                              "/scenes",
		"https://www.studsvr.com/scenes?page=3":                 "/scenes",
		"https://www.studsvr.com/pornstar/casey-everett":        "/pornstar/casey-everett",
		"https://www.studsvr.com/category/bareback-porn-videos": "/category/bareback-porn-videos",
	}
	for in, want := range cases {
		if got := listingPath(in); got != want {
			t.Errorf("listingPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	scenes := parseListing(fixture(t, "listing.html"))
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "24703" || sc.Title != "Birthday Boy" || sc.Duration != 45*60 {
		t.Errorf("scene = %+v", sc)
	}
	if strings.Join(sc.Performers, "|") != "Casey" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 5 || sc.Tags[0] != "Ball Licking" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if !strings.HasPrefix(sc.Description, "It's your birthday") {
		t.Errorf("description = %q", sc.Description)
	}
	if !strings.HasPrefix(sc.Thumbnail, "https://images") || !strings.Contains(sc.Thumbnail, "/scenes/stvr/") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if scenes[1].Title != "The Pool Guy's Tip" || scenes[2].ID != "24847" || scenes[2].Title != "Good Game" {
		t.Errorf("titles = %q / %q %q", scenes[1].Title, scenes[2].ID, scenes[2].Title)
	}
}

// Performer pages head a row with the cast, not the scene title.
func TestParsePerformerListing(t *testing.T) {
	scenes := parseListing(fixture(t, "performer.html"))
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "24703" || sc.Title != "" || sc.Duration != 45*60 || len(sc.Tags) != 5 {
		t.Errorf("scene = %+v", sc)
	}
}

func TestParseDetail(t *testing.T) {
	d, ok := parseDetail(fixture(t, "detail.html"))
	if !ok {
		t.Fatal("no detail")
	}
	if got := d.date.Format("2006-01-02"); got != "2018-10-24" {
		t.Errorf("date = %s", got)
	}
	if strings.Join(d.performers, "|") != "Casey Everett" {
		t.Errorf("performers = %v", d.performers)
	}
	if len(d.tags) != 12 || d.tags[11] != "Tattoos" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.title != "Birthday Boy" {
		t.Errorf("title = %q", d.title)
	}
	if d.duration != 45*60 || !strings.HasPrefix(d.description, "It's your birthday") {
		t.Errorf("detail = %+v", d)
	}
	if _, ok := parseDetail("<html></html>"); ok {
		t.Error("expected no detail")
	}
}

func newServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/scene/"):
			_, _ = w.Write([]byte(fixture(t, "detail.html")))
		case strings.HasPrefix(r.URL.Path, "/pornstar/") && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(fixture(t, "performer.html")))
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(fixture(t, "listing.html")))
		default:
			_, _ = w.Write([]byte(`<div class="feed"></div>`))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func collect(t *testing.T, s *Scraper, url string) []models.Scene {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, url, scraper.ListOpts{})
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

func TestListScenesModes(t *testing.T) {
	for _, tc := range []struct {
		url, listing string
		want         int
	}{
		{"https://www.studsvr.com/", "/scenes?page=1", 3},
		{"https://www.studsvr.com/pornstar/casey-everett", "/pornstar/casey-everett?page=1", 1},
		{"https://www.studsvr.com/category/bareback-porn-videos", "/category/bareback-porn-videos?page=1", 3},
	} {
		ts, paths := newServer(t)
		s := &Scraper{client: ts.Client(), base: ts.URL}
		scenes := collect(t, s, tc.url)
		if len(scenes) != tc.want {
			t.Fatalf("%s: got %d scenes, want %d", tc.url, len(scenes), tc.want)
		}
		var listed, page2 bool
		for _, p := range paths() {
			listed = listed || p == tc.listing
			page2 = page2 || strings.HasSuffix(p, "?page=2")
		}
		if !listed || !page2 {
			t.Errorf("%s: listing pages not walked, got %v", tc.url, paths())
		}
		sc := scenes[0]
		if sc.Title != "Birthday Boy" {
			t.Errorf("%s: title = %q", tc.url, sc.Title)
		}
		if sc.ID != "24703" || sc.SiteID != siteID || sc.StudioURL != tc.url || sc.Studio != "StudsVR" {
			t.Errorf("scene = %+v", sc)
		}
		if !strings.HasPrefix(sc.URL, ts.URL+"/scene/") {
			t.Errorf("url = %q", sc.URL)
		}
		if sc.Date.Format("2006-01-02") != "2018-10-24" || strings.Join(sc.Performers, "|") != "Casey Everett" || len(sc.Tags) != 12 {
			t.Errorf("fields = %v %v %v", sc.Date, sc.Performers, sc.Tags)
		}
	}
}

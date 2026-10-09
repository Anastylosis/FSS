package richardxxx

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
		"https://www.richard.xxx/home",
		"https://richard.xxx/",
		"https://www.richard.xxx/new-porn-videos?page=2",
		"https://www.richard.xxx/site/my-brothers-hot-friend",
		"https://www.richard.xxx/pornstar/aston-springs",
		"https://www.richard.xxx",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://www.richardriches.com/",
		"https://example.com/richard.xxx",
		"https://notrichard.xxx/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListingPath(t *testing.T) {
	cases := map[string]string{
		"https://www.richard.xxx/home":                             "/new-porn-videos",
		"https://www.richard.xxx/new-porn-videos?page=3":           "/new-porn-videos",
		"https://www.richard.xxx/site/men-hard-at-work":            "/site/men-hard-at-work",
		"https://www.richard.xxx/site/men-hard-at-work?nats=1.2.3": "/site/men-hard-at-work",
		"https://www.richard.xxx/pornstar/aston-springs":           "/pornstar/aston-springs",
	}
	for in, want := range cases {
		if got := listingPath(in); got != want {
			t.Errorf("listingPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "listing.html"))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	c := cards[0]
	if c.id != "25313" || c.brand != "Tonight's Boyfriend" {
		t.Errorf("id/brand = %q/%q", c.id, c.brand)
	}
	if c.title != "Aston Springs & Aaron Trainer in Tonight's Boyfriend" {
		t.Errorf("title = %q", c.title)
	}
	if strings.Join(c.performers, "|") != "Aston Springs|Aaron Trainer" {
		t.Errorf("performers = %v", c.performers)
	}
	if c.duration != 42*60 {
		t.Errorf("duration = %d", c.duration)
	}
	if got := c.date.Format("2006-01-02"); got != "2019-04-26" {
		t.Errorf("date = %s", got)
	}
	if strings.Join(c.tags, "|") != "Bareback" {
		t.Errorf("tags = %v", c.tags)
	}
	if strings.Contains(c.url, "nats") || !strings.HasSuffix(c.url, "-25313") {
		t.Errorf("url = %q", c.url)
	}
	if cards[1].id != "25282" || cards[1].duration != 39*60 || strings.Join(cards[1].tags, "|") != "Bareback" {
		t.Errorf("second card = %+v", cards[1])
	}
}

func TestParseDetail(t *testing.T) {
	v, ok := parseDetail(fixture(t, "detail.html"))
	if !ok || !strings.HasPrefix(v.Description, "Aston Springs just finished") {
		t.Errorf("detail = %+v ok=%v", v, ok)
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
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(fixture(t, "listing.html")))
		default:
			_, _ = w.Write([]byte(`<div class="scenes"></div>`))
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
	for _, tc := range []struct{ url, listing string }{
		{"https://www.richard.xxx/home", "/new-porn-videos?page=1"},
		{"https://www.richard.xxx/site/my-brothers-hot-friend", "/site/my-brothers-hot-friend?page=1"},
		{"https://www.richard.xxx/pornstar/aston-springs", "/pornstar/aston-springs?page=1"},
	} {
		ts, paths := newServer(t)
		s := &Scraper{client: ts.Client(), base: ts.URL}
		scenes := collect(t, s, tc.url)
		if len(scenes) != 2 {
			t.Fatalf("%s: got %d scenes, want 2", tc.url, len(scenes))
		}
		found := false
		for _, p := range paths() {
			found = found || p == tc.listing
		}
		if !found {
			t.Errorf("%s: %s not requested, got %v", tc.url, tc.listing, paths())
		}
		sc := scenes[0]
		if sc.ID != "25313" || sc.SiteID != siteID || sc.StudioURL != tc.url || sc.Studio != "Tonight's Boyfriend" {
			t.Errorf("scene = %+v", sc)
		}
		if sc.Title != "Aston Springs & Aaron Trainer in Tonight's Boyfriend" {
			t.Errorf("title = %q", sc.Title)
		}
		if !strings.HasPrefix(sc.URL, ts.URL+"/scene/") {
			t.Errorf("url = %q", sc.URL)
		}
		if !strings.HasPrefix(sc.Description, "Aston Springs just finished") {
			t.Errorf("description = %q", sc.Description)
		}
		if sc.Date.Format("2006-01-02") != "2019-04-26" || sc.Duration != 2520 || len(sc.Performers) != 2 {
			t.Errorf("fields = %v %d %v", sc.Date, sc.Duration, sc.Performers)
		}
		if !strings.HasPrefix(sc.Thumbnail, "https://images") {
			t.Errorf("thumbnail = %q", sc.Thumbnail)
		}
	}
}

package oldnanny

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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

type server struct {
	*httptest.Server
	mu   sync.Mutex
	hits []string
}

func (s *server) listingHits(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, h := range s.hits {
		if strings.HasPrefix(h, prefix) {
			n++
		}
	}
	return n
}

func newServer(t *testing.T) *server {
	t.Helper()
	list1, list2 := fixture(t, "list1.html"), fixture(t, "list2.html")
	details := map[string]string{
		"1600p": fixture(t, "detail1.html"),
		"1605p": fixture(t, "detail2.html"),
		"1602p": fixture(t, "detail3.html"),
	}
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/tour2/video/"):
			seg := strings.Split(p, "/")
			if d, ok := details[seg[4]]; ok {
				_, _ = w.Write([]byte(d))
				return
			}
			http.NotFound(w, r)
		case strings.Contains(p, "/tour2/scenes/"):
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(list1))
				return
			}
			// Pages past the end repeat the last page.
			_, _ = w.Write([]byte(list2))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
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

func TestListScenesSiteFilter(t *testing.T) {
	srv := newServer(t)
	s := New()
	s.base = srv.URL
	studioURL := "https://oldnanny.com/en/tour2/scenes/site/AgedLove"

	scenes, errs := collect(t, s, studioURL, scraper.ListOpts{Workers: 2})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	var first models.Scene
	for _, sc := range scenes {
		if sc.ID == "1600p" {
			first = sc
		}
	}
	if first.ID == "" {
		t.Fatal("scene 1600p missing")
	}
	if first.Title != "The MILF prefers a big black cock to a dildo in her ass" {
		t.Errorf("title = %q", first.Title)
	}
	if first.Studio != "Aged Love" || first.SiteID != "oldnanny" || first.StudioURL != studioURL {
		t.Errorf("studio fields = %q %q %q", first.Studio, first.SiteID, first.StudioURL)
	}
	if first.Date.Format("2006-01-02") != "2026-10-16" {
		t.Errorf("date = %v", first.Date)
	}
	if first.Duration != 22*60+17 {
		t.Errorf("duration = %d", first.Duration)
	}
	if len(first.Performers) != 2 || first.Performers[0] != "Adriana Love" || first.Performers[1] != "Joachim Kessef" {
		t.Errorf("performers = %v", first.Performers)
	}
	if len(first.Tags) != 10 || first.Tags[0] != "Black Boys" {
		t.Errorf("tags = %v", first.Tags)
	}
	if !strings.HasPrefix(first.URL, srv.URL+"/en/tour2/video/1600p/") {
		t.Errorf("url = %q", first.URL)
	}
	if !strings.Contains(first.Thumbnail, "media.oldnanny.com") {
		t.Errorf("thumbnail = %q", first.Thumbnail)
	}
}

func TestPagesPastTheEndRepeatAndStop(t *testing.T) {
	srv := newServer(t)
	s := New()
	s.base = srv.URL
	scenes, _ := collect(t, s, "https://oldnanny.com/en/tour2/scenes/site/AgedLove", scraper.ListOpts{Workers: 1})
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3 (no duplicates from the repeated page)", len(scenes))
	}
	if n := srv.listingHits("/en/tour2/scenes/site/AgedLove?page="); n != 3 {
		t.Errorf("listing pages fetched = %d, want 3 (1, 2, then the repeat of 2)", n)
	}
}

func TestNetworkListingUsesDetailSiteName(t *testing.T) {
	srv := newServer(t)
	s := New()
	s.base = srv.URL
	scenes, _ := collect(t, s, "https://oldnanny.com/", scraper.ListOpts{Workers: 1})
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	for _, sc := range scenes {
		if sc.Studio == "" {
			t.Errorf("scene %s has no studio", sc.ID)
		}
		if sc.ID == "1600p" && sc.Studio != "Aged Love" {
			t.Errorf("scene 1600p studio = %q, want the detail page's site", sc.Studio)
		}
	}
	if srv.listingHits("/en/tour2/scenes/all?page=1") != 1 {
		t.Errorf("hits = %v", srv.hits)
	}
}

func TestKnownIDsSkipDetail(t *testing.T) {
	srv := newServer(t)
	s := New()
	s.base = srv.URL
	opts := scraper.ListOpts{Workers: 1, KnownIDs: map[string]bool{"1605p": true}}
	collect(t, s, "https://oldnanny.com/en/tour2/scenes/site/AgedLove", opts)
	if n := srv.listingHits("/en/tour2/video/1605p/"); n != 0 {
		t.Errorf("known scene's detail fetched %d times", n)
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "list1.html"))
	if len(cards) != 2 {
		t.Fatalf("cards = %d", len(cards))
	}
	c := cards[1]
	if c.id != "1605p" || c.title != "A mature woman's large clitoris and anus are fucked by a BBC" {
		t.Errorf("card = %+v", c)
	}
	if c.date.Format("2006-01-02") != "2026-10-02" {
		t.Errorf("date = %v", c.date)
	}
	if len(c.performer) != 2 || c.performer[0] != "Andrea West" {
		t.Errorf("performers = %v", c.performer)
	}
}

func TestResolveListing(t *testing.T) {
	cases := []struct {
		in, path, site string
	}{
		{"https://oldnanny.com/", "scenes/all", ""},
		{"https://oldnanny.com/en/", "scenes/all", ""},
		{"https://oldnanny.com/en/tour2/scenes/all?page=3", "scenes/all", ""},
		{"https://oldnanny.com/en/tour2/scenes/site/OmaHotel", "scenes/site/OmaHotel", "OmaHotel"},
		{"https://oldnanny.com/de/tour2/scenes/site/UsaWives?page=2", "scenes/site/UsaWives", "UsaWives"},
		{"http://www.agedlove.com/", "scenes/site/AgedLove", "AgedLove"},
		{"https://oldnanny.com/en/tour2/scenes/model/adriana-love", "scenes/model/adriana-love", ""},
		{"https://oldnanny.com/en/tour2/scenes/tag/3/milf", "scenes/tag/3/milf", ""},
	}
	for _, c := range cases {
		l := resolveListing(c.in)
		if l.path != c.path || l.site != c.site {
			t.Errorf("%s -> %+v, want %s/%s", c.in, l, c.path, c.site)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://oldnanny.com/en/",
		"http://oldnanny.com",
		"https://www.oldnanny.com/en/tour2/scenes/site/AgedLove",
		"http://www.agedlove.com/",
		"https://agedlove.com",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://notoldnanny.com/",
		"https://oldnanny.com.evil.example/",
		"https://example.com/oldnanny.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestApplyDetailDuration(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, `<script type="application/ld+json">{"@type":"VideoObject","duration":"PT1H2M3S","uploadDate":"2025-01-02T00:00:00+00:00"}</script>`)
	if sc.Duration != 3723 || sc.Date.Format("2006-01-02") != "2025-01-02" {
		t.Errorf("duration=%d date=%v", sc.Duration, sc.Date)
	}
}

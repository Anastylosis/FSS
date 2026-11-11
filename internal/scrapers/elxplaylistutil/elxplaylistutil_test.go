package elxplaylistutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

var (
	lostCfg = SiteConfig{SiteID: "only3xlost", Domain: "only3xlost.com", StudioName: "Only 3x Lost"}
	vrCfg   = SiteConfig{SiteID: "only3xvr", Domain: "only3xvr.com", StudioName: "Only 3x VR", ListingOnly: true}
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
	s := New(lostCfg)
	for u, want := range map[string]bool{
		"https://only3xlost.com/categories/movies.html": true,
		"https://www.only3xlost.com/":                   true,
		"https://only3xvr.com/":                         false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestParseListingRelativeLinks(t *testing.T) {
	scenes := New(lostCfg).parseListing(fixture(t, "lost1.html"), "https://only3xlost.com/", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("got %d cards", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "only3xlost_bella_modele_0035" || sc.Title != "Italian Bella Modele lost on Tenerife" ||
		sc.URL != "https://only3xlost.com/updates/italian-bella-modele-lost-on-tenerife.html" ||
		sc.Thumbnail != "https://only3xlost.com/content/7_mia_evans/4-large.jpg" ||
		sc.Preview != "https://only3xlost.com/trailers/mh/68.mp4" ||
		len(sc.Performers) != 1 || sc.Performers[0] != "Bella Modele" {
		t.Errorf("scene = %+v", sc)
	}
}

func TestParseListingSpanPerformers(t *testing.T) {
	scenes := New(vrCfg).parseListing(fixture(t, "vr1.html"), "https://only3xvr.com/", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("got %d cards", len(scenes))
	}
	if scenes[0].Performers[0] != "Simin Kitty" || scenes[0].URL != "https://only3xvr.com/updates/simon-kitty-and-her-latvian-big-tits.html" {
		t.Errorf("anchor card = %+v", scenes[0])
	}
	if p := scenes[2].Performers; len(p) != 2 || p[0] != "Indica Cannabliss" || p[1] != "Lily Fatale" {
		t.Errorf("span performers = %v", p)
	}
}

func TestApplyDetail(t *testing.T) {
	scenes := New(lostCfg).parseListing(fixture(t, "lost1.html"), "u", time.Now())
	applyDetail(fixture(t, "lost_detail.html"), &scenes[0])
	sc := scenes[0]
	if !sc.Date.Equal(time.Date(2022, 8, 26, 0, 0, 0, 0, time.UTC)) || sc.Duration != 32*60 {
		t.Errorf("date = %v duration = %d", sc.Date, sc.Duration)
	}
	if !strings.HasPrefix(sc.Description, "Our favourite Italian girl") || !strings.Contains(sc.Description, "Regio Bravo") {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Tags) < 3 || sc.Tags[0] != "Blowjobs" {
		t.Errorf("tags = %v", sc.Tags)
	}
}

func serve(t *testing.T, listing1, listing2 string, detail bool) (*Scraper, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/categories/movies_1_d.html":
			_, _ = w.Write([]byte(listing1))
		case p == "/categories/movies_2_d.html":
			_, _ = w.Write([]byte(listing2))
		case strings.HasPrefix(p, "/categories/"):
			_, _ = w.Write([]byte("<html></html>"))
		case strings.Contains(p, "dulce-katy"):
			http.NotFound(w, r)
		case strings.HasPrefix(p, "/updates/") && detail:
			_, _ = w.Write([]byte(fixture(t, "lost_detail.html")))
		default:
			http.NotFound(w, r)
		}
	}))
	cfg := lostCfg
	cfg.ListingOnly = !detail
	s := New(cfg)
	s.base = srv.URL
	return s, srv.Close
}

func collect(t *testing.T, s *Scraper) (n int, scenes int) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), "https://only3xlost.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatal(r.Err)
		case scraper.KindScene:
			scenes++
			if !strings.HasPrefix(r.Scene.URL, s.base) {
				t.Errorf("URL = %q", r.Scene.URL)
			}
			if !r.Scene.Date.IsZero() {
				n++
			}
		}
	}
	return n, scenes
}

func TestListScenesWithDetail(t *testing.T) {
	s, closeFn := serve(t, fixture(t, "lost1.html"), fixture(t, "lost2.html"), true)
	defer closeFn()
	dated, scenes := collect(t, s)
	if scenes != 3 || dated != 2 {
		t.Errorf("scenes = %d, dated = %d, want 3 scenes, 2 dated (one detail page 404s)", scenes, dated)
	}
}

func TestListScenesListingOnly(t *testing.T) {
	s, closeFn := serve(t, fixture(t, "lost1.html"), fixture(t, "lost2.html"), false)
	defer closeFn()
	dated, scenes := collect(t, s)
	if scenes != 3 || dated != 0 {
		t.Errorf("scenes = %d, dated = %d, want 3, 0", scenes, dated)
	}
}

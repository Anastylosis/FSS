package jonnomedia

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

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func siteByID(t *testing.T, id string) siteConfig {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			return c
		}
	}
	t.Fatalf("no site %q", id)
	return siteConfig{}
}

// newServer serves page 1 of the listing from list, an empty page 2, the
// detail pages and a model page.
func newServer(t *testing.T, prefix, list, detail string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == prefix+"/" && r.URL.RawQuery == "videos/1":
			_, _ = w.Write(fixture(t, list))
		case r.URL.Path == prefix+"/" && strings.HasPrefix(r.URL.RawQuery, "videos/"):
			_, _ = w.Write([]byte("<html><body>no more</body></html>"))
		case strings.HasPrefix(r.URL.Path, "/twink/"):
			_, _ = w.Write(fixture(t, "model.html"))
		case r.URL.Path == prefix+"/refstat.php", strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = w.Write(fixture(t, detail))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var out []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			out = append(out, r.Scene)
		case scraper.KindError:
			t.Fatalf("scrape error: %v", r.Err)
		}
	}
	return out
}

func TestRootSiteEndToEnd(t *testing.T) {
	srv := newServer(t, "", "rootlist.html", "rootdetail.html")
	s := newScraper(siteByID(t, "spritzz"))
	s.base = srv.URL

	studioURL := "https://spritzz.com/"
	scenes := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "37270" || sc.SiteID != "spritzz" || sc.Studio != "Spritzz" || sc.StudioURL != studioURL {
		t.Errorf("identity: %+v", sc)
	}
	if sc.Title != "Big Cock Boy Needs To Unload" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.URL != srv.URL+"/video/big-cock-boy/37270" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Thumbnail != srv.URL+"/faceimages/faceimage1656663254780.jpg" {
		t.Errorf("thumb = %q", sc.Thumbnail)
	}
	if sc.Duration != 23*60+28 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if got := sc.Date.Format("2006-01-02"); got != "2026-04-09" {
		t.Errorf("date = %s", got)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Alec" || sc.Performers[1] != "Grischa" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !strings.HasPrefix(sc.Description, "When he enters Grisha's room") {
		t.Errorf("description = %q", sc.Description)
	}
}

func TestTourSiteEndToEnd(t *testing.T) {
	srv := newServer(t, "/tour", "tourlist.html", "tourdetail.html")
	s := newScraper(siteByID(t, "uknakedmen"))
	s.base = srv.URL

	studioURL := "https://www.uknakedmen.com/tour/show.php?a=669_2"
	scenes := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "37173" || sc.Title != "Sam Wallis And Sam Syron" || sc.StudioURL != studioURL {
		t.Errorf("scene: %+v", sc)
	}
	if sc.URL != srv.URL+"/tour/refstat.php?lid=37173&sid=669" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Thumbnail != srv.URL+"/tour/faceimages/BM1184_Sam_Wallis_And_Sam_Syron_021628500308978.jpg" {
		t.Errorf("thumb = %q", sc.Thumbnail)
	}
	if !strings.HasPrefix(sc.Description, "Hot dog, the meeting of the Sams") {
		t.Errorf("description = %q", sc.Description)
	}
	if !sc.Date.IsZero() || sc.Duration != 0 || len(sc.Performers) != 0 {
		t.Errorf("tour sites carry no date/duration/cast: %+v", sc)
	}
}

func TestModelPage(t *testing.T) {
	srv := newServer(t, "", "rootlist.html", "rootdetail.html")
	s := newScraper(siteByID(t, "youngbastards"))
	s.base = srv.URL

	studioURL := "https://youngbastards.com/twink/alec-loob/35958"
	scenes := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].ID != "33562" || scenes[0].Title != "Big Cock Boy Needs To Unload" || scenes[0].StudioURL != studioURL {
		t.Errorf("scene: %+v", scenes[0])
	}
	if scenes[0].URL != srv.URL+"/video/pizza-service-special/33562" {
		t.Errorf("url = %q", scenes[0].URL)
	}
}

func TestKnownIDsSkipDetail(t *testing.T) {
	srv := newServer(t, "", "rootlist.html", "rootdetail.html")
	s := newScraper(siteByID(t, "spritzz"))
	s.base = srv.URL
	scenes := collect(t, s, "https://spritzz.com/", scraper.ListOpts{KnownIDs: map[string]bool{"37236": true}})
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want the known one skipped", len(scenes))
	}
	if scenes[0].ID != "37270" || scenes[0].Duration == 0 || scenes[1].ID != "37234" || scenes[1].Duration != 0 {
		t.Errorf("only scenes before the first known one are enriched: %+v %+v", scenes[0], scenes[1])
	}
}

func TestParseListingSkipsUnlinkedCards(t *testing.T) {
	body := `<div class="itemv"><a href="/members">x</a><div class="nm-name">No id</div></div>`
	if got := parseListing(body); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestClockSeconds(t *testing.T) {
	if got := clockSeconds("1", "02", "03"); got != 3723 {
		t.Errorf("got %d", got)
	}
	if got := clockSeconds("15", "44", ""); got != 944 {
		t.Errorf("got %d", got)
	}
}

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		id, url string
		want    bool
	}{
		{"uknakedmen", "https://www.uknakedmen.com/tour/", true},
		{"uknakedmen", "https://uknakedmen.com/tour/show.php?a=669_2", true},
		{"uknakedmen", "https://ukhotjocks.com/", true},
		{"uknakedmen", "https://www.butchdixon.com/tour/", false},
		{"butchdixon", "https://www.butchdixon.com/tour/?videos/3", true},
		{"youngbastards", "https://youngbastards.com/twink/alec-loob/35958", true},
		{"youngbastards", "https://notyoungbastards.com/", false},
		{"spritzz", "https://spritzz.com/?videos/4", true},
		{"spritzz", "https://spritzz.com.evil.example/", false},
	}
	for _, c := range cases {
		s := newScraper(siteByID(t, c.id))
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s MatchesURL(%q) = %v, want %v", c.id, c.url, got, c.want)
		}
	}
}

func TestModelPathOnlyOnRootSites(t *testing.T) {
	root := newScraper(siteByID(t, "spritzz"))
	if got := root.modelPath("https://spritzz.com/twink/rick-belour/32424"); got != "/twink/rick-belour/32424" {
		t.Errorf("got %q", got)
	}
	if got := root.modelPath("https://spritzz.com/?videos/2"); got != "" {
		t.Errorf("listing url read as model: %q", got)
	}
	tour := newScraper(siteByID(t, "butchdixon"))
	if got := tour.modelPath("https://www.butchdixon.com/twink/x/1"); got != "" {
		t.Errorf("tour site has no model pages: %q", got)
	}
}

func TestPatterns(t *testing.T) {
	p := newScraper(siteByID(t, "uknakedmen")).Patterns()
	want := map[string]bool{
		"uknakedmen.com":                               true,
		"uknakedmen.com/tour/?videos/{page}":           true,
		"uknakedmen.com/tour/show.php?a={site}_{page}": true,
		"ukhotjocks.com":                               true,
	}
	for _, x := range p {
		delete(want, x)
	}
	if len(want) != 0 {
		t.Errorf("missing patterns %v in %v", want, p)
	}
}

func TestRedirectWithRawSpacesIsEscaped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tour/refstat.php" {
			w.Header()["Location"] = []string{"showgal.php?g=content/BD/Lance Charger ND10 /37129/102_1&s=744"}
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		if r.URL.Query().Get("g") != "content/BD/Lance Charger ND10 /37129/102_1" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(fixture(t, "tourdetail.html"))
	}))
	t.Cleanup(srv.Close)

	s := newScraper(siteByID(t, "butchdixon"))
	body, err := s.fetch(context.Background(), srv.URL+"/tour/refstat.php?lid=37129&sid=744")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Sam Wallis And Sam Syron") {
		t.Errorf("unexpected body %q", body)
	}
}

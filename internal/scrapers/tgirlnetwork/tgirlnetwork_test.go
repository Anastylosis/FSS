package tgirlnetwork

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.tgirl-network.com/",
		"https://tgirl-network.com/tour/index.php?section=1631&org=pvt",
		"https://www.tgirl-network.com/tour/mandy-mitchell/",
		"https://www.mandy-mitchell.com",
		"https://www.biankanascimento.com/ctour/",
		"http://www.ts-kimberjames.com",
		"https://www.tgirlpinups.com/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://nataliemars.xxx/",
		"https://shemale-club.com/",
		"https://www.kimberleelive.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		url  string
		want target
	}{
		{"https://www.tgirl-network.com/", target{studio: networkStudio}},
		{"https://www.tgirl-network.com/tour/movie-updates/", target{studio: networkStudio}},
		{"https://tgirl-network.com/tour/index.php?section=1631&org=pvt", target{studio: networkStudio}},
		{"https://www.tgirl-network.com/tour/bailey-jay/", target{slug: "bailey-jay", studio: networkStudio}},
		{"https://www.tgirl-network.com/tour/index.php?model=mandy&start=18", target{model: "mandy", studio: networkStudio}},
		{"https://www.mandy-mitchell.com", target{slug: "mandy-mitchell", studio: "Mandy Mitchell"}},
		{"https://www.bustykimberxxx.com/", target{slug: "kimber-james", studio: "Busty Kimber XXX"}},
	}
	for _, c := range cases {
		if got := resolve(c.url); got != c.want {
			t.Errorf("resolve(%q) = %+v, want %+v", c.url, got, c.want)
		}
	}
}

func TestParseCardsSkipsGalleries(t *testing.T) {
	cards, items := parseCards(fixture(t, "model.html"))
	if items != 3 {
		t.Errorf("items = %d, want 3 (two videos and a gallery)", items)
	}
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(cards))
	}
	c := cards[0]
	if c.id != "8191" || c.section != "257" {
		t.Errorf("card 0 = %+v", c)
	}
	if !strings.Contains(c.thumb, "view_image.php?gal=8191") {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestParseCardsFullTitleFromAlt(t *testing.T) {
	cards, _ := parseCards(fixture(t, "listing.html"))
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(cards))
	}
	if cards[0].id != "13833" || cards[1].id != "13831" {
		t.Errorf("ids = %s, %s", cards[0].id, cards[1].id)
	}
	if cards[1].title != "Garage Quickie Gets Sticky" {
		t.Errorf("title = %q", cards[1].title)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(fixture(t, "detail.html"))
	if d.title != "Crossing Your Wires: Bi encouragement JOI" {
		t.Errorf("title = %q", d.title)
	}
	if len(d.performers) != 1 || d.performers[0] != "Mandy Mitchell" {
		t.Errorf("performers = %v", d.performers)
	}
	if !strings.HasPrefix(d.description, "I know you are a totally straight guy") {
		t.Errorf("description = %q", d.description)
	}
}

func TestParseDetailPresentsFallback(t *testing.T) {
	body := `<meta name="keywords" content="tgirl network, ">
<div class="galpres">The TGirl Network Presents Bailey Jay In</span></div>
<h1 class="galtitle ac">Garage Quickie Gets Sticky</h1>`
	d := parseDetail(body)
	if len(d.performers) != 1 || d.performers[0] != "Bailey Jay" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.title != "Garage Quickie Gets Sticky" {
		t.Errorf("title = %q", d.title)
	}
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

func TestSoloSiteScrapesModelPage(t *testing.T) {
	model := fixture(t, "model.html")
	detail := fixture(t, "detail.html")
	var page2 atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tour/mandy-mitchell/":
			_, _ = w.Write([]byte(model))
		case "/tour/index.php":
			if r.URL.Query().Get("model") != "mandy" || r.URL.Query().Get("start") != "18" {
				t.Errorf("unexpected listing query %q", r.URL.RawQuery)
			}
			page2.Add(1)
			_, _ = w.Write([]byte("<html><body>no updates</body></html>"))
		case "/tour/show_video.php":
			_, _ = w.Write([]byte(detail))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL

	const studioURL = "https://www.mandy-mitchell.com"
	scenes, errs := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// model.html carries 3 cards, under a full page, so no second page is asked for.
	if page2.Load() != 0 {
		t.Errorf("fetched a second page of a short listing")
	}
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d, want 2", len(scenes))
	}
	sc := scenes[0]
	if sc.StudioURL != studioURL || sc.SiteID != siteID || sc.Studio != "Mandy Mitchell" {
		t.Errorf("scene identity = %q %q %q", sc.StudioURL, sc.SiteID, sc.Studio)
	}
	if sc.ID != "8191" || !strings.HasPrefix(sc.URL, srv.URL+"/tour/show_video.php?galid=8191") {
		t.Errorf("id/url = %q %q", sc.ID, sc.URL)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Mandy Mitchell" || sc.Description == "" {
		t.Errorf("detail not applied: %+v", sc)
	}
	if !strings.HasPrefix(sc.Thumbnail, srv.URL+"/tour/view_image.php") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

func cardsHTML(from, n int) string {
	var b strings.Builder
	for i := from; i < from+n; i++ {
		fmt.Fprintf(&b, `<div class="uthumb-item"><a href="/tour/show_video.php?galid=%d&section=1"><img src="/p.png" alt="Scene %d"></a></div>`, i, i)
	}
	return b.String()
}

func TestNetworkPaginatesAndStops(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tour/index.php":
			calls.Add(1)
			if r.URL.Query().Get("gtype") != "videos" {
				t.Errorf("network listing without gtype=videos: %q", r.URL.RawQuery)
			}
			switch r.URL.Query().Get("start") {
			case "0":
				_, _ = w.Write([]byte(cardsHTML(100, pageSize)))
			case "18":
				_, _ = w.Write([]byte(cardsHTML(200, 3)))
			default:
				t.Errorf("fetched past a short page: %q", r.URL.RawQuery)
			}
		case "/tour/show_video.php":
			_, _ = w.Write([]byte(`<h1 class="galtitle">Full Title</h1>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	scenes, errs := collect(t, s, "https://www.tgirl-network.com/", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if calls.Load() != 2 {
		t.Errorf("listing calls = %d, want 2", calls.Load())
	}
	if len(scenes) != pageSize+3 {
		t.Fatalf("scenes = %d, want %d", len(scenes), pageSize+3)
	}
	if scenes[0].Title != "Full Title" || scenes[0].Studio != networkStudio {
		t.Errorf("scene 0 = %+v", scenes[0])
	}
}

func TestKnownIDsStopAfterPage(t *testing.T) {
	var details atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tour/index.php":
			if r.URL.Query().Get("start") != "0" {
				t.Errorf("walked past a page holding a known scene")
			}
			_, _ = w.Write([]byte(cardsHTML(100, pageSize)))
		case "/tour/show_video.php":
			details.Add(1)
			_, _ = w.Write([]byte(`<h1 class="galtitle">T</h1>`))
		}
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	scenes, _ := collect(t, s, "https://www.tgirl-network.com/", scraper.ListOpts{KnownIDs: map[string]bool{"105": true}})
	if len(scenes) != pageSize-1 {
		t.Errorf("scenes = %d, want %d", len(scenes), pageSize-1)
	}
	if details.Load() != pageSize-1 {
		t.Errorf("detail fetches = %d, want %d (none for the known scene)", details.Load(), pageSize-1)
	}
}

func TestFirstPageWithoutCardsIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>redesigned</html>"))
	}))
	defer srv.Close()
	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	_, errs := collect(t, s, "https://www.tgirl-network.com/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v", errs)
	}
}

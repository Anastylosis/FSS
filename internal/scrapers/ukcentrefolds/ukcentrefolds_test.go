package ukcentrefolds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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
		if c.id == id {
			return c
		}
	}
	t.Fatalf("no site %q", id)
	return siteConfig{}
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

func serve(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fixture(t, name))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGridPagination(t *testing.T) {
	srv := serve(t, map[string]string{
		"/portfolio_category/videos/":        "grid_page1.html",
		"/portfolio_category/videos/page/2/": "grid_page2.html",
	})
	cfg := siteByID(t, "staceypoole")
	cfg.base = srv.URL
	s := newScraper(cfg)

	const studio = "https://staceypoole.co.uk/"
	scenes, errs := collect(t, s, studio, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	first := scenes[0]
	if first.ID != "123628" || first.Title != "Barbies’ Bathroom" || first.Duration != 692 {
		t.Errorf("first scene = %q %q %d", first.ID, first.Title, first.Duration)
	}
	if first.StudioURL != studio || first.SiteID != "staceypoole" || first.Studio != "Stacey Poole" {
		t.Errorf("studio fields = %q %q %q", first.StudioURL, first.SiteID, first.Studio)
	}
	if first.URL != "https://staceypoole.co.uk/portfolio/barbies-bathroom-1132-minutes-hd-video/" {
		t.Errorf("url = %q", first.URL)
	}
	if want := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC); !first.Date.Equal(want) {
		t.Errorf("date = %v, want %v", first.Date, want)
	}
	if len(first.Performers) != 1 || first.Performers[0] != "Stacey Poole" {
		t.Errorf("performers = %v", first.Performers)
	}
	if first.Thumbnail == "" {
		t.Error("missing thumbnail")
	}
	if scenes[1].Title != "Feeling Shadey" || scenes[1].Duration != 710 {
		t.Errorf("second = %q %d", scenes[1].Title, scenes[1].Duration)
	}
	if got := scenes[2].Title; got != "Outdoor, Nighttime Shower with Melissa Debling “Staceys Version”" {
		t.Errorf("page 2 title = %q", got)
	}
}

func TestGridKnownIDsStopsAfterPage(t *testing.T) {
	srv := serve(t, map[string]string{
		"/portfolio_category/videos/": "grid_page1.html",
	})
	cfg := siteByID(t, "alicebrookes")
	cfg.base = srv.URL
	scenes, errs := collect(t, newScraper(cfg), "https://www.alicebrookes.com/", scraper.ListOpts{
		KnownIDs: map[string]bool{"123317": true},
	})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 || scenes[0].ID != "123628" {
		t.Fatalf("got %d scenes", len(scenes))
	}
}

func TestGridEmptyFirstPageIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer srv.Close()
	cfg := siteByID(t, "melissadebling")
	cfg.base = srv.URL
	_, errs := collect(t, newScraper(cfg), "https://melissad.co.uk/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Fatalf("errs = %v", errs)
	}
}

func TestBlogLayout(t *testing.T) {
	srv := serve(t, map[string]string{
		"/portfolio-category/video/": "blog_page1.html",
	})
	cfg := siteByID(t, "glamourvilla")
	cfg.base = srv.URL
	scenes, errs := collect(t, newScraper(cfg), "https://www.glamourvilla.com/", scraper.ListOpts{})
	// The fixture links page 2, which the server 404s: the walk must try it.
	if len(errs) != 1 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	sc := scenes[1]
	if sc.ID != "73751" || sc.Title != "Jade Entwistle" || sc.Duration != 964 {
		t.Errorf("scene = %q %q %d", sc.ID, sc.Title, sc.Duration)
	}
	if want := time.Date(2016, 7, 14, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Jade Entwistle" {
		t.Errorf("performers = %v", sc.Performers)
	}
}

func TestHomeLayouts(t *testing.T) {
	cases := []struct {
		id, fixture, studio string
		want                int
		title               string
		dur                 int
	}{
		{"officialsammybraddy", "home_sammy.html", "https://www.officialsammybraddy.com/", 1, "Bathroom Fun", 1647},
		{"officialrae", "home_rae.html", "https://www.officialrae.com/", 2, "Bored at home", 829},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			srv := serve(t, map[string]string{"/": c.fixture})
			cfg := siteByID(t, c.id)
			cfg.base = srv.URL
			scenes, errs := collect(t, newScraper(cfg), c.studio, scraper.ListOpts{})
			if len(errs) > 0 {
				t.Fatalf("errors: %v", errs)
			}
			if len(scenes) != c.want {
				t.Fatalf("got %d scenes, want %d", len(scenes), c.want)
			}
			sc := scenes[0]
			if sc.Title != c.title || sc.Duration != c.dur || sc.StudioURL != c.studio {
				t.Errorf("scene = %q %d %q", sc.Title, sc.Duration, sc.StudioURL)
			}
			if sc.Thumbnail == "" || sc.Date.IsZero() {
				t.Errorf("thumb %q date %v", sc.Thumbnail, sc.Date)
			}
		})
	}
}

func TestSplitTitle(t *testing.T) {
	cases := []struct {
		in    string
		title string
		dur   int
	}{
		{"Country Gal &#8211; 08:38 Mins HD Video", "Country Gal", 518},
		{"Naked &#8211; 265 Pictures AND 4:07 Full HD Video!", "Naked", 247},
		{"Imogen Sparkle – 8:53 Minutes Video!", "Imogen Sparkle", 533},
		{"Mrs Claus- 12:56 Mins Bonus BTS Video", "Mrs Claus", 776},
		{"4th July Fun &#8211; 19:57 HD Video", "4th July Fun", 1197},
		{"Becca Hodge", "Becca Hodge", 0},
	}
	for _, c := range cases {
		title, dur := splitTitle(c.in)
		if title != c.title || dur != c.dur {
			t.Errorf("splitTitle(%q) = %q, %d", c.in, title, dur)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	cases := map[string]string{
		"https://staceypoole.co.uk/":                           "staceypoole",
		"https://staceypoole.co.uk/portfolio_category/videos/": "staceypoole",
		"https://alicebrookes.com":                             "alicebrookes",
		"https://melissad.co.uk/":                              "melissadebling",
		"https://www.glamourvilla.com/":                        "glamourvilla",
		"https://officialsammybraddy.com/":                     "officialsammybraddy",
		"https://www.officialrae.com/":                         "officialrae",
	}
	for u, id := range cases {
		matched := ""
		for _, c := range sites {
			if newScraper(c).MatchesURL(u) {
				matched = c.id
			}
		}
		if matched != id {
			t.Errorf("%s matched %q, want %q", u, matched, id)
		}
	}
	if newScraper(siteByID(t, "staceypoole")).MatchesURL("https://staceypoole.co.uk.evil.com/") {
		t.Error("matched a foreign host")
	}
}

package mooduniverse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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

func TestParseElitePain(t *testing.T) {
	s := New(sites[0])
	scenes := s.parse(fixture(t, "elitepain.html"), "https://elitepain.com/movies.php", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d, want 2", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1825" || sc.Title != "Cards of Pain RLD - AMIRAH" || sc.URL != s.base()+"/movie.php?id=1825" {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Duration != 75*60 || sc.Description != "various single tail whips, strap" {
		t.Errorf("Duration/Description = %d %q", sc.Duration, sc.Description)
	}
	if sc.LowestPrice != 24.95 || len(sc.PriceHistory) != 1 || !sc.PriceHistory[0].IsOnSale || sc.PriceHistory[0].Regular != 49.95 {
		t.Errorf("price = %v %+v", sc.LowestPrice, sc.PriceHistory)
	}
	if sc.Thumbnail != s.base()+"/content-images/logo/coprldamirah.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if scenes[1].Duration == 0 {
		t.Errorf("second scene has no duration: %+v", scenes[1])
	}
}

func TestParseCruelMistressesCastAndDedup(t *testing.T) {
	s := New(sites[2])
	scenes := s.parse(fixture(t, "cruelmistresses.html"), "https://cruel-mistresses.com/", time.Now())
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d, want 2 (duplicate card dropped)", len(scenes))
	}
	if got := scenes[0].Performers; len(got) != 2 || got[0] != "Artemis" || got[1] != "Natasha" {
		t.Errorf("Performers = %v", got)
	}
	if scenes[0].Duration != 27*60 {
		t.Errorf("Duration = %d", scenes[0].Duration)
	}
}

func TestParseDetails(t *testing.T) {
	for in, want := range map[string]int{
		"1 hour 15 min.; single tail whips": 4500,
		"85 min+extras.; various whips":     5100,
		"27 minutes":                        1620,
		"54 min., 170 strokes":              3240,
		"n/a":                               0,
	} {
		if got, _ := parseDetails(in); got != want {
			t.Errorf("parseDetails(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestListScenes(t *testing.T) {
	body := fixture(t, "elitepain.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movies.php":
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	testBase = srv.URL
	defer func() { testBase = "" }()

	ch, err := New(sites[0]).ListScenes(context.Background(), "https://elitepain.com/movies.php", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n, total := 0, 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatal(r.Err)
		case scraper.KindScene:
			n++
		case scraper.KindTotal:
			total = r.Total
		}
	}
	if n != 2 || total != 2 {
		t.Errorf("scenes = %d, total = %d, want 2, 2", n, total)
	}
}

func TestListScenesAgeGate(t *testing.T) {
	body := fixture(t, "elitepain.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/18.php":
			if r.URL.Query().Get("action") == "enter" {
				http.SetCookie(w, &http.Cookie{Name: "age_verified_18", Value: "1", Path: "/"})
			}
			http.Redirect(w, r, "/movies.php", http.StatusFound)
		case "/movies.php":
			if c, err := r.Cookie("age_verified_18"); err != nil || c.Value != "1" {
				http.Redirect(w, r, "/18.php", http.StatusFound)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	testBase = srv.URL
	defer func() { testBase = "" }()

	var cfg SiteConfig
	for _, c := range sites {
		if c.SiteID == "cruelstrapon" {
			cfg = c
		}
	}
	ch, err := New(cfg).ListScenes(context.Background(), "https://cruel-strapon.com/movies.php", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatal(r.Err)
		case scraper.KindScene:
			n++
		}
	}
	if n != 2 {
		t.Errorf("scenes = %d, want 2", n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(sites[0])
	for u, want := range map[string]bool{
		"https://elitepain.com/movies.php":     true,
		"http://www.elitepain.com/":            true,
		"https://mood-pictures.com/movies.php": false,
	} {
		if s.MatchesURL(u) != want {
			t.Errorf("MatchesURL(%q) != %v", u, want)
		}
	}
}

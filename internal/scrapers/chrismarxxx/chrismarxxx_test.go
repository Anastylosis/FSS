package chrismarxxx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/posts.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://www.marxxxmodels.com/": true,
		"https://chrismarxxx.com/":      true,
		"https://example.com/":          false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestPerformer(t *testing.T) {
	for title, want := range map[string]string{
		"Nicole Rae Solo #1":                     "Nicole Rae",
		"Gigi Sweets Is Horny":                   "Gigi Sweets",
		"Silvia Ludis Can’t Wait":                "Silvia Ludis",
		"Frankie “Stormy” Skye Wants It":         "Frankie “Stormy” Skye",
		"Alicia Williams Ass & Face Fucked":      "Alicia Williams",
		"Bonus Video: Eve Bardot Wants It":       "Eve Bardot",
		"Aurora Angel Puppy Girlfriend":          "Aurora Angel",
		"Jenna Fireworks Bonus Clip 69":          "Jenna Fireworks",
		"Wants It":                               "",
		"Just A Title With No Stop Word At All":  "",
		"Hannah Grace Bonus Video  Solo":         "Hannah Grace",
		"Nina Nova The Surprise":                 "Nina Nova",
		"Some Name Without Any Known Verb Words": "",
	} {
		if got := performer(title); got != want {
			t.Errorf("performer(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestToScene(t *testing.T) {
	var posts []post
	if err := json.Unmarshal(fixture(t), &posts); err != nil {
		t.Fatal(err)
	}
	sc := toScene(posts[0], "https://www.marxxxmodels.com/", time.Now())
	if sc.ID != "31835" || sc.Title != "Nicole Rae Solo #1" || sc.URL != "https://www.marxxxmodels.com/nicole-rae-solo-1/" {
		t.Errorf("scene = %+v", sc)
	}
	if !sc.Date.Equal(time.Date(2026, 11, 14, 20, 46, 19, 0, time.UTC)) || sc.Thumbnail == "" {
		t.Errorf("date = %v, thumbnail = %q", sc.Date, sc.Thumbnail)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Nicole Rae" {
		t.Errorf("performers = %v", sc.Performers)
	}
}

func TestListScenes(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-json/wp/v2/posts" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-WP-Total", "2")
		w.Header().Set("X-WP-TotalPages", "1")
		_, _ = w.Write(fixture(t))
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://www.marxxxmodels.com/", scraper.ListOpts{})
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
	if n != 2 || len(queries) != 1 {
		t.Errorf("scenes = %d, queries = %v", n, queries)
	}
}

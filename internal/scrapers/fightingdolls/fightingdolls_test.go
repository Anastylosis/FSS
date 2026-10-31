package fightingdolls

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

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://www.fighting-dolls.com/":                           true,
		"https://fighting-dolls.com":                                true,
		"https://www.fighting-dolls.com/all-fighting-dolls-videos/": true,
		"https://www.fighting-dolls.com/girls/nila-322/":            false,
		"https://example.com/":                                      false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "list.html"))
	if len(cards) != 2 {
		t.Fatalf("cards = %d", len(cards))
	}
	c := cards[0]
	if c.num != "7021" || c.title != "Mara vs. Nila" || c.duration != 22*60+22 || c.blurb != "Pins and submission." {
		t.Errorf("card = %+v", c)
	}
	if !strings.HasSuffix(c.thumb, "/data/movies/7021/1_nahled.jpg") || c.path != "/movies/mara-vs-nila-7021/" {
		t.Errorf("card = %+v", c)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(fixture(t, "detail.html"))
	if len(d.performers) != 2 || d.performers[0] != "Nila" || d.performers[1] != "Zeni" {
		t.Errorf("performers = %v", d.performers)
	}
	if len(d.tags) != 13 || d.tags[0] != "Armwrestling" || d.tags[12] != "Wrestling" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.desc != "Pins and submission." {
		t.Errorf("desc = %q", d.desc)
	}
}

func TestListScenes(t *testing.T) {
	list, detailBody := fixture(t, "list.html"), fixture(t, "detail.html")
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/all-fighting-dolls-videos/"):
			pages = append(pages, r.URL.Path)
			_, _ = w.Write(list)
		case strings.HasPrefix(r.URL.Path, "/movies/"):
			_, _ = w.Write(detailBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	studio := "https://www.fighting-dolls.com/all-fighting-dolls-videos/"
	ch, err := New().ListScenes(ctx, studio, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			sc := r.Scene
			if sc.StudioURL != studio || !strings.HasPrefix(sc.URL, srv.URL) || len(sc.Performers) != 2 || len(sc.Tags) == 0 {
				t.Errorf("scene = %+v", sc)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != 2 || len(pages) != 1 || pages[0] != "/all-fighting-dolls-videos/0/" {
		t.Errorf("scenes=%d pages=%v", n, pages)
	}
}

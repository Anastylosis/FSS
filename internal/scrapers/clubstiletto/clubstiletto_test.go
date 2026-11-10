package clubstiletto

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.clubstiletto.com/", "https://clubstiletto.com", "https://www.clubstiletto.com/front-page-all"} {
		if !s.MatchesURL(u) {
			t.Errorf("no match: %s", u)
		}
	}
	if s.MatchesURL("https://www.clubstiletto.com/content/x") || s.MatchesURL("https://example.com/") {
		t.Error("unexpected match")
	}
}

func TestParseCardsKeepsOnlyVideos(t *testing.T) {
	cards, rows := parseCards(read(t, "list.html"))
	if rows != 3 || len(cards) != 2 {
		t.Fatalf("rows = %d, video cards = %d", rows, len(cards))
	}
	c := cards[0]
	if c.id != "9307" || c.path != "/content/his-life-human-floor" || c.title != "His Life as a Human Floor" ||
		!strings.Contains(c.thumb, "still_posters/his-life-as-a-human-floor.png") {
		t.Errorf("card = %+v", c)
	}
}

func TestToScene(t *testing.T) {
	c := card{id: "9307", path: "/content/his-life-human-floor", title: "His Life as a Human Floor"}
	sc := toScene(c, read(t, "detail.html"), "https://www.clubstiletto.com/", time.Now())
	if !sc.Date.Equal(time.Date(2026, 11, 20, 18, 15, 0, 0, time.UTC)) || sc.Duration != 7*60 {
		t.Errorf("Date/Duration = %v %d", sc.Date, sc.Duration)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Jasmine, Miss" || !strings.HasPrefix(sc.Description, "Miss Jasmine steps over") {
		t.Errorf("Performers/Description = %v / %q", sc.Performers, sc.Description)
	}
	if sc.URL != siteBase+"/content/his-life-human-floor" || sc.Studio != "Club Stiletto" {
		t.Errorf("scene = %+v", sc)
	}
}

func TestListScenesWalksUntilEmptyPage(t *testing.T) {
	var details atomic.Int32
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/front-page-all":
			pages = append(pages, r.URL.Query().Get("page"))
			if r.URL.Query().Get("page") == "0" {
				_, _ = w.Write([]byte(read(t, "list.html")))
				return
			}
			_, _ = w.Write([]byte("<html><div class=\"view-content\"></div></html>"))
		case strings.HasPrefix(r.URL.Path, "/content/"):
			details.Add(1)
			_, _ = w.Write([]byte(read(t, "detail.html")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://www.clubstiletto.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			t.Fatal(r.Err)
		}
	}
	if n != 2 || details.Load() != 2 || len(pages) != 2 || pages[0] != "0" || pages[1] != "1" {
		t.Errorf("scenes = %d, details = %d, pages = %v", n, details.Load(), pages)
	}
}

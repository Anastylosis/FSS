package girlsgonewild

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
	for _, u := range []string{
		"https://girlsgonewild.com/",
		"https://girlsgonewild.com",
		"https://girlsgonewild.com/new-videos",
		"https://girlsgonewild.com/new-videos/2",
		"https://girlsgonewild.com/studios/firstanalteens_studio",
		"https://girlsgonewild.com/model/5251/olivia_westsun",
		"https://girlsgonewild.com/genre/anal",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("no match: %s", u)
		}
	}
	for _, u := range []string{"https://girlsgonewild.com/watch/1/x", "https://example.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("unexpected match: %s", u)
		}
	}
}

func TestListPath(t *testing.T) {
	for u, want := range map[string]string{
		"https://girlsgonewild.com/":                  "/new-videos",
		"https://girlsgonewild.com/studios/x_studio":  "/studios/x_studio",
		"https://girlsgonewild.com/model/5251/olivia": "/model/5251/olivia",
		"https://girlsgonewild.com/genre/anal":        "/genre/anal",
		"https://girlsgonewild.com/new-videos/7":      "/new-videos",
	} {
		if got := listPath(u); got != want {
			t.Errorf("listPath(%s) = %q, want %q", u, got, want)
		}
	}
}

func TestParseCardsAndLastPage(t *testing.T) {
	page := read(t, "list.html")
	cards := parseCards(page)
	if len(cards) != 3 {
		t.Fatalf("cards = %d", len(cards))
	}
	c := cards[0]
	if c.id != "5540062" || c.duration != 24*60 || !c.date.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) ||
		!strings.Contains(c.url, "/watch/5540062/") || c.title == "" || !strings.HasPrefix(c.thumb, "https://") {
		t.Errorf("card = %+v", c)
	}
	if n := lastPage(page, "/new-videos"); n != 903 {
		t.Errorf("lastPage = %d", n)
	}
	if n := lastPage("<html></html>", "/studios/x"); n != 1 {
		t.Errorf("lastPage without pagination = %d", n)
	}
}

func TestCardDuration(t *testing.T) {
	if cardDuration("1 h 5 min") != 3900 || cardDuration("24 min") != 1440 {
		t.Error("cardDuration")
	}
}

func TestToScene(t *testing.T) {
	c := parseCards(read(t, "list.html"))[0]
	sc := toScene(c, read(t, "detail.html"), "https://girlsgonewild.com/new-videos", time.Now())
	if len(sc.Performers) != 1 || sc.Performers[0] != "Olivia Westsun" || sc.Studio != "firstanalteens Studio" {
		t.Errorf("Performers/Studio = %v / %q", sc.Performers, sc.Studio)
	}
	if sc.Duration != 24*60+40 || len(sc.Tags) < 10 || sc.Description == "" || sc.Tags[0] != "skinny" {
		t.Errorf("Duration/Tags/Description = %d %v %q", sc.Duration, sc.Tags, sc.Description)
	}
}

func TestListScenesStopsAtKnownAndSkipsItsDetail(t *testing.T) {
	var details atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/new-videos":
			_, _ = w.Write([]byte(strings.ReplaceAll(read(t, "list.html"), "https://girlsgonewild.com", srv.URL)))
		case strings.HasPrefix(r.URL.Path, "/watch/"):
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

	// A known ID ends the walk after the page in hand, so page 2 (a 404 here)
	// is never requested.
	ch, err := New().ListScenes(context.Background(), "https://girlsgonewild.com/new-videos", scraper.ListOpts{KnownIDs: map[string]bool{"5554936": true}})
	if err != nil {
		t.Fatal(err)
	}
	n, errs := 0, 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
		case scraper.KindError:
			errs++
		}
	}
	if n != 2 || details.Load() != 2 || errs != 0 {
		t.Errorf("scenes = %d, detail fetches = %d, errors = %d", n, details.Load(), errs)
	}
}

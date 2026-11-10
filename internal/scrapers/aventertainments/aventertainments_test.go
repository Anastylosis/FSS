package aventertainments

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

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesAndParseStudioURL(t *testing.T) {
	s := New()
	cases := map[string]string{
		"https://www.aventertainments.com/dvd/studio?lang=1&cat=29&studio=768":                       "768",
		"https://www.aventertainments.com/dvd/studio?lang=1&culture=en-US&cat=29&studio=493":         "493",
		"https://www.aventertainments.com/29/768/1/studio_products":                                  "768",
		"https://www.aventertainments.com/studio_products.aspx?StudioID=418&Dept_ID=29&languageID=1": "418",
	}
	for u, want := range cases {
		if !s.MatchesURL(u) {
			t.Errorf("no match: %s", u)
			continue
		}
		if got, _, _ := parseStudioURL(u); got != want {
			t.Errorf("%s: studio = %q, want %q", u, got, want)
		}
	}
	for _, u := range []string{
		"https://www.aventertainments.com/ppv/studio?studio=60&lang=1&culture=en-US&v=1",
		"https://www.aventertainments.com/",
		"https://example.com/dvd/studio?studio=1",
	} {
		if s.MatchesURL(u) {
			t.Errorf("unexpected match: %s", u)
		}
	}
}

func TestParseCards(t *testing.T) {
	cards := parseCards(read(t, "studio.html"))
	if len(cards) != 3 {
		t.Fatalf("cards = %d", len(cards))
	}
	if cards[0].id != "162891" || !strings.HasPrefix(cards[0].title, "CATWALK POISON GCDV 006") ||
		!strings.HasSuffix(cards[0].thumb, "dvd1gcdv-006.webp") || cards[0].price != 24.50 {
		t.Errorf("card = %+v", cards[0])
	}
}

func TestToScene(t *testing.T) {
	c := parseCards(read(t, "studio.html"))[0]
	sc := toScene(c, string(read(t, "detail.html")), "https://x/detail", "u", time.Now())
	if len(sc.Performers) != 4 || sc.Performers[0] != "Ryu Enami" || sc.Studio != "CATWALK" || sc.Series != "CATWALK POISON GCDV" {
		t.Errorf("Performers/Studio/Series = %v %q %q", sc.Performers, sc.Studio, sc.Series)
	}
	if !sc.Date.Equal(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)) || sc.Duration != 113*60 {
		t.Errorf("Date/Duration = %v %d", sc.Date, sc.Duration)
	}
	if len(sc.Tags) == 0 || sc.Tags[0] != "3P" || sc.LowestPrice != 24.50 {
		t.Errorf("Tags/Price = %v %v", sc.Tags, sc.LowestPrice)
	}
	for _, tag := range sc.Tags {
		if tag == "New Release" {
			t.Error("store-only tag kept")
		}
	}
}

func TestListScenesWalksPagesAndSkipsKnownDetails(t *testing.T) {
	list, detail := read(t, "studio.html"), read(t, "detail.html")
	var details atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dvd/studio":
			if r.URL.Query().Get("studio") != "768" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write(list)
				return
			}
			_, _ = w.Write([]byte("<html></html>"))
		case "/dvd/detail":
			details.Add(1)
			_, _ = w.Write(detail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	known := map[string]bool{"161710": true}
	ch, err := New().ListScenes(context.Background(), "https://www.aventertainments.com/29/768/1/studio_products", scraper.ListOpts{KnownIDs: known})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			if r.Scene.Studio != "CATWALK" {
				t.Errorf("scene = %+v", r.Scene)
			}
		case scraper.KindError:
			t.Fatal(r.Err)
		}
	}
	if n != 2 || details.Load() != 2 {
		t.Errorf("scenes = %d, detail fetches = %d (known scene must not be fetched)", n, details.Load())
	}
}

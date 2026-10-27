package mistresscms

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

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "listing.html"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards", len(cards))
	}
	c := cards[0]
	if c.id != "926" || c.title != "MISTRESS ISIDE - BRUTAL WHIPPING PUNISHMENT HD" || c.price != 21.99 {
		t.Errorf("card = %+v", c)
	}
	if !c.date.Equal(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)) || c.thumb == "" {
		t.Errorf("date/thumb = %v %q", c.date, c.thumb)
	}
}

func TestApplyDetail(t *testing.T) {
	s := New(sites[0])
	sc := s.baseScene(card{id: "926", title: "t"}, "https://www.mistress-iside.com/", time.Now())
	applyDetail(&sc, fixture(t, "detail.html"))
	if sc.Duration != 18*60+3 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if !strings.HasPrefix(sc.Description, "I summoned this slave") {
		t.Errorf("Description = %q", sc.Description)
	}
	if len(sc.Tags) == 0 || sc.Tags[0] != "whipping" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Mistress Iside" {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

func TestEuros(t *testing.T) {
	for in, want := range map[string]float64{"21,99": 21.99, "1.021,50": 1021.5} {
		if got := euros(in); got != want {
			t.Errorf("euros(%q) = %v", in, got)
		}
	}
}

func TestRunWalksAndReportsDetailFailures(t *testing.T) {
	listing, detail := fixture(t, "listing.html"), fixture(t, "detail.html")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rewrite := func(s string) string { return strings.ReplaceAll(s, "https://www.mistress-iside.com", srv.URL) }
		switch {
		case r.URL.Path == "/en/store/videos/new/p/1/":
			_, _ = w.Write([]byte(rewrite(listing)))
		case strings.Contains(r.URL.Path, "multi-position"):
			http.Error(w, "gone", http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/en/store/videos/v/"):
			_, _ = w.Write([]byte(rewrite(detail)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := New(sites[0])
	s.base = srv.URL
	s.Client = srv.Client()

	ch, _ := s.ListScenes(context.Background(), "https://www.mistress-iside.com/", scraper.ListOpts{})
	scenes, errs := 0, 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.StudioURL != "https://www.mistress-iside.com/" {
				t.Errorf("StudioURL = %q", r.Scene.StudioURL)
			}
		case scraper.KindError:
			errs++
		}
	}
	// Page 2 is linked but 404s, so the walk ends with an error after page 1;
	// what matters here is every card is kept and the failed detail reported.
	if scenes != 3 || errs < 1 {
		t.Errorf("scenes=%d errs=%d", scenes, errs)
	}
}

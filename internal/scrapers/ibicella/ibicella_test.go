package ibicella

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
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

func TestParseList(t *testing.T) {
	cards := parseList(string(read(t, "list.html")))
	if len(cards) != 3 {
		t.Fatalf("got %d cards", len(cards))
	}
	if cards[1].slug != "100eur-pour-avaler-cei" || cards[1].price != 100 || cards[1].title != "100€ pour avaler - CEI" {
		t.Errorf("card: %+v", cards[1])
	}
	if cards[2].price != 12.99 || cards[2].duration != 12*60+47 {
		t.Errorf("card: %+v", cards[2])
	}
}

func TestApplyDetail(t *testing.T) {
	sc := parseList(string(read(t, "list.html")))[0].scene("https://ibicella.com/", time.Now())
	applyDetail(&sc, string(read(t, "detail.html")))
	if sc.Title != "Exhib CEI" || sc.Date.Format("2006-01-02") != "2026-08-28" || sc.Duration != 12*60+37 {
		t.Errorf("scene: %q %v %d", sc.Title, sc.Date, sc.Duration)
	}
	if !strings.Contains(sc.Description, "regretter") || strings.HasSuffix(sc.Description, "...") {
		t.Errorf("description %q", sc.Description)
	}
	if !strings.Contains(sc.Thumbnail, "/storage/media/") || len(sc.Tags) < 3 {
		t.Errorf("thumb %q tags %v", sc.Thumbnail, sc.Tags)
	}
	if len(sc.Categories) == 0 || sc.Categories[0] != "MANIPULATION" {
		t.Errorf("categories %v", sc.Categories)
	}
}

func TestEndToEnd(t *testing.T) {
	list, detail := read(t, "list.html"), read(t, "detail.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/shop/"):
			_, _ = w.Write(detail)
		case r.URL.Path == "/videos" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(list)
		case r.URL.Path == "/videos/manipulation" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(list)
		default:
			_, _ = w.Write([]byte("<html></html>"))
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	for _, studio := range []string{"https://ibicella.com/", "https://ibicella.com/videos/manipulation"} {
		s := New()
		s.client = srv.Client()
		ch, err := s.ListScenes(context.Background(), studio, scraper.ListOpts{})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for r := range ch {
			switch r.Kind {
			case scraper.KindScene:
				n++
				testutil.ValidateScene(t, r.Scene)
				if r.Scene.Date.IsZero() {
					t.Errorf("%s has no date", r.Scene.ID)
				}
			case scraper.KindError:
				t.Fatalf("error: %v", r.Err)
			}
		}
		if n != 3 {
			t.Fatalf("%s: got %d scenes", studio, n)
		}
	}
}

func TestListPath(t *testing.T) {
	for in, want := range map[string]string{
		"https://ibicella.com/":                  "/videos",
		"https://ibicella.com/videos":            "/videos",
		"https://ibicella.com/videos/joi-games/": "/videos/joi-games",
		"https://ibicella.com/videos/joi/edging": "/videos/joi/edging",
		"https://ibicella.com/photos":            "/videos",
	} {
		if got := listPath(in); got != want {
			t.Errorf("listPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://www.ibicella.com/videos") || s.MatchesURL("https://ibicella.com.evil.test/") {
		t.Error("MatchesURL wrong")
	}
}

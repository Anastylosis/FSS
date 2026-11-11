package dollshub

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

var cfg = SiteConfig{SiteID: "maturegapers", Domain: "maturegapers.com", StudioName: "Mature Gapers"}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New(cfg)
	for u, want := range map[string]bool{
		"https://maturegapers.com/scenes": true,
		"https://www.maturegapers.com/":   true,
		"https://filthygapers.com/":       false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

func TestEverySiteMatchesOnlyItself(t *testing.T) {
	for _, a := range sites {
		for _, b := range sites {
			if got := New(a).MatchesURL("https://" + b.Domain + "/"); got != (a.Domain == b.Domain) {
				t.Errorf("%s.MatchesURL(%s) = %v", a.SiteID, b.Domain, got)
			}
		}
	}
}

func TestParseListing(t *testing.T) {
	scenes := New(cfg).parseListing(fixture(t, "listing1.html"), "https://maturegapers.com/scenes", time.Now())
	if len(scenes) != 3 {
		t.Fatalf("got %d cards", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "129" || sc.Title != "Thick mature bitch Ellen Saint gets a good pounding" ||
		sc.URL != "https://maturegapers.com/scence-single?scene-name=Thick%20mature%20bitch%20Ellen%20Saint%20gets%20a%20good%20pounding-129" ||
		!strings.HasPrefix(sc.Thumbnail, "https://maturegapers.com/files/photos/") {
		t.Errorf("scene = %+v", sc)
	}
	if !sc.Date.IsZero() {
		t.Errorf("the listing's 1970 date must not become a scene date: %v", sc.Date)
	}
}

func TestApplyDetail(t *testing.T) {
	scenes := New(cfg).parseListing(fixture(t, "listing1.html"), "u", time.Now())
	applyDetail(fixture(t, "detail.html"), &scenes[0])
	sc := scenes[0]
	if len(sc.Performers) != 1 || sc.Performers[0] != "Ellen Saint" || sc.Duration != 40*60+49 {
		t.Errorf("performers = %v, duration = %d", sc.Performers, sc.Duration)
	}
	if !strings.HasPrefix(sc.Description, "Our experienced fucker") || strings.Contains(sc.Description, "<") {
		t.Errorf("description = %q", sc.Description)
	}
}

func TestListScenes(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/scenes":
			p := r.URL.Query().Get("page")
			pages = append(pages, p)
			switch p {
			case "1":
				_, _ = w.Write([]byte(fixture(t, "listing1.html")))
			case "2":
				_, _ = w.Write([]byte(fixture(t, "listing2.html")))
			default:
				http.NotFound(w, r)
			}
		case "/scence-single":
			_, _ = w.Write([]byte(fixture(t, "detail.html")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := New(cfg)
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://maturegapers.com/scenes", scraper.ListOpts{})
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
			if !strings.HasPrefix(r.Scene.URL, srv.URL) || len(r.Scene.Performers) != 1 {
				t.Errorf("scene = %+v", r.Scene)
			}
		}
	}
	if n != 4 || strings.Join(pages, ",") != "1,2,3" {
		t.Errorf("scenes = %d, pages = %v", n, pages)
	}
}

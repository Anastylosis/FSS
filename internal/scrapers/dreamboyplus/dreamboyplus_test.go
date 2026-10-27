package dreamboyplus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
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

func row(t *testing.T, id string) SiteConfig {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			return c
		}
	}
	t.Fatalf("no row %s", id)
	return SiteConfig{}
}

// Each URL reaches exactly one row.
func TestMatchesURL(t *testing.T) {
	for u, want := range map[string]string{
		"https://www.dreamboybondage.com/":                                 "dreamboybondage",
		"https://dreamboyplus.com/ropedstuds/":                             "ropedstuds",
		"https://dreamboyplus.com/categories/movies.html?site=boundupboys": "boundupboys",
		"https://dreamboyplus.com/":                                        "dreamboyplus",
		"https://dreamboyplus.com/categories/movies.html":                  "dreamboyplus",
	} {
		var got []string
		for _, c := range sites {
			if New(c).MatchesURL(u) {
				got = append(got, c.SiteID)
			}
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s matched %v, want %s", u, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "listing.html"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards", len(cards))
	}
	c := cards[0]
	if c.id == "" || c.title == "" || !strings.HasPrefix(c.url, "https://dreamboyplus.com/trailers/") {
		t.Errorf("card = %+v", c)
	}
	if strings.Contains(strings.TrimPrefix(c.thumb, "https://"), "//") || !strings.HasPrefix(c.thumb, "https://dreamboyplus.com/content/") {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestApplyDetail(t *testing.T) {
	var got models.Scene
	applyDetail(&got, fixture(t, "detail.html"), true)
	if !got.Date.Equal(time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)) || got.Duration != 11*60+53 {
		t.Errorf("Date = %v, Duration = %d", got.Date, got.Duration)
	}
	if !strings.HasPrefix(got.Description, "“So hot…” Ty Roderick says") || got.Series != "Dream Boy Bondage" {
		t.Errorf("Description = %q, Series = %q", got.Description, got.Series)
	}
}

func TestRunWalksUntilAnEmptyPage(t *testing.T) {
	listing, detail := fixture(t, "listing.html"), fixture(t, "detail.html")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := func(s string) string { return strings.ReplaceAll(s, "https://dreamboyplus.com", srv.URL) }
		switch {
		case r.URL.Path == "/categories/movies_1.html" && r.URL.Query().Get("site") == "ropedstuds":
			_, _ = w.Write([]byte(rw(listing)))
		case strings.HasPrefix(r.URL.Path, "/categories/"):
			_, _ = w.Write([]byte("<html></html>"))
		case strings.HasPrefix(r.URL.Path, "/trailers/"):
			_, _ = w.Write([]byte(rw(detail)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	s := New(row(t, "ropedstuds"))
	s.Client = srv.Client()
	ch, _ := s.ListScenes(context.Background(), "https://www.ropedstuds.com/", scraper.ListOpts{})
	n := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			n++
			if r.Scene.Date.IsZero() || r.Scene.StudioURL != "https://www.ropedstuds.com/" || r.Scene.Studio != "Roped Studs" {
				t.Errorf("scene = %+v", r.Scene)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if n != 3 {
		t.Errorf("got %d scenes", n)
	}
}

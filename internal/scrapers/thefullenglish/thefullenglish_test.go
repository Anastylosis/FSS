package thefullenglish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.thefullenglish.net/updates", "http://thefullenglish.net"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	if s.MatchesURL("https://thefullenglish.com/") {
		t.Error("matched a foreign host")
	}
}

func TestParseListingSkipsPhotosets(t *testing.T) {
	cards, hasNext := parseListing(fixture(t, "listing_p1.html"))
	if !hasNext {
		t.Error("next link missed")
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2 (photoset skipped)", len(cards))
	}
	c := cards[0]
	if c.id != "780" || c.title != "Too Much Information" || c.date == "" {
		t.Errorf("card = %+v", c)
	}
	if !strings.Contains(c.thumb, "expires=") || strings.Contains(c.thumb, "&amp;") {
		t.Errorf("thumb = %q", c.thumb)
	}
	if !strings.HasSuffix(c.url, "/updates/Too-Much-Information") {
		t.Errorf("url = %q", c.url)
	}
}

func TestParseDetail(t *testing.T) {
	d := parseDetail(fixture(t, "detail.html"))
	if !strings.HasPrefix(d, "Jonah resents") || !strings.Contains(d, "\n") {
		t.Errorf("description = %q", d)
	}
}

func TestListScenes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/updates" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(fixture(t, "listing_p1.html"))
		case r.URL.Path == "/updates":
			_, _ = w.Write(fixture(t, "listing_p2.html"))
		case r.URL.Path == "/models":
			_, _ = w.Write(fixture(t, "models.html"))
		case strings.HasPrefix(r.URL.Path, "/models/"):
			_, _ = w.Write(fixture(t, "model_page.html"))
		case strings.HasPrefix(r.URL.Path, "/updates/"):
			_, _ = w.Write(fixture(t, "detail.html"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := siteBase
	siteBase = srv.URL
	defer func() { siteBase = old }()

	ch, err := New().ListScenes(context.Background(), "https://www.thefullenglish.net/updates", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	first := scenes[0]
	if first.Date.IsZero() || first.Thumbnail == "" || !strings.HasPrefix(first.Description, "Jonah resents") {
		t.Errorf("scene = %+v", first)
	}
	if strings.Join(first.Performers, ",") != "Adam,Aidan" {
		t.Errorf("performers = %v", first.Performers)
	}
}

func TestOnSiteBase(t *testing.T) {
	old := siteBase
	siteBase = "http://test"
	defer func() { siteBase = old }()
	for in, want := range map[string]string{
		"https://www.thefullenglish.net/updates/Too-Much-Information": "http://test/updates/Too-Much-Information",
		"https://www.thefullenglish.net":                              "https://www.thefullenglish.net",
		"://bad":                                                      "://bad",
	} {
		if got := onSiteBase(in); got != want {
			t.Errorf("onSiteBase(%q) = %q, want %q", in, got, want)
		}
	}
}

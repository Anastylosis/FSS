package alexsisfaye

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

func newTestServer(t *testing.T) *httptest.Server {
	listing, detail := fixture(t, "listing.html"), fixture(t, "detail.html")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(listing))
		case strings.HasPrefix(r.URL.Path, "/videos/"):
			_, _ = w.Write([]byte(detail))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "listing.html"))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	c := cards[0]
	if c.slug != "i-m-your-free-use-booby-bitch" || c.title != "I'm Your Free Use Booby Bitch" {
		t.Errorf("card = %+v", c)
	}
	if got := c.date.Format("2006-01-02"); got != "2026-10-14" {
		t.Errorf("date = %s", got)
	}
	if c.duration != 7*60+34 {
		t.Errorf("duration = %d", c.duration)
	}
	if c.thumb != "/media-public/previews/1465/bigthumb-2117-e69f3c0f00ba2b6b29f6c4928b3ddb24.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
	if cards[1].duration != 11*60+24 || cards[1].date.Day() != 11 {
		t.Errorf("second card = %+v", cards[1])
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := &Scraper{client: ts.Client(), base: ts.URL}

	studioURL := "https://alexsisfaye.com/videos"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes []scraper.SceneResult
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Fatalf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			scenes = append(scenes, r)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	sc := scenes[0].Scene
	if sc.ID != "i-m-your-free-use-booby-bitch" || sc.SiteID != siteID || sc.StudioURL != studioURL {
		t.Errorf("identity = %q %q %q", sc.ID, sc.SiteID, sc.StudioURL)
	}
	if sc.URL != ts.URL+"/videos/i-m-your-free-use-booby-bitch" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Title != "I'm Your Free Use Booby Bitch" || sc.Studio != "Alexsis Faye" {
		t.Errorf("title/studio = %q %q", sc.Title, sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Alexsis Faye" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Thumbnail != ts.URL+"/media-public/previews/1465/bigthumb-2117-e69f3c0f00ba2b6b29f6c4928b3ddb24.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if !strings.HasPrefix(sc.Description, "First I shake this juicy ass") || !strings.Contains(sc.Description, "it's your cock") {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt unset")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://alexsisfaye.com", "https://alexsisfaye.com/", "https://www.alexsisfaye.com/videos?page=2"} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{"https://notalexsisfaye.com/", "https://example.com/alexsisfaye.com"} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

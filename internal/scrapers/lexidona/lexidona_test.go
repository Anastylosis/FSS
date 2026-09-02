package lexidona

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://lexidona.com/", "https://www.lexidona.com/videos/", "http://lexidona.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://lexidona.net/", "https://notlexidona.com/", "https://example.com/lexidona.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page1.html"))
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if !strings.HasPrefix(first.id, "video-") || first.path != "/videos/"+first.id+"/" {
		t.Errorf("id/path = %q/%q", first.id, first.path)
	}
	if first.title == "" {
		t.Error("title missing")
	}
	if len(first.tags) == 0 {
		t.Error("tags missing")
	}
	if first.duration == 0 {
		t.Error("duration missing")
	}
	if !strings.HasPrefix(first.thumbnail, "https://") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
}

// A detail page's "next video" link has the same anchor shape as a card but no
// figcaption; reading it as a card would invent a scene.
func TestParseListingIgnoresNextVideoLinks(t *testing.T) {
	body := []byte(`<a href="/videos/video-double-piss/">Next video</a>`)
	if got := parseListing(body); len(got) != 0 {
		t.Errorf("parseListing = %+v, want none", got)
	}
}

// The card's <em> packs the tag list and the runtime into one element.
func TestParseCardMeta(t *testing.T) {
	tags, dur := parseCardMeta("Home,Shaved,Vaginal<br />04:15")
	if fmt.Sprint(tags) != fmt.Sprint([]string{"Home", "Shaved", "Vaginal"}) {
		t.Errorf("tags = %v", tags)
	}
	if dur != 4*60+15 {
		t.Errorf("duration = %d", dur)
	}

	// A card with only a runtime yields no tags, and one with only tags no
	// runtime.
	if tags, dur := parseCardMeta("<br />1:02:03"); len(tags) != 0 || dur != 3723 {
		t.Errorf("tags/dur = %v/%d", tags, dur)
	}
	if tags, dur := parseCardMeta("Solo"); len(tags) != 1 || dur != 0 {
		t.Errorf("tags/dur = %v/%d", tags, dur)
	}
}

func TestEnrichFromDetail(t *testing.T) {
	item := listItem{duration: 1}
	enrichFromDetail(readFixture(t, "detail.html"), &item)
	if item.date != "February 27 2019" {
		t.Errorf("date = %q", item.date)
	}
	if item.duration != 4*60+15 {
		t.Errorf("duration = %d", item.duration)
	}
	if !strings.HasPrefix(item.description, "I love getting fucked") {
		t.Errorf("description = %q", item.description)
	}
	if !strings.HasSuffix(item.preview, ".mp4") {
		t.Errorf("preview = %q", item.preview)
	}
	// The detail's poster is a larger still than the card's.
	if !strings.HasSuffix(item.thumbnail, "hd.jpg") {
		t.Errorf("thumbnail = %q", item.thumbnail)
	}
}

func TestMaxPage(t *testing.T) {
	if got := maxPage(readFixture(t, "listing_page1.html")); got < 2 {
		t.Errorf("maxPage = %d", got)
	}
	if got := maxPage([]byte("<html></html>")); got != 0 {
		t.Errorf("maxPage = %d, want 0", got)
	}
}

func TestToScene(t *testing.T) {
	s := New()
	item := listItem{id: "video-doggiefuck", path: "/videos/video-doggiefuck/", title: "DoggieFuck", date: "February 27 2019", duration: 255}
	sc := s.toScene(item, "https://lexidona.com/", time.Now().UTC())
	if sc.URL != "https://lexidona.com/videos/video-doggiefuck/" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Date.Format("2006-01-02") != "2019-02-27" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}

	item.date = "not a date"
	if got := s.toScene(item, "https://lexidona.com/", time.Now().UTC()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

func TestListScenes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos/":
			_, _ = w.Write(readFixture(t, "listing_page1.html"))
		case strings.HasPrefix(r.URL.Path, "/videos/video-"):
			_, _ = w.Write(readFixture(t, "detail.html"))
		default:
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://lexidona.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.Description == "" || sc.Date.IsZero() {
			t.Errorf("detail enrichment missing on %s", sc.ID)
		}
		if !strings.HasPrefix(sc.URL, srv.URL) {
			t.Errorf("URL = %q, want it under the test server", sc.URL)
		}
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/videos/video-") {
			_, _ = w.Write(readFixture(t, "detail.html"))
			return
		}
		_, _ = w.Write(readFixture(t, "listing_page1.html"))
	}))
	defer srv.Close()

	known := parseListing(readFixture(t, "listing_page1.html"))[1].id
	s := New()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), "https://lexidona.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{known: true}})
	scenes, stopped := 0, false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected a StoppedEarly result")
	}
	if scenes != 1 {
		t.Errorf("scenes = %d, want 1", scenes)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

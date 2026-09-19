package famegirls

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://famegirls.net/", "http://www.famegirls.net", "https://famegirls.net/videos/1785/diana-video-342/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://famegirls.com/", "https://example.com/famegirls.net", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

const detailPage = `<html><head>
<meta property="og:title" content="Diana video 342 &amp; friends">
<meta property="og:description" content="Diana 342 in a short sexy dress">
<meta property="og:image" content="https://famegirls.net/contents/videos_screenshots/1785/preview.jpg">
<meta property="video:release_date" content="2026-06-19T00:00:00Z">
<meta property="video:duration" content="1745">
</head><body>
<a href="https://famegirls.net/models/diana/">Diana</a>
<a href="https://famegirls.net/models/diana/">Diana again</a>
</body></html>`

func TestParseScene(t *testing.T) {
	scene, err := parseScene(detailPage, sceneRef{id: "1785", slug: "diana-video-342"}, "https://famegirls.net/videos/", time.Now().UTC())
	if err != nil {
		t.Fatalf("parseScene: %v", err)
	}
	if scene.ID != "1785" || scene.SiteID != siteID || scene.Studio != studioName {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", scene.ID, scene.SiteID, scene.Studio)
	}
	if scene.Title != "Diana video 342 & friends" {
		t.Errorf("Title = %q", scene.Title)
	}
	// The OpenGraph block is the only precise date and runtime on the page.
	if got := scene.Date.Format("2006-01-02"); got != "2026-06-19" {
		t.Errorf("Date = %s", got)
	}
	if scene.Duration != 1745 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if strings.Join(scene.Performers, "|") != "Diana" {
		t.Errorf("Performers = %v, want one de-duplicated name", scene.Performers)
	}
	if scene.Description == "" || scene.Thumbnail == "" {
		t.Errorf("Description/Thumbnail = %q/%q", scene.Description, scene.Thumbnail)
	}
}

func TestTitleCase(t *testing.T) {
	if got := titleCase("mary jane"); got != "Mary Jane" {
		t.Errorf("titleCase = %q", got)
	}
	if got := titleCase(""); got != "" {
		t.Errorf("titleCase('') = %q", got)
	}
}

func TestParseSceneWithoutTitleIsAnError(t *testing.T) {
	if _, err := parseScene("<html></html>", sceneRef{id: "1"}, "u", time.Now()); err == nil {
		t.Error("want an error when og:title is gone")
	}
}

func TestListScenesWalksUntilEmpty(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/videos/1785/"), strings.HasPrefix(r.URL.Path, "/videos/1786/"):
			_, _ = fmt.Fprint(w, detailPage)
		case strings.HasPrefix(r.URL.Path, "/videos/"):
			pages++
			switch r.URL.Path {
			case "/videos/1/":
				_, _ = fmt.Fprint(w, `<a href="/videos/1785/diana-video-342/">a</a><a href="/videos/1786/diana-video-343/">b</a>`)
			default:
				_, _ = fmt.Fprint(w, `<html>no videos</html>`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/videos/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var ids []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
			if !strings.HasPrefix(r.Scene.URL, siteBase+"/videos/") {
				t.Errorf("scene URL = %q, want a %s address", r.Scene.URL, siteBase)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("got %d scenes (%v), want 2", len(ids), ids)
	}
	if pages != 2 {
		t.Errorf("fetched %d listing pages, want 2", pages)
	}
}

// A model URL scrapes only that model's listing.
func TestModelURLScrapesTheModelListing(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/videos/1785/") {
			_, _ = fmt.Fprint(w, detailPage)
			return
		}
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/models/diana/1/" {
			_, _ = fmt.Fprint(w, `<a href="/videos/1785/diana-video-342/">a</a>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html></html>`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL+"/models/diana/", scraper.ListOpts{})
	var n int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d scenes, want 1", n)
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "/videos/1/") {
			t.Error("a model URL must not fall back to the full catalogue")
		}
	}
}

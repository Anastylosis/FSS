package puzzyfun

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
	for _, u := range []string{"https://www.puzzyfun.com/", "http://puzzyfun.com", "https://www.puzzyfun.com/collections"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://puzzyfun.net/", "https://example.com/puzzyfun.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The player markup links the same scene by a 32-hex internal id, and every
// card also carries a /buy variant; neither is a distinct scene.
func TestIsHash(t *testing.T) {
	if !isHash("1c86e111e85953d6dced1d1b1f629195") {
		t.Error("a 32-hex id must be recognised")
	}
	for _, s := range []string{"a-hot-party", "short", strings.Repeat("z", 32)} {
		if isHash(s) {
			t.Errorf("isHash(%q) = true", s)
		}
	}
}

const detailPage = `<html><head>
<meta property="og:title" content="A hot party &amp; more">
<meta property="og:description" content="Yeah, this is a real highlight.">
<meta property="og:image" content="https://files6.shopmaker.com/collections/x/lg-0.jpg">
</head><body>
<span>2026-08-18</span> &minus; <a href="/models/julia-maze">Julia Maze</a> &minus; <span>34:28 minutes</span>
<a href="/collections?tag=creampie">Creampie</a>
<a href="/collections?tag=old+men+fuck+pornstar">Old men</a>
</body></html>`

func TestParseScene(t *testing.T) {
	scene, err := parseScene(detailPage, "a-hot-party", "https://www.puzzyfun.com/collections", time.Now().UTC())
	if err != nil {
		t.Fatalf("parseScene: %v", err)
	}
	if scene.ID != "a-hot-party" || scene.SiteID != siteID {
		t.Errorf("ID/SiteID = %q/%q", scene.ID, scene.SiteID)
	}
	if scene.Title != "A hot party & more" {
		t.Errorf("Title = %q", scene.Title)
	}
	if got := scene.Date.Format("2006-01-02"); got != "2026-08-18" {
		t.Errorf("Date = %s", got)
	}
	if scene.Duration != 2068 {
		t.Errorf("Duration = %d, want 2068", scene.Duration)
	}
	if strings.Join(scene.Performers, "|") != "Julia Maze" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if strings.Join(scene.Tags, "|") != "creampie|old men fuck pornstar" {
		t.Errorf("Tags = %v", scene.Tags)
	}
}

func TestParseSceneWithoutTitleIsAnError(t *testing.T) {
	if _, err := parseScene("<html></html>", "x", "u", time.Now()); err == nil {
		t.Error("want an error when og:title is gone")
	}
}

func TestListScenesSkipsBuyAndHashLinks(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/collections/a-hot-party", "/collections/another-scene":
			_, _ = fmt.Fprint(w, detailPage)
		case "/collections":
			pages++
			_, _ = fmt.Fprint(w, `
			<a href="/collections/a-hot-party">x</a>
			<a href="/collections/a-hot-party/buy">buy</a>
			<a href="/collections/1c86e111e85953d6dced1d1b1f629195">player id</a>
			<a href="/collections/another-scene">y</a>`)
		default:
			pages++
			_, _ = fmt.Fprint(w, `<html>no collections</html>`)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/collections", scraper.ListOpts{Workers: 2})
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
		}
	}
	if len(ids) != 2 {
		t.Fatalf("got %d scenes (%v), want 2 — the /buy and hashed links are the same scenes", len(ids), ids)
	}
	if pages != 2 {
		t.Errorf("fetched %d listing pages, want 2", pages)
	}
}

package inkasex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.inkasex.com/",
		"https://inkasex.com/videos/latest",
		"https://en.inkasex.com/videos/latest",
		"https://www.inkasex.com/videos/category/anal",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://inkasexy.com/", "https://example.com/inkasex.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The PlayTube slug is title-plus-id; only the id survives a rename.
func TestSceneID(t *testing.T) {
	cases := map[string]string{
		"debate-sexual_uzMuM7SKt9TWqIo": "uzMuM7SKt9TWqIo",
		"no-underscore":                 "no-underscore",
		"trailing_":                     "trailing_",
	}
	for in, want := range cases {
		if got := sceneID(in); got != want {
			t.Errorf("sceneID(%q) = %q, want %q", in, got, want)
		}
	}
}

// Category slugs separate words with underscores: reading them as plain words
// filed "doggy_style" as "doggy" and "reverse_cowgirl" as "reverse".
func TestCategoriesKeepWholeSlugs(t *testing.T) {
	body := `<a href="https://www.inkasex.com/videos/category/doggy_style">x</a>
	<a href="https://www.inkasex.com/videos/category/reverse_cowgirl">y</a>
	<a href="https://www.inkasex.com/videos/category/anal">z</a>
	<a href="https://www.inkasex.com/videos/category/anal">dupe</a>
	<a href="https://www.inkasex.com/videos/category/1040">numeric id, skipped</a>`
	got := strings.Join(categories(body), "|")
	if got != "doggy style|reverse cowgirl|anal" {
		t.Errorf("categories = %q", got)
	}
}

const detailPage = `<html><head>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoObject",
 "name":"Debate Sexual","description":"Una descripción.",
 "thumbnailUrl":["https://www.inkasex.com/upload2/photos/a.jpg"],
 "uploadDate":"2026-06-01T14:28:39+00:00","duration":"PT30M38S",
 "actor":[{"@type":"Person","name":"Lisa Bullock"}]}</script>
</head><body>
<a href="https://www.inkasex.com/videos/category/anal">Anal</a>
</body></html>`

func TestToScene(t *testing.T) {
	vo := parseutil.ExtractVideoObject([]byte(detailPage))
	if vo == nil {
		t.Fatal("no VideoObject")
	}
	scene := toScene(*vo, "debate-sexual_uzMuM7SKt9TWqIo", "https://www.inkasex.com/videos/latest", detailPage, time.Now().UTC())
	if scene.ID != "uzMuM7SKt9TWqIo" || scene.Title != "Debate Sexual" {
		t.Errorf("ID/Title = %q/%q", scene.ID, scene.Title)
	}
	if scene.Duration != 1838 {
		t.Errorf("Duration = %d, want 1838", scene.Duration)
	}
	if scene.Date.Format("2006-01-02") != "2026-06-01" {
		t.Errorf("Date = %v", scene.Date)
	}
	if strings.Join(scene.Performers, "|") != "Lisa Bullock" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if strings.Join(scene.Categories, "|") != "anal" {
		t.Errorf("Categories = %v", scene.Categories)
	}
	// thumbnailUrl is published as an array here; the first entry is kept.
	if scene.Thumbnail != "https://www.inkasex.com/upload2/photos/a.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
}

func TestListScenesPaginatesAndStopsOnEmpty(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = fmt.Fprint(w, detailPage)
		case r.URL.Path == "/videos/latest":
			pages++
			switch r.URL.Query().Get("page_id") {
			case "1":
				_, _ = fmt.Fprint(w, `<a href="https://www.inkasex.com/video/one_AAA.html">1</a><a href="/video/two_BBB.html">2</a>`)
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

	ch, err := s.ListScenes(context.Background(), srv.URL+"/videos/latest", scraper.ListOpts{Workers: 2})
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
			if !strings.HasPrefix(r.Scene.URL, siteBase+"/video/") {
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

func TestCategoryURLScrapesThatCategory(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/video/") {
			_, _ = fmt.Fprint(w, detailPage)
			return
		}
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/videos/category/anal" && r.URL.Query().Get("page_id") == "1" {
			_, _ = fmt.Fprint(w, `<a href="/video/only_ZZZ.html">x</a>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html></html>`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/videos/category/anal", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
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
		if p == "/videos/latest" {
			t.Error("a category URL must not fall back to the full catalogue")
		}
	}
}

func TestMissingVideoObjectIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/video/") {
			_, _ = fmt.Fprint(w, `<html><body>gone</body></html>`)
			return
		}
		if r.URL.Query().Get("page_id") == "1" {
			_, _ = fmt.Fprint(w, `<a href="/video/broken_AAA.html">x</a>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html></html>`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL+"/videos/latest", scraper.ListOpts{Workers: 1})
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if kind := scraper.Classify(r.Err); kind != scraper.FailureParse {
				t.Errorf("failure kind = %v, want FailureParse", kind)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

package enjoyx

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
		"https://enjoyx.com",
		"https://enjoyx.com/video",
		"https://www.enjoyx.com/video?page=3",
		"http://enjoyx.com/model/jadilica",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://enjoyxx.com/", "https://example.com/enjoyx.com", "", "https://notenjoyx.com"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The sort tabs live at the same path depth as scenes, so a naive link scrape
// files /video/best as a scene and then 13 duplicates of it per page.
func TestFetchSlugsSkipsSortTabsAndDuplicates(t *testing.T) {
	page := `<a href="/video/best">Best</a>
	<a href="https://enjoyx.com/video/new">New</a>
	<a href="https://enjoyx.com/video/first-scene"><img></a>
	<a href="/video/first-scene">same scene twice</a>
	<a href="https://enjoyx.com/video/second-scene"></a>
	<a href="https://enjoyx.com/model/someone"></a>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	got, err := s.fetchSlugs(context.Background(), srv.URL+"/video?page=1")
	if err != nil {
		t.Fatalf("fetchSlugs: %v", err)
	}
	want := []string{"first-scene", "second-scene"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("slugs = %v, want %v", got, want)
	}
}

const detailJSONLD = `<html><head><script type="application/ld+json">
{"@context":"https://schema.org","@type":"VideoObject",
 "url":"https://enjoyx.com/video/jadilica-double-sin",
 "name":"Jadilica: Double sin",
 "description":"A premium XXX video.",
 "genre":["BBG","Blowjob","Threesome"],
 "thumbnailUrl":"https://cdn.example.com/poster.jpg",
 "actor":[{"@type":"Person","name":"Jadilica"},{"@type":"Person","name":"Jimmy Bud"}],
 "keywords":["Jadilica","Jimmy Bud","BBG","Blowjob"],
 "uploadDate":"2026-07-04T02:28:28+00:00",
 "duration":"PT12M16S"}
</script></head><body></body></html>`

func TestToSceneFromVideoObject(t *testing.T) {
	vo := parseutil.ExtractVideoObject([]byte(detailJSONLD))
	if vo == nil {
		t.Fatal("no VideoObject extracted")
	}
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	scene := toScene(*vo, "jadilica-double-sin", "https://enjoyx.com/video", now)

	if scene.ID != "jadilica-double-sin" || scene.SiteID != siteID {
		t.Errorf("ID/SiteID = %q/%q", scene.ID, scene.SiteID)
	}
	if scene.Title != "Jadilica: Double sin" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 736 {
		t.Errorf("Duration = %d, want 736", scene.Duration)
	}
	if scene.Date.Format("2006-01-02") != "2026-07-04" {
		t.Errorf("Date = %v", scene.Date)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Jadilica" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	// genre[] is the tag taxonomy; keywords[] repeats the performers.
	if strings.Join(scene.Tags, ",") != "BBG,Blowjob,Threesome" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Thumbnail != "https://cdn.example.com/poster.jpg" || scene.Studio != studioName {
		t.Errorf("Thumbnail/Studio = %q/%q", scene.Thumbnail, scene.Studio)
	}
}

// Without genre[], the comma-joined keywords are the only tags on offer.
func TestTagsFallBackToKeywords(t *testing.T) {
	vo := parseutil.VideoObject{Keywords: "Solo, Toys , "}
	got := tagsFor(vo)
	if strings.Join(got, "|") != "Solo|Toys" {
		t.Errorf("tagsFor = %v", got)
	}
}

func TestListScenesWalksPagesAndStopsOnAnEmptyOne(t *testing.T) {
	var listPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/video/"):
			_, _ = fmt.Fprint(w, strings.ReplaceAll(detailJSONLD, "jadilica-double-sin", strings.TrimPrefix(r.URL.Path, "/video/")))
		case r.URL.Path == "/video":
			listPages++
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = fmt.Fprint(w, `<a href="/video/one"></a><a href="/video/two"></a>`)
			case "2":
				_, _ = fmt.Fprint(w, `<a href="/video/three"></a>`)
			default:
				_, _ = fmt.Fprint(w, `<a href="/video/best"></a>`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/video", scraper.ListOpts{Workers: 2})
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
			// The stored URL must be the site's own, never the test server's.
			if !strings.HasPrefix(r.Scene.URL, siteBase+"/video/") {
				t.Errorf("scene URL = %q, want a %s address", r.Scene.URL, siteBase)
			}
		}
	}
	if len(ids) != 3 {
		t.Fatalf("got %d scenes (%v), want 3", len(ids), ids)
	}
	if listPages != 3 {
		t.Errorf("fetched %d listing pages, want 3 (the third is the empty one that stops the walk)", listPages)
	}
}

// A model page is the same card markup one path over, so it must paginate
// through /model/{slug} rather than falling back to the whole catalogue.
func TestModelURLScrapesTheModelListing(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/video/") {
			_, _ = fmt.Fprint(w, detailJSONLD)
			return
		}
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/model/jadilica" && r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, `<a href="/video/only-scene"></a>`)
			return
		}
		_, _ = fmt.Fprint(w, `<html></html>`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/model/jadilica", scraper.ListOpts{})
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
		if p == "/video" {
			t.Error("a model URL must not fall through to the full catalogue")
		}
	}
}

// A detail page without the JSON-LD block is a parse failure, not an empty
// scene: reported as such, and the rest of the page still yields its scenes.
func TestDetailWithoutVideoObjectIsReportedAndSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/video/broken":
			_, _ = fmt.Fprint(w, `<html><body>no json-ld here</body></html>`)
		case "/video/fine":
			_, _ = fmt.Fprint(w, detailJSONLD)
		case "/video":
			if r.URL.Query().Get("page") == "1" {
				_, _ = fmt.Fprint(w, `<a href="/video/broken"></a><a href="/video/fine"></a>`)
				return
			}
			_, _ = fmt.Fprint(w, `<html></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/video", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes, errs int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
			if kind := scraper.Classify(r.Err); kind != scraper.FailureParse {
				t.Errorf("failure kind = %v, want FailureParse", kind)
			}
		}
	}
	if scenes != 1 || errs != 1 {
		t.Errorf("got %d scenes and %d errors, want 1 and 1", scenes, errs)
	}
}

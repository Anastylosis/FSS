package riggsfilms

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

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseCastIgnoresRelatedScenes(t *testing.T) {
	got := parseCast(fixture(t, "scene.html"))
	if len(got) != 2 || got[0] != "Olive May" || got[1] != "Brock Cooper" {
		t.Errorf("cast = %v", got)
	}
}

func serve(t *testing.T) *Scraper {
	t.Helper()
	vs, scene, perf := fixture(t, "video-sitemap.xml"), fixture(t, "scene.html"), fixture(t, "performer.html")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := func(s string) string { return strings.ReplaceAll(s, "https://riggsfilms.com", srv.URL) }
		switch {
		case r.URL.Path == "/video-sitemap.xml":
			_, _ = w.Write([]byte(rw(vs)))
		case r.URL.Path == "/pornstars/olive-may/":
			_, _ = w.Write([]byte(rw(perf)))
		case strings.HasPrefix(r.URL.Path, "/scenes/olive-may"):
			_, _ = w.Write([]byte(rw(scene)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s := New()
	s.base = srv.URL
	s.client = srv.Client()
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) (map[string]bool, int, bool) {
	t.Helper()
	ch, _ := s.ListScenes(context.Background(), studioURL, opts)
	ids := map[string]bool{}
	errs, stopped := 0, false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids[r.Scene.ID] = true
			if r.Scene.StudioURL != studioURL {
				t.Errorf("StudioURL = %q", r.Scene.StudioURL)
			}
			if r.Scene.ID == "olive-may-gets-a-mouth-full-of-cum" {
				sc := r.Scene
				if sc.Title != "Olive May Gets a Mouth Full Of Cum" || sc.Duration != 1206 || len(sc.Performers) != 2 || len(sc.Tags) == 0 {
					t.Errorf("scene = %+v", sc)
				}
				if !sc.Date.Equal(time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)) {
					t.Errorf("Date = %v", sc.Date)
				}
			}
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return ids, errs, stopped
}

// The second sitemap scene's page 404s: reported, and the scene still
// emitted from the sitemap's own fields.
func TestRunFromVideoSitemap(t *testing.T) {
	ids, errs, _ := collect(t, serve(t), "https://riggsfilms.com", scraper.ListOpts{})
	if len(ids) != 2 || errs != 1 {
		t.Errorf("ids=%v errs=%d", ids, errs)
	}
}

func TestPerformerURLFilters(t *testing.T) {
	ids, _, _ := collect(t, serve(t), "https://riggsfilms.com/pornstars/olive-may/", scraper.ListOpts{})
	if len(ids) != 1 || !ids["olive-may-gets-a-mouth-full-of-cum"] {
		t.Errorf("ids=%v", ids)
	}
}

func TestKnownScenesAreSkipped(t *testing.T) {
	ids, _, stopped := collect(t, serve(t), "https://riggsfilms.com", scraper.ListOpts{KnownIDs: map[string]bool{"katija-cortez-coco-bae-fucking-each-other-with-a-dildo": true}})
	if len(ids) != 1 || !stopped {
		t.Errorf("ids=%v stopped=%v", ids, stopped)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://riggsfilms.com/pornstars/coco-bae/") || s.MatchesURL("https://riggsfilms.vip/") {
		t.Error("MatchesURL")
	}
}

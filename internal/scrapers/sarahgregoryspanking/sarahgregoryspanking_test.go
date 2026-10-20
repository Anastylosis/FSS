package sarahgregoryspanking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	p1, p2, join := fixture(t, "tour.html"), fixture(t, "tour2.html"), fixture(t, "join.html")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/tour.html":
			_, _ = w.Write(p1)
		case "/tour2.html":
			_, _ = w.Write(p2)
		case "/join.html":
			_, _ = w.Write(join)
		default:
			http.Redirect(w, r, "/join.html", http.StatusFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) ([]models.Scene, []scraper.SceneResult) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var others []scraper.SceneResult
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes = append(scenes, r.Scene)
		} else {
			others = append(others, r)
		}
	}
	return scenes, others
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.sarahgregoryspanking.com/",
		"https://sarahgregoryspanking.com",
		"http://www.sarahgregoryspanking.com/tour.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://sarahgregoryspanking.com.evil.example/",
		"https://notsarahgregoryspanking.com/",
		"https://www.sarahgregorypass.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestListScenes(t *testing.T) {
	srv, _ := newTestServer(t)
	s := New()
	s.base = srv.URL
	const studio = "https://www.sarahgregoryspanking.com/"
	scenes, others := collect(t, s, studio, scraper.ListOpts{})
	for _, r := range others {
		if r.Kind == scraper.KindError {
			t.Fatalf("unexpected error: %v", r.Err)
		}
	}
	// 2 on page 1, 3 on page 2 of which one repeats page 1; page 3 redirects
	// to /join.html and ends the walk.
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	first := scenes[0]
	if first.ID != "caning-competition-part-two" || first.Title != "Caning Competition Part Two" {
		t.Errorf("first scene = %q / %q", first.ID, first.Title)
	}
	if !strings.HasPrefix(first.Description, "In this exclusive custom feature, Alana Noex and Lily Swan — two") {
		t.Errorf("description = %q", first.Description)
	}
	if first.Thumbnail != srv.URL+"/new_updates/caning-competition-part-two-bg.jpg" {
		t.Errorf("thumbnail = %q", first.Thumbnail)
	}
	if first.URL != srv.URL+"/tour.html#caning-competition-part-two" {
		t.Errorf("url = %q", first.URL)
	}
	if scenes[1].Title != "Sarah's Triple Spanking" || scenes[1].Description != "" {
		t.Errorf("second scene = %q / %q", scenes[1].Title, scenes[1].Description)
	}
	old := scenes[2]
	if old.ID != "black-and-blues" || old.Thumbnail != srv.URL+"/new_updates/black-and-blues-2.jpg" {
		t.Errorf("old-layout scene = %q / %q", old.ID, old.Thumbnail)
	}
	if !strings.Contains(old.Description, `"they didn't work!"`) {
		t.Errorf("entities not unescaped: %q", old.Description)
	}
	for _, sc := range scenes {
		if sc.StudioURL != studio || sc.SiteID != siteID || sc.Studio != studioName || sc.ScrapedAt.IsZero() {
			t.Errorf("bad stamping on %q: %+v", sc.ID, sc)
		}
	}
}

func TestKnownIDsStopsAfterPage(t *testing.T) {
	srv, hits := newTestServer(t)
	s := New()
	s.base = srv.URL
	scenes, others := collect(t, s, "https://www.sarahgregoryspanking.com/", scraper.ListOpts{
		KnownIDs: map[string]bool{"caning-competition-part-two": true},
	})
	if len(scenes) != 1 || scenes[0].ID != "sarahs-triple-spanking" {
		t.Fatalf("got %+v", scenes)
	}
	stopped := false
	for _, r := range others {
		if r.Kind == scraper.KindStoppedEarly {
			stopped = true
		}
	}
	if !stopped || hits.Load() != 1 {
		t.Errorf("stopped=%v hits=%d, want early stop after one page", stopped, hits.Load())
	}
}

func TestEmptyFirstPageIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer srv.Close()
	s := New()
	s.base = srv.URL
	scenes, others := collect(t, s, "https://www.sarahgregoryspanking.com/", scraper.ListOpts{})
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	var gotErr error
	for _, r := range others {
		if r.Kind == scraper.KindError {
			gotErr = r.Err
		}
	}
	if gotErr == nil || scraper.Classify(gotErr) != scraper.FailureParse {
		t.Errorf("want parse error, got %v", gotErr)
	}
}

func TestSceneID(t *testing.T) {
	cases := []struct{ img, title, want string }{
		{"/new_updates/caning-competition-part-two-new1.jpg", "x", "caning-competition-part-two"},
		{"/new_updates/the-cheat-2.jpg", "x", "the-cheat"},
		{"/new_updates/odd.jpg", "Sarah's Big Day!", "sarah-s-big-day"},
		{"", "Bad Mother", "bad-mother"},
	}
	for _, c := range cases {
		if got := sceneID(c.img, c.title); got != c.want {
			t.Errorf("sceneID(%q,%q) = %q, want %q", c.img, c.title, got, c.want)
		}
	}
}

package concoxxxion

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://hornyhousehold.com/", true},
		{"https://hornyhousehold.com/watch-newest-clips-and-scenes.html?page=2", true},
		{"https://www.conorcoxxx.com/conor-coxxx-clips.html", true},
		{"https://conorcoxxx.com/horny-household-clips.html", true},
		{"https://spankmonster.com/", false},
		{"https://hornyhousehold.com.evil.net/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestListingFor(t *testing.T) {
	cases := []struct{ in, base, path string }{
		{"https://hornyhousehold.com/", "https://hornyhousehold.com", newestPath},
		{"https://hornyhousehold.com/watch-newest-clips-and-scenes.html", "https://hornyhousehold.com", newestPath},
		{"https://www.conorcoxxx.com", "https://www.conorcoxxx.com", "/conor-coxxx-clips.html"},
		{"https://www.conorcoxxx.com/horny-household-clips.html?page=3", "https://www.conorcoxxx.com", "/horny-household-clips.html"},
		{"https://conorcoxxx.com/watch-newest-clips-and-scenes.html", "https://conorcoxxx.com", newestPath},
	}
	for _, c := range cases {
		base, path := listingFor(c.in)
		if base != c.base || path != c.path {
			t.Errorf("listingFor(%q) = %q, %q; want %q, %q", c.in, base, path, c.base, c.path)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(readFixture(t, "listing_hh.html"), "https://hornyhousehold.com")
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	got := items[0]
	if got.id != "1804879" {
		t.Errorf("id = %q", got.id)
	}
	if got.url != "https://hornyhousehold.com/1804879/horny-household-swinger-stepsister-secret-takes-her-stepbrothers-huge-cock-streaming-scene-video.html" {
		t.Errorf("url = %q", got.url)
	}
	if got.title != "Swinger Stepsister Secret Takes Her StepBrother's Huge Cock" {
		t.Errorf("title = %q", got.title)
	}
	if !slices.Equal(got.performers, []string{"Nickey Huntsman"}) {
		t.Errorf("performers = %v", got.performers)
	}
	if got.minutes != 44 {
		t.Errorf("minutes = %d", got.minutes)
	}
	if got.thumb != "https://imgs4cdn.adultempire.com/frame/5136199?w=700&tc=1120" {
		t.Errorf("thumb = %q", got.thumb)
	}

	cc := parseListing(readFixture(t, "listing_cc.html"), "https://www.conorcoxxx.com")
	if len(cc) != 3 || cc[0].id != "1802241" || !strings.Contains(cc[0].url, "/concoxxxion-") {
		t.Errorf("conor listing parsed wrong: %+v", cc)
	}
}

func TestParseListingEmpty(t *testing.T) {
	if items := parseListing([]byte(`<html></html>`), "x"); len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

func TestApplyDetail(t *testing.T) {
	scene := models.Scene{Studio: defaultStudio}
	applyDetail(&scene, string(readFixture(t, "detail.html")))

	if scene.Title != "Swinger Stepsister Secret Takes Her StepBrother's Huge Cock" {
		t.Errorf("Title = %q", scene.Title)
	}
	if want := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC); !scene.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", scene.Date, want)
	}
	if scene.Duration != 44*60 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if scene.Director != "Conor Coxxx" {
		t.Errorf("Director = %q", scene.Director)
	}
	if scene.Studio != "Horny Household" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if !strings.HasPrefix(scene.Description, "The scene opens with Nickey naked") {
		t.Errorf("Description = %q", scene.Description)
	}
	if !slices.Equal(scene.Performers, []string{"Nickey Huntsman"}) {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if !slices.Contains(scene.Tags, "Big Boobs") || !slices.Contains(scene.Tags, "Rimming") {
		t.Errorf("Tags = %v", scene.Tags)
	}
}

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"\n\t\t\tNickey Huntsman\n\t": "Nickey Huntsman",
		"A &amp; B":                   "A & B",
		"":                            "",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- end-to-end ----

func serve(t *testing.T, listing string, paths *[]string) *httptest.Server {
	t.Helper()
	list := readFixture(t, listing)
	detail := readFixture(t, "detail.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("ageConfirmed"); err != nil || c.Value != "true" {
			http.Redirect(w, r, "/AgeConfirmation", http.StatusFound)
			return
		}
		if strings.HasSuffix(r.URL.Path, "-streaming-scene-video.html") {
			_, _ = w.Write(detail)
			return
		}
		if paths != nil {
			*paths = append(*paths, r.URL.Path)
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write(list)
			return
		}
		_, _ = fmt.Fprint(w, `<html><body></body></html>`)
	}))
	t.Cleanup(srv.Close)
	orig := siteBase
	siteBase = srv.URL
	t.Cleanup(func() { siteBase = orig })
	return srv
}

func TestListScenesHornyHousehold(t *testing.T) {
	var paths []string
	srv := serve(t, "listing_hh.html", &paths)
	s := New()
	s.Client = srv.Client()

	studioURL := "https://hornyhousehold.com/"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if paths[0] != newestPath {
		t.Errorf("listing path = %q", paths[0])
	}
	sc := scenes[0]
	if sc.ID != "1804879" || sc.SiteID != siteID || sc.StudioURL != studioURL || sc.Studio != "Horny Household" {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.URL, srv.URL+"/1804879/") {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Date.IsZero() || sc.Duration != 44*60 || sc.Description == "" || len(sc.Tags) == 0 || sc.Thumbnail == "" || sc.ScrapedAt.IsZero() {
		t.Errorf("scene incomplete: %+v", sc)
	}
}

func TestListScenesConorChannelPaths(t *testing.T) {
	for _, path := range []string{"/conor-coxxx-clips.html", "/horny-household-clips.html", ""} {
		var paths []string
		srv := serve(t, "listing_cc.html", &paths)
		s := New()
		s.Client = srv.Client()

		studioURL := "https://www.conorcoxxx.com" + path
		ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
		if err != nil {
			t.Fatal(err)
		}
		scenes := testutil.CollectScenes(t, ch)
		if len(scenes) != 3 || scenes[0].StudioURL != studioURL {
			t.Fatalf("%q: got %d scenes", path, len(scenes))
		}
		want := path
		if want == "" {
			want = "/conor-coxxx-clips.html"
		}
		if paths[0] != want {
			t.Errorf("%q: listing path = %q, want %q", path, paths[0], want)
		}
	}
}

func TestDetailFailureIsNotFatal(t *testing.T) {
	list := readFixture(t, "listing_hh.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "-streaming-scene-video.html") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write(list)
			return
		}
		_, _ = fmt.Fprint(w, `<html></html>`)
	}))
	defer srv.Close()
	orig := siteBase
	siteBase = srv.URL
	defer func() { siteBase = orig }()

	s := New()
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), "https://hornyhousehold.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	for _, sc := range scenes {
		if sc.Title == "" || len(sc.Performers) == 0 || sc.Duration == 0 {
			t.Errorf("scene %s lost its card data", sc.ID)
		}
	}
}

func TestContextCancellation(t *testing.T) {
	srv := serve(t, "listing_hh.html", nil)
	s := New()
	s.Client = srv.Client()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, "https://hornyhousehold.com/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("channel did not close after context cancellation")
	}
}

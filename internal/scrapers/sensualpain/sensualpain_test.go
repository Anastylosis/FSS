package sensualpain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	tracks := fixture(t, "tracks.html")
	media := fixture(t, "media.html")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/tracks.php":
			_, _ = w.Write(tracks)
		case r.URL.Path == "/media.php" && r.URL.Query().Get("id") == "paintoy-casey-cane-ass-caning":
			_, _ = w.Write(media)
		default:
			http.NotFound(w, r)
		}
	}))
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
		"https://sensualpain.com",
		"https://sensualpain.com/",
		"https://www.sensualpain.com/tracks.php",
		"http://sensualpain.com/media.php?id=x",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://notsensualpain.com/",
		"https://sensualpain.com.evil.example/",
		"https://example.com/sensualpain.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(fixture(t, "tracks.html"))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0].id != "0668" || items[0].title != "Paintoy Casey Cane Ass Caning" || items[0].duration != 1213 {
		t.Errorf("item 0 = %+v", items[0])
	}
	if got := items[0].date.Format("2006-01-02"); got != "2026-09-13" {
		t.Errorf("date = %s", got)
	}
	if items[1].duration != 5182 {
		t.Errorf("hour-long duration = %d, want 5182", items[1].duration)
	}
	if items[2].duration != 0 {
		t.Errorf("missing duration = %d, want 0", items[2].duration)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Paintoy Casey Cane Ass Caning": "paintoy-casey-cane-ass-caning",
		"At The Whipping Post - Part 1": "at-the-whipping-post-part-1",
		"  Funnel Fuckery!  ":           "funnel-fuckery",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := New()
	s.base = ts.URL
	s.client = ts.Client()

	const studioURL = "https://sensualpain.com"
	scenes, others := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	var total, errs int
	for _, r := range others {
		switch r.Kind {
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			errs++
		}
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if errs != 2 {
		t.Errorf("errors = %d, want 2 (detail pages that 404)", errs)
	}

	sc := scenes[0]
	if sc.ID != "0668" || sc.SiteID != "sensualpain" || sc.StudioURL != studioURL || sc.Studio != "Sensual Pain" {
		t.Errorf("identity fields: %+v", sc)
	}
	if sc.Title != "Paintoy Casey Cane Ass Caning" {
		t.Errorf("title = %q", sc.Title)
	}
	if want := ts.URL + "/media.php?id=paintoy-casey-cane-ass-caning"; sc.URL != want {
		t.Errorf("url = %q, want %q", sc.URL, want)
	}
	if sc.Duration != 1213 || sc.Date.Format("2006-01-02") != "2026-09-13" {
		t.Errorf("duration/date = %d / %s", sc.Duration, sc.Date)
	}
	if !strings.HasPrefix(sc.Description, "Thrill seeker Casey") {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Tags) != 6 || sc.Tags[3] != "Casey Cane" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Thumbnail != "https://sensualpain.com/content/0668_Paintoy_Casey_Cane_Ass_Caning/Main.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt unset")
	}

	// A scene whose detail page fails keeps its listing fields.
	if scenes[1].Title != "Four Sessions With Paintoy Eris Mae" || scenes[1].Duration != 5182 || scenes[1].Description != "" {
		t.Errorf("scene 1 = %+v", scenes[1])
	}
}

func TestListScenesKnownIDsStopsEarly(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	s := New()
	s.base = ts.URL
	s.client = ts.Client()

	scenes, others := collect(t, s, "https://sensualpain.com", scraper.ListOpts{KnownIDs: map[string]bool{"0667": true}})
	if len(scenes) != 1 || scenes[0].ID != "0668" {
		t.Fatalf("scenes = %+v", scenes)
	}
	stopped := false
	for _, r := range others {
		if r.Kind == scraper.KindStoppedEarly {
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

func TestListScenesNoRecordingsIsParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer ts.Close()
	s := New()
	s.base = ts.URL
	s.client = ts.Client()

	scenes, others := collect(t, s, "https://sensualpain.com", scraper.ListOpts{})
	if len(scenes) != 0 || len(others) != 1 || others[0].Kind != scraper.KindError {
		t.Fatalf("scenes=%d others=%+v", len(scenes), others)
	}
	if scraper.Classify(others[0].Err) != scraper.FailureParse {
		t.Errorf("kind = %v, want parse", scraper.Classify(others[0].Err))
	}
}

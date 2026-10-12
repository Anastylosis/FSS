package restrainedelegance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const disclaimerPage = `<html><body><form method="POST" action="/whatsnew.php?p=1" class="age-disclaimer-form"></form></body></html>`

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type gateStats struct {
	posts atomic.Int32
	gets  atomic.Int32
}

// newTestServer mimics the site: an empty body while a session opens, the
// disclaimer form until it is POSTed, then the fixture pages.
func newTestServer(t *testing.T, stats *gateStats) *httptest.Server {
	t.Helper()
	page1, page2 := fixture(t, "page1.html"), fixture(t, "page2.html")
	var opened atomic.Bool
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			stats.posts.Add(1)
			if err := r.ParseForm(); err != nil || r.Form.Get("confirm_age") != "1" || r.Form.Get("confirm_terms") != "1" {
				_, _ = fmt.Fprint(w, disclaimerPage)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "age_disclaimer_accepted", Value: "yes", Path: "/"})
			http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
			return
		}
		stats.gets.Add(1)
		if !opened.Swap(true) {
			return
		}
		if c, err := r.Cookie("age_disclaimer_accepted"); err != nil || c.Value != "yes" {
			_, _ = fmt.Fprint(w, disclaimerPage)
			return
		}
		switch r.URL.Query().Get("p") {
		case "1":
			_, _ = fmt.Fprint(w, page1)
		default:
			_, _ = fmt.Fprint(w, page2)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	return &Scraper{client: c, base: ts.URL}
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) (scenes []models.Scene, errs, stopped int) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	return scenes, errs, stopped
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.restrainedelegance.com/",
		"https://restrainedelegance.com",
		"https://www.restrainedelegance.com/whatsnew.php?p=3",
		"http://www.restrainedelegance.com/modelbio.php?id=Ariel+Anderssen",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://estore.restrainedelegance.com/store/elegancestudios/",
		"https://www.restrainedelegance.org/",
		"https://notrestrainedelegance.com/",
		"https://example.com/restrainedelegance.com",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestResolveModel(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.restrainedelegance.com/":                                        "",
		"https://www.restrainedelegance.com/whatsnew.php?p=4":                        "",
		"https://www.restrainedelegance.com/modelbio.php?id=Ariel+Anderssen":         "Ariel Anderssen",
		"https://www.restrainedelegance.com/modelbio.php?id=Zoe+Page&members=1":      "Zoe Page",
		"https://www.restrainedelegance.com/modelbio.php":                            "",
		"https://www.restrainedelegance.com/other.php?id=Ariel+Anderssen":            "",
		"https://www.restrainedelegance.com/modelbio.php?id=Chanta%20Rose#galleries": "Chanta Rose",
	} {
		if got := resolveModel(in); got != want {
			t.Errorf("resolveModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePage(t *testing.T) {
	scenes := parsePage(fixture(t, "page1.html"), "https://example.test")
	if len(scenes) != 1 {
		t.Fatalf("got %d video entries, want 1 (photosets are dropped)", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "VID0631_Ariel_MakeUpBoundUp1" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.Title != "Makeup Bound Up" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 9*60+25 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if want := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Ariel Anderssen" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 8 || sc.Tags[0] != "rope bondage" || sc.Tags[7] != "nude" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if !strings.HasPrefix(sc.Description, "Ariel and I are meant to be going out for the evening") ||
		!strings.HasSuffix(sc.Description, "amusing ourselves for the evening, won't we?") {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://example.test/images/VID0631_Ariel_MakeUpBoundUp1i.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != "https://example.test/whatsnew.php#VID0631_Ariel_MakeUpBoundUp1" {
		t.Errorf("URL = %q", sc.URL)
	}
}

func TestParsePageVariants(t *testing.T) {
	scenes := parsePage(fixture(t, "page2.html"), "https://example.test")
	if len(scenes) != 3 {
		t.Fatalf("got %d video entries, want 3 (a '9 photos' video and artwork are dropped)", len(scenes))
	}

	archive := scenes[0]
	if archive.Title != "Maria's Daily Whipping" {
		t.Errorf("archive title = %q", archive.Title)
	}
	if archive.Description != "" {
		t.Errorf("the members teaser is not a description, got %q", archive.Description)
	}
	if len(archive.Performers) != 2 || archive.Performers[1] != "Maria Stevens" {
		t.Errorf("archive performers = %v", archive.Performers)
	}
	if want := time.Date(2007, 12, 18, 0, 0, 0, 0, time.UTC); !archive.Date.Equal(want) {
		t.Errorf("archive date = %v, want %v", archive.Date, want)
	}
	if archive.Duration != 720 {
		t.Errorf("archive duration = %d", archive.Duration)
	}

	scarves := scenes[1]
	if scarves.ID != "videoarielpennyscarves11" || len(scarves.Performers) != 2 {
		t.Errorf("scarves = %+v", scarves)
	}
	if !strings.Contains(scarves.Description, "\n\nShe doesn't like it messed up") {
		t.Errorf("paragraphs should be joined by a blank line, got %q", scarves.Description)
	}

	if tutorial := scenes[2]; tutorial.Title != "Behind The Scenes: Hannah's First Suspension" {
		t.Errorf("an <h2> between title and kind must not hide the video, got %q", tutorial.Title)
	}
}

func TestLastPage(t *testing.T) {
	if got := lastPage(fixture(t, "page1.html")); got != 2 {
		t.Errorf("lastPage = %d, want 2", got)
	}
	if got := lastPage("<html></html>"); got != 1 {
		t.Errorf("lastPage without a pager = %d, want 1", got)
	}
}

func TestListScenes(t *testing.T) {
	var stats gateStats
	ts := newTestServer(t, &stats)
	defer ts.Close()

	studioURL := ts.URL + "/whatsnew.php"
	scenes, errs, _ := collect(t, newTestScraper(ts), studioURL, scraper.ListOpts{})
	if errs != 0 {
		t.Fatalf("got %d errors", errs)
	}
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	first := scenes[0]
	if first.SiteID != siteID || first.Studio != studioName || first.StudioURL != studioURL {
		t.Errorf("scene = %+v", first)
	}
	if first.Title != "Makeup Bound Up" || !strings.HasPrefix(first.URL, ts.URL) || first.ScrapedAt.IsZero() {
		t.Errorf("scene = %+v", first)
	}
	if stats.posts.Load() != 1 {
		t.Errorf("age disclaimer POSTed %d times, want once for the whole run", stats.posts.Load())
	}
}

func TestListScenesModelFilter(t *testing.T) {
	var stats gateStats
	ts := newTestServer(t, &stats)
	defer ts.Close()

	studioURL := ts.URL + "/modelbio.php?id=ariel+anderssen"
	scenes, errs, _ := collect(t, newTestScraper(ts), studioURL, scraper.ListOpts{})
	if errs != 0 {
		t.Fatalf("got %d errors", errs)
	}
	if len(scenes) != 2 || scenes[0].Title != "Makeup Bound Up" || scenes[1].Title != "A Fetish For Scarves" {
		t.Fatalf("scenes = %+v", scenes)
	}
	if scenes[0].StudioURL != studioURL {
		t.Errorf("StudioURL = %q, want the URL passed in", scenes[0].StudioURL)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	var stats gateStats
	ts := newTestServer(t, &stats)
	defer ts.Close()

	scenes, _, stopped := collect(t, newTestScraper(ts), ts.URL+"/whatsnew.php", scraper.ListOpts{
		KnownIDs: map[string]bool{"VID0631_Ariel_MakeUpBoundUp1": true},
	})
	if len(scenes) != 0 || stopped != 1 {
		t.Errorf("got %d scenes and %d early stops, want 0 and 1", len(scenes), stopped)
	}
}

func TestPageWithoutEntriesIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>nothing here</body></html>")
	}))
	defer ts.Close()

	_, errs, _ := collect(t, newTestScraper(ts), ts.URL+"/whatsnew.php", scraper.ListOpts{})
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestDisclaimerThatNeverClearsFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, disclaimerPage)
	}))
	defer ts.Close()

	_, errs, _ := collect(t, newTestScraper(ts), ts.URL+"/whatsnew.php", scraper.ListOpts{})
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

package handdomination

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func card(id, thumb, title, modelID, model, date, dur string) string {
	return fmt.Sprintf(`<div class="browse_updates">
    <ul>
        <li><a href="video.php?video_id=%s"><img src="%s" alt="x" title="x" width="300px" height="169px" /></a></li>
        <li>%s</li>
        <li class="model_name">featuring: 
                  <a href="model_bio.php?model_id=%s">%s</a></li> 
        <li class="posted">date: %s</li>
        <li class="duration">duration: %s mins</li>
    </ul>
</div>`, id, thumb, title, modelID, model, date, dur)
}

const detailPage = `<html><body><div id="Container">
<div class="update_container">
 <ul>
  <li class="videoSource"><a href="join.php"><video poster="update/audrey_lords_13001/poster.jpg"></video></a></li>
  <li><h3 class="videoTitle">Facesitting femdom handjob ends abruptly!</h3></li>
  <li class="modelsFeatured"><p>featuring: <a href="model_bio.php?model_id=72 ">Audrey Lords</a></p></li>
  <li class="videoCategories"><p>categories: 
    <a href="category.php">ruined orgasm</a>  <a href="category.php">face sitting</a>  <a href="category.php">cock &amp; ball slapping</a></p></li>
  <li class="update_posted"><p>date: Jun 17, 2019</p></li>
  <li class="update_duration"><p>duration: 16:33</p></li>
  <li class="videoDescription"><p>Bound at the wrists &amp; ankles, Audrey&#39;s captive is helpless.</p></li>
 </ul>
</div></div></body></html>`

func listingHTML() string {
	return `<html><body><div id="Container">` +
		card("229", "update/audrey_lords_13001/thumb.jpg", "Facesitting femdom handjob ends", "72", "Audrey Lords", "Jun 17, 2019", "16:33") +
		card("228", "update/lola_lynn_12674/thumb.jpg", "Prolonged edging pushed cock over the", "60", "Lola Lynn", "Jun 11, 2019", "15:40") +
		card("227", "update/x/thumb.jpg", "Third scene", "72", "Audrey Lords", "Jun 05, 2019", "10:41") +
		`</div></body></html>`
}

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/browse_video.php":
			_, _ = fmt.Fprint(w, listingHTML())
		case "/video.php":
			_, _ = fmt.Fprint(w, detailPage)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), base: ts.URL}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs, stopped, total int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		case scraper.KindTotal:
			total = r.Total
		}
	}
	return scenes, errs, stopped, total
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"http://www.handdomination.com/", true},
		{"https://handdomination.com", true},
		{"http://www.handdomination.com/model_bio.php?model_id=72", true},
		{"http://handdomination.com.evil.net/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	entries := parseListing(listingHTML())
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	e := entries[0]
	if e.id != "229" {
		t.Errorf("id = %q", e.id)
	}
	if e.thumb != "update/audrey_lords_13001/thumb.jpg" {
		t.Errorf("thumb = %q", e.thumb)
	}
	if e.title != "Facesitting femdom handjob ends" {
		t.Errorf("title = %q", e.title)
	}
	if len(e.models) != 1 || e.models[0] != "Audrey Lords" {
		t.Errorf("models = %v", e.models)
	}
	if len(e.modelIDs) != 1 || e.modelIDs[0] != "72" {
		t.Errorf("modelIDs = %v", e.modelIDs)
	}
	if e.date != "Jun 17, 2019" {
		t.Errorf("date = %q", e.date)
	}
	if e.duration != "16:33" {
		t.Errorf("duration = %q", e.duration)
	}
	// The cards share their closing </div>, so a terminator-consuming pattern
	// would drop every second one.
	for i, want := range []string{"229", "228", "227"} {
		if entries[i].id != want {
			t.Errorf("entry %d id = %q, want %q", i, entries[i].id, want)
		}
	}
}

func TestApplyDetail(t *testing.T) {
	sc := models.Scene{Title: "Facesitting femdom handjob ends"}
	applyDetail(&sc, detailPage, "http://www.handdomination.com")

	// The listing truncates the title; the detail page is the authority.
	if sc.Title != "Facesitting femdom handjob ends abruptly!" {
		t.Errorf("title = %q", sc.Title)
	}
	if len(sc.Tags) != 3 || sc.Tags[0] != "ruined orgasm" || sc.Tags[2] != "cock & ball slapping" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Description != "Bound at the wrists & ankles, Audrey's captive is helpless." {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Thumbnail != "http://www.handdomination.com/update/audrey_lords_13001/poster.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

func TestResolveURL(t *testing.T) {
	const base = "http://www.handdomination.com"
	tests := []struct{ ref, want string }{
		{"update/x/thumb.jpg", base + "/update/x/thumb.jpg"},
		{"/update/x/thumb.jpg", base + "/update/x/thumb.jpg"},
		{"https://cdn.example.com/a.jpg", "https://cdn.example.com/a.jpg"},
	}
	for _, tt := range tests {
		if got := resolveURL(base, tt.ref); got != tt.want {
			t.Errorf("resolveURL(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _, total := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	sc := scenes[0]
	if sc.ID != "229" || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", sc.URL)
	}
	if sc.Duration != 993 {
		t.Errorf("duration = %d, want 993", sc.Duration)
	}
	if sc.Date.Format("2006-01-02") != "2019-06-17" {
		t.Errorf("date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Audrey Lords" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 3 {
		t.Errorf("tags = %v", sc.Tags)
	}
	// A zero-padded day parses too.
	if scenes[2].Date.Format("2006-01-02") != "2019-06-05" {
		t.Errorf("padded date = %v", scenes[2].Date)
	}
}

// The model page carries only a couple of that model's scenes, so the mode
// filters the full listing instead of parsing it.
func TestListScenesModelMode(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/model_bio.php?model_id=60", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 1 || scenes[0].ID != "228" {
		t.Errorf("scenes = %v, want only 228", scenes)
	}
}

// A model the listing never credits is a missing sub-listing, not lost scenes:
// it must not make the traversal look incomplete.
func TestListScenesUnknownModelIsAbsent(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/model_bio.php?model_id=999", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(r.Err); got != scraper.FailureAbsent {
				t.Errorf("Classify = %v, want FailureAbsent", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"228": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped, _ := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "229" {
		t.Errorf("scenes = %v, want only 229", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

// A browse page that loads but yields no cards is a parser failure, not an
// empty catalogue.
func TestEmptyListingIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><div id="Container"></div></body></html>`)
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(r.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

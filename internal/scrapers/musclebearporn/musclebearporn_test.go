package musclebearporn

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

func cardHTML(id, title, slug, thumb string) string {
	return fmt.Sprintf(`<div class="col-sm-6 video-thumb" data-setid="%s">
	<div class="show show-first">
		<a title="%s" href="https://www.musclebearporn.com/tour/updates/%s.html">
			<img id="set-target-%s" width="720" height="480" alt="%s" src="%s" cnt="1" v="0" />
			<h3 class="scene-title">%s</h3>
		</a>
	</div>
</div>`, id, title, slug, id, title, thumb, title)
}

// The theme hides the metadata block in an HTML comment instead of dropping
// it; the spans are still served, so that is where the record comes from.
const detailPage = `<html><body>
<div class="container-inner">
<!-- <span class="update_title">Ageless Boy</span>-->
<!-- <span class="tour_update_models">
 <a href="http://join.musclebearporn.com/strack/x/join">Logan Grant</a>
 <a href="http://join.musclebearporn.com/strack/y/join">Will Angell</a>
</span> -->
<!-- <span class="update_date">09/04/2026</span>-->
<!-- <span class="latest_update_description">Logan Grant is a top Daddy &amp; more.</span>-->
<!-- <span class="tour_update_tags"><a href="/tour/categories/bareback.html">Bareback</a>, <a href="/tour/categories/daddy.html">Daddy</a></span> -->
</div>
<div id="video-player-page">
	<video id="videoProtectedPlayer" controls width="100%" poster="https://musclebearporn.com/tour/content/contentthumbs/33/37/3337-4x.jpg">
	<source src="/trailers/MBP841.mp4" type="video/mp4" id="srcVideoPlayer" />
	</video>
</div>
</body></html>`

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/tour/categories/movies.html":
			_, _ = fmt.Fprint(w, `<div class="row">`+
				cardHTML("316", "Ageless Boy", "Ageless-Boy", "https://cdn.example.com/316.jpg")+
				cardHTML("315", "Sling Slut #2", "Sling-Slut-2", "https://cdn.example.com/315.jpg")+
				`</div>`)
		case r.URL.Path == "/tour/categories/movies_2_d.html":
			_, _ = fmt.Fprint(w, `<div class="row">`+cardHTML("314", "Third", "Third", "https://cdn.example.com/314.jpg")+`</div>`)
		case strings.HasPrefix(r.URL.Path, "/tour/updates/"):
			_, _ = fmt.Fprint(w, detailPage)
		default:
			_, _ = fmt.Fprint(w, `<html><body><div class="row"></div></body></html>`)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), base: ts.URL}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs, stopped int) {
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
	tests := []struct {
		url  string
		want bool
	}{
		{"https://musclebearporn.com/tour/", true},
		{"https://www.musclebearporn.com", true},
		{"http://www.musclebearporn.com/tour/categories/movies.html", true},
		{"https://musclebearporn.com.evil.net/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestCategorySlug(t *testing.T) {
	tests := []struct{ url, want string }{
		{"https://www.musclebearporn.com/tour/", "movies"},
		{"https://musclebearporn.com/", "movies"},
		{"https://www.musclebearporn.com/tour/categories/movies.html", "movies"},
		{"https://www.musclebearporn.com/tour/categories/movies_11_d.html", "movies"},
		{"https://www.musclebearporn.com/tour/categories/bareback.html", "bareback"},
		{"https://www.musclebearporn.com/tour/categories/bareback_3_d.html", "bareback"},
	}
	for _, tt := range tests {
		if got := categorySlug(tt.url); got != tt.want {
			t.Errorf("categorySlug(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New()
	if got := s.listingURL("movies", 1); got != "https://www.musclebearporn.com/tour/categories/movies.html" {
		t.Errorf("page 1 = %q", got)
	}
	if got := s.listingURL("movies", 11); got != "https://www.musclebearporn.com/tour/categories/movies_11_d.html" {
		t.Errorf("page 11 = %q", got)
	}
}

func TestParseListing(t *testing.T) {
	page := `<div class="row">` +
		cardHTML("316", "Ageless Boy", "Ageless-Boy", "https://cdn.example.com/316.jpg") +
		cardHTML("315", "Sling Slut #2", "Sling-Slut-2", "https://cdn.example.com/315.jpg") +
		`</div>`
	cards := parseListing(page)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	c := cards[0]
	if c.id != "316" || c.title != "Ageless Boy" || c.path != "/tour/updates/Ageless-Boy.html" {
		t.Errorf("card = %+v", c)
	}
	if c.thumb != "https://cdn.example.com/316.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
	// The grid shares its closing tags, so adjacent cards must all survive.
	if cards[1].id != "315" {
		t.Errorf("second id = %q", cards[1].id)
	}
}

func TestApplyDetail(t *testing.T) {
	sc := models.Scene{Title: "Ageless Boy", Thumbnail: "https://cdn.example.com/316.jpg"}
	applyDetail(&sc, detailPage, "https://www.musclebearporn.com")

	if sc.Description != "Logan Grant is a top Daddy & more." {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Logan Grant" || sc.Performers[1] != "Will Angell" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 2 || sc.Tags[0] != "Bareback" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Date.Format("2006-01-02") != "2026-09-04" {
		t.Errorf("date = %v", sc.Date)
	}
	// The player's poster is the larger variant, so it replaces the card's.
	if sc.Thumbnail != "https://musclebearporn.com/tour/content/contentthumbs/33/37/3337-4x.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.Preview != "https://www.musclebearporn.com/trailers/MBP841.mp4" {
		t.Errorf("preview = %q", sc.Preview)
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "316" || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", sc.URL)
	}
	if len(sc.Performers) != 2 || sc.Date.IsZero() {
		t.Errorf("detail not applied: %+v", sc)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{
		KnownIDs: map[string]bool{"315": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "316" {
		t.Errorf("scenes = %v, want only 316", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

// A first page that loads but yields no cards is a parser failure, not an
// empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><div class="row"></div></body></html>`)
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{})
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

package raunchybastards

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

func cardHTML(id, slug, title, mins, likes string, models ...string) string {
	var perf []string
	for _, m := range models {
		perf = append(perf, fmt.Sprintf(`<a href="/profile/1-%s" >%s</a>`, strings.ToLower(m), m))
	}
	return fmt.Sprintf(`<div class="scene_container col-12 flexGrow">
	<figure class="">
		<a href="/scene/%s-%s"><img src="https://static.raunchybastards.com/_thumbs/scn_%s.jpg" class="img-responsive" alt="Photo of %s"><div class="short_info"><i class="icon-clock-1"></i>%s min</div></a>
	</figure>
	<div class="scene_title">
		<div><div class="wrapperSceneTitle"><a href="/scene/%s-%s">%s</a></div></div>
		<div style="clear: both;"><h4>%s</h4></div>
		<div class="info_2 clearfix"><span data-actionid="ScnLik%s" class="fireActionFavLik tooltip-1 likesLbl off" title="" >%s</span></div>
	</div>
</div>`, id, slug, id, title, mins, id, slug, title, strings.Join(perf, ", "), id, likes)
}

func listingHTML(ids ...string) string {
	var b strings.Builder
	b.WriteString(`<html><body><div class="row equal">`)
	for _, id := range ids {
		b.WriteString(cardHTML(id, "scene-"+id, "Scene "+id, "58", "228", "Clay", "Leif"))
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

const detailPage = `<html><body>
<div class="container margin_60">
	<div class="p-5">
		<h2 class="main_title sectionMainTitle">Teen Boy Cunt Popped and Plugged</h2>
		<div class="row"><p><a href="/profile/1-clay" >Clay</a></p></div>
	</div>
	<div class="row container_styled_1">
		<div class="p-5">
			<p>Leif is one of those guys.<br />
<br />
Gideon &amp; I had fun.</p>
			<h5 class="strong">Categories: <a href="/scenes/category/1-18-19" >18 / 19</a>, <a href="/scenes/category/15-felching" >Felching</a></h5>
			<h5 class="strong">Details:  <i class="icon-clock-1"></i>58 min</h5>
		</div>
	</div>
</div></body></html>`

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/scenes" || strings.HasPrefix(r.URL.Path, "/scenes/category/") || strings.HasPrefix(r.URL.Path, "/profile/"):
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = fmt.Fprint(w, listingHTML("458", "426"))
			case "2":
				_, _ = fmt.Fprint(w, listingHTML("410"))
			default:
				// Past the end the CMS clamps back to page 1.
				_, _ = fmt.Fprint(w, listingHTML("458", "426"))
			}
		case strings.HasPrefix(r.URL.Path, "/scene/"):
			_, _ = fmt.Fprint(w, detailPage)
		default:
			http.NotFound(w, r)
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
		{"https://www.raunchybastards.com/", true},
		{"https://raunchybastards.com/scenes", true},
		{"http://www.raunchybastards.com/profile/1-clay", true},
		{"https://raunchybastards.com.evil.net/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestListingPath(t *testing.T) {
	tests := []struct{ url, want string }{
		{"https://www.raunchybastards.com/", "/scenes"},
		{"https://www.raunchybastards.com", "/scenes"},
		{"https://www.raunchybastards.com/scenes", "/scenes"},
		{"https://www.raunchybastards.com/scenes?sort=title-asc", "/scenes"},
		{"https://www.raunchybastards.com/scenes/category/15-felching", "/scenes/category/15-felching"},
		{"https://www.raunchybastards.com/scenes/category/15-felching/", "/scenes/category/15-felching"},
		{"https://www.raunchybastards.com/profile/1-clay", "/profile/1-clay"},
		// A scene page is not a listing.
		{"https://www.raunchybastards.com/scene/18-teen-boy", "/scenes"},
		{"https://www.raunchybastards.com/categories", "/scenes"},
	}
	for _, tt := range tests {
		if got := listingPath(tt.url); got != tt.want {
			t.Errorf("listingPath(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(listingHTML("458", "426", "410"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3", len(cards))
	}
	c := cards[0]
	if c.id != "458" || c.slug != "scene-458" {
		t.Errorf("id/slug = %q/%q", c.id, c.slug)
	}
	if c.title != "Scene 458" {
		t.Errorf("title = %q", c.title)
	}
	if c.thumb != "https://static.raunchybastards.com/_thumbs/scn_458.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
	if len(c.performers) != 2 || c.performers[0] != "Clay" || c.performers[1] != "Leif" {
		t.Errorf("performers = %v", c.performers)
	}
	if c.duration != 3480 {
		t.Errorf("duration = %d, want 3480", c.duration)
	}
	if c.likes != 228 {
		t.Errorf("likes = %d", c.likes)
	}
	// The grid shares its closing tags, so adjacent cards must all survive.
	for i, want := range []string{"458", "426", "410"} {
		if cards[i].id != want {
			t.Errorf("card %d id = %q, want %q", i, cards[i].id, want)
		}
	}
}

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, detailPage)

	// The header block also lives in a .p-5 div; only the one that opens with
	// a paragraph is the synopsis.
	if sc.Description != "Leif is one of those guys. Gideon & I had fun." {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Categories) != 2 || sc.Categories[0] != "18 / 19" || sc.Categories[1] != "Felching" {
		t.Errorf("categories = %v", sc.Categories)
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
	scenes, errs, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	// Page 3 clamps back to page 1, whose ids are already seen, so the walk
	// stops there rather than looping.
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "458" || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", sc.URL)
	}
	if sc.Description == "" || len(sc.Categories) != 2 {
		t.Errorf("detail not applied: %+v", sc)
	}
	// The site publishes no release date.
	if !sc.Date.IsZero() {
		t.Errorf("date = %v, want zero", sc.Date)
	}
}

// The default order is the editor's pick, under which an early stop would
// truncate arbitrarily, so every request asks for newest-first.
func TestListingRequestsNewestFirst(t *testing.T) {
	var queries []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/scene/") {
			_, _ = fmt.Fprint(w, detailPage)
			return
		}
		queries = append(queries, r.URL.Query().Get("sort"))
		_, _ = fmt.Fprint(w, listingHTML("458"))
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	collect(ch)
	if len(queries) == 0 {
		t.Fatal("no listing requests")
	}
	for i, q := range queries {
		if q != sortNewest {
			t.Errorf("request %d sort = %q, want %q", i, q, sortNewest)
		}
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"426": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "458" {
		t.Errorf("scenes = %v, want only 458", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

// A first page that loads but yields no cards is a parser failure, not an
// empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><div class="row equal"></div></body></html>`)
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

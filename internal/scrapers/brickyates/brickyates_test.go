package brickyates

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

func cardHTML(id, slug, title, thumb string) string {
	return fmt.Sprintf(`<div class="item-video hover">
	<div class="item-thumb">
		<a href="https://www.brickyates.com/tour/trailers/%s.html" title="%s">
			<img id="set-target-%s" width="630" height="355" alt="" class="mainThumb thumbs stdimage" src0_1x="%s" src0_2x="%s" />
		</a>
	</div>
</div>`, slug, title, id, thumb, thumb)
}

const detailPage = `<html><body>
<div class="bodyArea">
	<video poster="//www.brickyates.com/tour/content/poster.jpg"></video>
	<div class="videoDetails clear">
		<h1><strong>3-Way Relationships: 4 of 4 - The Entr&eacute;e</strong></h1>
		<p>The last part of the series &amp; then some.</p>
	</div>
	<div class="videoInfo clear">
		<p><span>Date Added:</span>
			January 23, 2024</p>
		<i>|</i>
		<p>
23&nbsp;minute(s)&nbsp;of video</p>
		<i>|</i>
		<p><span>Rating:</span> 3.7/5.0</p>
	</div>
	<div class="featuring clear">
		<ul>
			<li class="label">Featuring:</li>
			<li class="update_models">
			<a href="https://www.brickyates.com/tour/models/AnitaJohnson.html">Anita Johnson</a>	</li>
			<li class="update_models">
			<a href="https://www.brickyates.com/tour/models/Henry.html">Henry</a>	</li>
		</ul>
	</div>
	<div class="featuring clear">
		<ul>
			<li class="label">Tags:</li><li><a href="https://www.brickyates.com/tour/categories/Amateur/1/latest/">Amateur</a></li><li><a href="https://www.brickyates.com/tour/categories/Bareback/1/latest/">Bareback</a></li>
		</ul>
	</div>
</div>
</body></html>`

func newTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/tour/trailers/"):
			_, _ = fmt.Fprint(w, detailPage)
		case r.URL.Path == "/tour/categories/Movies/1/latest/":
			_, _ = fmt.Fprint(w, `<div class="items clear">`+
				cardHTML("331", "entree", "3-Way Relationships: 4 of 4 - The Entr&eacute;e", "/tour/content/10132-1x.jpg")+
				cardHTML("330", "starter", "The Starter", "/tour/content/10120-1x.jpg")+`</div>`)
		case r.URL.Path == "/tour/categories/Movies/2/latest/":
			_, _ = fmt.Fprint(w, `<div class="items clear">`+cardHTML("329", "third", "Third", "/tour/content/10100-1x.jpg")+`</div>`)
		case r.URL.Path == "/tour/models/Henry.html":
			_, _ = fmt.Fprint(w, `<div class="items clear">`+cardHTML("331", "entree", "Entree", "/tour/content/10132-1x.jpg")+`</div>`)
		default:
			_, _ = fmt.Fprint(w, `<div class="items clear"></div>`)
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
		{"https://brickyates.com/", true},
		{"https://www.brickyates.com/tour/", true},
		{"http://www.brickyates.com/tour/models/Henry.html", true},
		{"https://brickyates.com.evil.net/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestResolveListing(t *testing.T) {
	tests := []struct {
		url      string
		category string
		model    string
	}{
		{"https://www.brickyates.com/", "Movies", ""},
		{"https://www.brickyates.com/tour/", "Movies", ""},
		{"https://www.brickyates.com/tour/categories/Movies/1/latest/", "Movies", ""},
		{"https://www.brickyates.com/tour/categories/Bareback/3/latest/", "Bareback", ""},
		{"https://www.brickyates.com/tour/models/Henry.html", "", "Henry"},
	}
	for _, tt := range tests {
		got := resolveListing(tt.url)
		if got.category != tt.category || got.model != tt.model {
			t.Errorf("resolveListing(%q) = %+v, want %q/%q", tt.url, got, tt.category, tt.model)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New()
	if got := s.listingURL(listing{category: "Movies"}, 3); got != "https://www.brickyates.com/tour/categories/Movies/3/latest/" {
		t.Errorf("category = %q", got)
	}
	// A model page is one page, so the page number never enters its URL.
	if got := s.listingURL(listing{model: "Henry"}, 4); got != "https://www.brickyates.com/tour/models/Henry.html" {
		t.Errorf("model = %q", got)
	}
}

func TestParseListing(t *testing.T) {
	page := `<div class="items clear">` +
		cardHTML("331", "entree", "The Entr&eacute;e", "/tour/content/10132-1x.jpg") +
		cardHTML("330", "starter", "The Starter", "/tour/content/10120-1x.jpg") +
		`</div>`
	cards := parseListing(page)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	c := cards[0]
	if c.id != "331" || c.path != "/tour/trailers/entree.html" {
		t.Errorf("card = %+v", c)
	}
	if c.title != "The Entrée" {
		t.Errorf("title = %q", c.title)
	}
	if c.thumb != "/tour/content/10132-1x.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
	// The card's link precedes its id, so adjacent cards must not steal each
	// other's.
	if cards[1].id != "330" || cards[1].path != "/tour/trailers/starter.html" {
		t.Errorf("second card = %+v", cards[1])
	}
}

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, detailPage, "https://www.brickyates.com")

	if sc.Title != "3-Way Relationships: 4 of 4 - The Entrée" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Description != "The last part of the series & then some." {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Date.Format("2006-01-02") != "2024-01-23" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Duration != 1380 {
		t.Errorf("duration = %d", sc.Duration)
	}
	// The cast and the tags share the block shape; only the cast's items carry
	// the update_models class.
	if len(sc.Performers) != 2 || sc.Performers[0] != "Anita Johnson" || sc.Performers[1] != "Henry" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 2 || sc.Tags[0] != "Amateur" || sc.Tags[1] != "Bareback" {
		t.Errorf("tags = %v", sc.Tags)
	}
	// The player poster is protocol-relative.
	if sc.Thumbnail != "https://www.brickyates.com/tour/content/poster.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

func TestResolveURL(t *testing.T) {
	const base = "https://www.brickyates.com"
	tests := []struct{ ref, want string }{
		{"/tour/content/a.jpg", base + "/tour/content/a.jpg"},
		{"//www.brickyates.com/tour/a.jpg", "https://www.brickyates.com/tour/a.jpg"},
		{"https://cdn.example.com/a.jpg", "https://cdn.example.com/a.jpg"},
		{"tour/a.jpg", base + "/tour/a.jpg"},
		{"", ""},
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
	if !strings.HasPrefix(scenes[0].URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", scenes[0].URL)
	}
	if scenes[0].Studio != studioName || len(scenes[0].Performers) != 2 {
		t.Errorf("scene = %+v", scenes[0])
	}
}

// A model page is a single page, so the walk is one request.
func TestListScenesModelIsASinglePage(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tour/trailers/") {
			_, _ = fmt.Fprint(w, detailPage)
			return
		}
		calls++
		_, _ = fmt.Fprint(w, `<div class="items clear">`+cardHTML("331", "entree", "Entree", "/a.jpg")+`</div>`)
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/models/Henry.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, _ := collect(ch)
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
	if calls != 1 {
		t.Errorf("fetched %d listing pages, want 1", calls)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/tour/", scraper.ListOpts{
		KnownIDs: map[string]bool{"330": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "331" {
		t.Errorf("scenes = %v, want only 331", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

// A first page that loads but yields no cards is a parser failure, not an
// empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<div class="items clear"></div>`)
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

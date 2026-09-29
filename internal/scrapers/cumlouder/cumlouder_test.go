package cumlouder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.cumlouder.com/site/breakingasses/", true},
		{"https://cumlouder.com/site/breakingasses/", true},
		{"https://www.cumlouder.com/girl/saida-sinner/", true},
		{"https://cumlouder.com/girl/someone/", true},
		{"https://www.cumlouder.com/site/boobday", true},
		{"https://www.cumlouder.com/", false},
		{"https://www.cumlouder.com/porn/", false},
		{"https://example.com/site/foo/", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestSceneSlug(t *testing.T) {
	cases := []struct {
		href string
		want string
	}{
		{"/porn-video/anal-withdrawal/", "anal-withdrawal"},
		{"https://www.cumlouder.com/porn-video/some-scene/", "some-scene"},
		{"/porn-video/scene", "scene"},
		{"/girl/someone/", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := sceneSlug(c.href)
		if got != c.want {
			t.Errorf("sceneSlug(%q) = %q, want %q", c.href, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"25:30 m", 1530},
		{"01:00 h", 3600},
		{"01:30 h", 5400},
		{"10:00 m", 600},
		{"00:45 m", 45},
		{"1:30:00", 5400},
		{"", 0},
	}
	for _, c := range cases {
		got := parseDuration(c.input)
		if got != c.want {
			t.Errorf("parseDuration(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParseIntCommas(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"51812", 51812},
		{"1,234", 1234},
		{"1.234", 1234},
		{"0", 0},
	}
	for _, c := range cases {
		got := parseIntCommas(c.input)
		if got != c.want {
			t.Errorf("parseIntCommas(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestApproxDate(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		rel  string
		want time.Time
	}{
		{"2 months ago", now.AddDate(0, -2, 0)},
		{"1 year ago", now.AddDate(-1, 0, 0)},
		{"3 weeks ago", now.AddDate(0, 0, -21)},
		{"5 days ago", now.AddDate(0, 0, -5)},
		{"", time.Time{}},
		{"just now", time.Time{}},
	}
	for _, c := range cases {
		got := approxDate(c.rel, now)
		if !got.Equal(c.want) {
			t.Errorf("approxDate(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestParseTotal(t *testing.T) {
	body := []byte(`<div class="video-rating"> <span class="ico-videos sprite"></span> 411 Videos </div>`)
	got := parseTotal(body)
	if got != 411 {
		t.Errorf("parseTotal = %d, want 411", got)
	}

	if parseTotal([]byte(`no videos here`)) != 0 {
		t.Error("parseTotal should return 0 for no match")
	}
}

func TestParseCards(t *testing.T) {
	html := []byte(`
<a class="muestra-escena" href="/porn-video/test-scene/">
  <img class="thumb lazy jsblur blur"
       data-src="https://im0.imgcm.com/thumb.jpg"
       alt="Test Scene Title">
  <h2> <span class="ico-h2 sprite"></span> Test Scene Title</h2>
  <span class="box-fecha-mins">
    <span class="vistas">
      <span class="ico-vistas sprite"></span> 12,345 views
    </span>
    <span class="fecha"> <span class="ico-fecha sprite"></span> 3 months ago</span>
  </span>
  <span class="minutos"> <span class="ico-minutos sprite"></span> 25:30 m</span>
</a>
<a class="muestra-escena" href="/porn-video/another-scene/">
  <img class="thumb lazy" data-src="https://im0.imgcm.com/thumb2.jpg" alt="Another Scene">
  <h2> <span class="ico-h2 sprite"></span> Another Scene</h2>
  <span class="vistas"><span class="ico-vistas sprite"></span> 500 views</span>
  <span class="fecha"><span class="ico-fecha sprite"></span> 1 year ago</span>
  <span class="minutos"><span class="ico-minutos sprite"></span> 01:15 h</span>
</a>`)

	items := parseCards(html)
	if len(items) != 2 {
		t.Fatalf("parseCards returned %d items, want 2", len(items))
	}

	item := items[0]
	if item.slug != "test-scene" {
		t.Errorf("slug = %q, want %q", item.slug, "test-scene")
	}
	if item.title != "Test Scene Title" {
		t.Errorf("title = %q, want %q", item.title, "Test Scene Title")
	}
	if item.thumb != "https://im0.imgcm.com/thumb.jpg" {
		t.Errorf("thumb = %q", item.thumb)
	}
	if item.views != 12345 {
		t.Errorf("views = %d, want 12345", item.views)
	}
	if item.duration != 1530 {
		t.Errorf("duration = %d, want 1530 (25:30)", item.duration)
	}
	if item.relDate != "3 months ago" {
		t.Errorf("relDate = %q, want %q", item.relDate, "3 months ago")
	}

	item2 := items[1]
	if item2.duration != 4500 {
		t.Errorf("duration = %d, want 4500 (01:15 h)", item2.duration)
	}
}

func TestDedup(t *testing.T) {
	items := []listItem{
		{slug: "a", title: "A"},
		{slug: "b", title: "B"},
		{slug: "a", title: "A dup"},
	}
	got := dedup(items)
	if len(got) != 2 {
		t.Fatalf("dedup returned %d, want 2", len(got))
	}
}

func TestListScenes(t *testing.T) {
	detailHTML := `<html>
<a class="tag-link" href="/porn-videos/anal/">anal</a>
<a class="tag-link" href="/porn-videos/blonde/">blonde</a>
<a class="pornstar-link" href="/girl/someone/">Someone Famous</a>
<p><strong>Description:</strong> A great scene.</p>
<div class="duracion"><span>20:00 m</span></div>
<div class="viewed">Views: 150</div>
</html>`

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/site/test"):
			page1HTML := fmt.Sprintf(`<html>
<div class="video-rating"> <span class="ico-videos sprite"></span> 2 Videos </div>
<a class="muestra-escena" href="%s/porn-video/scene-one/">
  <img class="thumb lazy" data-src="https://cdn/thumb1.jpg" alt="Scene One">
  <h2><span class="ico-h2 sprite"></span> Scene One</h2>
  <span class="vistas"><span class="ico-vistas sprite"></span> 100 views</span>
  <span class="fecha"><span class="ico-fecha sprite"></span> 1 month ago</span>
  <span class="minutos"><span class="ico-minutos sprite"></span> 20:00 m</span>
</a>
<a class="muestra-escena" href="%s/porn-video/scene-two/">
  <img class="thumb lazy" data-src="https://cdn/thumb2.jpg" alt="Scene Two">
  <h2><span class="ico-h2 sprite"></span> Scene Two</h2>
  <span class="vistas"><span class="ico-vistas sprite"></span> 200 views</span>
  <span class="fecha"><span class="ico-fecha sprite"></span> 2 months ago</span>
  <span class="minutos"><span class="ico-minutos sprite"></span> 30:00 m</span>
</a></html>`, ts.URL, ts.URL)
			_, _ = fmt.Fprint(w, page1HTML)
		case strings.HasPrefix(r.URL.Path, "/porn-video/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/site/test/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	scenes := map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			scenes[r.Scene.ID] = r.Scene.Title
			if r.Scene.ID == "scene-one" {
				if len(r.Scene.Tags) != 2 {
					t.Errorf("scene-one tags = %v, want 2", r.Scene.Tags)
				}
				if len(r.Scene.Performers) != 1 || r.Scene.Performers[0] != "Someone Famous" {
					t.Errorf("scene-one performers = %v", r.Scene.Performers)
				}
				if r.Scene.Description != "A great scene." {
					t.Errorf("scene-one description = %q", r.Scene.Description)
				}
			}
		}
	}

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(scenes), scenes)
	}
}

func TestListScenesGirl(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		html := fmt.Sprintf(`<html>
<a class="muestra-escena" href="%s/porn-video/girl-scene/">
  <img class="thumb lazy" data-src="https://cdn/t.jpg" alt="Girl Scene">
  <h2><span class="ico-h2 sprite"></span> Girl Scene</h2>
  <span class="vistas"><span class="ico-vistas sprite"></span> 50 views</span>
  <span class="fecha"><span class="ico-fecha sprite"></span> 5 days ago</span>
  <span class="minutos"><span class="ico-minutos sprite"></span> 15:00 m</span>
</a></html>`, ts.URL)
		_, _ = fmt.Fprint(w, html)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/girl/someone/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var count int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d scenes, want 1", count)
	}
}

// A /girl/ page paginates at `/girl/{slug}/{page}/` just as a /site/ page does,
// but the walk used to derive its page count only for /site/ pages — so a
// performer's catalogue was whatever fitted on page one. Live-checked: Aletta
// Ocean's 21 scenes arrive 15 then 6.
func TestListScenesGirlFollowsThePager(t *testing.T) {
	card := func(base, slug, title string) string {
		return fmt.Sprintf(`<a class="muestra-escena" href="%s/porn-video/%s/">
  <img class="thumb lazy" data-src="https://cdn/t.jpg" alt="%s">
  <h2><span class="ico-h2 sprite"></span> %s</h2>
  <span class="minutos"><span class="ico-minutos sprite"></span> 15:00 m</span>
</a>`, base, slug, title, title)
	}

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/girl/someone/":
			_, _ = fmt.Fprint(w, "<html>"+card(ts.URL, "a", "A")+card(ts.URL, "b", "B")+"</html>")
		case "/girl/someone/2/":
			_, _ = fmt.Fprint(w, "<html>"+card(ts.URL, "c", "C")+"</html>")
		case "/girl/someone/3/":
			// Past the end the site answers 404, which ends the listing.
			http.NotFound(w, r)
		default:
			_, _ = fmt.Fprint(w, `<html></html>`)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/girl/someone/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	ids := map[string]bool{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids[r.Scene.ID] = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if len(ids) != 3 || !ids["a"] || !ids["b"] || !ids["c"] {
		t.Errorf("scene IDs = %v, want a, b and c", ids)
	}
}

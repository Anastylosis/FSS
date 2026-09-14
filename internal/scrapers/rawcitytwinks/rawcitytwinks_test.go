package rawcitytwinks

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.rawcitytwinks.com/",
		"https://rawcitytwinks.com/tour/categories/movies/1/latest/",
		"https://breeditraw.net",
		"http://www.breeditraw.net/tour/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://rawcity.com/", "https://example.com/breeditraw.net", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func cardHTML(slug, title, date, runtime string) string {
	return fmt.Sprintf(`<div class="item-video hover">
	<div class="item-thumb">
		<a href="https://www.rawcitytwinks.com/tour/trailers/%s.html" title="%s">
			<img class="mainThumb" src0_1x="/tour/content/thumbs/%s-1x.jpg" />
		</a>
	</div>
	<div class="item-info clear">
		<h4><a href="https://www.rawcitytwinks.com/tour/trailers/%s.html" title="%s">%s</a></h4>
		<div class="time">%s</div>
		<div class="date">%s</div>
	</div>
</div>`, slug, title, slug, slug, title, title, runtime, date)
}

func TestParseCards(t *testing.T) {
	page := `<html><body>` + cardHTML("Major-Kydfoster-Dee", "Major + Kydf Foster &amp; Dee", "2025-03-22", "19:01") +
		cardHTML("Buck-Conscious", "Daddy Buck + Conscious", "2025-03-08", "13:52") + `</body></html>`

	cards := parseCards(page)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].slug != "Major-Kydfoster-Dee" {
		t.Errorf("slug = %q", cards[0].slug)
	}
	if cards[0].title != "Major + Kydf Foster & Dee" {
		t.Errorf("title = %q, want the entity decoded", cards[0].title)
	}
	if cards[0].duration != 1141 {
		t.Errorf("duration = %d, want 1141", cards[0].duration)
	}
	if got := cards[0].date.Format("2006-01-02"); got != "2025-03-22" {
		t.Errorf("date = %s", got)
	}
	if !strings.HasPrefix(cards[0].thumb, "/tour/content/") {
		t.Errorf("thumb = %q", cards[0].thumb)
	}
}

func TestModelName(t *testing.T) {
	if got := modelName(`<h2 class="title">About Clark &amp; Tatum</h2>`); got != "Clark & Tatum" {
		t.Errorf("modelName = %q", got)
	}
	if got := modelName(`<h2>Video Updates</h2>`); got != "" {
		t.Errorf("modelName = %q, want empty", got)
	}
}

func TestAbsURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://cdn.example.com/a.jpg", "https://cdn.example.com/a.jpg"},
		{"//cdn.example.com/a.jpg", "https://cdn.example.com/a.jpg"},
		{"/tour/content/a.jpg", siteBase + "/tour/content/a.jpg"},
		{"content/a.jpg", siteBase + "/tour/content/a.jpg"},
	}
	for _, c := range cases {
		if got := absURL(c.in); got != c.want {
			t.Errorf("absURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The tour serves an empty page past the end; a listing walk must stop there
// rather than loop, and a repeated page must not double-count.
func TestListScenesWalksAndResolvesCast(t *testing.T) {
	var modelIndexPages, modelPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/tour/categories/movies/1/"):
			_, _ = fmt.Fprint(w, cardHTML("scene-a", "Scene A", "2025-03-22", "19:01")+cardHTML("scene-b", "Scene B", "2025-03-08", "10:00"))
		case strings.HasPrefix(p, "/tour/categories/movies/2/"):
			_, _ = fmt.Fprint(w, `<html><body>no cards</body></html>`)
		case strings.HasPrefix(p, "/tour/models/1/"):
			modelIndexPages++
			_, _ = fmt.Fprint(w, `<a href="/tour/models/clark.html">x</a>`)
		case strings.HasPrefix(p, "/tour/models/2/"):
			modelIndexPages++
			_, _ = fmt.Fprint(w, `<html>no models</html>`)
		case p == "/tour/models/clark.html":
			modelPages++
			_, _ = fmt.Fprint(w, `<h2>About Clark Tatum</h2>`+cardHTML("scene-a", "Scene A", "2025-03-22", "19:01"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	cast := map[string][]string{}
	var total int
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindScene:
			cast[r.Scene.ID] = r.Scene.Performers
			if !strings.HasPrefix(r.Scene.URL, siteBase+"/tour/trailers/") {
				t.Errorf("scene URL = %q, want a %s address", r.Scene.URL, siteBase)
			}
		}
	}
	if len(cast) != 2 || total != 2 {
		t.Fatalf("got %d scenes (total hint %d), want 2", len(cast), total)
	}
	if strings.Join(cast["scene-a"], "|") != "Clark Tatum" {
		t.Errorf("scene-a cast = %v", cast["scene-a"])
	}
	if len(cast["scene-b"]) != 0 {
		t.Errorf("scene-b cast = %v, want none", cast["scene-b"])
	}
	if modelPages != 1 {
		t.Errorf("fetched %d model pages, want 1", modelPages)
	}
}

// A model URL scrapes only that model's scenes and credits them to them.
func TestModelURLScrapesOneModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tour/models/clark.html" {
			_, _ = fmt.Fprint(w, `<h2>About Clark Tatum</h2>`+cardHTML("scene-a", "Scene A", "2025-03-22", "19:01"))
			return
		}
		t.Errorf("unexpected fetch of %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/tour/models/clark.html", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var n int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			n++
			if strings.Join(r.Scene.Performers, "|") != "Clark Tatum" {
				t.Errorf("performers = %v", r.Scene.Performers)
			}
		}
	}
	if n != 1 {
		t.Errorf("got %d scenes, want 1", n)
	}
}

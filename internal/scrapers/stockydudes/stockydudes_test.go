package stockydudes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.stockydudes.com", "http://stockydudes.com/scenes", "https://www.stockydudes.com/profile/1280-rhino"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://stockydudes.net/", "https://example.com/stockydudes.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListingPath(t *testing.T) {
	cases := map[string]string{
		"https://www.stockydudes.com":                             "/scenes",
		"https://www.stockydudes.com/scenes":                      "/scenes",
		"https://www.stockydudes.com/scenes/category/1-bareback/": "/scenes/category/1-bareback",
		"https://www.stockydudes.com/profile/1280-rhino":          "/profile/1280-rhino",
	}
	for in, want := range cases {
		if got := listingPath(in); got != want {
			t.Errorf("listingPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCards(t *testing.T) {
	cards := parseCards(read(t, "page.html"))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	c := cards[0]
	if c.id != "1198" || c.slug != "1198-rhino-and-andrew" || c.title != "Rhino and Andrew" {
		t.Errorf("id/slug/title = %q/%q/%q", c.id, c.slug, c.title)
	}
	if c.duration != 21*60 || c.date.Format("2006-01-02") != "2026-11-06" {
		t.Errorf("duration/date = %d/%v", c.duration, c.date)
	}
	if strings.Join(c.performers, "|") != "Andrew Brooks|Rhino" {
		t.Errorf("performers = %v", c.performers)
	}
	if !strings.HasSuffix(c.thumb, ".jpg") {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestParseLoadMore(t *testing.T) {
	lm := parseLoadMore(read(t, "page.html"))
	if !lm.ok || lm.from != 2 || lm.sort != "published-newer" || lm.filter != "a%3A0%3A%7B%7D" {
		t.Errorf("loadMore = %+v", lm)
	}
	if parseLoadMore("<html></html>").ok {
		t.Error("a page without the element must not paginate")
	}
}

func TestApplyDetail(t *testing.T) {
	var c item
	c.applyDetail(read(t, "detail.html"))
	if c.description != "Rhino gives up his furry booty to Andrew Brooks" {
		t.Errorf("description = %q", c.description)
	}
	if len(c.categories) != 5 || c.categories[0] != "Bareback" {
		t.Errorf("categories = %v", c.categories)
	}
	if !strings.Contains(c.thumb, "scn_1198") {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestParseChunkRejectsAnErrorStatus(t *testing.T) {
	if _, err := parseChunk([]byte(`{"status":"error","text":"Invalid call."}`)); err == nil {
		t.Error("want an error")
	}
}

func serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/scenes":
			_, _ = fmt.Fprint(w, read(t, "page.html"))
		case r.URL.Path == "/_ajaxLoadScenes.php":
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				_, _ = fmt.Fprint(w, `{"status":"error","text":"Invalid call."}`)
				return
			}
			if r.URL.Query().Get("from") == "2" {
				_, _ = fmt.Fprint(w, read(t, "chunk.json"))
				return
			}
			_, _ = fmt.Fprint(w, read(t, "chunk_end.json"))
		case strings.HasPrefix(r.URL.Path, "/scene/"):
			_, _ = fmt.Fprint(w, read(t, "detail.html"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunEndToEnd(t *testing.T) {
	srv := serve(t)
	s := New()
	s.base = srv.URL
	ch, err := s.ListScenes(context.Background(), "https://www.stockydudes.com", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids[r.Scene.ID] = true
			if r.Scene.Description == "" || len(r.Scene.Categories) == 0 || !strings.HasPrefix(r.Scene.URL, srv.URL) {
				t.Errorf("scene %s: description/categories/url = %q/%v/%q", r.Scene.ID, r.Scene.Description, r.Scene.Categories, r.Scene.URL)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	// Two inline cards plus the two the "Show More" chunk adds; the fixtures
	// overlap on ids, so only distinct ones count.
	if len(ids) != 4 {
		t.Errorf("scenes = %v", ids)
	}
}

func TestRunStopsAtTheKnownPage(t *testing.T) {
	srv := serve(t)
	s := New()
	s.base = srv.URL
	ch, _ := s.ListScenes(context.Background(), "https://www.stockydudes.com", scraper.ListOpts{Workers: 1, KnownIDs: map[string]bool{"1197": true}, Delay: time.Millisecond})
	var scenes int
	var stopped bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped || scenes != 1 {
		t.Errorf("stopped=%v scenes=%d, want stopped after the inline page with 1 new scene", stopped, scenes)
	}
}

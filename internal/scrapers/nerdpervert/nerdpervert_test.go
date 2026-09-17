package nerdpervert

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
	for _, u := range []string{"https://nerdpervert.com/", "http://www.nerdpervert.com", "https://nerdpervert.com/tour/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://nerdperverts.com/", "https://example.com/nerdpervert.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func card(setID, slug, title, date string) string {
	return fmt.Sprintf(`<div class="item video-item">
	<h3 class="item-title"><a href="https://nerdpervert.com/tour/trailers/%s.html" onClick="VASuspend();">%s </a></h3>
	<a href="https://nerdpervert.com/tour/trailers/%s.html" title="%s" class="thumb">
		<span class="inner"><img id="set-target-%s" class="thumb stdimage" src="/tour/content//contentthumbs/57/58/5758-1x.jpg" src0_1x="/tour/content//contentthumbs/57/58/5758-1x.jpg" /></span>
	</a>
	<div class="info"><div class="left-info"></div><div class="right-info"><!-- %s --></div></div>
</div>`, slug, title, slug, title, setID, date)
}

func TestParseScenes(t *testing.T) {
	body := card("1272", "Jennifer-Leigh-Restart-Suck-video", "Jennifer Leigh &amp; Restart Suck", "2026-09-11")
	scenes := parseScenes(body, "https://nerdpervert.com/tour/", time.Now().UTC())
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1272" {
		t.Errorf("ID = %q, want the set id", sc.ID)
	}
	if sc.Title != "Jennifer Leigh & Restart Suck" {
		t.Errorf("Title = %q", sc.Title)
	}
	// The publish date is only ever rendered as an HTML comment.
	if got := sc.Date.Format("2006-01-02"); got != "2026-09-11" {
		t.Errorf("Date = %s", got)
	}
	if sc.Thumbnail != siteBase+"/tour/content//contentthumbs/57/58/5758-1x.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != siteBase+"/tour/trailers/Jennifer-Leigh-Restart-Suck-video.html" {
		t.Errorf("URL = %q", sc.URL)
	}
}

// The tour renders each episode twice (grid and rail).
func TestParseScenesDedupes(t *testing.T) {
	body := card("1", "a", "A", "2026-01-01") + card("1", "a", "A", "2026-01-01")
	if got := parseScenes(body, "x", time.Now()); len(got) != 1 {
		t.Errorf("got %d scenes, want 1", len(got))
	}
}

func TestListScenes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tour/" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, card("1", "a", "A", "2026-01-01")+card("2", "b", "B", "2026-01-02"))
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes, total int
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindScene:
			scenes++
			if !strings.HasPrefix(r.Scene.URL, siteBase) {
				t.Errorf("scene URL = %q, want a %s address", r.Scene.URL, siteBase)
			}
		}
	}
	if scenes != 2 || total != 2 {
		t.Errorf("got %d scenes (total %d), want 2", scenes, total)
	}
}

// A tour with no cards is a parse failure, not an empty catalogue.
func TestEmptyTourIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>redesigned</body></html>`)
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if kind := scraper.Classify(r.Err); kind != scraper.FailureParse {
				t.Errorf("failure kind = %v, want FailureParse", kind)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

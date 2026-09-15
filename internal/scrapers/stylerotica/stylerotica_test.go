package stylerotica

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
	for _, u := range []string{"https://www.stylerotica.com/", "http://stylerotica.com", "https://stylerotica.com/updates/x.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://styleroticax.com/", "https://example.com/stylerotica.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

const homepage = `<html><body>
<div class="updatesArea clear">
<div class="updateItem">
	<a href="https://www.stylerotica.com/updates/Pink-Lingerie.html">
		<img class="stdimage" src0_1x="content/Pink_Lingerie/1.jpg" />
	</a>
	<div class="updateDetails">
		<h4><a href="https://www.stylerotica.com/updates/Pink-Lingerie.html"> Pink &amp; Lingerie </a></h4>
		<p><span class="tour_update_models"><a href="https://www.stylerotica.com/models/Kato.html">Kato</a></span>
		<span class="availdate">09/11/2026</span></p>
	</div>
</div>
<div class="updateItem">
	<a href="https://www.stylerotica.com/updates/Pretty.html">
		<img class="stdimage" src0_1x="content/Pretty/1.jpg" />
	</a>
	<div class="updateDetails">
		<h4><a href="https://www.stylerotica.com/updates/Pretty.html">Pretty</a></h4>
		<p><span class="tour_update_models"><a href="https://www.stylerotica.com/models/Ava.html">Ava</a></span>
		<span class="availdate">08/28/2026</span></p>
	</div>
</div>
<div class="updateItem">
	<a href="https://www.stylerotica.com/updates/Pretty.html">
		<img class="stdimage" src0_1x="content/Pretty/1.jpg" />
	</a>
	<div class="updateDetails">
		<h4><a href="https://www.stylerotica.com/updates/Pretty.html">Pretty</a></h4>
		<p><span class="availdate">08/28/2026</span></p>
	</div>
</div>
</div></body></html>`

// The carousel repeats cards, so the same scene appears more than once.
func TestParseScenesDedupesTheCarousel(t *testing.T) {
	scenes := parseScenes(homepage, "https://www.stylerotica.com/", time.Now().UTC())
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (the third card repeats the second)", len(scenes))
	}
	first := scenes[0]
	if first.ID != "Pink-Lingerie" || first.SiteID != siteID {
		t.Errorf("ID/SiteID = %q/%q", first.ID, first.SiteID)
	}
	if first.Title != "Pink & Lingerie" {
		t.Errorf("Title = %q, want the entity decoded", first.Title)
	}
	if strings.Join(first.Performers, "|") != "Kato" {
		t.Errorf("Performers = %v", first.Performers)
	}
	if got := first.Date.Format("2006-01-02"); got != "2026-09-11" {
		t.Errorf("Date = %s", got)
	}
	if first.Thumbnail != siteBase+"/content/Pink_Lingerie/1.jpg" {
		t.Errorf("Thumbnail = %q", first.Thumbnail)
	}
	if first.URL != siteBase+"/updates/Pink-Lingerie.html" {
		t.Errorf("URL = %q", first.URL)
	}
}

func TestListScenes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, homepage)
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
		}
	}
	if scenes != 2 || total != 2 {
		t.Errorf("got %d scenes (total hint %d), want 2", scenes, total)
	}
}

// A homepage with no cards is a parse failure, not an empty catalogue — the
// difference decides whether an authoritative save may delete.
func TestEmptyHomepageIsAParseError(t *testing.T) {
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

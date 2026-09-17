package cademaddox

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
	for _, u := range []string{"https://cademaddox.com/", "http://www.cademaddox.com", "https://cademaddox.com/updates/x.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://cademaddoxx.com/", "https://example.com/cademaddox.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func card(setID, slug, title, date, runtime string) string {
	return fmt.Sprintf(`<div class="category_listing_wrapper_updates_custom">
<div style="cursor: pointer;" class="updateDetails">
	<a href="https://cademaddox.com/updates/%s.html">
		<img alt="x" id="set-target-%s" class="update_thumb thumbs stdimage" src="https://cdn.example.com/%s-1x.jpg" src0_1x="https://cdn.example.com/%s-1x.jpg" />
		<h4> %s</h4>
		<div class="details">
			<span style="display: inline-block;" class="availdate">%s</span>
			<span style="display: inline-block; float: right;" class="availdate">%s min</span>
		</div>
	</a>
</div></div>`, slug, setID, setID, setID, title, date, runtime)
}

func TestParseScenes(t *testing.T) {
	body := card("232", "Cock-Sucking-Compilation", "Cock Sucking &amp; Compilation", "Sep 14, 2026", "39:39")
	scenes := parseScenes(body, "https://cademaddox.com/", time.Now().UTC())
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0]
	// The set id is stable; the update slug is the title and moves with it.
	if sc.ID != "232" {
		t.Errorf("ID = %q, want the set id", sc.ID)
	}
	if sc.Title != "Cock Sucking & Compilation" {
		t.Errorf("Title = %q", sc.Title)
	}
	if got := sc.Date.Format("2006-01-02"); got != "2026-09-14" {
		t.Errorf("Date = %s", got)
	}
	// Date and runtime share the same `availdate` class; only one is a date.
	if sc.Duration != 2379 {
		t.Errorf("Duration = %d, want 2379", sc.Duration)
	}
	if sc.Thumbnail == "" || sc.URL != "https://cademaddox.com/updates/Cock-Sucking-Compilation.html" {
		t.Errorf("Thumbnail/URL = %q/%q", sc.Thumbnail, sc.URL)
	}
}

func TestParseScenesFallsBackToTheSlug(t *testing.T) {
	body := `<div class="updateDetails"><a href="https://cademaddox.com/updates/No-Heading-Here.html"><img src="x.jpg" /></a></div>`
	scenes := parseScenes(body, "https://cademaddox.com/", time.Now().UTC())
	if len(scenes) != 1 || scenes[0].Title != "No Heading Here" {
		t.Fatalf("scenes = %+v", scenes)
	}
	if scenes[0].ID != "No-Heading-Here" {
		t.Errorf("ID = %q, want the slug when no set id is present", scenes[0].ID)
	}
}

func TestListScenesWalksUntilEmpty(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		switch {
		case strings.Contains(r.URL.Path, "videos_1_d"):
			_, _ = fmt.Fprint(w, card("1", "one", "One", "Sep 1, 2026", "10:00")+card("2", "two", "Two", "Sep 2, 2026", "11:00"))
		case strings.Contains(r.URL.Path, "videos_2_d"):
			_, _ = fmt.Fprint(w, card("3", "three", "Three", "Sep 3, 2026", "12:00"))
		default:
			_, _ = fmt.Fprint(w, `<html><body>no updates</body></html>`)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var ids []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		}
	}
	if strings.Join(ids, ",") != "1,2,3" {
		t.Errorf("ids = %v", ids)
	}
	if pages != 3 {
		t.Errorf("fetched %d pages, want 3", pages)
	}
}

// The tour re-serves its last page past the end, so a repeat must stop the walk.
func TestRepeatedPageEndsTheWalk(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages++
		_, _ = fmt.Fprint(w, card("1", "one", "One", "Sep 1, 2026", "10:00"))
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	var scenes int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 1 || pages > 2 {
		t.Errorf("got %d scenes over %d pages, want 1 over at most 2", scenes, pages)
	}
}

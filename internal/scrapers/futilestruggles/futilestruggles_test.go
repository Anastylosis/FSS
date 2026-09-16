package futilestruggles

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
	for _, u := range []string{"https://www.futilestruggles.com/trial/", "http://futilestruggles.com", "https://futilestruggles.com/trial/index.php?page=2"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://futilestruggle.com/", "https://example.com/futilestruggles.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The real card wraps its thumbnail in the same gallery link as its title, and
// separates the date from its cell with an HTML comment.
func card(id, title, models, runtime, date string) string {
	return fmt.Sprintf(`<div class="category_listing_wrapper_updates">
<div class="update_details" data-setid="%s">
	<!-- Update Thumbnail -->
	<a href="gallery.php?id=%s&type=vids" >
		<img class="update_thumb thumbs" src="images/p14.jpg" /><div class="videoloadline"></div>
	</a>
	<!-- Title -->
	<a href="gallery.php?id=%s&type=vids">%s</a>
	<br />
	<!-- List Of Models -->
	<span class="update_models"> <a href="sets.php?id=174">%s</a> </span>
	<div class="update_counts"> %s&nbsp;min&nbsp;of video </div>
	<div class="table"><div class="row">
		<div class="cell update_date">
		<!-- Date -->
		%s		</div>
	</div></div>
</div></div>`, id, id, id, title, models, runtime, date)
}

func TestParseScenes(t *testing.T) {
	body := card("2612", "Alice Maze - First Time Day 1 &amp; 2", "Alice Maze", "15", "09/16/2026")
	scenes := parseScenes(body, "https://www.futilestruggles.com/trial/", time.Now().UTC())
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "2612" || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", sc.ID, sc.SiteID, sc.Studio)
	}
	// The thumbnail link comes first and carries no text; the title is the
	// second link with the same href.
	if sc.Title != "Alice Maze - First Time Day 1 & 2" {
		t.Errorf("Title = %q", sc.Title)
	}
	if strings.Join(sc.Performers, "|") != "Alice Maze" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	// "15 min of video" is whole minutes.
	if sc.Duration != 900 {
		t.Errorf("Duration = %d, want 900", sc.Duration)
	}
	if got := sc.Date.Format("2006-01-02"); got != "2026-09-16" {
		t.Errorf("Date = %s", got)
	}
	if sc.Thumbnail != siteBase+tourPath+"/images/p14.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != siteBase+tourPath+"/gallery.php?id=2612&type=vids" {
		t.Errorf("URL = %q", sc.URL)
	}
}

func TestListScenesWalksUntilEmpty(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = fmt.Fprint(w, card("1", "One", "A", "10", "01/02/2026")+card("2", "Two", "B", "20", "01/03/2026"))
		case "2":
			_, _ = fmt.Fprint(w, card("3", "Three", "C", "30", "01/04/2026"))
		default:
			_, _ = fmt.Fprint(w, `<html><body>no updates</body></html>`)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL+"/trial/", scraper.ListOpts{})
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

// A tour that clamps past its last page re-serves it; without the dedup the
// walk would never end.
func TestRepeatedPageEndsTheWalk(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages++
		_, _ = fmt.Fprint(w, card("1", "One", "A", "10", "01/02/2026"))
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL+"/trial/", scraper.ListOpts{})
	var scenes int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if pages > 2 {
		t.Errorf("fetched %d pages — a repeated page must end the walk", pages)
	}
}

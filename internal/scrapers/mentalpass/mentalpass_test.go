package mentalpass

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

func testCfg() SiteConfig {
	return SiteConfig{SiteID: "bitchstop", Domain: "bitchstop.com", StudioName: "Bitch Stop"}
}

func TestMatchesURL(t *testing.T) {
	s := New(testCfg())
	for _, u := range []string{"https://www.bitchstop.com/", "http://bitchstop.com", "https://bitchstop.com/?next=11"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://czasting.com/", "https://example.com/bitchstop.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// ?next= counts scenes, not pages, so page 2 starts at 11.
func TestPageURL(t *testing.T) {
	s := New(testCfg())
	cases := map[int]string{
		1: "https://www.bitchstop.com/",
		2: "https://www.bitchstop.com/?next=11",
		3: "https://www.bitchstop.com/?next=21",
	}
	for page, want := range cases {
		if got := s.pageURL(page); got != want {
			t.Errorf("pageURL(%d) = %q, want %q", page, got, want)
		}
	}
}

func article(dir, heading, text string) string {
	return fmt.Sprintf(`<article>
	<h2>%s</h2>
	<div id="Foto"><img src="./category/%s//117-bitchstop-left.jpg" alt="x" /></div>
	<div id="Text"><div class="getAccess"><a href="http://join.bitchstop.com/">GET INSTANT ACCESS</a></div> %s </div>
</article>`, heading, dir, text)
}

func TestParseScenes(t *testing.T) {
	body := article("650-petra-16-117-bs", `Bitch STOP 117 - <a href="http://join.bitchstop.com/">Petra 16 </a>`, "Well boys and girls &amp; here we are.")
	scenes := parseScenes(body, testCfg(), "https://www.bitchstop.com/", time.Now().UTC())
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "650-petra-16-117-bs" || sc.SiteID != "bitchstop" || sc.Studio != "Bitch Stop" {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", sc.ID, sc.SiteID, sc.Studio)
	}
	if sc.Title != "Bitch STOP 117 - Petra 16" {
		t.Errorf("Title = %q", sc.Title)
	}
	if strings.Join(sc.Performers, "|") != "Petra 16" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	// The join call-to-action sits inside the description block and is not part
	// of the description.
	if sc.Description != "Well boys and girls & here we are." {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://www.bitchstop.com/category/650-petra-16-117-bs//117-bitchstop-left.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
}

// Czech GFS breaks its own name up with markup and wraps the heading in a
// comment, so the title has to survive both.
func TestParseHeadingWithMarkupInTheName(t *testing.T) {
	title, performers := parseHeading(`<span>Czech</span> <span>GFS</span> 038<a href="x">Tereza 10 </a>-->`)
	if title != "Czech GFS 038 Tereza 10" {
		t.Errorf("title = %q", title)
	}
	if strings.Join(performers, "|") != "Tereza 10" {
		t.Errorf("performers = %v", performers)
	}
}

func TestListScenesStopsAtTheEnd(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		switch r.URL.Query().Get("next") {
		case "":
			_, _ = fmt.Fprint(w, article("dir-1", `Bitch STOP 1 - <a href="j">A</a>`, "one"))
		case "11":
			_, _ = fmt.Fprint(w, article("dir-2", `Bitch STOP 2 - <a href="j">B</a>`, "two"))
		default:
			// Past the end the tour serves the shell with no articles.
			_, _ = fmt.Fprint(w, `<html><body><header>nothing</header></body></html>`)
		}
	}))
	defer srv.Close()

	s := New(testCfg())
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
	if strings.Join(ids, ",") != "dir-1,dir-2" {
		t.Errorf("ids = %v", ids)
	}
	if pages != 3 {
		t.Errorf("fetched %d pages, want 3 (the third is empty and ends the walk)", pages)
	}
}

// A tour that re-serves a page it already gave must not loop.
func TestRepeatedPageEndsTheWalk(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages++
		_, _ = fmt.Fprint(w, article("same-dir", `Bitch STOP 1 - <a href="j">A</a>`, "one"))
	}))
	defer srv.Close()

	s := New(testCfg())
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
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

func TestSitesRegistered(t *testing.T) {
	if len(sites) != 3 {
		t.Fatalf("got %d sites, want 3", len(sites))
	}
	for _, cfg := range sites {
		if cfg.SiteID == "" || cfg.Domain == "" || cfg.StudioName == "" {
			t.Errorf("incomplete config: %+v", cfg)
		}
	}
}

package baberotica

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

func fixturePage(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/scene.json")
	if err != nil {
		t.Fatal(err)
	}
	return `<html><head><script type="application/ld+json">` + string(b) + `</script></head><body></body></html>`
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://baberotica.com", "https://www.baberoticavr.com/", "http://baberoticavr.com/lucia-01/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://baberotica.net/", "https://example.com/baberotica.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestParseScene(t *testing.T) {
	scene, err := parseScene([]byte(fixturePage(t)), sites["baberoticavr"], "https://baberoticavr.com", "https://baberoticavr.com/lucia-01/", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if scene.ID != "lucia-01" || scene.SiteID != "baberoticavr" || scene.Studio != "BaberoticaVR" {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", scene.ID, scene.SiteID, scene.Studio)
	}
	if scene.Title != "Perky Boobed Lucia Plays With Her Tiny Pussy" || scene.Duration != 776 {
		t.Errorf("Title/Duration = %q/%d", scene.Title, scene.Duration)
	}
	if scene.Date.Format("2006-01-02") != "2026-06-06" {
		t.Errorf("Date = %v", scene.Date)
	}
	if strings.Join(scene.Performers, "|") != "Lucia" || scene.Description == "" || scene.Thumbnail == "" {
		t.Errorf("performers/desc/thumb = %v/%q/%q", scene.Performers, scene.Description, scene.Thumbnail)
	}
}

func TestParseSceneWithoutJSONLDIsAParseError(t *testing.T) {
	_, err := parseScene([]byte("<html></html>"), sites["baberotica"], "u", "https://baberotica.com/x/", time.Now())
	if err == nil || scraper.Classify(err) != scraper.FailureParse {
		t.Fatalf("err = %v", err)
	}
}

func TestSiteFor(t *testing.T) {
	if siteFor("https://www.baberoticavr.com/").id != "baberoticavr" || siteFor("https://baberotica.com").id != "baberotica" {
		t.Error("siteFor picked the wrong site")
	}
}

func TestRunEndToEnd(t *testing.T) {
	page := fixturePage(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-sitemap.xml":
			_, _ = fmt.Fprintf(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>%s/wp-sitemap-posts-vms_videos-1.xml</loc></sitemap><sitemap><loc>%s/wp-sitemap-posts-page-1.xml</loc></sitemap></sitemapindex>`, srv.URL, srv.URL)
		case "/wp-sitemap-posts-vms_videos-1.xml":
			_, _ = fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%s/lucia-01/</loc></url></urlset>`, srv.URL)
		case "/lucia-01/":
			_, _ = fmt.Fprint(w, page)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL
	ch, err := s.ListScenes(context.Background(), "https://baberoticavr.com", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.ID != "lucia-01" {
				t.Errorf("ID = %q", r.Scene.ID)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if scenes != 1 {
		t.Errorf("scenes = %d", scenes)
	}
}

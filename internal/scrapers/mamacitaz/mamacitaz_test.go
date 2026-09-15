package mamacitaz

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
	for _, u := range []string{
		"https://mamacitaz.com",
		"https://mamacitaz.com/videos.en.html",
		"https://chicasloca.com/",
		"https://carnedelmercado.com",
		"https://herbigass.com/",
		"https://operacionlimpieza.com",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://mamacitaz.net/", "https://example.com/chicasloca.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// Every sister domain is a channel on the hub; scraping one must fetch that
// channel rather than the whole network.
func TestChannelForDomain(t *testing.T) {
	cases := map[string]string{
		"https://chicasloca.com/":                    "chicas-loca",
		"https://carnedelmercado.com/videos.en.html": "carne-del-mercado",
		"https://herbigass.com":                      "her-big-ass",
		"https://operacionlimpieza.com/":             "operacion-limpieza",
		"https://mamacitaz.com/videos.en.html":       "",
	}
	for in, want := range cases {
		if got := channelForDomain(in); got != want {
			t.Errorf("channelForDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

const detailPage = `<html><head>
<meta property="og:image" content="https://cdn.example.com/poster.jpg">
<meta itemprop="duration" content="T29M58S">
<meta itemprop="uploadDate" content="2023-10-09T17:16:22+00:00">
<meta itemprop="description" content="Romanian beauty &amp; Spanish babe.">
</head><body>
<h1>Hot Euro Babes Fuck Each Other Outdoors</h1>
<a href="https://mamacitaz.com/channels/chicas-loca.en.html">Chicas Loca</a> presents
<a href="https://mamacitaz.com/models/julia-de-lucia.en.html">Julia De Lucia</a>,
<a href="https://mamacitaz.com/models/gigi-love.en.html">Gigi Love</a>
<a href="https://mamacitaz.com/models/gigi-love.en.html">Gigi Love</a>
<div class="tags">
<a href="https://mamacitaz.com/tags/crazy-sex.en.html">Crazy Sex</a>
<a href="https://mamacitaz.com/tags/dyke.en.html">Dyke</a>
</div>
</body></html>`

func TestParseScene(t *testing.T) {
	scene, err := parseScene(detailPage, sceneRef{id: "1624", slug: "hot-euro-babes"}, "https://chicasloca.com/", time.Now().UTC())
	if err != nil {
		t.Fatalf("parseScene: %v", err)
	}
	if scene.ID != "1624" || scene.SiteID != siteID {
		t.Errorf("ID/SiteID = %q/%q", scene.ID, scene.SiteID)
	}
	if scene.Title != "Hot Euro Babes Fuck Each Other Outdoors" {
		t.Errorf("Title = %q", scene.Title)
	}
	// The channel is the real studio: five StashDB studios share this tour.
	if scene.Studio != "Chicas Loca" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Duration != 1798 {
		t.Errorf("Duration = %d, want 1798", scene.Duration)
	}
	if got := scene.Date.Format("2006-01-02"); got != "2023-10-09" {
		t.Errorf("Date = %s", got)
	}
	if strings.Join(scene.Performers, "|") != "Julia De Lucia|Gigi Love" {
		t.Errorf("Performers = %v, want both, de-duplicated", scene.Performers)
	}
	if scene.Description != "Romanian beauty & Spanish babe." {
		t.Errorf("Description = %q", scene.Description)
	}
	if strings.Join(scene.Tags, "|") != "crazy sex|dyke" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
}

func TestParseSceneWithoutTitleIsAnError(t *testing.T) {
	if _, err := parseScene(`<html><body></body></html>`, sceneRef{id: "1"}, "https://mamacitaz.com", time.Now()); err == nil {
		t.Error("want an error when the title is gone")
	}
}

func TestListScenesWalksAndStops(t *testing.T) {
	var listPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/watch/"):
			_, _ = fmt.Fprint(w, detailPage)
		case r.URL.Path == "/channels/chicas-loca.en.html":
			listPages++
			if r.URL.Query().Get("page") == "1" {
				_, _ = fmt.Fprint(w, `<a href="/watch/1/one.en.html">1</a><a href="/watch/2/two.en.html">2</a><a href="/watch/1/one.en.html">dupe</a>`)
				return
			}
			_, _ = fmt.Fprint(w, `<html>no scenes</html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://chicasloca.com/", scraper.ListOpts{Workers: 2})
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
			if !strings.HasPrefix(r.Scene.URL, siteBase+"/watch/") {
				t.Errorf("scene URL = %q, want a %s address", r.Scene.URL, siteBase)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("got %d scenes (%v), want 2 — the repeated card is one scene", len(ids), ids)
	}
	if listPages != 2 {
		t.Errorf("fetched %d listing pages, want 2", listPages)
	}
}

package ericvideos

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
	for _, u := range []string{"https://www.ericvideos.com/", "http://ericvideos.com", "https://www.ericvideos.com/EN/vod/1/page2"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://ericvideo.com/", "https://example.com/ericvideos.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func listingCard(id, slug, title, runtime string) string {
	return fmt.Sprintf(`<div id="desc_%s" class="desc hidden"><div class="text">A description for %s</div>
	<a href="/EN/vod/1/%s/%s" class="lien_block">Watch</a></div>
<div class="vod-item col-12">
	<a href="/EN/vod/1/%s/%s" class="lien_block" title="%s">
		<div class="panel-body">
			<div class="vod_image_block"><div class="video_titre"><h3>%s</h3></div>
			<img src="/medias-cache/imgvid/2/380x272/%s_0.jpg" class="img-fluid" /></div>
			<div class="infos"><span class="duree"><span class="icon-clock"></span> %s min</span></div>
		</div>
	</a>
</div>`, id, id, id, slug, id, slug, title, title, id, runtime)
}

func TestParseCards(t *testing.T) {
	body := listingCard("623", "even-starts-teasing", "Even starts teasing &amp; more", "17")
	cards := parseCards(body)
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	c := cards[0]
	if c.id != "623" || c.slug != "even-starts-teasing" {
		t.Errorf("id/slug = %q/%q", c.id, c.slug)
	}
	if c.title != "Even starts teasing & more" {
		t.Errorf("title = %q", c.title)
	}
	// The runtime is whole minutes.
	if c.duration != 1020 {
		t.Errorf("duration = %d, want 1020", c.duration)
	}
	// The description block is rendered outside the card it belongs to.
	if c.description != "A description for 623" {
		t.Errorf("description = %q", c.description)
	}
	if c.thumb == "" {
		t.Error("thumb is empty")
	}
}

func TestListScenesWalksPagesAndResolvesCast(t *testing.T) {
	var gateHits, actorPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/warning-all.php"):
			gateHits++
			_, _ = fmt.Fprint(w, "ok")
		case r.URL.Path == "/EN/vod/1/page1":
			_, _ = fmt.Fprint(w, listingCard("1", "one", "One", "10")+listingCard("2", "two", "Two", "20"))
		case r.URL.Path == "/EN/vod/1/page2":
			_, _ = fmt.Fprint(w, `<html>no cards</html>`)
		case r.URL.Path == "/EN/acteurs/":
			_, _ = fmt.Fprint(w, `<a href="/EN/acteurs/11/Tiago">Tiago</a>`)
		case strings.HasPrefix(r.URL.Path, "/EN/acteurs/11/"):
			actorPages++
			_, _ = fmt.Fprint(w, `<a href="/EN/vod/1/1/one">One</a>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client.Transport = srv.Client().Transport
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	cast := map[string][]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			cast[r.Scene.ID] = r.Scene.Performers
			if !strings.HasPrefix(r.Scene.URL, siteBase+"/EN/vod/") {
				t.Errorf("scene URL = %q, want a %s address", r.Scene.URL, siteBase)
			}
		}
	}
	if len(cast) != 2 {
		t.Fatalf("got %d scenes, want 2", len(cast))
	}
	if strings.Join(cast["1"], "|") != "Tiago" {
		t.Errorf("scene 1 cast = %v", cast["1"])
	}
	if len(cast["2"]) != 0 {
		t.Errorf("scene 2 cast = %v, want none", cast["2"])
	}
	if gateHits != 1 {
		t.Errorf("age gate hit %d times, want once", gateHits)
	}
	if actorPages != 1 {
		t.Errorf("fetched %d actor pages, want 1", actorPages)
	}
}

// A listing with no cards is a parse failure, not an empty catalogue.
func TestEmptyListingIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>redesigned</body></html>`)
	}))
	defer srv.Close()

	s := New()
	s.client.Transport = srv.Client().Transport
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

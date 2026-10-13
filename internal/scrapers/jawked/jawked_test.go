package jawked

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.jawked.com",
		"https://jawked.com/videos/",
		"https://www.jawked.com/models/avery-beck-279.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("want match: %s", u)
		}
	}
	for _, u := range []string{
		"https://www.notjawked.com/",
		"https://jawked.com.evil.test/",
		"https://www.brickyates.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("want no match: %s", u)
		}
	}
}

func TestModelSlug(t *testing.T) {
	cases := map[string]string{
		"https://www.jawked.com/models/avery-beck-279.html": "avery-beck-279",
		"https://www.jawked.com/videos/":                    "",
		"https://www.jawked.com/models/":                    "",
		"https://www.jawked.com/":                           "",
	}
	for in, want := range cases {
		if got := modelSlug(in); got != want {
			t.Errorf("modelSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "listing1.html"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3", len(cards))
	}
	c := cards[0]
	if c.id != "1271" || c.path != "/video/my-ass-can-take-it-1271.html" || c.title != "My Ass Can Take It" {
		t.Errorf("card = %+v", c)
	}
	if c.date.Format("2006-01-02") != "2026-10-09" {
		t.Errorf("date = %v", c.date)
	}
	if !strings.HasPrefix(c.thumb, "https://media.jawked.com/thumbs_pg/") {
		t.Errorf("thumb = %q", c.thumb)
	}
}

func TestParseListingSkipsGalleries(t *testing.T) {
	cards := parseListing(fixture(t, "model.html"))
	if len(cards) != 1 || cards[0].id != "1216" {
		t.Fatalf("cards = %+v", cards)
	}
}

func TestApplyDetail(t *testing.T) {
	sc := &models.Scene{}
	applyDetail(sc, fixture(t, "detail.html"))
	if strings.Join(sc.Performers, ",") != "Avery Beck,Dakota Lovell" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !strings.HasPrefix(sc.Description, "Avery Beck doesn't have to be very persuasive") || strings.Contains(sc.Description, "<") {
		t.Errorf("description = %q", sc.Description)
	}
	if !strings.HasSuffix(sc.Thumbnail, "001216-feat_lg.jpg") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *sync.Map) {
	t.Helper()
	hits := &sync.Map{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Store(r.URL.Path, r.Header.Get("Cookie"))
		var body string
		switch {
		case r.URL.Path == "/videos/":
			body = fixture(t, "listing1.html")
		case r.URL.Path == "/videos/page2.html":
			body = fixture(t, "listing2.html")
		case r.URL.Path == "/models/avery-beck-279.html":
			body = fixture(t, "model.html")
		case strings.HasPrefix(r.URL.Path, "/video/"):
			body = fixture(t, "detail.html")
		default:
			// The real site serves page 1 again for an out-of-range page.
			body = fixture(t, "listing1.html")
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) []models.Scene {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var out []models.Scene
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			out = append(out, r.Scene)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	return out
}

func TestListScenesWalksAllPages(t *testing.T) {
	srv, hits := newTestServer(t)
	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	url := "https://www.jawked.com/videos/"
	scenes := collect(t, s, url, scraper.ListOpts{Workers: 2})
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}
	first := scenes[0]
	if first.ID != "1271" || first.SiteID != "jawked" || first.Studio != "Jawked" || first.Title != "My Ass Can Take It" {
		t.Errorf("first = %+v", first)
	}
	if first.StudioURL != url {
		t.Errorf("StudioURL = %q", first.StudioURL)
	}
	if first.URL != srv.URL+"/video/my-ass-can-take-it-1271.html" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Date.Format("2006-01-02") != "2026-10-09" {
		t.Errorf("date = %v", first.Date)
	}
	if len(first.Performers) != 2 || first.Description == "" {
		t.Errorf("detail not applied: %+v", first)
	}
	if v, ok := hits.Load("/video/my-ass-can-take-it-1271.html"); !ok || v != "warningHidden=hide" {
		t.Errorf("cookie = %v", v)
	}
}

func TestListScenesModelPage(t *testing.T) {
	srv, hits := newTestServer(t)
	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	url := "https://www.jawked.com/models/avery-beck-279.html"
	scenes := collect(t, s, url, scraper.ListOpts{})
	if len(scenes) != 1 || scenes[0].ID != "1216" || scenes[0].StudioURL != url {
		t.Fatalf("scenes = %+v", scenes)
	}
	if _, ok := hits.Load("/videos/"); ok {
		t.Error("model mode fetched the main listing")
	}
}

func TestListScenesKnownIDsSkipsDetail(t *testing.T) {
	srv, hits := newTestServer(t)
	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://www.jawked.com/videos/", scraper.ListOpts{
		KnownIDs: map[string]bool{"1270": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if strings.Join(ids, ",") != "1271,1269" || !stopped {
		t.Errorf("ids = %v stopped = %v", ids, stopped)
	}
	if _, ok := hits.Load("/video/oliver-in-company-1270.html"); ok {
		t.Error("fetched detail for a known scene")
	}
	if _, ok := hits.Load("/videos/page2.html"); ok {
		t.Error("walked past the page holding a known scene")
	}
}

func TestListScenesCancelled(t *testing.T) {
	srv, _ := newTestServer(t)
	s := New()
	s.client = srv.Client()
	s.base = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch, _ := s.ListScenes(ctx, "https://www.jawked.com/videos/", scraper.ListOpts{})
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed after cancel")
	}
}

package flavaworks

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func siteByID(t *testing.T, id string) SiteConfig {
	t.Helper()
	for _, cfg := range sites {
		if cfg.SiteID == id {
			return cfg
		}
	}
	t.Fatalf("no site %q", id)
	return SiteConfig{}
}

const gatePage = `<html><body><form method="POST" action="/age-gate/confirm">
<input type="hidden" name="_token" value="Z1gtnq&amp;Y6ye" autocomplete="off">
<input type="hidden" name="intended" value="https://www.thugboy.com">
<button type="submit">I am 18 or older</button>
</form></body></html>`

func cardHTML(id, title, mins, dateStr, models string) string {
	return fmt.Sprintf(`<a href="/scene/%s" class="card">
    <div class="card-img-wrapper">
        <img class="card-img" src="https://flavamedia.b-cdn.net/scenes/%s/poster.jpg" alt="%s" loading="lazy">
        <span class="card-badge">HD</span>
        <span class="card-duration">%s</span>
    </div>
    <div class="card-body">
        <div class="card-title">%s</div>
        <div class="card-models">%s</div>
        <div class="card-meta flex items-center gap-1"><span>%s</span><span>&middot;</span></div>
    </div>
</a>`, id, id, title, mins, title, models, dateStr)
}

// crossPromoCard is the "Also on FlavaFlix.com" block: the same markup with an
// absolute href, pointing at another site's catalogue.
func crossPromoCard(id string) string {
	return fmt.Sprintf(`<a href="https://www.flavaflix.com/scene/%s" target="_blank" class="card">
    <div class="card-body"><div class="card-title">Elsewhere</div></div>
</a>`, id)
}

const detailPage = `<html><body><main>
<h1 class="scene-title">Lego</h1>
<div class="scene-description"><p>In Puerto Rico Leo seems to reign supreme &amp; then some.</p></div>
</main></body></html>`

func newTestServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var mu sync.Mutex
	gated := false
	var gateCalls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.URL.Path == "/age-gate/confirm" {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil || r.PostForm.Get("_token") != "Z1gtnq&Y6ye" {
				t.Errorf("age gate token = %q", r.PostForm.Get("_token"))
			}
			gateCalls++
			gated = true
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if !gated {
			_, _ = fmt.Fprint(w, gatePage)
			return
		}
		switch {
		case r.URL.Path == "/scenes" || strings.HasPrefix(r.URL.Path, "/model/"):
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = fmt.Fprint(w, `<div class="grid grid-4">`+
					cardHTML("10482", "Lego", "18:49", "Sep 15, 2026", "Lego")+
					cardHTML("10623", "Baby Boy + Rudy", "25:00", "Sep 01, 2026", "Baby Boy, Rudy")+
					crossPromoCard("99999")+`</div>`)
			case "2":
				_, _ = fmt.Fprint(w, `<div class="grid grid-4">`+cardHTML("10439", "Third", "10:00", "Aug 3, 2026", "Midnight")+`</div>`)
			default:
				_, _ = fmt.Fprint(w, `<div class="grid grid-4">`+crossPromoCard("99999")+`</div>`)
			}
		case strings.HasPrefix(r.URL.Path, "/scene/"):
			_, _ = fmt.Fprint(w, detailPage)
		default:
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		}
	}))
	return ts, &gateCalls
}

func newTestScraper(ts *httptest.Server, cfg SiteConfig) *Scraper {
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	return &Scraper{cfg: cfg, client: c, base: ts.URL,
		matchRe: New(cfg).matchRe}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs, stopped int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	return scenes, errs, stopped
}

func TestRegisteredSitesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.SiteID] {
			t.Errorf("duplicate site id %q", cfg.SiteID)
		}
		seen[cfg.SiteID] = true
		if cfg.Domain == "" || cfg.StudioName == "" {
			t.Errorf("site %q has an empty domain or studio name", cfg.SiteID)
		}
	}
	if len(sites) != 5 {
		t.Errorf("expected the 5 FlavaWorks sites, got %d", len(sites))
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(siteByID(t, "thugboy"))
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.thugboy.com/", true},
		{"https://thugboy.com", true},
		{"http://www.thugboy.com/model/263", true},
		{"https://thugboy.com.evil.net/", false},
		{"https://www.papicock.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestParseListingSkipsCrossPromoCards(t *testing.T) {
	page := `<div class="grid grid-4">` +
		cardHTML("10482", "Lego", "18:49", "Sep 15, 2026", "Lego") +
		crossPromoCard("99999") +
		cardHTML("10623", "Baby Boy + Rudy", "25:00", "Sep 01, 2026", "Baby Boy, Rudy") +
		`</div>`
	cards := parseListing(page)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].id != "10482" || cards[1].id != "10623" {
		t.Errorf("ids = %q, %q", cards[0].id, cards[1].id)
	}
	c := cards[0]
	if c.title != "Lego" {
		t.Errorf("title = %q", c.title)
	}
	if c.thumb != "https://flavamedia.b-cdn.net/scenes/10482/poster.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
	if c.duration != "18:49" {
		t.Errorf("duration = %q", c.duration)
	}
	if c.date != "Sep 15, 2026" {
		t.Errorf("date = %q", c.date)
	}
	if len(cards[1].models) != 2 || cards[1].models[0] != "Baby Boy" || cards[1].models[1] != "Rudy" {
		t.Errorf("models = %v", cards[1].models)
	}
}

func TestListScenes(t *testing.T) {
	ts, gateCalls := newTestServer(t)
	defer ts.Close()

	s := newTestScraper(ts, siteByID(t, "thugboy"))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if *gateCalls != 1 {
		t.Errorf("age gate posted %d times, want 1", *gateCalls)
	}

	sc := scenes[0]
	if sc.ID != "10482" || sc.SiteID != "thugboy" || sc.Studio != "Thug Boy" {
		t.Errorf("scene = %+v", sc)
	}
	if !strings.HasPrefix(sc.URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", sc.URL)
	}
	if sc.Duration != 1129 {
		t.Errorf("duration = %d, want 1129", sc.Duration)
	}
	if sc.Date.Format("2006-01-02") != "2026-09-15" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Description != "In Puerto Rico Leo seems to reign supreme & then some." {
		t.Errorf("description = %q", sc.Description)
	}
	// A padded and an unpadded day both parse.
	if scenes[1].Date.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("padded date = %v", scenes[1].Date)
	}
	if scenes[2].Date.Format("2006-01-02") != "2026-08-03" {
		t.Errorf("unpadded date = %v", scenes[2].Date)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	s := newTestScraper(ts, siteByID(t, "thugboy"))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"10623": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "10482" {
		t.Errorf("scenes = %v, want only 10482", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

// Every page is the gate itself until it is posted, so a first page with no
// cards would otherwise read as an empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/age-gate/confirm" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		_, _ = fmt.Fprint(w, `<html><body><div class="grid grid-4"></div></body></html>`)
	}))
	defer ts.Close()

	s := newTestScraper(ts, siteByID(t, "thugboy"))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if got := scraper.Classify(r.Err); got != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", got)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

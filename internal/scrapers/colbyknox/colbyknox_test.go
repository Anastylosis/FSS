package colbyknox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

func cardsHTML() string {
	card := func(slug, title, dur string) string {
		return fmt.Sprintf(`<a href="/video/%s" class="card card--video text-white">
  <div class="card-info p-2 p-lg-3">
    <h3 class="h6 mb-1 text-truncate">%s</h3>
    <div class="d-flex">
      <i class="icon-clock icon--md" aria-hidden="true"></i>
      <span>%s</span>
    </div>
  </div>
  <img class="img-fluid w-100" alt="%s" src="https://assetscdn.colbyknox.com/%s.jpg" />
  <i class="icon-play-video-lg card-play-icon"></i>
</a>`, slug, title, dur, title, slug)
	}
	return card("pup-poundtown", "Take a Pup to Poundtown", "23:12") +
		card("be-right-back", "Be Right Back", "18:05")
}

func listingJSON() string {
	b, _ := json.Marshal(map[string]string{"html": cardsHTML()})
	return string(b)
}

func detailHTML() string {
	return `<html><head>
<meta name="description" content="Good dogs need tender care in this ColbyKnox update.">
</head><body>
<a href="/models/colby-chambers" class="video-model d-flex mb-3">
  <div class="video-model-img me-3">
    <img src="https://assetscdn.colbyknox.com/colby.jpg" alt="Colby Chambers">
  </div>
  <div><h3 class="h6 mb-0">Colby Chambers</h3></div>
</a>
<a href="/models/mickey-knox" class="video-model d-flex mb-3">
  <div class="video-model-img me-3">
    <img src="https://assetscdn.colbyknox.com/mickey.jpg" alt="Mickey Knox">
  </div>
  <div><h3 class="h6 mb-0">Mickey Knox</h3></div>
</a>
</body></html>`
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.colbyknox.com/videos", true},
		{"https://colbyknox.com/videos/foo", true},
		{"https://example.com/x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestParseCards ----

func TestParseCards(t *testing.T) {
	items := parseCards(cardsHTML())
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2: %+v", len(items), items)
	}
	if items[0].slug != "pup-poundtown" || items[0].title != "Take a Pup to Poundtown" {
		t.Errorf("item0 = %+v", items[0])
	}
	if items[0].duration != 23*60+12 {
		t.Errorf("item0 duration = %d, want %d", items[0].duration, 23*60+12)
	}
	if !strings.HasSuffix(items[0].thumbnail, "pup-poundtown.jpg") {
		t.Errorf("item0 thumbnail = %q", items[0].thumbnail)
	}
}

// ---- TestFetchListing ----

func TestFetchListing(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			t.Errorf("missing X-Requested-With header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, listingJSON())
	}))
	defer ts.Close()
	siteBase = ts.URL

	s := &Scraper{Client: ts.Client()}
	items, err := s.fetchListing(context.Background(), 1)
	if err != nil {
		t.Fatalf("fetchListing error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
}

// ---- TestToScene ----

func TestToScene(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML())
	}))
	defer ts.Close()
	siteBase = ts.URL

	s := &Scraper{Client: ts.Client()}
	it := listItem{slug: "pup-poundtown", title: "Take a Pup to Poundtown", duration: 1392}
	now := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	sc := s.toScene(context.Background(), "studioURL", it, now)

	if sc.ID != "pup-poundtown" || sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("identity = %+v", sc)
	}
	if sc.URL != siteBase+"/video/pup-poundtown" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Duration != 1392 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if !strings.Contains(sc.Description, "tender care") {
		t.Errorf("Description = %q", sc.Description)
	}
	if strings.Join(sc.Performers, ",") != "Colby Chambers,Mickey Knox" {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

// ---- TestListScenes (end-to-end) ----

func TestListScenes(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/videos" && r.URL.Query().Get("page") == "1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, listingJSON())
		case r.URL.Path == "/videos":
			// page 2+ -> empty cards -> Done.
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"html":""}`)
		default:
			_, _ = fmt.Fprint(w, detailHTML())
		}
	}))
	defer ts.Close()
	siteBase = ts.URL

	s := &Scraper{Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "studioURL", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	got := map[string]string{}
	for r := range ch {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
			continue
		}
		if r.Kind == scraper.KindScene {
			got[r.Scene.ID] = r.Scene.Title
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(got), got)
	}
	if got["pup-poundtown"] != "Take a Pup to Poundtown" {
		t.Errorf("scenes = %v", got)
	}
}

// The tour moved scene links from /videos/{slug} to /video/{slug} (the old
// form 301s); cards in either form must parse.
func TestCardReAcceptsBothLinkForms(t *testing.T) {
	for _, href := range []string{"/video/movie-can-wait", "/videos/movie-can-wait"} {
		card := `<a href="` + href + `" class="card card--video text-white position-relative"><h3 class="h6 mb-1">T</h3></a>`
		m := cardRe.FindStringSubmatch(card)
		if m == nil || m[1] != "movie-can-wait" {
			t.Errorf("%s: got %v", href, m)
		}
	}
}

package defloration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

// feedHTML mirrors the real /freetour.php markup: a header card with a <video>
// + poster, and a model card with an <img> still + a story-full description.
func feedHTML() string {
	return `<html><body>
<main class="feed">
<article class="feed-card" data-item-id="lenajoy-video">
  <header class="feed-card__top"><strong>Defloration - Real Virgins</strong></header>
  <div class="story-block">
    <p class="story-preview">Watch genuine 18+ virgins lose their virginity.</p>
  </div>
  <video class="feed-media"
    src="https://openvideos.r.worldssl.net/s01sites_open/lenajoy_freetour1_506.mp4"
    poster="imgs/lenajoy_freetour1_poster.jpg" controls></video>
</article>
<article class="feed-card" data-item-id="margot-voland">
  <header class="feed-card__top"><strong>Margot &amp; Voland</strong></header>
  <div class="story-block">
    <p class="story-preview">I couldn&#039;t stop thinking...<button class="read-more">read more</button></p>
    <p class="story-full" hidden>I couldn&#039;t stop thinking about that day.<br />Some dreams fade.<button class="show-less">show less</button></p>
  </div>
  <img class="feed-media" src="imgs/margot_voland.jpg" alt="Margot Voland" loading="lazy">
</article>
<article class="feed-card">
  <header class="feed-card__top"><strong>No ID Card</strong></header>
</article>
</main>
</body></html>`
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.defloration.com/freetour.php?language=en", true},
		{"https://defloration.com/", true},
		{"http://defloration.com/freetour.php", true},
		{"https://example.com/x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestCleanText ----

func TestCleanText(t *testing.T) {
	got := cleanText(`  Hello&amp;world  <button>show less</button>  <br />done  `)
	if got != "Hello&world done" {
		t.Errorf("cleanText = %q", got)
	}
}

// ---- TestToScene ----

func TestToScene(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()
	siteBase = "https://www.defloration.com"

	cards := cardSplitRe.Split(feedHTML(), -1)[1:]
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards, got %d", len(cards))
	}

	now := time.Now().UTC()
	// Video card -> poster thumbnail.
	v, ok := toScene("studioURL", "LIST", cards[0], now)
	if !ok {
		t.Fatal("video card not parsed")
	}
	if v.ID != "lenajoy-video" {
		t.Errorf("ID = %q", v.ID)
	}
	if v.Title != "Defloration - Real Virgins" {
		t.Errorf("Title = %q", v.Title)
	}
	if v.URL != "LIST" {
		t.Errorf("URL = %q", v.URL)
	}
	if v.Studio != "Defloration" || v.SiteID != "defloration" {
		t.Errorf("Studio/SiteID = %q/%q", v.Studio, v.SiteID)
	}
	if v.Thumbnail != "https://www.defloration.com/imgs/lenajoy_freetour1_poster.jpg" {
		t.Errorf("Thumbnail = %q", v.Thumbnail)
	}

	// Img card -> still thumbnail, story-full description, entity-decoded title.
	m, ok := toScene("studioURL", "LIST", cards[1], now)
	if !ok {
		t.Fatal("img card not parsed")
	}
	if m.Title != "Margot & Voland" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Thumbnail != "https://www.defloration.com/imgs/margot_voland.jpg" {
		t.Errorf("Thumbnail = %q", m.Thumbnail)
	}
	want := "I couldn't stop thinking about that day. Some dreams fade."
	if m.Description != want {
		t.Errorf("Description = %q, want %q", m.Description, want)
	}

	// Card with no data-item-id is rejected.
	if _, ok := toScene("studioURL", "LIST", cards[2], now); ok {
		t.Error("card without item id should be rejected")
	}
}

// ---- TestListScenes (end-to-end) ----

func TestListScenes(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, feedHTML())
	}))
	defer ts.Close()
	siteBase = ts.URL

	s := &Scraper{Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "studioURL", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var total int
	got := map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindScene:
			got[r.Scene.ID] = r.Scene.Title
		}
	}
	if total != 3 {
		t.Errorf("progress total = %d, want 3", total)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(got), got)
	}
	if got["lenajoy-video"] == "" || got["margot-voland"] != "Margot & Voland" {
		t.Errorf("scenes = %v", got)
	}
}

// The feed serves an unknown language as English rather than refusing it, so
// the advertised set is what keeps a typo from silently storing English.
func TestListScenesRequestsLanguage(t *testing.T) {
	orig := siteBase
	defer func() { siteBase = orig }()

	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("language")
		_, _ = fmt.Fprint(w, feedHTML())
	}))
	defer ts.Close()
	siteBase = ts.URL

	for _, tc := range []struct{ requested, want string }{
		{"de", "de"},
		{"", "en"},
	} {
		s := &Scraper{Client: ts.Client()}
		ch, err := s.ListScenes(context.Background(), "studioURL", scraper.ListOpts{Language: tc.requested})
		if err != nil {
			t.Fatalf("ListScenes: %v", err)
		}
		for range ch { //nolint:revive // drain so the goroutine can finish its sends
		}
		if got != tc.want {
			t.Errorf("language=%q for requested %q, want %q", got, tc.requested, tc.want)
		}
	}
}

func TestLanguagesAreNormalizedTags(t *testing.T) {
	langs := New().Languages()
	if len(langs) == 0 || langs[0] != "en" {
		t.Fatalf("Languages() = %v, want en first", langs)
	}
	for _, l := range langs {
		if tag, err := scraper.NormalizeLanguage(l); err != nil || tag != l {
			t.Errorf("Languages() entry %q is not a normalized tag", l)
		}
	}
}

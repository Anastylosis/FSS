package darkreachupdatesutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

const listingHTML = `<html><body>

<div class="updates clear">
  <div class="model">
    <a href="https://join.clubkayden.com/signup/signup.php?nats=ABC">
      <img src="/content/OF-2024-01-31/0.jpg" alt="" />
    </a>
  </div>
  <div class="modelDetails">
    <h3><a href="https://join.clubkayden.com/signup/signup.php?nats=ABC">In my PJs</a></h3>
    <div class="date"></div>
    <p>Good Morning from me here at home in my Pajamas. <br /><a href="https://join.clubkayden.com/signup/signup.php?nats=ABC">join now</a></p>
  </div>
</div>

<div class="updates clear">
  <div class="model">
    <a href="https://join.clubkayden.com/signup/signup.php?nats=ABC">
      <img src="/content/OF-2024-01-26/0.jpg" alt="" />
    </a>
  </div>
  <div class="modelDetails">
    <h3><a href="https://join.clubkayden.com/signup/signup.php?nats=ABC">Rear View Mirror</a></h3>
    <p>Remember Mirror, Signal and then make your move. <br /><a href="…">join now</a></p>
  </div>
</div>

<div class="pagination">
  <a href="https://clubkayden.com/updates/page_2.html">2</a>
  <a href="https://clubkayden.com/updates/page_5.html">5</a>
</div>
</body></html>`

const emptyHTML = `<html><body></body></html>`

func TestParseListing(t *testing.T) {
	items := parseListing([]byte(listingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	first := items[0]
	if first.id != "OF-2024-01-31" {
		t.Errorf("ID = %q", first.id)
	}
	if first.title != "In my PJs" {
		t.Errorf("Title = %q", first.title)
	}
	if !strings.HasPrefix(first.description, "Good Morning from me") {
		t.Errorf("Description = %q", first.description)
	}
	if strings.Contains(first.description, "join now") {
		t.Errorf("Description should strip 'join now' CTA: %q", first.description)
	}
	if first.thumb != "/content/OF-2024-01-31/0.jpg" {
		t.Errorf("Thumb (raw) = %q", first.thumb)
	}
}

func TestParseListing_dedupes(t *testing.T) {
	items := parseListing([]byte(listingHTML + listingHTML))
	if len(items) != 2 {
		t.Errorf("got %d after dedup, want 2", len(items))
	}
}

func TestListingURL(t *testing.T) {
	s := New(SiteConfig{SiteBase: "https://example.com", MatchRe: regexp.MustCompile(`.*`)})
	if got := s.listingURL(1); got != "https://example.com/" {
		t.Errorf("page 1 = %q, want bare root", got)
	}
	if got := s.listingURL(2); got != "https://example.com/updates/page_2.html" {
		t.Errorf("page 2 = %q", got)
	}
}

func TestEstimateTotal(t *testing.T) {
	got := estimateTotal([]byte(listingHTML), 2)
	if got != 10 {
		t.Errorf("estimateTotal = %d, want 10 (max page 5 × 2)", got)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{MatchRe: regexp.MustCompile(`^https?://(?:www\.)?clubkayden\.com`)})
	if !s.MatchesURL("https://clubkayden.com/") {
		t.Error("should match")
	}
	if s.MatchesURL("https://example.com/") {
		t.Error("should not match")
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		_, _ = fmt.Fprint(w, emptyHTML)
	}))
	defer ts.Close()
	s := New(SiteConfig{
		ID: "clubkayden", SiteBase: ts.URL, Studio: "Club Kayden",
		MatchRe: regexp.MustCompile(`.*`),
	})
	studioURL := ts.URL + "/studio/"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			scenes++
			if r.Scene.StudioURL != studioURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, studioURL)
			}
			if !strings.HasPrefix(r.Scene.URL, ts.URL+"/#scene-") {
				t.Errorf("URL = %q (expected synthesised anchor)", r.Scene.URL)
			}
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	s := New(SiteConfig{
		ID: "clubkayden", SiteBase: ts.URL, Studio: "Club Kayden",
		MatchRe: regexp.MustCompile(`.*`),
	})
	ch, _ := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"OF-2024-01-26": true},
	})
	var scenes int
	var stopped bool
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if scenes != 1 {
		t.Errorf("got %d, want 1", scenes)
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

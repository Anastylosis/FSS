package collegeuniform

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

const listingHTML = `<html><body>
<div class="category_listing_block">
<div class="category_listing_wrapper_updates">

<div class="update_details" data-setid="3894">
  <a href="https://college-uniform.com/updates/Getting-it-On-Leya-Desantis.html">
    <img alt="LeyaDesantis004" class="stdimage update_thumb thumbs" src="content/LeyaDesantis004/1.jpg" src0_1x="content/LeyaDesantis004/1.jpg" />
  </a>
  <a href="https://college-uniform.com/updates/Getting-it-On-Leya-Desantis.html">Getting it On</a>
  <span class="update_models">
    <a href="https://college-uniform.com/models/Leya-Desantis.html">Leya Desantis</a>
  </span>
  <div class="update_counts">21&nbsp;min&nbsp;of video</div>
  <div class="table">
    <div class="row">
      <div class="cell update_date">05/27/2026</div>
    </div>
  </div>
</div>

<div class="update_details" data-setid="3895">
  <a href="https://college-uniform.com/updates/Almost-Summer.html">
    <img class="stdimage update_thumb thumbs" src0_1x="content/Almost-Summer/1.jpg" />
  </a>
  <a href="https://college-uniform.com/updates/Almost-Summer.html">Almost Summer</a>
  <span class="update_models">
    <a href="https://college-uniform.com/models/Alana-Chase.html">Alana Chase</a>
    <a href="https://college-uniform.com/models/Charley-Atwell.html">Charley Atwell</a>
  </span>
  <div class="update_counts">18&nbsp;min&nbsp;of video</div>
  <div class="cell update_date">05/19/2026</div>
</div>

</div></div>

<a href="categories/updates_5_d.html">5</a>
</body></html>`

const emptyHTML = `<html><body></body></html>`

func TestParseListing(t *testing.T) {
	items := parseListing([]byte(listingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	first := items[0]
	if first.id != "3894" {
		t.Errorf("ID = %q, want 3894", first.id)
	}
	if first.title != "Getting it On" {
		t.Errorf("Title = %q", first.title)
	}
	if first.duration != 21*60 {
		t.Errorf("Duration = %d, want 1260", first.duration)
	}
	if first.date.Year() != 2026 || first.date.Month() != 5 || first.date.Day() != 27 {
		t.Errorf("Date = %v", first.date)
	}
	if len(first.performers) != 1 || first.performers[0] != "Leya Desantis" {
		t.Errorf("Performers = %v", first.performers)
	}

	second := items[1]
	if len(second.performers) != 2 {
		t.Errorf("Second performers = %v", second.performers)
	}
}

func TestParseListing_dedupes(t *testing.T) {
	items := parseListing([]byte(listingHTML + listingHTML))
	if len(items) != 2 {
		t.Errorf("got %d after dedup, want 2", len(items))
	}
}

func TestListingURL(t *testing.T) {
	s := &Scraper{base: "https://example.com"}
	if got := s.listingURL(3); got != "https://example.com/categories/updates_3_d.html" {
		t.Errorf("page 3 = %q", got)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	if !s.MatchesURL("https://college-uniform.com/") {
		t.Error("should match")
	}
	if s.MatchesURL("https://example.com/") {
		t.Error("should not match")
	}
}

func TestEstimateTotal(t *testing.T) {
	got := estimateTotal([]byte(listingHTML), 2)
	if got != 10 {
		t.Errorf("estimateTotal = %d, want 10 (max page 5 × 2)", got)
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/categories/updates_1_d.html" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		_, _ = fmt.Fprint(w, emptyHTML)
	}))
	defer ts.Close()
	s := New()
	s.base = ts.URL

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
			// Fixture contains absolute college-uniform.com URLs (matches real
			// site markup); they survive the parse intact.
			if !strings.Contains(r.Scene.URL, "/updates/") {
				t.Errorf("URL missing /updates/: %q", r.Scene.URL)
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
		if r.URL.Path == "/categories/updates_1_d.html" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	s := New()
	s.base = ts.URL
	ch, _ := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"3895": true},
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

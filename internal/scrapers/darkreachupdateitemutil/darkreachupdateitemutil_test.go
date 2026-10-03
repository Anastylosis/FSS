package darkreachupdateitemutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

// Two card variants — spartavideo-style (bare h5) and angelasommers-style
// (h4 + duration + date) — plus one watchyoujerk (h5 + performers).

const listingHTML = `<html><body>

<div class="updateItem">
  <div class="updateThumb">
    <a href="https://spartavideo.com/updates/MilitaryMania-5.html">
      <img class="stdimage" src0_1x="content/MilitaryMania-5/1.jpg" />
    </a>
  </div>
  <div class="updateInfo">
    <h5><a href="https://spartavideo.com/updates/MilitaryMania-5.html">Military Mania Sc 5</a></h5>
  </div>
</div>

<div class="updateItem">
  <a href="https://www.angelasommers.com/updates/I-Want-You-To-Cum-With-Me.html">
    <img id="set-target-495" class="stdimage" src0_1x="/content/50/66/5066-1x.jpg" />
  </a>
  <div class="updateDetails">
    <h4><a href="https://www.angelasommers.com/updates/I-Want-You-To-Cum-With-Me.html">I Want You To Cum With Me</a></h4>
    <p>
      <i class="fas fa-file-video"></i> 10&nbsp;min<br />
      <i class="fas fa-calendar"></i> 08/25/2022
    </p>
  </div>
</div>

<div class="updateItem">
  <div class="updateThumb">
    <a href="http://watchyoujerk.com/updates/Sophie-Strauss-Titty-Twister.html">
      <img class="stdimage" src0_1x="content/wyj-sophie/1.jpg" />
    </a>
  </div>
  <div class="updateInfo">
    <h5><a href="http://watchyoujerk.com/updates/Sophie-Strauss-Titty-Twister.html">Sophie Strauss Titty Twister</a></h5>
    <span class="tour_update_models">
      <a href="http://watchyoujerk.com/models/SophieStrauss.html">Sophie Strauss</a>
    </span>
  </div>
</div>

<div class="global_pagination">
  <li class="active"><a href="https://spartavideo.com/categories/updates_1_d.html">1</a></li>
  <li><a href="https://spartavideo.com/categories/updates_2_d.html">2</a></li>
  <li><a href="https://spartavideo.com/categories/updates_8_d.html">8</a></li>
</div>
</body></html>`

const emptyHTML = `<html><body></body></html>`

func TestParseListing_handlesAllVariants(t *testing.T) {
	items := parseListing([]byte(listingHTML))
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}

	// 1. Spartavideo variant — bare h5, no date/duration/models.
	spv := items[0]
	if spv.id != "MilitaryMania-5" {
		t.Errorf("spartavideo ID = %q", spv.id)
	}
	if spv.title != "Military Mania Sc 5" {
		t.Errorf("spartavideo title = %q", spv.title)
	}
	if !spv.date.IsZero() {
		t.Errorf("spartavideo date should be zero, got %v", spv.date)
	}

	// 2. Angelasommers variant — h4 + duration + date.
	ang := items[1]
	if ang.title != "I Want You To Cum With Me" {
		t.Errorf("angelasommers title = %q", ang.title)
	}
	if ang.duration != 10*60 {
		t.Errorf("angelasommers duration = %d, want 600", ang.duration)
	}
	if ang.date.Year() != 2022 || ang.date.Month() != 8 || ang.date.Day() != 25 {
		t.Errorf("angelasommers date = %v, want 2022-08-25", ang.date)
	}

	// 3. Watchyoujerk variant — h5 + performers.
	wyj := items[2]
	if wyj.title != "Sophie Strauss Titty Twister" {
		t.Errorf("watchyoujerk title = %q", wyj.title)
	}
	if len(wyj.performers) != 1 || wyj.performers[0] != "Sophie Strauss" {
		t.Errorf("watchyoujerk performers = %v", wyj.performers)
	}
}

func TestParseListing_dottedDate(t *testing.T) {
	// HammerBoys renders dates as DD.MM.YYYY (dots) instead of MM/DD/YYYY.
	const html = `<div class="updateItem">
  <a href="/updates/Sexy-Lukas.html"><img class="thumbs stdimage" src="/content/Sexy_Lukas/1.jpg"></a>
  <h5><a href="/updates/Sexy-Lukas.html">Sexy Lukas Big</a></h5>
  <span class="availdate">08.06.2022</span>
</div>`
	items := parseListing([]byte(html))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	d := items[0].date
	if d.Year() != 2022 || d.Month() != 6 || d.Day() != 8 {
		t.Errorf("dotted date = %v, want 2022-06-08", d)
	}
}

func TestParseListing_dedupes(t *testing.T) {
	doubled := listingHTML + listingHTML
	items := parseListing([]byte(doubled))
	if len(items) != 3 {
		t.Errorf("got %d after dedup, want 3", len(items))
	}
}

func TestEstimateTotal(t *testing.T) {
	// 3 cards × max-page 8 = 24
	got := estimateTotal([]byte(listingHTML), 3)
	if got != 24 {
		t.Errorf("estimateTotal = %d, want 24", got)
	}
}

func TestListingURL(t *testing.T) {
	s := New(SiteConfig{SiteBase: "https://example.com", MatchRe: regexp.MustCompile(`.*`)})
	if got := s.listingURL(1); got != "https://example.com/categories/updates_1_d.html" {
		t.Errorf("page 1 = %q", got)
	}
	if got := s.listingURL(5); got != "https://example.com/categories/updates_5_d.html" {
		t.Errorf("page 5 = %q", got)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?spartavideo\.com`),
	})
	if !s.MatchesURL("https://spartavideo.com/") {
		t.Error("should match")
	}
	if s.MatchesURL("https://example.com/") {
		t.Error("should not match")
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

	s := New(SiteConfig{
		ID:       "spartavideo",
		SiteBase: ts.URL,
		Studio:   "Sparta Video",
		MatchRe:  regexp.MustCompile(`.*`),
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
		}
		if r.Kind == scraper.KindError {
			t.Errorf("error: %v", r.Err)
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3", scenes)
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
	s := New(SiteConfig{
		ID: "spartavideo", SiteBase: ts.URL, Studio: "Sparta Video",
		MatchRe: regexp.MustCompile(`.*`),
	})
	ch, _ := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"Sophie-Strauss-Titty-Twister": true},
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
	if scenes != 2 {
		t.Errorf("got %d, want 2", scenes)
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

func TestListScenes_modelPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/models/Sophie-Strauss.html":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID: "spartavideo", SiteBase: ts.URL, Studio: "Sparta Video",
		MatchRe: regexp.MustCompile(`.*`),
	})
	studioURL := ts.URL + "/models/Sophie-Strauss.html"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.StudioURL != studioURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, studioURL)
			}
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3", scenes)
	}
}

// Not every site on this template renders model pages as updateItem cards —
// bigbootytgirls.com uses an `update_table_right` block. Returning quietly
// there makes a model URL indistinguishable from a performer with no scenes,
// so an unparseable model page is reported.
func TestListScenes_modelPageWithNoCardsIsReported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body><div class="update_table_right">`+
			`<a href="/updates/Some-Scene.html">Some Scene</a></div></body></html>`)
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID: "spartavideo", SiteBase: ts.URL, Studio: "Sparta Video",
		MatchRe: regexp.MustCompile(`.*`),
	})
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/Nobody.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			t.Error("unexpected scene")
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	if len(errs) == 0 {
		t.Fatal("an unparseable model page reported no error")
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

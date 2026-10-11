package extrememoviepassutil

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

// Fixtures derived from real sexycuckold.com markup.

const listingHTML = `<html><body>
<div class="modelfeature  grabthis">
  <div class="modelimg">
    <div class="wrapper">
      <a href="https://join.sexycuckold.com/signup/signup.php?nats=ABC&amp;step=2" title="Watch busty teen cuckold fucked">
        <img id="set-target-99331-8821891" width="285" height="190"
             class="update_thumb thumbs stdimage"
             src0_1x="https://c80ee7697a.mjedge.net/tour//contentthumbs/93/31/99331-1x.jpg" />
        <div class="description">
          <p class="description_content"><i class="fa fa-clock-o"></i> 31 min &nbsp;
             <i class="fa fa-eye"></i> 14428 Views &nbsp;
             <i class="fa fa-thumbs-up"></i> 100  %</p>
        </div>
      </a>
    </div>
  </div>
  <div class="modeldata">
    <a href="https://join.sexycuckold.com/signup/signup.php?nats=ABC&amp;step=2" title="Sc 4k Stan005 Asya Murkovski 01" style="font-size:16px; line-height: 1.9em;">busty teen cuckold fucked</a>
    <p><i class="fa fa-calendar-check-o"></i> Date <font color="#48ff00">2026-05-28</font></p>
    <p>
      <span class="update_models">
        <a href="https://www.sexycuckold.com/tour/models/Asya-Murkovski.html">Asya Murkovski</a>
      </span>
    </p>
  </div>
</div>

<div class="modelfeature  grabthis">
  <div class="modelimg">
    <div class="wrapper">
      <a href="https://join.sexycuckold.com/signup/signup.php?nats=ABC&amp;step=2" title="Watch Cuckolding With A BBC">
        <img id="set-target-80658-6650672" class="update_thumb thumbs stdimage"
             src0_1x="https://c80ee7697a.mjedge.net/tour//contentthumbs/06/58/80658-1x.jpg" />
        <div class="description">
          <p class="description_content"><i class="fa fa-clock-o"></i> 36 min &nbsp;
             <i class="fa fa-eye"></i> 29329 Views</p>
        </div>
      </a>
    </div>
  </div>
  <div class="modeldata">
    <a href="https://join.sexycuckold.com/signup/signup.php?nats=ABC&amp;step=2" title="Sc 4k Fal003" style="font-size:16px;">Cuckolding With A BBC</a>
    <p><i class="fa fa-calendar-check-o"></i> Date <font color="#48ff00">2026-05-23</font></p>
    <p>
      <span class="update_models">
        <a href="https://www.sexycuckold.com/tour/models/teh-angel.html">Teh Angel</a>
        <a href="https://www.sexycuckold.com/tour/models/sissy.html">Sissy</a>
      </span>
    </p>
  </div>
</div>

<ul class="pagination">
  <li class="active"><a href="/tour/categories/movies/1/latest/">1</a></li>
  <li><a href="/tour/categories/movies/2/latest/">2</a></li>
  <li><a href="/tour/categories/movies/10/latest/">10</a></li>
</ul>
</body></html>`

const emptyListingHTML = `<html><body><div class="pagination">no more</div></body></html>`

// pornOnStageCardHTML mirrors live pornonstage.com markup — the modeldata div
// has a stray space ("modeldata" >), uses "Updated YYYY-MM-DD" instead of
// "Date <font>...</font>", and writes duration as MM:SS instead of just N.
const pornOnStageCardHTML = `<html><body>
<div class="modelfeature  grabthis">
  <div class="modelimg">
    <div class="wrapper">
      <a href="https://join.pornonstage.com/signup/signup.php?nats=ABC" title="Watch Babe loves live porn">
        <img id="set-target-15422-380217" class="update_thumb thumbs stdimage"
             src0_1x="/tour/content//contentthumbs/32/64/23264-1x.jpg" />
        <div class='description'>
          <p class='description_content' align="center"><i class="fa fa-clock-o"></i> 14:12 min &nbsp;
             <i class="fa fa-thumbs-up"></i> 80 %</p>
        </div>
      </a>
    </div>
  </div>
  <div class="modeldata" >
    <a href="https://join.pornonstage.com/signup/signup.php?nats=ABC" title="Sf Beograd05-17" style="font-size:16px; line-height: 1.9em;">Babe loves live porn</a>
    <p><i class="fa fa-calendar-check-o"></i> Updated 2026-05-28</p>
  </div>
</div>
</body></html>`

func testConfig(base string) SiteConfig {
	return SiteConfig{
		ID:       "sexycuckold",
		SiteBase: base,
		Studio:   "SexyCuckold",
		Patterns: []string{"sexycuckold.com"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?sexycuckold\.com`),
	}
}

func TestParseListing_extractsCards(t *testing.T) {
	items := parseListing([]byte(listingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.id != "99331" {
		t.Errorf("ID = %q, want 99331", first.id)
	}
	if first.title != "busty teen cuckold fucked" {
		t.Errorf("Title = %q", first.title)
	}
	if first.date.Year() != 2026 || first.date.Month() != 5 || first.date.Day() != 28 {
		t.Errorf("Date = %v, want 2026-05-28", first.date)
	}
	if first.duration != 31*60 {
		t.Errorf("Duration = %d, want %d (31 min)", first.duration, 31*60)
	}
	if first.views != 14428 {
		t.Errorf("Views = %d, want 14428", first.views)
	}
	if first.thumb != "https://c80ee7697a.mjedge.net/tour//contentthumbs/93/31/99331-1x.jpg" {
		t.Errorf("Thumb = %q", first.thumb)
	}
	if len(first.performers) != 1 || first.performers[0] != "Asya Murkovski" {
		t.Errorf("Performers = %v", first.performers)
	}

	second := items[1]
	if second.id != "80658" {
		t.Errorf("Second ID = %q", second.id)
	}
	if len(second.performers) != 2 {
		t.Errorf("Second performers = %v", second.performers)
	}
}

// TestParseListing_pornOnStageVariant pins live failures we hit on
// pornonstage.com: stray whitespace in `<div class="modeldata" >`, the
// "Updated YYYY-MM-DD" date phrasing, and MM:SS duration format.
func TestParseListing_pornOnStageVariant(t *testing.T) {
	items := parseListing([]byte(pornOnStageCardHTML))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.id != "15422" {
		t.Errorf("ID = %q, want 15422", it.id)
	}
	if it.title != "Babe loves live porn" {
		t.Errorf("Title = %q (modeldata-with-extra-space parse failed?)", it.title)
	}
	if it.date.Year() != 2026 || it.date.Month() != 5 || it.date.Day() != 28 {
		t.Errorf("Date = %v, want 2026-05-28 (Updated-form parse failed?)", it.date)
	}
	// 14:12 → 14 minutes 12 seconds = 852 seconds.
	if it.duration != 14*60+12 {
		t.Errorf("Duration = %d, want %d (MM:SS form parse failed?)", it.duration, 14*60+12)
	}
}

func TestParseListing_dedupes(t *testing.T) {
	doubled := listingHTML + listingHTML
	items := parseListing([]byte(doubled))
	if len(items) != 2 {
		t.Errorf("got %d items after dedup, want 2", len(items))
	}
}

func TestEstimateTotal(t *testing.T) {
	// 2 cards × max-page 10 = 20.
	got := estimateTotal([]byte(listingHTML), 2)
	if got != 20 {
		t.Errorf("estimateTotal = %d, want 20", got)
	}
}

func TestScraper_listingURL(t *testing.T) {
	s := New(testConfig("https://example.com"))
	tests := []struct {
		page int
		want string
	}{
		{1, "https://example.com/tour/categories/movies/1/latest/"},
		{2, "https://example.com/tour/categories/movies/2/latest/"},
		{50, "https://example.com/tour/categories/movies/50/latest/"},
	}
	for _, c := range tests {
		got := s.listingURL(c.page)
		if got != c.want {
			t.Errorf("page %d → %q, want %q", c.page, got, c.want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(testConfig("https://www.sexycuckold.com"))
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.sexycuckold.com/tour/", true},
		{"http://sexycuckold.com/", true},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case strings.HasPrefix(r.URL.Path, "/tour/categories/movies/1/latest"):
			_, _ = fmt.Fprint(w, listingHTML)
		case strings.HasPrefix(r.URL.Path, "/tour/categories/movies/"):
			// All other pages are empty — stop signal.
			_, _ = fmt.Fprint(w, emptyListingHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "sexycuckold",
		SiteBase: ts.URL,
		Studio:   "SexyCuckold",
		MatchRe:  regexp.MustCompile(`.*`),
	})

	studioURL := ts.URL + "/studio/"
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
			if r.Scene.Studio != "SexyCuckold" {
				t.Errorf("Studio = %q", r.Scene.Studio)
			}
			if !strings.HasPrefix(r.Scene.URL, ts.URL+"/tour/#scene-") {
				t.Errorf("URL = %q (expected synthesised /tour/#scene-{id})", r.Scene.URL)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if strings.HasPrefix(r.URL.Path, "/tour/categories/movies/1/latest") {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "sexycuckold",
		SiteBase: ts.URL,
		Studio:   "SexyCuckold",
		MatchRe:  regexp.MustCompile(`.*`),
	})

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"80658": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		scenes       int
		stoppedEarly bool
	)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if scenes != 1 {
		t.Errorf("got %d scenes, want 1 (stopped before known)", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

func TestListScenes_modelPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/tour/models/Asya-Murkovski.html":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "sexycuckold",
		SiteBase: ts.URL,
		Studio:   "SexyCuckold",
		MatchRe:  regexp.MustCompile(`.*`),
	})

	modelURL := ts.URL + "/tour/models/Asya-Murkovski.html"
	ch, err := s.ListScenes(context.Background(), modelURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.StudioURL != modelURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, modelURL)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

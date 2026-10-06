package ghostproclassic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// Fixture mirrors a real creampiethais.com /categories/updates_1_p.html
// listing: two cards with rich data, plus a pagination block.
const listingHTML = `<html><body>
<div class="latestUpdateB" data-setid="2008">
  <div class="videoPic">
    <a href="https://join.creampiethais.com/signup/signup.php">
      <img id="set-target-2008" class="update_thumb thumbs stdimage"
           src0_1x="/content//contentthumbs/41/62/54162-1x.jpg" />
    </a>
  </div>
  <div class="latestUpdateBinfo">
    <h4 class="link_bright">
      <a href="https://join.creampiethais.com/signup/signup.php">Sara</a>
    </h4>
    <p class="description-right">Amazing half Arab / half Thai big tittied Sara is a natural wonder. She is one of the best fucks of my life.</p>
    <p class="link_light">
      <i class="mlisti fa-solid fa-user text_med s_icon"></i>
      <a class="link_bright infolink" href="https://creampiethais.com/models/sara.html">Sara</a>
    </p>
    <ul class="videoInfo">
      <li class="text_med"><i class="fas fa-video"></i>24 min</li>
    </ul>
  </div>
</div>

<div class="latestUpdateB" data-setid="1816">
  <div class="videoPic">
    <a href="https://join.creampiethais.com/signup/signup.php">
      <img id="set-target-1816" class="update_thumb thumbs stdimage"
           src0_1x="/content//contentthumbs/39/15/53915-1x.jpg" />
    </a>
  </div>
  <div class="latestUpdateBinfo">
    <h4 class="link_bright">
      <a href="https://join.creampiethais.com/signup/signup.php">Mai</a>
    </h4>
    <p class="description-right">Big titty bargirl Mai is a nymphomaniac who lusts for a warm creampies up her cunt</p>
    <p class="link_light">
      <a class="link_bright infolink" href="https://creampiethais.com/models/mai.html">Mai</a>
    </p>
    <ul class="videoInfo">
      <li class="text_med"><i class="fas fa-video"></i>17 min</li>
    </ul>
  </div>
</div>

<div class="pagination">
  <a href="updates_1_p.html">1</a>
  <a href="updates_2_p.html">2</a>
  <a href="updates_10_p.html">»»</a>
</div>
</body></html>`

const emptyHTML = `<html><body><div class="pagination">no more</div></body></html>`

func testConfig(base string) SiteConfig {
	return SiteConfig{
		ID:       "creampiethais",
		SiteBase: base,
		SiteName: "Creampie Thais",
		Patterns: []string{"creampiethais.com/"},
		MatchRe:  regexp.MustCompile(`.*`),
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing([]byte(listingHTML), "https://creampiethais.com")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.id != "2008" {
		t.Errorf("ID = %q, want 2008", first.id)
	}
	if first.duration != 24*60 {
		t.Errorf("Duration = %d, want %d (24 min)", first.duration, 24*60)
	}
	if !strings.HasPrefix(first.description, "Amazing half Arab") {
		t.Errorf("Description = %q", first.description)
	}
	// Title synthesised from first sentence — should end after "wonder."
	if !strings.HasPrefix(first.title, "Amazing half Arab") || !strings.HasSuffix(first.title, "wonder.") {
		t.Errorf("Title = %q (expected first-sentence synthesis)", first.title)
	}
	if len(first.performers) != 1 || first.performers[0] != "Sara" {
		t.Errorf("Performers = %v", first.performers)
	}
	if first.thumb != "https://creampiethais.com/content//contentthumbs/41/62/54162-1x.jpg" {
		t.Errorf("Thumb = %q", first.thumb)
	}

	second := items[1]
	if second.id != "1816" {
		t.Errorf("Second ID = %q", second.id)
	}
	if second.duration != 17*60 {
		t.Errorf("Second duration = %d, want 1020", second.duration)
	}
}

func TestParseListing_dedupesAndDedupesPerformers(t *testing.T) {
	doubled := listingHTML + listingHTML
	items := parseListing([]byte(doubled), "https://x.example")
	if len(items) != 2 {
		t.Errorf("got %d items after dedup, want 2", len(items))
	}
}

func TestEstimateTotal(t *testing.T) {
	// pagination block has links to pages 1, 2 and 10 → max 10 × 2 cards = 20.
	got := estimateTotal([]byte(listingHTML), 2)
	if got != 20 {
		t.Errorf("estimateTotal = %d, want 20", got)
	}
}

func TestSynthesizeTitle(t *testing.T) {
	cases := []struct {
		desc, id, want string
	}{
		{"", "42", "Scene 42"},
		{"This is a complete sentence. And another.", "1", "This is a complete sentence."},
		{"Bang! Pow!", "2", "Bang!"},
		{
			"A very long single sentence with no punctuation that just goes on and on for a while still going",
			"3",
			"A very long single sentence with no punctuation that just goes on and on for a…",
		},
		{"Short.", "4", "Short."},
	}
	for _, c := range cases {
		got := synthesizeTitle(c.desc, c.id)
		if got != c.want {
			t.Errorf("synthesizeTitle(%q) = %q, want %q", c.desc, got, c.want)
		}
	}
}

func TestCleanHTML(t *testing.T) {
	in := `<p>Hello&nbsp;<strong>world</strong>.<br/>Line two</p>`
	want := "Hello world . Line two"
	if got := cleanHTML(in); got != want {
		t.Errorf("cleanHTML = %q, want %q", got, want)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?creampiethais\.com`),
	})
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://creampiethais.com/", true},
		{"http://www.creampiethais.com/categories/updates_2_p.html", true},
		{"https://asiansuckdolls.com/", false},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.ok {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.ok)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New(testConfig("https://creampiethais.com"))
	if got := s.listingURL(1); got != "https://creampiethais.com/categories/updates_1_p.html" {
		t.Errorf("page 1 → %q", got)
	}
	if got := s.listingURL(42); got != "https://creampiethais.com/categories/updates_42_p.html" {
		t.Errorf("page 42 → %q", got)
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/categories/updates_1_p.html":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			_, _ = fmt.Fprint(w, emptyHTML)
		}
	}))
	defer ts.Close()

	s := New(testConfig(ts.URL))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.StudioURL != ts.URL+"/" {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, ts.URL+"/")
			}
			if r.Scene.Studio != "Ghost Pro Productions" {
				t.Errorf("Studio = %q", r.Scene.Studio)
			}
			if r.Scene.Series != "Creampie Thais" {
				t.Errorf("Series = %q", r.Scene.Series)
			}
			if !strings.HasPrefix(r.Scene.URL, ts.URL+"/#scene-") {
				t.Errorf("URL = %q (expected scheme+host prefix + #scene-)", r.Scene.URL)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

// The listing has no date and no dependable order — creampiethais' first page
// runs set ids 2008, 1816, 1700, 1644, 10061, 10079 — so there is no position
// at which stopping early is safe. KnownIDs is ignored and every run re-walks.
func TestListScenes_knownIDsDoNotStopTheWalk(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/categories/updates_1_p.html" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		// Past the last page the template serves a card-free page, which is
		// the clean stop signal.
		_, _ = fmt.Fprint(w, `<html><body></body></html>`)
	}))
	defer ts.Close()

	s := New(testConfig(ts.URL))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		// The second card. A truncating stop would leave exactly one.
		KnownIDs: map[string]bool{"1816": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	var stoppedEarly bool
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
	if scenes != 2 {
		t.Errorf("got %d scenes, want both — the walk was truncated", scenes)
	}
	if stoppedEarly {
		t.Error("StoppedEarly was reported; this listing has no usable early stop")
	}
}

func TestSitesTable_uniqueIDsAndDomainIsolation(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.ID] {
			t.Errorf("duplicate ID: %q", cfg.ID)
		}
		seen[cfg.ID] = true
		for _, other := range sites {
			if other.ID == cfg.ID {
				continue
			}
			otherURL := other.SiteBase + "/"
			if cfg.MatchRe.MatchString(otherURL) {
				t.Errorf("site %q matched %s", cfg.ID, otherURL)
			}
		}
		if !cfg.MatchRe.MatchString(cfg.SiteBase + "/") {
			t.Errorf("site %q does not match its own SiteBase", cfg.ID)
		}
	}
	if len(sites) != 4 {
		t.Errorf("expected 4 sites, got %d", len(sites))
	}
}

func TestDedupStrings(t *testing.T) {
	got := dedupStrings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Config table integrity — see testutil.CheckSiteTable for what this catches and
// why a config-only table needs it even when the *util carries the parsing tests.
func TestSiteTableIntegrity(t *testing.T) {
	rows := make([]testutil.SiteRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.SiteRow{
			ID: c.ID, Base: c.SiteBase, Studio: c.SiteName,
			Patterns: c.Patterns, MatchRe: c.MatchRe,
		})
	}
	testutil.CheckSiteTable(t, rows)
}

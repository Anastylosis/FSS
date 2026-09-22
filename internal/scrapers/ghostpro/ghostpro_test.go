package ghostpro

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// Trimmed `__NEXT_DATA__` payload modelled on a real asiansuckdolls.com
// /videos response. Two items, both with non-trivial fields exercised in
// the assertions below.
const nextDataHTML = `<html><body>
<div id="__next">...</div>
<script id="__NEXT_DATA__" type="application/json">
{
  "props": {
    "pageProps": {
      "contents": {
        "total": 4,
        "page": 1,
        "per_page": 2,
        "total_pages": 2,
        "data": [
          {
            "id": 2076,
            "title": "Tak",
            "slug": "tak",
            "publish_date": "2026/05/24 00:00:00",
            "videos_duration": "19:08",
            "short_description": "Tak short.",
            "description": "Tak is a beautiful little Thai girl with a nice plump rump.",
            "thumb": "https://cdn.example/contentthumbs/83/70/8370-2x.jpg",
            "tags": ["Photos","Movies","AsianSuckDolls.com","Sites","Set Updates","Updates","Tour Updates","Asian","Thai","Blowjob"],
            "models": ["Tak"],
            "views": "49",
            "link": "https://join.asiansuckdolls.com/signup/signup.php?nats=ABC&step=2",
            "trailer_url": "https://cdn.example/trailer/tak.mp4"
          },
          {
            "id": 1459,
            "title": "Aon",
            "slug": "aon",
            "publish_date": "2026/05/17 00:00:00",
            "videos_duration": "1:02:30",
            "short_description": "",
            "description": "",
            "thumb": "https://cdn.example/contentthumbs/14/59/1459-2x.jpg",
            "tags": ["Photos","Movies"],
            "models": ["Aon", "Mai"],
            "views": "",
            "link": "https://join.asiansuckdolls.com/signup/signup.php?nats=ABC&step=2",
            "trailer_url": ""
          }
        ]
      },
      "order_by": "publish_date",
      "sort_by": "desc"
    }
  },
  "page": "/videos"
}
</script>
</body></html>`

// Page-2 payload — last page, just one item.
const nextDataPage2HTML = `<html><body>
<script id="__NEXT_DATA__" type="application/json">
{
  "props": {
    "pageProps": {
      "contents": {
        "total": 4,
        "page": 2,
        "per_page": 2,
        "total_pages": 2,
        "data": [
          {
            "id": 1000,
            "title": "Nim",
            "slug": "nim",
            "publish_date": "2026/05/03 00:00:00",
            "videos_duration": "12:34",
            "description": "Older scene.",
            "thumb": "https://cdn.example/nim.jpg",
            "tags": ["Tour Updates"],
            "models": ["Nim"],
            "views": "10",
            "link": "https://join.asiansuckdolls.com/signup/signup.php",
            "trailer_url": ""
          }
        ]
      }
    }
  }
}
</script>
</body></html>`

// Empty page (past end, defensive — total_pages should already cut us off).
const nextDataEmptyHTML = `<html><body>
<script id="__NEXT_DATA__" type="application/json">
{"props":{"pageProps":{"contents":{"total":4,"page":3,"per_page":2,"total_pages":2,"data":[]}}}}
</script>
</body></html>`

func testConfig(base string) SiteConfig {
	return SiteConfig{
		ID:       "asiansuckdolls",
		SiteBase: base,
		SiteName: "Asian Suck Dolls",
		Patterns: []string{"asiansuckdolls.com/"},
		MatchRe:  regexp.MustCompile(`.*`),
	}
}

func TestParseListing(t *testing.T) {
	c, err := parseListing([]byte(nextDataHTML))
	if err != nil {
		t.Fatal(err)
	}
	if c.Total != 4 || c.TotalPages != 2 {
		t.Errorf("total=%d total_pages=%d, want 4 / 2", c.Total, c.TotalPages)
	}
	if len(c.Data) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.Data))
	}
	first := c.Data[0]
	if first.ID != 2076 || first.Title != "Tak" {
		t.Errorf("entry 0 = %+v", first)
	}
	if first.PublishDate != "2026/05/24 00:00:00" {
		t.Errorf("PublishDate = %q", first.PublishDate)
	}
	if first.VideosDuration != "19:08" {
		t.Errorf("VideosDuration = %q", first.VideosDuration)
	}
	if len(first.Tags) != 10 {
		t.Errorf("tags len = %d", len(first.Tags))
	}
}

func TestToScene_fullFields(t *testing.T) {
	s := New(testConfig("https://asiansuckdolls.com"))
	c, _ := parseListing([]byte(nextDataHTML))
	scene := s.toScene(c.Data[0], testNow())

	if scene.ID != "2076" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Tak" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Studio != "Ghost Pro Productions" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Series != "Asian Suck Dolls" {
		t.Errorf("Series = %q, want %q", scene.Series, "Asian Suck Dolls")
	}
	if scene.URL != "https://asiansuckdolls.com/videos#scene-2076" {
		t.Errorf("URL = %q (expected synthesised /videos#scene-{id})", scene.URL)
	}
	if scene.Duration != 19*60+8 {
		t.Errorf("Duration = %d, want 1148 (19:08)", scene.Duration)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 5 || scene.Date.Day() != 24 {
		t.Errorf("Date = %v", scene.Date)
	}
	if scene.Views != 49 {
		t.Errorf("Views = %d, want 49", scene.Views)
	}
	// Description prefers the long form when both present.
	if !strings.HasPrefix(scene.Description, "Tak is a beautiful") {
		t.Errorf("Description = %q (should prefer long description)", scene.Description)
	}
	// Boilerplate tags ("Photos", "Movies", "AsianSuckDolls.com", ...) should be stripped.
	for _, tag := range scene.Tags {
		switch strings.ToLower(tag) {
		case "photos", "movies", "sites", "updates", "tour updates", "set updates":
			t.Errorf("boilerplate tag survived: %q", tag)
		}
		if strings.HasSuffix(strings.ToLower(tag), ".com") {
			t.Errorf("domain-tag survived: %q", tag)
		}
	}
	if len(scene.Tags) != 3 {
		t.Errorf("tags after cleaning = %v (want Asian, Thai, Blowjob)", scene.Tags)
	}
	if scene.Preview != "https://cdn.example/trailer/tak.mp4" {
		t.Errorf("Preview = %q", scene.Preview)
	}
}

func TestToScene_fallsBackToShortDescription(t *testing.T) {
	cfg := testConfig("https://asiansuckdolls.com")
	entry := sceneEntry{
		ID:               1,
		Title:            "x",
		ShortDescription: "just the short one",
	}
	s := New(cfg)
	scene := s.toScene(entry, testNow())
	if scene.Description != "just the short one" {
		t.Errorf("Description = %q (should fall back to short_description)", scene.Description)
	}
}

func TestToScene_durationVariants(t *testing.T) {
	cfg := testConfig("https://x.example")
	s := New(cfg)
	cases := []struct {
		dur  string
		want int
	}{
		{"19:08", 19*60 + 8},
		{"1:02:30", 3600 + 2*60 + 30},
		{"00:30", 30},
		{"", 0},
		{"bogus", 0},
	}
	for _, c := range cases {
		got := s.toScene(sceneEntry{ID: 1, VideosDuration: c.dur}, testNow()).Duration
		if got != c.want {
			t.Errorf("duration %q → %d, want %d", c.dur, got, c.want)
		}
	}
}

func TestParseListing_emptyNextData(t *testing.T) {
	if _, err := parseListing([]byte("<html>no next data</html>")); err == nil {
		t.Error("expected error when __NEXT_DATA__ missing")
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		ID:      "asiansuckdolls",
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?asiansuckdolls\.com`),
	})
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://asiansuckdolls.com/videos", true},
		{"http://www.asiansuckdolls.com/", true},
		{"https://example.com/", false},
		{"https://creampiethais.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.ok {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.ok)
		}
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.RawQuery {
		case "":
			_, _ = fmt.Fprint(w, nextDataHTML)
		case "page=2":
			_, _ = fmt.Fprint(w, nextDataPage2HTML)
		default:
			_, _ = fmt.Fprint(w, nextDataEmptyHTML)
		}
	}))
	defer ts.Close()

	cfg := SiteConfig{
		ID: "asiansuckdolls", SiteBase: ts.URL, SiteName: "Asian Suck Dolls",
		MatchRe: regexp.MustCompile(`.*`),
	}
	s := New(cfg)

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes, total int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3 (2 + 1 across two pages)", scenes)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, nextDataHTML)
	}))
	defer ts.Close()

	cfg := SiteConfig{
		ID: "asiansuckdolls", SiteBase: ts.URL, SiteName: "Asian Suck Dolls",
		MatchRe: regexp.MustCompile(`.*`),
	}
	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{
		// Second scene's ID — first should pass through, then stop.
		KnownIDs: map[string]bool{"1459": true},
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
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1 (stop before known)", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

func TestListingURL(t *testing.T) {
	s := New(SiteConfig{
		ID: "asiansuckdolls", SiteBase: "https://asiansuckdolls.com",
	})
	cases := []struct {
		page int
		want string
	}{
		{1, "https://asiansuckdolls.com/videos"},
		{2, "https://asiansuckdolls.com/videos?page=2"},
		{99, "https://asiansuckdolls.com/videos?page=99"},
	}
	for _, c := range cases {
		got := s.listingURL(c.page)
		if got != c.want {
			t.Errorf("page %d → %q, want %q", c.page, got, c.want)
		}
	}
}

// Registry sanity check — make sure all 9 sites in the table register
// distinct IDs and each MatchRe rejects the others' domains.
func TestSitesTable_uniqueIDsAndDomainIsolation(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.ID] {
			t.Errorf("duplicate ID in sites table: %q", cfg.ID)
		}
		seen[cfg.ID] = true
		// Each MatchRe must reject every OTHER site's canonical URL.
		for _, other := range sites {
			if other.ID == cfg.ID {
				continue
			}
			otherURL := other.SiteBase + "/videos"
			if cfg.MatchRe.MatchString(otherURL) {
				t.Errorf("site %q.MatchRe matched %s (should not)", cfg.ID, otherURL)
			}
		}
		// Self-match sanity check.
		if !cfg.MatchRe.MatchString(cfg.SiteBase + "/videos") {
			t.Errorf("site %q.MatchRe does not match its own SiteBase", cfg.ID)
		}
	}
	if len(sites) != 9 {
		t.Errorf("expected 9 registered sites, got %d", len(sites))
	}
}

func testNow() time.Time { return time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC) }

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

// The template is not exclusive to the Ghost Pro network: a site outside it
// names its own studio, and then has no series to record.
func TestStudioAndSeriesNaming(t *testing.T) {
	base := SiteConfig{ID: "x", SiteBase: "https://x.com", SiteName: "Tussinee"}
	s := New(base)
	sc := s.toScene(sceneEntry{ID: 1, Title: "T"}, time.Now().UTC())
	if sc.Studio != defaultStudioName {
		t.Errorf("studio = %q, want %q", sc.Studio, defaultStudioName)
	}
	if sc.Series != "Tussinee" {
		t.Errorf("series = %q", sc.Series)
	}

	own := SiteConfig{ID: "y", SiteBase: "https://y.com", SiteName: "Severe Sex Films", StudioName: "Severe Sex Films"}
	sc = New(own).toScene(sceneEntry{ID: 2, Title: "T"}, time.Now().UTC())
	if sc.Studio != "Severe Sex Films" {
		t.Errorf("studio = %q", sc.Studio)
	}
	if sc.Series != "" {
		t.Errorf("series = %q, want empty", sc.Series)
	}
}

// Only the sites that sell scenes individually quote a price; the Ghost Pro
// sites leave the field null and must record no snapshot.
func TestContentPriceIsOptional(t *testing.T) {
	s := New(SiteConfig{ID: "x", SiteBase: "https://x.com", SiteName: "X"})
	now := time.Now().UTC()

	if sc := s.toScene(sceneEntry{ID: 1, Title: "T"}, now); len(sc.PriceHistory) != 0 {
		t.Errorf("price history = %v, want none", sc.PriceHistory)
	}
	zero := 0.0
	if sc := s.toScene(sceneEntry{ID: 2, Title: "T", ContentPrice: &zero}, now); len(sc.PriceHistory) != 0 {
		t.Errorf("zero price recorded: %v", sc.PriceHistory)
	}
	fifteen := 15.0
	sc := s.toScene(sceneEntry{ID: 3, Title: "T", ContentPrice: &fifteen}, now)
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 15 {
		t.Errorf("price history = %v", sc.PriceHistory)
	}
}

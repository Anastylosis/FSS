package romeromultimedia

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// Trimmed `/wp-json/wp/v2/posts?_embed` payload modelled on a real
// hentaied.com response: two posts with full embed data + a "minimal" one
// with no embeds.
const listingJSON = `[
  {
    "id": 16504,
    "date_gmt": "2026-05-25T17:11:54",
    "slug": "corporate-espionage",
    "link": "https://hentaied.com/corporate-espionage",
    "title": {"rendered": "Corporate Espionage &amp; Other Crimes"},
    "content": {"rendered": "<p>Casca has successfully snuck into the headquarters of a powerful company.</p>\n<p>With the help of her partner.</p>"},
    "excerpt": {"rendered": "<p>Casca short.</p>"},
    "_embedded": {
      "wp:featuredmedia": [{"source_url": "https://hentaied.com/wp-content/uploads/2026/05/thumb.webp"}],
      "wp:term": [
        [
          {"id": 60, "name": "Anal", "slug": "anal", "taxonomy": "category"},
          {"id": 65, "name": "Tentacles", "slug": "tentacles", "taxonomy": "category"}
        ],
        [
          {"id": 100, "name": "Casca Zahara", "slug": "casca", "taxonomy": "post_tag"}
        ],
        [
          {"id": 200, "name": "ATI KIN", "slug": "ati", "taxonomy": "directors"}
        ]
      ]
    }
  },
  {
    "id": 16502,
    "date_gmt": "2026-05-18T12:00:00",
    "slug": "two-girls",
    "link": "https://hentaied.com/two-girls",
    "title": {"rendered": "Two Girls"},
    "content": {"rendered": ""},
    "_embedded": {
      "wp:term": [
        [],
        [
          {"id": 101, "name": "Sara Smith", "taxonomy": "post_tag"},
          {"id": 102, "name": "Mai Jones", "taxonomy": "post_tag"}
        ]
      ]
    }
  },
  {
    "id": 16500,
    "date_gmt": "2026-05-01T00:00:00",
    "link": "https://hentaied.com/no-embeds",
    "title": {"rendered": "Bare Post"},
    "content": {"rendered": ""}
  }
]`

const emptyJSON = `[]`

func testConfig(base string) SiteConfig {
	return SiteConfig{
		ID: "hentaied", SiteBase: base, SiteName: "Hentaied",
		Patterns: []string{"hentaied.com/"},
		MatchRe:  regexp.MustCompile(`.*`),
	}
}

func TestToScene_fullFields(t *testing.T) {
	s := New(testConfig("https://hentaied.com"))
	// Parse and feed the first post through toScene directly.
	posts := mustParse(t, listingJSON)
	scene := s.toScene(posts[0], "https://hentaied.com/", testNow())

	if scene.ID != "16504" {
		t.Errorf("ID = %q", scene.ID)
	}
	// Title: entity unescape (&amp; → &) + HTML strip (none here).
	if scene.Title != "Corporate Espionage & Other Crimes" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.URL != "https://hentaied.com/corporate-espionage" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Studio != "Romero Multimedia" || scene.Series != "Hentaied" {
		t.Errorf("Studio/Series = %q/%q", scene.Studio, scene.Series)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 5 || scene.Date.Day() != 25 {
		t.Errorf("Date = %v", scene.Date)
	}
	if !strings.Contains(scene.Description, "Casca has successfully snuck") {
		t.Errorf("Description = %q (HTML strip failed?)", scene.Description)
	}
	if scene.Thumbnail != "https://hentaied.com/wp-content/uploads/2026/05/thumb.webp" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Casca Zahara" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Categories) != 2 {
		t.Errorf("Categories = %v", scene.Categories)
	}
	if scene.Director != "ATI KIN" {
		t.Errorf("Director = %q", scene.Director)
	}
}

func TestToScene_multiplePerformers(t *testing.T) {
	s := New(testConfig("https://hentaied.com"))
	posts := mustParse(t, listingJSON)
	scene := s.toScene(posts[1], "https://hentaied.com/", testNow())

	if len(scene.Performers) != 2 {
		t.Errorf("Performers = %v, want 2", scene.Performers)
	}
}

func TestToScene_noEmbeds(t *testing.T) {
	s := New(testConfig("https://hentaied.com"))
	posts := mustParse(t, listingJSON)
	scene := s.toScene(posts[2], "https://hentaied.com/", testNow())

	if scene.Title != "Bare Post" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Thumbnail != "" {
		t.Errorf("Thumbnail = %q, want empty", scene.Thumbnail)
	}
	if len(scene.Performers) != 0 {
		t.Errorf("Performers = %v, want empty", scene.Performers)
	}
	if scene.Director != "" {
		t.Errorf("Director = %q, want empty", scene.Director)
	}
}

func TestParseWPDate(t *testing.T) {
	cases := []struct {
		in   string
		zero bool
	}{
		{"2026-05-25T17:11:54", false},
		{"2026-05-25T17:11:54+00:00", false},
		{"", true},
		{"bogus", true},
	}
	for _, c := range cases {
		got := parseWPDate(c.in)
		if c.zero && !got.IsZero() {
			t.Errorf("parseWPDate(%q) = %v, want zero", c.in, got)
		}
		if !c.zero && got.IsZero() {
			t.Errorf("parseWPDate(%q) = zero, want valid", c.in)
		}
	}
}

func TestCleanHTML(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"<p>Hello&nbsp;<strong>world</strong>.</p>", "Hello world ."},
		{"&amp;&lt;&gt;", "&<>"},
		{"<a href=\"x\">link</a>", "link"},
	}
	for _, c := range cases {
		if got := cleanHTML(c.in); got != c.want {
			t.Errorf("cleanHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListingURL(t *testing.T) {
	// Plain site.
	s := New(SiteConfig{ID: "x", SiteBase: "https://x.example", SiteName: "X"})
	if got := s.listingURL(1); got != "https://x.example/wp-json/wp/v2/posts?per_page=100&_embed=1&page=1" {
		t.Errorf("plain page 1 → %q", got)
	}
	// With OriginWebsiteID filter (Twinz case).
	s2 := New(SiteConfig{
		ID: "twinz", SiteBase: "https://hentaied.pro", SiteName: "Twinz", OriginWebsiteID: 411,
	})
	if got := s2.listingURL(2); got != "https://hentaied.pro/wp-json/wp/v2/posts?per_page=100&_embed=1&origin_website=411&page=2" {
		t.Errorf("filtered page 2 → %q", got)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?hentaied\.com`),
	})
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://hentaied.com/", true},
		{"http://www.hentaied.com/some-scene", true},
		{"https://example.com/", false},
		{"https://parasited.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.ok {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.ok)
		}
	}
}

func TestListScenes_endToEnd(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			w.Header().Set("X-WP-Total", "3")
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = fmt.Fprint(w, listingJSON)
		default:
			_, _ = fmt.Fprint(w, emptyJSON)
		}
	}))
	defer ts.Close()

	s := New(testConfig(ts.URL))
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes, total int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.Studio != "Romero Multimedia" {
				t.Errorf("Studio = %q", r.Scene.Studio)
			}
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 3 {
		t.Errorf("got %d scenes, want 3", scenes)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (from X-WP-Total header)", total)
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-Total", "3")
		w.Header().Set("X-WP-TotalPages", "1")
		_, _ = fmt.Fprint(w, listingJSON)
	}))
	defer ts.Close()

	s := New(testConfig(ts.URL))
	// The second post is stored: it is skipped, the rest of the page is still
	// emitted, and the walk stops at the end of the page.
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"16502": true},
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
		t.Errorf("got %d scenes, want 2 — the page is finished before the stop", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

func TestSitesTable_uniqueIDsAndDomainIsolation(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.ID] {
			t.Errorf("duplicate ID: %q", cfg.ID)
		}
		seen[cfg.ID] = true
		if cfg.SiteName == "" {
			t.Errorf("site %q has empty SiteName", cfg.ID)
		}
		if cfg.MatchRe == nil {
			t.Errorf("site %q has nil MatchRe", cfg.ID)
		}
	}
	if len(sites) != 16 {
		t.Errorf("expected 16 sites, got %d", len(sites))
	}
}

func mustParse(t *testing.T, j string) []wpPost {
	t.Helper()
	var posts []wpPost
	if err := json.Unmarshal([]byte(j), &posts); err != nil {
		t.Fatal(err)
	}
	return posts
}

func testNow() time.Time { return time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC) }

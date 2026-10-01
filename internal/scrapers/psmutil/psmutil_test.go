package psmutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// Fixture derived from real citebeur.com /en/videos JSON-LD. Two
// VideoObjects, with the raw newlines/tabs inside description that
// PornSiteManager emits (invalid JSON per RFC 8259 — the util patches them).
const listingHTML = `<html><head>
<title>Videos</title>
<script type="application/ld+json">
{
	"@context": "https://schema.org",
	"@type": "ItemList",
	"name": "New videos",
	"itemListElement":
	[
		{
			"@type": "ListItem",
			"position": 1,
			"item":
			{
				"@type": "VideoObject",
				"url": "https://www.citebeur.com/en/videos/detail/46311-hey-bro-let-s-fuck-him-together",
				"thumbnailUrl": "https://gcs.pornsitemanager.com/store/3/6/9/65a93527cb7d65dd160cc963/hd/img.jpg",
				"datePublished": "2026-05-27",
				"uploadDate": "2026-05-27T09:00:00-01:00",
				"description": "When Kalys arrives at the parking lot, he meets his buddy Choppeur — multi-line description
with embedded newlines that should survive JSON parsing.",
				"name": "Hey bro, let&#039;s fuck him together",
				"actor": [
					{"@type": "Person", "name": "Choppeur"},
					{"@type": "Person", "name": "Kévin Frenchboy"},
					{"@type": "Person", "name": "Kalys"}
				]
			}
		},
		{
			"@type": "ListItem",
			"position": 2,
			"item":
			{
				"@type": "VideoObject",
				"url": "https://www.citebeur.com/en/videos/detail/46310-another-one",
				"thumbnailUrl": "https://gcs.pornsitemanager.com/store/x/y/z/another/hd/img.jpg",
				"datePublished": "2026-05-20",
				"description": "Second scene description.",
				"name": "Another One",
				"actor": [
					{"@type": "Person", "name": "Solo Performer"}
				]
			}
		}
	]
}
</script>
</head><body>...</body></html>`

const emptyListingHTML = `<html><head>
<script type="application/ld+json">
{
	"@context": "https://schema.org",
	"@type": "ItemList",
	"name": "New videos",
	"itemListElement": []
}
</script>
</head><body>no videos</body></html>`

// past-end: PSM sometimes ships a different JSON-LD type (BreadcrumbList) on
// no-results pages and drops the ItemList entirely. parseListing must treat
// that as zero items (signal to stop), not an error.
const pastEndHTML = `<html><head>
<script type="application/ld+json">
{
	"@context": "https://schema.org",
	"@type": "BreadcrumbList",
	"itemListElement": []
}
</script>
</head><body>404</body></html>`

const noJSONLDHTML = `<html><head><title>Plain</title></head><body>no JSON-LD here</body></html>`

func testConfig(base string) SiteConfig {
	return SiteConfig{
		ID:       "citebeur",
		SiteBase: base,
		Studio:   "Citebeur",
		Locale:   "en",
		Patterns: []string{"citebeur.com"},
		MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?citebeur\.com`),
	}
}

func TestFixControlChars(t *testing.T) {
	in := `{"name": "foo
bar	baz", "@context": "https://schema.org"}`
	out := fixControlChars(in)
	if strings.Contains(out, "\n") || strings.Contains(out, "\t") {
		t.Errorf("raw control chars survived inside string: %q", out)
	}
	// Control chars OUTSIDE strings must be preserved (irrelevant for JSON but
	// ensures we don't over-escape).
	in2 := "{\"a\": 1}\n"
	if !strings.HasSuffix(fixControlChars(in2), "\n") {
		t.Error("trailing newline outside string was escaped")
	}
}

func TestParseListing_videoObjects(t *testing.T) {
	videos, err := parseListing([]byte(listingHTML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("got %d videos, want 2", len(videos))
	}

	v := videos[0]
	if v.URL != "https://www.citebeur.com/en/videos/detail/46311-hey-bro-let-s-fuck-him-together" {
		t.Errorf("URL = %q", v.URL)
	}
	if v.Name != "Hey bro, let&#039;s fuck him together" {
		t.Errorf("Name = %q", v.Name)
	}
	if !strings.Contains(v.Description, "embedded newlines") {
		t.Errorf("description didn't preserve content past newline: %q", v.Description)
	}
	if len(v.Actor) != 3 || v.Actor[0].Name != "Choppeur" {
		t.Errorf("Actors = %+v", v.Actor)
	}
}

func TestParseListing_emptyItemList(t *testing.T) {
	videos, err := parseListing([]byte(emptyListingHTML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(videos) != 0 {
		t.Errorf("got %d videos, want 0", len(videos))
	}
}

func TestParseListing_pastEndOrOtherJSONLD(t *testing.T) {
	videos, err := parseListing([]byte(pastEndHTML))
	if err != nil {
		t.Fatalf("non-ItemList JSON-LD must not error: %v", err)
	}
	if len(videos) != 0 {
		t.Errorf("got %d videos for past-end page, want 0", len(videos))
	}
}

func TestParseListing_noJSONLD(t *testing.T) {
	videos, err := parseListing([]byte(noJSONLDHTML))
	if err != nil {
		t.Fatalf("missing JSON-LD must not error: %v", err)
	}
	if len(videos) != 0 {
		t.Errorf("got %d videos for HTML with no JSON-LD, want 0", len(videos))
	}
}

// TestParseListing_htmlFallback pins the Citebeur category-page case: the
// page has no JSON-LD ItemList but does render the grid in plain HTML. A card
// links to its detail page twice; the title is the second link's text, while
// the image alt is the uploaded file's name and must not win.
const categoryHTMLFallback = `<html><body>
<div class="row">

<div class="col-12 col-md-6 col-lg-4">
		<a class="d-block" href="/en/videos/detail/53067-henry-hanson-and-the-king-of-cruising-berlin-parking">
		<div class="position-relative my-item">
			<div class="ratio ratio-16x9 rounded overflow-hidden">
				<img class="vc-img "
					alt="Capture d’écran 2026 09 24 à 19 39 20"
					src="https://images.gayvideo.network/store/2/2/0/6ab68f96c13509a1e40f3022/sd/capture-d-e-cran-2026-09-24-a-19-39-20.jpg" loading="lazy">
			</div>
		</div>
		</a>
		<!-- VIDEO NAME -->
		<a class="d-block h110 pt-3 pb-0 px-3 text-truncate text-center text-primary fw-bold" href="/en/videos/detail/53067-henry-hanson-and-the-king-of-cruising-berlin-parking">
									Henry Hanson and The King of Cruising — Berlin Parking
					</a>
</div>

<div class="col-12">
		<a href="https://www.frenchporn.fr/en/videos/detail/99999-cross-site-ad"><img alt="ad" src="https://images.gayvideo.network/ad.jpg"></a>
</div>

<div class="col-12 col-md-6 col-lg-4">
		<a class="d-block" href="/en/videos/detail/44742-the-good-neighbor-sucks-andolini">
				<img class="vc-img " alt="IMG_0042" src="https://images.gayvideo.network/store/x/y/z/abc/sd/good-neighbor.jpg" loading="lazy">
		</a>
		<a class="d-block text-primary fw-bold" href="/en/videos/detail/44742-the-good-neighbor-sucks-andolini">
			The Good Neighbor Sucks Andolini
		</a>
</div>

</div>
</body></html>`

func TestParseListing_htmlFallback(t *testing.T) {
	videos, err := parseListing([]byte(categoryHTMLFallback))
	if err != nil {
		t.Fatalf("HTML fallback must not error: %v", err)
	}
	if len(videos) != 2 {
		t.Fatalf("got %d videos from HTML fallback, want 2 (cross-site ad must be ignored)", len(videos))
	}
	first := videos[0]
	if first.URL != "/en/videos/detail/53067-henry-hanson-and-the-king-of-cruising-berlin-parking" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Name != "Henry Hanson and The King of Cruising — Berlin Parking" {
		t.Errorf("Name = %q, want the title link's text, not the image alt", first.Name)
	}
	if !strings.HasPrefix(first.ThumbnailURL, "https://images.gayvideo.network/") {
		t.Errorf("ThumbnailURL = %q", first.ThumbnailURL)
	}
	if !first.fromHTML {
		t.Error("HTML-fallback card not marked fromHTML")
	}
	if videos[1].Name != "The Good Neighbor Sucks Andolini" {
		t.Errorf("second Name = %q", videos[1].Name)
	}
}

// detailHTML is a scene detail page: its VideoObject carries the date, HD
// thumbnail and description a category card lacks, and its "text" field
// holds a raw newline, as PornSiteManager emits it.
const detailHTML = `<html><head>
<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "VideoObject",
			"name": "Henry Hanson and The King of Cruising — Berlin Parking",
			"url": "https://www.citebeur.com/en/videos/detail/53067-henry-hanson-and-the-king-of-cruising-berlin-parking",
			"thumbnailUrl": "https://images.gayvideo.network/store/2/2/0/6ab68f96c13509a1e40f3022/hd/capture.jpg",
			"datePublished": "2026-09-30",
			"uploadDate": "2026-09-30T09:00:00-01:00",
			"description": "In a Berlin car park, The King of Cruising deep-throats Henry Hanson&#039;s cock.",
			"text": "First paragraph.

Second paragraph.",
			"actor": [{"@type": "Person", "name": "Henry Hanson"}, {"@type": "Person", "name": "The King of Cruising"}],
			"author": {"@type": "Organization", "name": "Citebeur"}
		}
</script>
</head><body></body></html>`

func TestParseDetail(t *testing.T) {
	v, ok := parseDetail([]byte(detailHTML))
	if !ok {
		t.Fatal("no VideoObject parsed from detail page")
	}
	if v.DatePublished != "2026-09-30" || !strings.HasSuffix(v.ThumbnailURL, "/hd/capture.jpg") {
		t.Errorf("got date %q thumb %q", v.DatePublished, v.ThumbnailURL)
	}
	if len(v.Actor) != 2 {
		t.Errorf("got %d actors, want 2", len(v.Actor))
	}
	if _, ok := parseDetail([]byte(emptyListingHTML)); ok {
		t.Error("page without a VideoObject reported one")
	}
}

func TestExtractSceneID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.citebeur.com/en/videos/detail/46311-hey-bro-let-s-fuck-him-together", "46311"},
		{"/en/videos/detail/12345-slug-only", "12345"},
		// Non-numeric ID fallback — last path segment used verbatim.
		{"https://www.citebeur.com/en/modeles/detail/kevin-frenchboy", "kevin-frenchboy"},
		{"https://example.com/en/videos/detail/9", "9"},
	}
	for _, c := range tests {
		t.Run(c.url, func(t *testing.T) {
			got := extractSceneID(c.url)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCategoryFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.citebeur.com/", ""},
		{"https://www.citebeur.com/en/videos", ""},
		{"https://www.citebeur.com/en/videos?page=2", ""},
		{"https://www.citebeur.com/en/videos/arab-french", "arab-french"},
		{"https://www.citebeur.com/en/videos/arab-french?page=3", "arab-french"},
		{"https://www.citebeur.com/fr/videos/categorie", "categorie"},
	}
	for _, c := range tests {
		t.Run(c.url, func(t *testing.T) {
			got := categoryFromURL(c.url)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestScraper_listingURL(t *testing.T) {
	s := New(testConfig("https://example.com"))
	tests := []struct {
		category string
		page     int
		want     string
	}{
		{"", 1, "https://example.com/en/videos"},
		{"", 2, "https://example.com/en/videos?page=2"},
		{"arab-french", 1, "https://example.com/en/videos/arab-french"},
		{"arab-french", 5, "https://example.com/en/videos/arab-french?page=5"},
	}
	for _, c := range tests {
		got := s.listingURL(c.category, c.page)
		if got != c.want {
			t.Errorf("listingURL(%q, %d) = %q, want %q", c.category, c.page, got, c.want)
		}
	}
}

func TestNew_defaultsLocaleToEn(t *testing.T) {
	cfg := SiteConfig{ID: "x", SiteBase: "https://example.com", MatchRe: regexp.MustCompile(`.*`)}
	s := New(cfg)
	if s.cfg.Locale != "en" {
		t.Errorf("Locale = %q, want en (default)", s.cfg.Locale)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(testConfig("https://www.citebeur.com"))
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.citebeur.com/en/videos", true},
		{"http://citebeur.com/", true},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// End-to-end: paginates until we hit an empty page, emits Scenes.
func TestListScenes_endToEnd(t *testing.T) {
	hits := map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.String()]++
		w.Header().Set("Content-Type", "text/html")
		switch {
		case r.URL.Path == "/en/videos" && r.URL.RawQuery == "":
			_, _ = fmt.Fprint(w, listingHTML)
		case r.URL.Path == "/en/videos" && r.URL.Query().Get("page") == "2":
			_, _ = fmt.Fprint(w, emptyListingHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "citebeur",
		SiteBase: ts.URL,
		Studio:   "Citebeur",
		Locale:   "en",
		MatchRe:  regexp.MustCompile(`.*`),
	})

	ch, err := s.ListScenes(context.Background(), ts.URL+"/en/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var scenes int
	var titles []string
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			titles = append(titles, r.Scene.Title)
			if r.Scene.Studio != "Citebeur" {
				t.Errorf("Studio = %q", r.Scene.Studio)
			}
			if r.Scene.Date.Year() != 2026 {
				t.Errorf("Date not parsed: %v", r.Scene.Date)
			}
			if len(r.Scene.Performers) == 0 {
				t.Errorf("scene %q has no performers", r.Scene.Title)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if scenes != 2 {
		t.Errorf("got %d scenes, want 2 (titles=%v)", scenes, titles)
	}
	if hits["/en/videos"] == 0 {
		t.Errorf("page 1 was never fetched (hits=%+v)", hits)
	}
	if hits["/en/videos?page=2"] == 0 {
		t.Errorf("page 2 (empty) was never fetched (hits=%+v)", hits)
	}

	// First scene's title should have HTML entities un-escaped on the Scene.
	if !strings.Contains(titles[0], "let's fuck") {
		t.Errorf("title 0 = %q (expected HTML entity unescape)", titles[0])
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/en/videos" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "citebeur",
		SiteBase: ts.URL,
		Studio:   "Citebeur",
		MatchRe:  regexp.MustCompile(`.*`),
	})

	// Listing has IDs 46311 (first) and 46310 (second). Mark 46310 as known
	// → first scene emits, then StoppedEarly fires before 46310.
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"46310": true},
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
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

// TestListScenes_categoryEnrichesFromDetail drives a category page, whose
// cards come from the HTML fallback, through run: each card is completed
// from its detail page, and a detail page without JSON-LD is reported as a
// parse failure while the card is kept with its listing data.
func TestListScenes_categoryEnrichesFromDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/en/videos/arab-french-gay":
			if r.URL.Query().Get("page") == "" {
				_, _ = fmt.Fprint(w, categoryHTMLFallback)
				return
			}
			_, _ = fmt.Fprint(w, emptyListingHTML)
		case "/en/videos/detail/53067-henry-hanson-and-the-king-of-cruising-berlin-parking":
			_, _ = fmt.Fprint(w, detailHTML)
		case "/en/videos/detail/44742-the-good-neighbor-sucks-andolini":
			_, _ = fmt.Fprint(w, "<html><body>no structured data</body></html>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "citebeur",
		SiteBase: ts.URL,
		Studio:   "Citebeur",
		MatchRe:  regexp.MustCompile(`.*`),
	})
	ch, err := s.ListScenes(context.Background(), ts.URL+"/en/videos/arab-french-gay", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := map[string]models.Scene{}
	var parseErrs int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes[r.Scene.ID] = r.Scene
		case scraper.KindError:
			if scraper.Classify(r.Err) != scraper.FailureParse {
				t.Errorf("error not classified as a parse failure: %v", r.Err)
			}
			parseErrs++
		}
	}
	if len(scenes) != 2 || parseErrs != 1 {
		t.Fatalf("got %d scenes and %d parse errors, want 2 and 1", len(scenes), parseErrs)
	}

	good := scenes["53067"]
	if want := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC); !good.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", good.Date, want)
	}
	if !strings.Contains(good.Thumbnail, "/hd/") {
		t.Errorf("Thumbnail = %q, want the detail page's HD one", good.Thumbnail)
	}
	if !strings.Contains(good.Description, "Henry Hanson's") || len(good.Performers) != 2 {
		t.Errorf("Description = %q, Performers = %v", good.Description, good.Performers)
	}

	kept := scenes["44742"]
	if kept.Title != "The Good Neighbor Sucks Andolini" || !kept.Date.IsZero() {
		t.Errorf("failed-detail card: Title = %q, Date = %v", kept.Title, kept.Date)
	}
}

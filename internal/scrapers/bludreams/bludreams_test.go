package bludreams

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://bludreamsxxx.com", true},
		{"https://bludreamsxxx.com/", true},
		{"https://www.bludreamsxxx.com/access/", true},
		{"http://bludreamsxxx.com/access/categories/joi.html", true},
		{"https://bludreamsxxx.com/access/models/JewelzBlu.html", true},
		{"https://bludreamsxxx.example.com/", false},
		{"https://example.com/bludreamsxxx.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestClassifyURL(t *testing.T) {
	cases := []struct {
		url  string
		kind urlKind
		slug string
	}{
		{"https://bludreamsxxx.com/", kindUpdates, ""},
		{"https://bludreamsxxx.com/access/", kindUpdates, ""},
		{"https://bludreamsxxx.com/access/categories/movies.html", kindUpdates, ""},
		{"https://bludreamsxxx.com/access/categories/movies_3_d.html", kindUpdates, ""},
		{"https://bludreamsxxx.com/access/categories/joi.html", kindCategory, "joi"},
		{"https://bludreamsxxx.com/access/categories/joi_2_d.html", kindCategory, "joi"},
		{"https://bludreamsxxx.com/access/models/JewelzBlu.html", kindModel, "JewelzBlu"},
		{"https://bludreamsxxx.com/access/models/models.html", kindUpdates, ""},
	}
	for _, c := range cases {
		kind, slug := classifyURL(c.url)
		if kind != c.kind || slug != c.slug {
			t.Errorf("classifyURL(%q) = %v/%q, want %v/%q", c.url, kind, slug, c.kind, c.slug)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New()
	s.base = "https://bludreamsxxx.com"
	cases := []struct {
		kind    urlKind
		slug    string
		page    int
		modelID string
		want    string
	}{
		{kindUpdates, "", 1, "", "https://bludreamsxxx.com/access/categories/movies_1_d.html"},
		{kindUpdates, "", 7, "", "https://bludreamsxxx.com/access/categories/movies_7_d.html"},
		{kindCategory, "joi", 2, "", "https://bludreamsxxx.com/access/categories/joi_2_d.html"},
		{kindModel, "JewelzBlu", 1, "", "https://bludreamsxxx.com/access/models/JewelzBlu.html"},
		{kindModel, "JewelzBlu", 3, "3", "https://bludreamsxxx.com/access/sets.php?id=3&page=3"},
	}
	for _, c := range cases {
		got := s.listingURL("https://bludreamsxxx.com/access/models/JewelzBlu.html", c.kind, c.slug, c.page, c.modelID)
		if got != c.want {
			t.Errorf("listingURL(%v,%q,%d) = %q, want %q", c.kind, c.slug, c.page, got, c.want)
		}
	}
}

func TestParseListing(t *testing.T) {
	body := readFixture(t, "listing_page1.html")
	items := parseListing(body)
	if len(items) != 2 {
		t.Fatalf("got %d cards, want 2", len(items))
	}
	first := items[0]
	if first.id != "350" {
		t.Errorf("id = %q", first.id)
	}
	if first.title != "Galactic JOI" {
		t.Errorf("title = %q", first.title)
	}
	if first.url != "https://bludreamsxxx.com/access/scenes/Galactic-JOI_vids.html" {
		t.Errorf("url = %q", first.url)
	}
	// 4x is the widest still the card offers, and must win over the 1x listed first.
	if !strings.HasSuffix(first.thumbnail, "-4x.jpg") {
		t.Errorf("thumbnail = %q", first.thumbnail)
	}
	if !strings.HasSuffix(first.preview, ".mp4") {
		t.Errorf("preview = %q", first.preview)
	}
	if first.duration != 7*60 {
		t.Errorf("duration = %d", first.duration)
	}
	if first.date != "06/21/2026" {
		t.Errorf("date = %q", first.date)
	}
	if len(first.performers) != 1 || first.performers[0] != "Jewelz Blu" {
		t.Errorf("performers = %v", first.performers)
	}
}

func TestParseListingLinkedModelsAndEntities(t *testing.T) {
	items := parseListing(readFixture(t, "listing_page2.html"))
	if len(items) != 1 {
		t.Fatalf("got %d cards, want 1", len(items))
	}
	it := items[0]
	if it.title != "Old Set & Friends" {
		t.Errorf("title = %q", it.title)
	}
	if len(it.performers) != 2 || it.performers[0] != "Jewelz Blu" || it.performers[1] != "Guest Star" {
		t.Errorf("performers = %v", it.performers)
	}
	if it.duration != 22*60 {
		t.Errorf("duration = %d", it.duration)
	}
	if !strings.HasSuffix(it.thumbnail, "-1x.jpg") {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
	if it.preview != "" {
		t.Errorf("preview = %q, want empty for an image-only card", it.preview)
	}
}

func TestParseDetail(t *testing.T) {
	desc, tags := parseDetail(readFixture(t, "detail.html"))
	if desc != "Let me take your cock to places it hasn't been to before!" {
		t.Errorf("description = %q", desc)
	}
	if len(tags) != 1 || tags[0] != "joi" {
		t.Errorf("tags = %v", tags)
	}
}

func TestParseModels(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"\n\t\tJewelz Blu\t", []string{"Jewelz Blu"}},
		{"Featuring:\n\tJewelz Blu , Veronica Vixen\n", []string{"Jewelz Blu", "Veronica Vixen"}},
		{`<a href="x">Jewelz Blu</a>, <a href="y">Guest</a>`, []string{"Jewelz Blu", "Guest"}},
		{"   ", nil},
	}
	for _, c := range cases {
		got := parseModels(c.in)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("parseModels(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPagination(t *testing.T) {
	body := readFixture(t, "listing_page1.html")
	if got := maxPage(body); got != 27 {
		t.Errorf("maxPage = %d, want 27", got)
	}
	if !hasNextPage(body, 1) {
		t.Error("page 1 of 27 should have a next page")
	}
	if hasNextPage(body, 27) {
		t.Error("page 27 of 27 should be the last")
	}
	if got := estimateTotal(body, 12); got != 27*12 {
		t.Errorf("estimateTotal = %d", got)
	}
	if got := estimateTotal([]byte("<html>no pager</html>"), 5); got != 5 {
		t.Errorf("estimateTotal without a pager = %d, want 5", got)
	}
}

func TestExtractModelID(t *testing.T) {
	body := []byte(`<div class="global_pagination"><a href="sets.php?id=3">1</a><a href="sets.php?id=3&page=2">2</a></div>`)
	if got := extractModelID(body); got != "3" {
		t.Errorf("extractModelID = %q", got)
	}
	if got := extractModelID([]byte("<html></html>")); got != "" {
		t.Errorf("extractModelID = %q, want empty", got)
	}
}

func TestToScene(t *testing.T) {
	item := listItem{
		id:          "350",
		title:       "Galactic JOI",
		url:         "/access/scenes/Galactic-JOI_vids.html",
		thumbnail:   "/access/content//contentthumbs/11/68/1168-4x.jpg",
		preview:     "/access/content//contentthumbs/11/68/1168.mp4",
		performers:  []string{"Jewelz Blu"},
		date:        "06/21/2026",
		duration:    420,
		description: "Desc.",
		tags:        []string{"joi"},
	}
	sc := item.toScene("https://bludreamsxxx.com", "https://bludreamsxxx.com/", fixedNow())
	if sc.SiteID != siteID || sc.Studio != studioName {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}
	if sc.URL != "https://bludreamsxxx.com/access/scenes/Galactic-JOI_vids.html" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Thumbnail != "https://bludreamsxxx.com/access/content//contentthumbs/11/68/1168-4x.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Date.Format("2006-01-02") != "2026-06-21" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Duration != 420 {
		t.Errorf("Duration = %d", sc.Duration)
	}
}

func TestToSceneKeepsAbsoluteURLs(t *testing.T) {
	item := listItem{id: "1", title: "T", url: "https://bludreamsxxx.com/access/scenes/T_vids.html"}
	sc := item.toScene("https://bludreamsxxx.com", "https://bludreamsxxx.com/", fixedNow())
	if sc.URL != item.url {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Thumbnail != "" || sc.Preview != "" {
		t.Errorf("empty media should stay empty, got %q/%q", sc.Thumbnail, sc.Preview)
	}
}

func TestListScenes(t *testing.T) {
	var mu sync.Mutex
	var listingPages []string

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/categories/movies_1_d.html"):
			mu.Lock()
			listingPages = append(listingPages, r.URL.Path)
			mu.Unlock()
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.Contains(r.URL.Path, "/categories/movies_2_d.html"):
			mu.Lock()
			listingPages = append(listingPages, r.URL.Path)
			mu.Unlock()
			_, _ = w.Write(serveFixture(t, "listing_page2.html", srv.URL))
		case strings.HasSuffix(r.URL.Path, "_vids.html"):
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://bludreamsxxx.com/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	byID := map[string]models.Scene{}
	total := 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			byID[res.Scene.ID] = res.Scene
		case scraper.KindTotal:
			total = res.Total
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}

	// The page-1 fixture keeps two of the twelve cards but the site's own
	// 27-page pager, so the estimate is 27 * 2.
	if total != 54 {
		t.Errorf("Total = %d, want 54", total)
	}
	if len(byID) != 3 {
		t.Fatalf("got %d scenes, want 3", len(byID))
	}
	if len(listingPages) != 2 {
		t.Errorf("listing pages fetched = %v", listingPages)
	}

	sc, ok := byID["350"]
	if !ok {
		t.Fatal("scene 350 missing")
	}
	// The description and tags come from the detail page, not the card.
	if sc.Description == "" || len(sc.Tags) != 1 {
		t.Errorf("detail enrichment missing: %q %v", sc.Description, sc.Tags)
	}
	if got := byID["12"].URL; got != srv.URL+"/access/scenes/Old-Set_vids.html" {
		t.Errorf("relative URL not resolved against the base: %q", got)
	}
}

func TestListScenesStopsAtKnownID(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "movies_1_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.HasSuffix(r.URL.Path, "_vids.html"):
			_, _ = w.Write(serveFixture(t, "detail.html", srv.URL))
		default:
			t.Errorf("unexpected fetch of %s after the early stop", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://bludreamsxxx.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{"349": true}})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes []models.Scene
	stopped := false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	if !stopped {
		t.Error("expected a StoppedEarly result")
	}
	if len(scenes) != 1 || scenes[0].ID != "350" {
		t.Errorf("scenes = %v", scenes)
	}
}

func TestListScenesReportsDetailFailureButKeepsScene(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "movies_1_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_page1.html", srv.URL))
		case strings.Contains(r.URL.Path, "movies_2_d.html"):
			_, _ = w.Write(serveFixture(t, "listing_page2.html", srv.URL))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	s := New()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://bludreamsxxx.com/access/categories/movies_1_d.html", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	scenes, errs := 0, 0
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 3 {
		t.Errorf("scenes = %d, want 3 — a dead detail page must not drop the card", scenes)
	}
	if errs != 3 {
		t.Errorf("errors = %d, want 3", errs)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

// serveFixture rewrites the live host the fixtures carry in their self-links
// onto the test server. The site writes absolute URLs in its own markup, so a
// fixture served verbatim would send the detail fetches to production and the
// test would pass on a machine with a network and fail on CI.
func serveFixture(t *testing.T, name, base string) []byte {
	t.Helper()
	b := readFixture(t, name)
	rewritten := strings.ReplaceAll(string(b), defaultURL, base)
	if strings.Contains(rewritten, defaultURL) {
		t.Fatalf("fixture %s still points at the live site", name)
	}
	return []byte(rewritten)
}

func fixedNow() time.Time {
	return time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
}

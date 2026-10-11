package vixenutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{SiteID: "vixen", Domain: "vixen.com", StudioName: "Vixen"})
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.vixen.com/videos", true},
		{"https://vixen.com/videos/some-scene", true},
		{"https://www.blacked.com/videos", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"00:34:56", 34*60 + 56},
		{"01:17:19", 1*3600 + 17*60 + 19},
		{"23:42", 23*60 + 42},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseutil.ParseDurationColon(tt.in); got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func makeListingJSON(nodes []node, total, page int) string {
	edges := make([]edge, len(nodes))
	for i, n := range nodes {
		edges[i] = edge{Node: n}
	}
	nd := nextData{}
	nd.Props.PageProps.Edges = edges
	nd.Props.PageProps.TotalCount = total
	nd.Props.PageProps.PageNum = page
	b, _ := json.Marshal(nd)
	return fmt.Sprintf(`<html><script id="__NEXT_DATA__" type="application/json">%s</script></html>`, string(b))
}

func makeDetailJSON(v video) string {
	nd := nextData{}
	nd.Props.PageProps.Video = &v
	b, _ := json.Marshal(nd)
	return fmt.Sprintf(`<html><script id="__NEXT_DATA__" type="application/json">%s</script></html>`, string(b))
}

func makePerformerJSON(nodes []node, total int) string {
	nd := nextData{}
	nd.Props.PageProps.Videos = nodes
	nd.Props.PageProps.TotalCount = total
	b, _ := json.Marshal(nd)
	return fmt.Sprintf(`<html><script id="__NEXT_DATA__" type="application/json">%s</script></html>`, string(b))
}

func testNode(id, title, slug, date string, performers []string) node {
	models := make([]modelRef, len(performers))
	for i, p := range performers {
		models[i] = modelRef{Name: p}
	}
	return node{
		VideoID:       id,
		Title:         title,
		Slug:          slug,
		Site:          "vixen",
		ReleaseDate:   date,
		ModelsSlugged: models,
		Images: nodeImages{
			Listing: []image{
				{Src: "https://cdn.vixen.com/portrait.jpg", Width: 320, Height: 362},
				{Src: "https://cdn.vixen.com/landscape.jpg", Width: 628, Height: 352},
			},
		},
	}
}

func TestExtractNextData(t *testing.T) {
	html := makeListingJSON([]node{testNode("100", "Test", "test-scene", "2026-01-15T18:30:00.000Z", []string{"Alice"})}, 50, 1)
	nd, err := extractNextData([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if len(nd.Props.PageProps.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(nd.Props.PageProps.Edges))
	}
	if nd.Props.PageProps.TotalCount != 50 {
		t.Errorf("totalCount = %d, want 50", nd.Props.PageProps.TotalCount)
	}
	n := nd.Props.PageProps.Edges[0].Node
	if n.VideoID != "100" {
		t.Errorf("videoId = %q, want %q", n.VideoID, "100")
	}
}

func TestExtractNextDataMissing(t *testing.T) {
	_, err := extractNextData([]byte(`<html><body>no data</body></html>`))
	if err == nil {
		t.Error("expected error for missing __NEXT_DATA__")
	}
}

func TestNodeToScene(t *testing.T) {
	s := New(SiteConfig{SiteID: "vixen", Domain: "vixen.com", StudioName: "Vixen"})
	n := testNode("100", "Test Scene", "test-scene", "2026-01-15T18:30:00.000Z", []string{"Alice", "Bob"})

	scene := s.nodeToScene(n, "https://www.vixen.com/videos", fixedTime())
	if scene.StudioURL != "https://www.vixen.com/videos" {
		t.Errorf("StudioURL = %q", scene.StudioURL)
	}
	if scene.ID != "100" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Test Scene" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Thumbnail != "https://cdn.vixen.com/landscape.jpg" {
		t.Errorf("Thumbnail = %q (expected landscape)", scene.Thumbnail)
	}
	if scene.Date.Format("2006-01-02") != "2026-01-15" {
		t.Errorf("Date = %v", scene.Date)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Alice" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Studio != "Vixen" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if !strings.Contains(scene.URL, "/videos/test-scene") {
		t.Errorf("URL = %q", scene.URL)
	}
}

func TestNodeToScenePortraitFallback(t *testing.T) {
	s := New(SiteConfig{SiteID: "vixen", Domain: "vixen.com", StudioName: "Vixen"})
	n := node{
		VideoID: "101",
		Slug:    "scene",
		Images: nodeImages{
			Listing: []image{
				{Src: "https://cdn.vixen.com/only-portrait.jpg", Width: 320, Height: 362},
			},
		},
	}
	scene := s.nodeToScene(n, "https://www.vixen.com/videos", fixedTime())
	if scene.Thumbnail != "https://cdn.vixen.com/only-portrait.jpg" {
		t.Errorf("Thumbnail = %q (expected portrait fallback)", scene.Thumbnail)
	}
}

func TestListScenes(t *testing.T) {
	n := testNode("200", "Full Scene", "full-scene", "2026-03-01T12:00:00.000Z", []string{"Performer One"})
	listHTML := makeListingJSON([]node{n}, 1, 1)

	detailHTML := makeDetailJSON(video{
		VideoID:     "200",
		Title:       "Full Scene",
		Slug:        "full-scene",
		Description: "A test description.",
		RunLength:   "00:34:56",
		Directors:   []director{{Name: "Director X"}},
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/videos/full-scene"):
			_, _ = fmt.Fprint(w, detailHTML)
		case strings.Contains(r.URL.Path, "/videos"):
			_, _ = fmt.Fprint(w, listHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "example.com", StudioName: "Test"})
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for r := range ch {
		if r.Kind == scraper.KindScene {
			found = true
			if want := ts.URL + "/videos"; r.Scene.StudioURL != want {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, want)
			}
			if r.Scene.ID != "200" {
				t.Errorf("ID = %q", r.Scene.ID)
			}
			if r.Scene.Description != "A test description." {
				t.Errorf("Description = %q", r.Scene.Description)
			}
			if r.Scene.Duration != 34*60+56 {
				t.Errorf("Duration = %d, want %d", r.Scene.Duration, 34*60+56)
			}
			if r.Scene.Director != "Director X" {
				t.Errorf("Director = %q", r.Scene.Director)
			}
		}
	}
	if !found {
		t.Error("no scene result found")
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	n := testNode("300", "Known Scene", "known", "2026-01-01T00:00:00.000Z", nil)
	listHTML := makeListingJSON([]node{n}, 1, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listHTML)
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "example.com", StudioName: "Test"})
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{
		KnownIDs: map[string]bool{"300": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var stoppedEarly bool
	for r := range ch {
		if r.Kind == scraper.KindStoppedEarly {
			stoppedEarly = true
		}
		if r.Kind == scraper.KindScene {
			t.Error("got scene, expected stop")
		}
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestPerformerPage(t *testing.T) {
	n := testNode("400", "Performer Scene", "perf-scene", "2026-02-01T00:00:00.000Z", []string{"Star"})
	perfHTML := makePerformerJSON([]node{n}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, perfHTML)
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "testsite", Domain: "example.com", StudioName: "Test"})
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/performers/star", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for r := range ch {
		if r.Kind == scraper.KindScene {
			found = true
			if want := ts.URL + "/performers/star"; r.Scene.StudioURL != want {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, want)
			}
			if r.Scene.ID != "400" {
				t.Errorf("ID = %q", r.Scene.ID)
			}
		}
	}
	if !found {
		t.Error("no scene from performer page")
	}
}

func TestScraperInterface(t *testing.T) {
	s := New(SiteConfig{SiteID: "vixen", Domain: "vixen.com", StudioName: "Vixen"})
	var _ scraper.StudioScraper = s
	if s.ID() != "vixen" {
		t.Errorf("ID() = %q", s.ID())
	}
	if len(s.Patterns()) != 3 {
		t.Errorf("Patterns() length = %d, want 3", len(s.Patterns()))
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// --- golden fixture ----------------------------------------------------------
//
// TestExtractNextData and TestNodeToScene build node values in Go, so encode and
// decode share the struct tag and a renamed one round-trips unnoticed. This
// fixture wraps a byte-verbatim slice of the `edges` array from a live
// https://www.vixen.com/videos?page=1 __NEXT_DATA__ payload (first 2 of 24 edges;
// totalCount and pageNum copied from the same response) so the wire names are
// pinned independently of the structs.
//
// Shapes a hand-written fixture would have got wrong:
//   - `videoId` is a **quoted string** ("107143") even though it looks numeric.
//     node.VideoID is a string for that reason; writing it as a JSON number would
//     make the fixture pass while a real scrape failed to decode.
//   - `releaseDate` is RFC3339 with milliseconds and a Z offset
//     ("2026-07-26T17:30:00.000Z"), which is what time.RFC3339 is parsed against.
//   - `modelsSlugged` lists every performer including the male performer, so a
//     scene's Performers is not just the headline model.
//   - each `images.listing` entry carries nested `webp` / `highdpi` objects and a
//     `__typename` the scraper ignores — kept in, so unknown-field tolerance stays
//     covered.
func TestGoldenNextDataListing(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "next_data_listing.json"))
	if err != nil {
		t.Fatal(err)
	}

	var nd nextData
	if err := json.Unmarshal(body, &nd); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	pp := nd.Props.PageProps
	if len(pp.Edges) != 2 {
		t.Fatalf("decoded %d edges, want 2", len(pp.Edges))
	}
	if pp.TotalCount != 630 {
		t.Errorf("TotalCount = %d (totalCount), want 630 — pagination depends on it", pp.TotalCount)
	}

	n := pp.Edges[0].Node
	if n.VideoID != "107143" {
		t.Errorf("VideoID = %q (videoId), want the string \"107143\"", n.VideoID)
	}
	if n.Slug != "elegant-beauty-needs-passionate-sex" {
		t.Errorf("Slug = %q (slug)", n.Slug)
	}
	if n.Title != "Elegant Beauty Needs Passionate Sex" {
		t.Errorf("Title = %q (title)", n.Title)
	}
	if n.Site != "vixen" {
		t.Errorf("Site = %q (site)", n.Site)
	}
	if len(n.ModelsSlugged) != 2 {
		t.Errorf("ModelsSlugged has %d entries (modelsSlugged), want 2", len(n.ModelsSlugged))
	}
	if len(n.Images.Listing) == 0 {
		t.Fatal("Images.Listing is empty (images.listing) — every scene loses its thumbnail")
	}
	if n.Images.Listing[0].Src == "" || n.Images.Listing[0].Width == 0 {
		t.Errorf("listing image = %+v (src/width)", n.Images.Listing[0])
	}

	// The exact wire format of releaseDate, not merely that it is non-empty:
	// Scene.Date is parsed with time.RFC3339 and a zero date is only an advisory
	// warning at validation time, so a format change degrades quietly.
	if n.ReleaseDate != "2026-07-26T17:30:00.000Z" {
		t.Errorf("ReleaseDate = %q (releaseDate), want RFC3339 with ms and Z", n.ReleaseDate)
	}
	if _, err := time.Parse(time.RFC3339, n.ReleaseDate); err != nil {
		t.Errorf("releaseDate %q does not parse as RFC3339: %v", n.ReleaseDate, err)
	}
}

// The captured body must stay a capture; re-encoding it normalises exactly the
// details the fixture exists to pin.
func TestGoldenNextDataIsRawCapture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "next_data_listing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"videoId":"107143"`)) {
		t.Error(`fixture lost the quoted "videoId":"107143" form — it looks re-encoded`)
	}
	if !bytes.Contains(body, []byte(`"__typename"`)) {
		t.Error("fixture lost __typename; unknown-field tolerance is no longer covered")
	}
}

package wankitnowutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var _ scraper.StudioScraper = (*Scraper)(nil)

var testCfg = SiteConfig{
	ID:       "testsite",
	Domain:   "testsite.com",
	Studio:   "Test Studio",
	Patterns: []string{"testsite.com/videos"},
	MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?testsite\.com`),
}

func TestNormalizePerformer(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"JANE DOE", "Jane Doe"},
		{"jane doe", "Jane Doe"},
		{"Jane Doe", "Jane Doe"},
		{"SINGLE", "Single"},
		{"  extra   spaces  ", "Extra Spaces"},
	}
	for _, tt := range tests {
		if got := normalizePerformer(tt.in); got != tt.want {
			t.Errorf("normalizePerformer(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToScene(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	sc := sceneJSON{
		ID:              42,
		Title:           "Test Scene",
		Slug:            "test-scene",
		Description:     "A test description",
		PublishDate:     "2026/05/01 10:00:00",
		SecondsDuration: 600,
		Thumb:           "https://cdn.example.com/thumb.jpg",
		Models:          []string{"JANE DOE", "JOHN SMITH"},
		Tags:            []string{"tag1", "tag2"},
	}

	scene := toScene(sc, "testsite", "https://www.testsite.com", "Test Studio", now)

	if scene.ID != "42" {
		t.Errorf("ID = %q, want %q", scene.ID, "42")
	}
	if scene.SiteID != "testsite" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "testsite")
	}
	if scene.Title != "Test Scene" {
		t.Errorf("Title = %q, want %q", scene.Title, "Test Scene")
	}
	if scene.URL != "https://www.testsite.com/videos/test-scene" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Date.Format("2006-01-02") != "2026-05-01" {
		t.Errorf("Date = %v, want 2026-05-01", scene.Date)
	}
	if scene.Duration != 600 {
		t.Errorf("Duration = %d, want 600", scene.Duration)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Jane Doe" || scene.Performers[1] != "John Smith" {
		t.Errorf("Performers = %v, want [Jane Doe John Smith]", scene.Performers)
	}
	if len(scene.Tags) != 2 {
		t.Errorf("Tags = %v, want 2 tags", scene.Tags)
	}
	if scene.Studio != "Test Studio" {
		t.Errorf("Studio = %q, want %q", scene.Studio, "Test Studio")
	}
	if scene.Thumbnail != "https://cdn.example.com/thumb.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
}

func TestToSceneEmptyDate(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	sc := sceneJSON{ID: 1, Title: "No Date"}
	scene := toScene(sc, "testsite", "https://www.testsite.com", "Test Studio", now)
	if !scene.Date.IsZero() {
		t.Errorf("Date should be zero for empty publish_date, got %v", scene.Date)
	}
}

func TestToSceneEmptyModels(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	sc := sceneJSON{ID: 1, Title: "No Models", Models: []string{"", "  "}}
	scene := toScene(sc, "testsite", "https://www.testsite.com", "Test Studio", now)
	if len(scene.Performers) != 0 {
		t.Errorf("Performers should be empty for blank models, got %v", scene.Performers)
	}
}

func TestBuildIDRegex(t *testing.T) {
	tests := []struct {
		html string
		want string
	}{
		{`"buildId":"abc123xyz"`, "abc123xyz"},
		{`"buildId" : "spaced-id"`, "spaced-id"},
		{`no build id here`, ""},
	}
	for _, tt := range tests {
		m := buildIDRe.FindStringSubmatch(tt.html)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != tt.want {
			t.Errorf("buildIDRe on %q = %q, want %q", tt.html, got, tt.want)
		}
	}
}

func newTestServer(scenes []sceneJSON, totalPages int) *httptest.Server {
	return newTestServerWithModels(scenes, totalPages, nil)
}

func newTestServerWithModels(scenes []sceneJSON, totalPages int, modelScenes map[string][]sceneJSON) *httptest.Server {
	modelPaths := make(map[string][]sceneJSON, len(modelScenes))
	for slug, ms := range modelScenes {
		modelPaths["/_next/data/test-build-123/models/"+slug+".json"] = ms
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ms, ok := modelPaths[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			resp := modelDataResponse{}
			resp.PageProps.ModelLatestContents = ms
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<script>{"buildId":"test-build-123"}</script>`)
		case "/_next/data/test-build-123/videos.json":
			w.Header().Set("Content-Type", "application/json")
			resp := nextDataResponse{}
			resp.PageProps.Contents.Total = len(scenes)
			resp.PageProps.Contents.TotalPages = totalPages
			resp.PageProps.Contents.Data = scenes
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRun(t *testing.T) {
	scenes := []sceneJSON{
		{ID: 1, Title: "Scene One", Slug: "scene-one", Models: []string{"JANE DOE"}, SecondsDuration: 300},
		{ID: 2, Title: "Scene Two", Slug: "scene-two", Models: []string{"JOHN SMITH"}, SecondsDuration: 600},
	}

	srv := newTestServer(scenes, 1)
	defer srv.Close()

	cfg := testCfg
	cfg.BaseURL = srv.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	if got[0].Title != "Scene One" {
		t.Errorf("scenes[0].Title = %q, want %q", got[0].Title, "Scene One")
	}
	if got[0].Performers[0] != "Jane Doe" {
		t.Errorf("scenes[0].Performers[0] = %q, want %q", got[0].Performers[0], "Jane Doe")
	}
	if got[1].Duration != 600 {
		t.Errorf("scenes[1].Duration = %d, want 600", got[1].Duration)
	}
	if got[0].SiteID != "testsite" {
		t.Errorf("scenes[0].SiteID = %q, want %q", got[0].SiteID, "testsite")
	}
}

func TestKnownIDs(t *testing.T) {
	scenes := []sceneJSON{
		{ID: 1, Title: "Scene One", Slug: "scene-one"},
		{ID: 2, Title: "Scene Two", Slug: "scene-two"},
		{ID: 3, Title: "Scene Three", Slug: "scene-three"},
	}

	srv := newTestServer(scenes, 1)
	defer srv.Close()

	cfg := testCfg
	cfg.BaseURL = srv.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"2": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, stopped := testutil.CollectScenesWithStop(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2 — the page is finished before the stop", len(got))
	}
	if got[0].Title != "Scene One" {
		t.Errorf("scenes[0].Title = %q, want %q", got[0].Title, "Scene One")
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
}

func TestModelPage(t *testing.T) {
	modelScenes := map[string][]sceneJSON{
		"jane-doe": {
			{ID: 10, Title: "Model Scene One", Slug: "model-scene-one", Models: []string{"JANE DOE"}, SecondsDuration: 300},
			{ID: 11, Title: "Model Scene Two", Slug: "model-scene-two", Models: []string{"JANE DOE"}, SecondsDuration: 450},
		},
	}

	srv := newTestServerWithModels(nil, 0, modelScenes)
	defer srv.Close()

	cfg := testCfg
	cfg.BaseURL = srv.URL

	s := New(cfg)
	ch, err := s.ListScenes(context.Background(), srv.URL+"/models/jane-doe", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.CollectScenes(t, ch)
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	if got[0].Title != "Model Scene One" {
		t.Errorf("scenes[0].Title = %q, want %q", got[0].Title, "Model Scene One")
	}
	if got[0].Performers[0] != "Jane Doe" {
		t.Errorf("scenes[0].Performers[0] = %q, want %q", got[0].Performers[0], "Jane Doe")
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build sceneJSON values in Go, so encode and decode share the
// struct tag and a renamed tag round-trips unnoticed. This one decodes a
// _next/data page captured verbatim from wankitnow.com and runs a row through
// toScene.
//
// Notable shapes: the payload is buried two levels deep under
// `pageProps.contents`, `publish_date` uses the unusual "2026/07/26 00:00:00"
// layout rather than RFC3339, and `models` (plain strings) sits beside
// `models_slugs` (objects) — an easy pair to conflate.
func TestGoldenNextData(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "videos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resp nextDataResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	c := resp.PageProps.Contents
	if c.Total == 0 || c.TotalPages == 0 {
		t.Errorf("total/total_pages = %d/%d — pagination depends on both", c.Total, c.TotalPages)
	}
	if len(c.Data) != 2 {
		t.Fatalf("decoded %d scenes, want 2 (pageProps.contents.data)", len(c.Data))
	}

	j := c.Data[0]
	if j.ID != 31729 {
		t.Errorf("ID = %d (id), want 31729", j.ID)
	}
	if j.Slug != "cuck-tales-2" {
		t.Errorf("Slug = %q (slug) — the scene URL is built from it", j.Slug)
	}
	if j.SecondsDuration != 810 {
		t.Errorf("SecondsDuration = %d (seconds_duration), want 810", j.SecondsDuration)
	}
	if j.PublishDate != "2026/07/26 00:00:00" {
		t.Errorf("PublishDate = %q (publish_date)", j.PublishDate)
	}
	if len(j.Models) == 0 {
		t.Error("Models is empty (models, plain strings)")
	}
	if len(j.ModelsSlugs) == 0 || j.ModelsSlugs[0].Slug == "" {
		t.Errorf("ModelsSlugs = %+v (models_slugs, objects)", j.ModelsSlugs)
	}
	if j.Rating == 0 {
		t.Error("Rating is 0 (rating)")
	}

	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	sc := toScene(j, "wankitnow", "https://www.wankitnow.com", "Wank It Now", now)

	if sc.Title != "CUCK TALES" {
		t.Errorf("scene Title = %q", sc.Title)
	}
	if sc.Duration != 810 {
		t.Errorf("scene Duration = %d, want 810", sc.Duration)
	}
	// The "2026/07/26 00:00:00" layout must actually parse.
	if sc.Date.Format("2006-01-02") != "2026-07-26" {
		t.Errorf("scene Date = %v, want 2026-07-26 (publish_date layout)", sc.Date)
	}
	if !strings.Contains(sc.URL, "cuck-tales-2") {
		t.Errorf("scene URL = %q", sc.URL)
	}
	if sc.Thumbnail == "" {
		t.Error("scene Thumbnail is empty (thumb)")
	}
	if len(sc.Performers) == 0 {
		t.Error("scene Performers is empty (models)")
	}
}

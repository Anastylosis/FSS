package railwayutil

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var testCfg = SiteConfig{
	ID:       "testsite",
	SiteCode: "TST",
	Studio:   "Test Studio",
	SiteBase: "https://testsite.com",
	Patterns: []string{"testsite.com/#/models"},
	MatchRe:  regexp.MustCompile(`^https?://(?:www\.)?testsite\.com`),
}

func TestExtractPerformer(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Abby Adams 1", "Abby Adams"},
		{"Allie Nicole all", "Allie Nicole"},
		{"Amber Jayne 12", "Amber Jayne"},
		{"Solo Name", "Solo Name"},
		{"Name", "Name"},
	}
	for _, tt := range tests {
		if got := ExtractPerformer(tt.name); got != tt.want {
			t.Errorf("ExtractPerformer(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		s    string
		want int
	}{
		{"00:10:13", 613},
		{"10:13", 613},
		{"05:00", 300},
		{"", 0},
	}
	for _, tt := range tests {
		if got := ParseDuration(tt.s); got != tt.want {
			t.Errorf("ParseDuration(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestParseFilter(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://testsite.com/#/models", ""},
		{"https://testsite.com/#/models/Abby%20Adams", "abby adams"},
		{"https://testsite.com/#/models/Allie Nicole", "allie nicole"},
		{"https://testsite.com/", ""},
	}
	for _, tt := range tests {
		if got := ParseFilter(tt.url); got != tt.want {
			t.Errorf("ParseFilter(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func newTestServer(videos []APIVideo) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(videos)
	}))
}

func TestRun(t *testing.T) {
	videos := []APIVideo{
		{ID: "aaa", Name: "Abby Adams 1", Site: "TST", Duration: "10:13"},
		{ID: "bbb", Name: "Abby Adams 2", Site: "TST", Duration: "08:30"},
		{ID: "ccc", Name: "Allie Nicole 1", Site: "TST", Duration: "05:00"},
	}

	srv := newTestServer(videos)
	defer srv.Close()

	orig := APIBase
	t.Cleanup(func() { APIBase = orig })
	APIBase = srv.URL

	s := New(testCfg)
	ch, err := s.ListScenes(context.Background(), "https://testsite.com/#/models", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	if scenes[0].Title != "Abby Adams 1" {
		t.Errorf("scenes[0].Title = %q, want %q", scenes[0].Title, "Abby Adams 1")
	}
	if scenes[0].Duration != 613 {
		t.Errorf("scenes[0].Duration = %d, want 613", scenes[0].Duration)
	}
	if len(scenes[0].Performers) != 1 || scenes[0].Performers[0] != "Abby Adams" {
		t.Errorf("scenes[0].Performers = %v, want [Abby Adams]", scenes[0].Performers)
	}
	if scenes[0].SiteID != "testsite" {
		t.Errorf("scenes[0].SiteID = %q, want %q", scenes[0].SiteID, "testsite")
	}
}

func TestRunWithFilter(t *testing.T) {
	videos := []APIVideo{
		{ID: "aaa", Name: "Abby Adams 1", Site: "TST", Duration: "10:13"},
		{ID: "bbb", Name: "Abby Adams 2", Site: "TST", Duration: "08:30"},
		{ID: "ccc", Name: "Allie Nicole 1", Site: "TST", Duration: "05:00"},
	}

	srv := newTestServer(videos)
	defer srv.Close()

	orig := APIBase
	t.Cleanup(func() { APIBase = orig })
	APIBase = srv.URL

	s := New(testCfg)
	ch, err := s.ListScenes(context.Background(), "https://testsite.com/#/models/Abby%20Adams", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
}

func TestKnownIDs(t *testing.T) {
	videos := []APIVideo{
		{ID: "aaa", Name: "Abby Adams 1", Site: "TST", Duration: "10:13"},
		{ID: "bbb", Name: "Abby Adams 2", Site: "TST", Duration: "08:30"},
		{ID: "ccc", Name: "Allie Nicole 1", Site: "TST", Duration: "05:00"},
	}

	srv := newTestServer(videos)
	defer srv.Close()

	orig := APIBase
	t.Cleanup(func() { APIBase = orig })
	APIBase = srv.URL

	s := New(testCfg)
	ch, err := s.ListScenes(context.Background(), "https://testsite.com/#/models", scraper.ListOpts{
		KnownIDs: map[string]bool{"bbb": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (known ID skipped, not stopped early)", len(scenes))
	}
	if scenes[0].ID != "aaa" || scenes[1].ID != "ccc" {
		t.Errorf("scenes = [%s, %s], want [aaa, ccc]", scenes[0].ID, scenes[1].ID)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build APIVideo values in Go and encode them with
// json.NewEncoder, so encode and decode share the struct tag and a renamed one
// round-trips unnoticed. This is a byte-verbatim slice of a live
// https://sites-api-production.up.railway.app/videos/SE response (first three of
// the catalogue). No credential.
//
// Shapes a hand-written fixture would have got wrong:
//   - **the id field is `_id`, not `id`** — a MongoDB ObjectId hex string, not a
//     number. Scene.ID is built from it, so a wrong guess re-keys every scene and
//     an incremental scrape re-adds the whole catalogue.
//   - `duration` is a **"HH:MM:SS" string**, not seconds, and it keeps the
//     leading zero hour ("00:08:41").
//   - `video4K` is camelCase with a digit in the middle — easy to write as
//     `video_4k` or `video4k`.
//   - the response is a **bare top-level array**, with no envelope or count, so
//     the scraper fetches the whole catalogue in one call.
//   - each record carries Mongoose's `__v` version key that nothing decodes —
//     retained, so unknown-field tolerance stays covered.
func TestGoldenVideos(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "videos.json"))
	if err != nil {
		t.Fatal(err)
	}

	var videos []APIVideo
	if err := json.Unmarshal(body, &videos); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if len(videos) != 3 {
		t.Fatalf("decoded %d videos, want 3", len(videos))
	}

	v := videos[0]
	if v.ID != "698cf02ef2ee6afc5e41231f" {
		t.Errorf("ID = %q (_id), want the ObjectId hex — the field is `_id`, not `id`", v.ID)
	}
	if len(v.ID) != 24 {
		t.Errorf("ID %q is not a 24-char ObjectId; Scene.ID is derived from it", v.ID)
	}
	if v.Name != "Abigail 1" {
		t.Errorf("Name = %q (name)", v.Name)
	}
	if v.Site != "SE" {
		t.Errorf("Site = %q (site) — the per-site code the listing is filtered by", v.Site)
	}
	if v.Video4K {
		t.Error("Video4K is true (video4K) on a non-4K entry")
	}

	// duration as HH:MM:SS, and that ParseDuration still understands it.
	if v.Duration != "00:08:41" {
		t.Errorf("Duration = %q (duration), want a HH:MM:SS string rather than seconds", v.Duration)
	}
	if got := ParseDuration(v.Duration); got != 521 {
		t.Errorf("ParseDuration(%q) = %d, want 521 seconds", v.Duration, got)
	}
}

func TestGoldenVideosIsRawCapture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "videos.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		t.Error("fixture is no longer a bare top-level array; the API returns one with no envelope")
	}
	if !bytes.Contains(body, []byte(`"_id":`)) {
		t.Error(`fixture lost the "_id" key`)
	}
	if !bytes.Contains(body, []byte(`"__v":`)) {
		t.Error("fixture lost Mongoose's __v; unknown-field tolerance is no longer covered")
	}
}

// The tour is a hash-router with no per-scene route, so every scene by one
// performer used to share a single URL and collapse downstream. The video id
// rides as a query the router ignores.
func TestSceneURLIsPerScene(t *testing.T) {
	s := New(SiteConfig{ID: "smokingerotica", SiteCode: "SE", Studio: "Smoking Erotica",
		SiteBase: "https://smokingerotica.com"})

	a := s.sceneURL("Abigail", "698cf02ef2ee6afc5e41231f")
	b := s.sceneURL("Abigail", "698cf02ef2ee6afc5e412320")
	if a == b {
		t.Errorf("two scenes by one performer share a URL: %q", a)
	}
	if a != "https://smokingerotica.com/#/models/Abigail?video=698cf02ef2ee6afc5e41231f" {
		t.Errorf("sceneURL = %q", a)
	}
	// A name with a space still addresses the model route.
	if got := s.sceneURL("Lady Anne", "1"); got != "https://smokingerotica.com/#/models/Lady%20Anne?video=1" {
		t.Errorf("sceneURL = %q", got)
	}
	// With no id the bare model route stands.
	if got := s.sceneURL("Abigail", ""); got != "https://smokingerotica.com/#/models/Abigail" {
		t.Errorf("sceneURL = %q", got)
	}
}

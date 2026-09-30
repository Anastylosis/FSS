package faphouse

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

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://faphouse.com/models/angel-the-dreamgirl", true},
		{"https://www.faphouse.com/models/angel-the-dreamgirl", true},
		{"https://faphouse.com/studios/brazzers", true},
		{"https://faphouse.com/models/angel-the-dreamgirl?sort=new", true},
		{"https://faphouse.com/videos/some-slug", false},
		{"https://faphouse.com/", false},
		{"https://example.com/models/foo", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseStudioURL(t *testing.T) {
	cases := []struct {
		url      string
		wantType string
		wantSlug string
	}{
		{"https://faphouse.com/models/angel-the-dreamgirl", "models", "angel-the-dreamgirl"},
		{"https://faphouse.com/studios/brazzers", "studios", "brazzers"},
		{"https://faphouse.com/models/foo?sort=new", "models", "foo"},
	}
	for _, c := range cases {
		gotType, gotSlug := parseStudioURL(c.url)
		if gotType != c.wantType || gotSlug != c.wantSlug {
			t.Errorf("parseStudioURL(%q) = (%q, %q), want (%q, %q)", c.url, gotType, gotSlug, c.wantType, c.wantSlug)
		}
	}
}

func TestParseListingPage(t *testing.T) {
	html := listingHTML([]testCard{
		{id: "100", title: "First Video", dur: "21:46", thumb: "https://cdn.example.com/t1.jpg", preview: "https://cdn.example.com/p1.mp4", price: "€17.07", studio: "Test Studio"},
		{id: "200", title: "Second Video", dur: "1:05:30", thumb: "https://cdn.example.com/t2.jpg", preview: "", price: "", studio: "Test Studio"},
	}, 42)

	cards, total := parseListingPage([]byte(html))
	if total != 42 {
		t.Errorf("total = %d, want 42", total)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}

	c := cards[0]
	if c.id != "100" {
		t.Errorf("id = %q", c.id)
	}
	if c.title != "First Video" {
		t.Errorf("title = %q", c.title)
	}
	if c.duration != 1306 {
		t.Errorf("duration = %d, want 1306", c.duration)
	}
	if c.thumbnail != "https://cdn.example.com/t1.jpg" {
		t.Errorf("thumbnail = %q", c.thumbnail)
	}
	if c.preview != "https://cdn.example.com/p1.mp4" {
		t.Errorf("preview = %q", c.preview)
	}
	if c.price != 17.07 {
		t.Errorf("price = %f, want 17.07", c.price)
	}
	if c.studioName != "Test Studio" {
		t.Errorf("studioName = %q", c.studioName)
	}
	if c.detailPath != "/videos/first-video-abc123" {
		t.Errorf("detailPath = %q", c.detailPath)
	}

	c2 := cards[1]
	if c2.duration != 3930 {
		t.Errorf("duration h:m:s = %d, want 3930", c2.duration)
	}
	if c2.price != 0 {
		t.Errorf("price = %f, want 0 (no price)", c2.price)
	}
}

func TestParseDetailPage(t *testing.T) {
	vs := viewState{
		Video: videoMeta{
			PublishedAt:   "2025-03-29",
			PornstarNames: []string{"Angel The Dreamgirl", "Second Performer"},
		},
	}
	vsJSON, _ := json.Marshal(vs)

	body := fmt.Sprintf(`<html><body>
<script id="view-state-data" type="application/json">%s</script>
<div class="video-info-details__description"><details><p>A great description</p></details></div>
<a class="vid-c" href="/c/blowjob/videos">Blowjob</a>
<a class="vid-c" href="/c/milf/videos">MILF</a>
<a class="vid-c" href="/c/pov/videos">POV</a>
</body></html>`, string(vsJSON))

	info := parseDetailPage([]byte(body))

	if info.date.Year() != 2025 || info.date.Month() != 3 || info.date.Day() != 29 {
		t.Errorf("date = %v", info.date)
	}
	if len(info.performers) != 2 || info.performers[0] != "Angel The Dreamgirl" {
		t.Errorf("performers = %v", info.performers)
	}
	if info.description != "A great description" {
		t.Errorf("description = %q", info.description)
	}
	if len(info.categories) != 3 || info.categories[0] != "Blowjob" || info.categories[2] != "POV" {
		t.Errorf("categories = %v", info.categories)
	}
}

func TestParseDetailPageHTMLDateFallback(t *testing.T) {
	body := `<html><body>
<span class="video-publish-date">Published: 29.03.2025</span>
</body></html>`

	info := parseDetailPage([]byte(body))
	if info.date.Year() != 2025 || info.date.Month() != 3 || info.date.Day() != 29 {
		t.Errorf("fallback date = %v", info.date)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"21:46", 1306},
		{"0:45", 45},
		{"1:05:30", 3930},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseutil.ParseDurationColon(c.input); got != c.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParsePrice(t *testing.T) {
	cases := []struct {
		input string
		want  float64
	}{
		{"€17.07", 17.07},
		{"€9,99", 9.99},
		{"$25.00", 25.00},
		{"15", 15},
		{"", 0},
	}
	for _, c := range cases {
		if got := parsePrice(c.input); got != c.want {
			t.Errorf("parsePrice(%q) = %f, want %f", c.input, got, c.want)
		}
	}
}

func TestParseDDMMYYYY(t *testing.T) {
	d := parseDDMMYYYY("29.03.2025")
	if d.Year() != 2025 || d.Month() != 3 || d.Day() != 29 {
		t.Errorf("parseDDMMYYYY = %v", d)
	}
}

func TestToScene(t *testing.T) {
	c := card{
		id:          "12345",
		title:       "Test Scene",
		detailPath:  "/videos/test-scene-abc",
		duration:    600,
		thumbnail:   "https://cdn.example.com/thumb.jpg",
		preview:     "https://cdn.example.com/preview.mp4",
		price:       17.07,
		studioName:  "Test Studio",
		date:        time.Date(2025, 3, 29, 0, 0, 0, 0, time.UTC),
		description: "A description",
		categories:  []string{"Blowjob", "MILF"},
		performers:  []string{"Angel The Dreamgirl"},
	}

	sc := toScene(c, "https://faphouse.com/models/test-studio", fixedTime())
	if sc.ID != "12345" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "faphouse" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.URL != "https://faphouse.com/videos/test-scene-abc" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Title != "Test Scene" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 600 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.Studio != "Test Studio" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Angel The Dreamgirl" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if len(sc.Categories) != 2 {
		t.Errorf("Categories = %v", sc.Categories)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 17.07 {
		t.Errorf("PriceHistory = %v", sc.PriceHistory)
	}
}

func TestToSceneNoPerformers(t *testing.T) {
	c := card{
		id:         "1",
		title:      "Test",
		detailPath: "/videos/test",
		studioName: "Studio Name",
	}
	sc := toScene(c, "https://faphouse.com/models/test", fixedTime())
	if len(sc.Performers) != 1 || sc.Performers[0] != "Studio Name" {
		t.Errorf("Performers fallback = %v", sc.Performers)
	}
}

func TestToSceneNoPrice(t *testing.T) {
	c := card{
		id:         "1",
		title:      "Premium",
		detailPath: "/videos/premium",
	}
	sc := toScene(c, "https://faphouse.com/models/test", fixedTime())
	if len(sc.PriceHistory) != 0 {
		t.Errorf("PriceHistory should be empty for no-price, got %v", sc.PriceHistory)
	}
}

// --- test helpers ---

type testCard struct {
	id, title, dur, thumb, preview, price, studio string
}

func listingHTML(cards []testCard, total int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<html><body><span class="switcher-block__counter">%d</span>`, total)
	for _, c := range cards {
		slug := strings.ReplaceAll(strings.ToLower(c.title), " ", "-") + "-abc123"
		priceAttr := ""
		if c.price != "" {
			priceAttr = fmt.Sprintf(` data-el-price="%s"`, c.price)
		}
		previewAttr := ""
		if c.preview != "" {
			previewAttr = fmt.Sprintf(` data-el-video="%s"`, c.preview)
		}
		fmt.Fprintf(&sb, `
<div class="thumb tv" data-test-id="video-thumb-%s" data-id="%s"%s>
<a class="t-vl" href="/videos/%s">
<div class="t-vi">HD <span>%s</span></div>
<img class="t-i" src="%s" alt="%s" />
</a>
<a class="t-ti-s" href="/models/slug">%s</a>
<button%s></button>
</div>`,
			c.id, c.id, previewAttr,
			slug, c.dur, c.thumb, c.title,
			c.studio, priceAttr)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func newTestServer(typePath, slug string, pages [][]testCard, total int, details map[string]string) *httptest.Server {
	pageIdx := 0
	listingPrefix := fmt.Sprintf("/%s/%s", typePath, slug)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		if html, ok := details[r.URL.Path]; ok {
			_, _ = fmt.Fprint(w, html)
			return
		}

		if strings.HasPrefix(r.URL.Path, listingPrefix) {
			if pageIdx >= len(pages) {
				_, _ = fmt.Fprint(w, `<html><body></body></html>`)
				return
			}
			cards := pages[pageIdx]
			pageIdx++
			_, _ = fmt.Fprint(w, listingHTML(cards, total))
			return
		}

		// A card with no detail fixture gets an empty page rather than a 404,
		// so a test about something else need not account for the detail
		// failures that are now reported.
		if strings.HasPrefix(r.URL.Path, "/videos/") {
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
			return
		}

		http.NotFound(w, r)
	}))
}

func detailPageHTML(date string, performers []string, desc string, categories []string) string {
	vs := viewState{
		Video: videoMeta{
			PublishedAt:   date,
			PornstarNames: performers,
		},
	}
	vsJSON, _ := json.Marshal(vs)

	var sb strings.Builder
	fmt.Fprintf(&sb, `<html><body><script id="view-state-data" type="application/json">%s</script>`, string(vsJSON))
	if desc != "" {
		fmt.Fprintf(&sb, `<div class="video-info-details__description"><details><p>%s</p></details></div>`, desc)
	}
	for _, cat := range categories {
		fmt.Fprintf(&sb, `<a class="vid-c" href="/c/%s/videos">%s</a>`, strings.ToLower(cat), cat)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func TestListScenes(t *testing.T) {
	cards := []testCard{
		{id: "100", title: "Scene One", dur: "10:00", thumb: "https://cdn/t1.jpg", preview: "https://cdn/p1.mp4", price: "€10.00", studio: "Creator"},
		{id: "200", title: "Scene Two", dur: "5:00", thumb: "https://cdn/t2.jpg", preview: "", price: "", studio: "Creator"},
	}
	details := map[string]string{
		"/videos/scene-one-abc123": detailPageHTML("2025-03-01", []string{"Performer A"}, "Desc one", []string{"MILF"}),
		"/videos/scene-two-abc123": detailPageHTML("2025-02-15", []string{"Performer B"}, "Desc two", []string{"POV", "Blonde"}),
	}
	ts := newTestServer("models", "testcreator", [][]testCard{cards}, 2, details)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/testcreator", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	sc := scenes[0]
	if sc.Title != "Scene One" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Date.Month() != 3 || sc.Date.Day() != 1 {
		t.Errorf("date = %v", sc.Date)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Performer A" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Description != "Desc one" {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Categories) != 1 || sc.Categories[0] != "MILF" {
		t.Errorf("categories = %v", sc.Categories)
	}

	sc2 := scenes[1]
	if len(sc2.Categories) != 2 {
		t.Errorf("categories = %v", sc2.Categories)
	}
}

func TestListScenesPagination(t *testing.T) {
	page1 := make([]testCard, perPage)
	for i := range page1 {
		page1[i] = testCard{
			id: fmt.Sprintf("%d", i+1), title: fmt.Sprintf("Scene %d", i+1),
			dur: "1:00", thumb: "https://cdn/t.jpg", studio: "C",
		}
	}
	page2 := []testCard{
		{id: "61", title: "Scene 61", dur: "1:00", thumb: "https://cdn/t.jpg", studio: "C"},
		{id: "62", title: "Scene 62", dur: "1:00", thumb: "https://cdn/t.jpg", studio: "C"},
	}

	ts := newTestServer("models", "creator", [][]testCard{page1, page2}, 62, map[string]string{})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/creator", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 62 {
		t.Fatalf("got %d scenes, want 62", len(scenes))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	cards := []testCard{
		{id: "300", title: "New", dur: "1:00", thumb: "https://cdn/t.jpg", studio: "C"},
		{id: "200", title: "Also New", dur: "1:00", thumb: "https://cdn/t.jpg", studio: "C"},
		{id: "100", title: "Known", dur: "1:00", thumb: "https://cdn/t.jpg", studio: "C"},
		{id: "50", title: "Old", dur: "1:00", thumb: "https://cdn/t.jpg", studio: "C"},
	}

	ts := newTestServer("models", "creator", [][]testCard{cards}, 4, map[string]string{})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/creator", scraper.ListOpts{
		KnownIDs: map[string]bool{"100": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3 — the page is finished before the stop", len(scenes))
	}
	if scenes[0].ID != "300" || scenes[1].ID != "200" {
		t.Errorf("scenes = %v, %v", scenes[0].ID, scenes[1].ID)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build viewState/videoMeta values in Go, so encode and decode
// share the struct tag and a renamed one round-trips unnoticed. This drives the
// real entry point — parseDetailPage over page bytes — against a **verbatim
// `video` object** sliced out of a live https://faphouse.com/videos/0E8HtA page.
//
// ⚠️ The surrounding blob is *not* captured whole, deliberately. The live
// `view-state-data` script carries a top-level **`csrfToken`** alongside a large
// amount of A/B-test and attribution state; committing it would put session
// material in the repo. viewState decodes only `video`, so the fixture keeps that
// object byte-for-byte and reconstructs the script wrapper around it.
// TestGoldenViewStateCarriesNoToken enforces that.
//
// This pins the JSON half of parseDetailPage. The HTML-regex half (publishRe,
// descRe, categoryRe) is covered by the existing detail-page tests above.
//
// Shapes a hand-written fixture would have got wrong:
//   - `publishedAt` is a **bare date** ("2019-05-14"), not a timestamp, and is
//     parsed with a "2006-01-02" layout. The page separately renders a
//     DD.MM.YYYY string that publishRe reads as a fallback — two different
//     formats for the same fact on one page.
//   - `pornstarNames` is a flat **array of strings**, unlike the object arrays
//     most sibling platforms use, and it sits beside a `pornstarIds` array that
//     nothing decodes.
//   - the `video` object carries ~60 keys of experiment flags; all of them are
//     retained here, so unknown-field tolerance stays covered.
func TestGoldenViewState(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "view_state.html"))
	if err != nil {
		t.Fatal(err)
	}

	info := parseDetailPage(body)

	want := time.Date(2019, 5, 14, 0, 0, 0, 0, time.UTC)
	if !info.date.Equal(want) {
		t.Errorf("date = %v, want %v (from video.publishedAt, a bare Y-m-d)", info.date, want)
	}
	if len(info.performers) != 1 || info.performers[0] != "Angel The Dreamgirl" {
		t.Errorf("performers = %v (video.pornstarNames), want one name", info.performers)
	}
}

// Decode the fixture through viewState directly too, so a tag rename is reported
// against the field rather than only as a missing date.
func TestGoldenViewStateDecodesVideoMeta(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "view_state.html"))
	if err != nil {
		t.Fatal(err)
	}
	m := viewStateRe.FindSubmatch(body)
	if m == nil {
		t.Fatal("view-state-data script not found in the fixture")
	}
	var vs viewState
	if err := json.Unmarshal(m[1], &vs); err != nil {
		t.Fatalf("decoding view state: %v", err)
	}
	if vs.Video.PublishedAt != "2019-05-14" {
		t.Errorf("PublishedAt = %q (video.publishedAt)", vs.Video.PublishedAt)
	}
	if len(vs.Video.PornstarNames) == 0 {
		t.Error("PornstarNames is empty (video.pornstarNames) — a flat []string, not objects")
	}
}

func TestGoldenViewStateCarriesNoToken(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "view_state.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"csrfToken", "csrf", "sessionId", "Set-Cookie"} {
		if bytes.Contains(body, []byte(marker)) {
			t.Errorf("fixture contains %q — the live view-state blob carries a CSRF token "+
				"and must not be committed whole; keep only the `video` object", marker)
		}
	}
	if !bytes.Contains(body, []byte(`"pornstarIds"`)) {
		t.Error("fixture lost pornstarIds; the retained unknown fields are what cover " +
			"unknown-field tolerance")
	}
}

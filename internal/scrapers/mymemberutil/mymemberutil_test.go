package mymemberutil

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

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

var testPerformers = map[string]bool{
	"alice test":  true,
	"bob example": true,
}

func testConfig() SiteConfig {
	return SiteConfig{
		SiteID:          "testsite",
		Domain:          "test.example.com",
		StudioName:      "Test Studio",
		KnownPerformers: testPerformers,
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"MILF1928 - Breakfast Fuck 3", "milf1928-breakfast-fuck-3"},
		{"Simple Title", "simple-title"},
		{"Test!@#$Title", "test-title"},
		{"  Trim Spaces  ", "trim-spaces"},
	}
	for _, c := range cases {
		if got := Slugify(c.input); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		input string
		year  int
		month int
		day   int
	}{
		{"2026-04-17T16:00:13.000000Z", 2026, 4, 17},
		{"2025-12-08T23:45:22.000000Z", 2025, 12, 8},
	}
	for _, c := range cases {
		d := ParseDate(c.input)
		if d.Year() != c.year || int(d.Month()) != c.month || d.Day() != c.day {
			t.Errorf("ParseDate(%q) = %v", c.input, d)
		}
	}

	if d := ParseDate(""); !d.IsZero() {
		t.Errorf("ParseDate(\"\") should be zero, got %v", d)
	}
}

func TestSplitKeywords(t *testing.T) {
	kw := "Alice Test, Bob Example, 4K, MILF, Step-Mother Fantasy"
	performers, tags := SplitKeywords(kw, testPerformers)

	if len(performers) != 2 || performers[0] != "Alice Test" || performers[1] != "Bob Example" {
		t.Errorf("performers = %v", performers)
	}
	if len(tags) != 3 || tags[0] != "4K" || tags[2] != "Step-Mother Fantasy" {
		t.Errorf("tags = %v", tags)
	}
}

func TestSplitKeywordsTrimsHandleUnderscores(t *testing.T) {
	performers, tags := SplitKeywords("Zlatexa_, Latex", map[string]bool{"zlatexa_": true})
	if len(performers) != 1 || performers[0] != "Zlatexa" {
		t.Errorf("performers = %v", performers)
	}
	if len(tags) != 1 || tags[0] != "Latex" {
		t.Errorf("tags = %v", tags)
	}
}

func TestSplitKeywordsEmpty(t *testing.T) {
	performers, tags := SplitKeywords("", testPerformers)
	if len(performers) != 0 || len(tags) != 0 {
		t.Errorf("expected empty, got performers=%v tags=%v", performers, tags)
	}
}

func TestVideoPrice(t *testing.T) {
	cases := []struct {
		name   string
		price  any
		want   float64
		wantOK bool
	}{
		{"float", 24.99, 24.99, true},
		{"string", "19.99", 19.99, true},
		{"nil", nil, 0, false},
	}
	for _, c := range cases {
		vid := APIVideo{StreamPrice: c.price}
		got, ok := vid.Price()
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: Price() = (%v, %v), want (%v, %v)", c.name, got, ok, c.want, c.wantOK)
		}
	}
}

func makeTestVideos() []APIVideo {
	return []APIVideo{
		{
			ID:                        100,
			Title:                     "Test Scene One",
			IsPublished:               true,
			PublishDate:               "2026-04-01T12:00:00.000000Z",
			Duration:                  900,
			ContentMappingID:          101,
			ViewsCount:                500,
			LikesCount:                20,
			CommentsCount:             5,
			StreamPrice:               19.99,
			PosterSrc:                 "https://cdn.example.com/thumb1.jpg",
			SystemPreviewVideoFullSrc: "https://cdn.example.com/preview1.mp4",
		},
		{
			ID:                        200,
			Title:                     "Test Scene Two",
			IsPublished:               true,
			PublishDate:               "2026-03-15T10:00:00.000000Z",
			Duration:                  1200,
			ContentMappingID:          201,
			ViewsCount:                300,
			LikesCount:                10,
			StreamPrice:               "24.99",
			PosterSrc:                 "https://cdn.example.com/thumb2.jpg",
			SystemPreviewVideoFullSrc: "https://cdn.example.com/preview2.mp4",
			Has4K:                     true,
		},
	}
}

const detailHTML = `<!DOCTYPE html><html><head>
<meta property="og:description" content="Scene description for testing."/>
<meta property="og:image" content="https://cdn.example.com/og-thumb.jpg"/>
</head><body>
<script>self.__next_f.push([1,"keywords\":\"Alice Test, Bob Example, 4K, MILF, Step-Mother Fantasy\""])</script>
</body></html>`

func newTestServer(videos []APIVideo) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case apiPath:
			page := VideosPage{
				CurrentPage: 1,
				LastPage:    1,
				Total:       len(videos),
				PerPage:     30,
				Data:        videos,
			}
			outer := apiResponse{OK: true}
			outer.Data, _ = json.Marshal(page)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(outer)
		default:
			_, _ = w.Write([]byte(detailHTML))
		}
	}))
}

func TestFetchPage(t *testing.T) {
	videos := makeTestVideos()
	ts := newTestServer(videos)
	defer ts.Close()

	s := New(testConfig())
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	vp, err := s.FetchPage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	if vp.Total != 2 {
		t.Errorf("Total = %d, want 2", vp.Total)
	}
	if len(vp.Data) != 2 {
		t.Fatalf("got %d items, want 2", len(vp.Data))
	}
	if vp.Data[0].Title != "Test Scene One" {
		t.Errorf("Title = %q", vp.Data[0].Title)
	}
}

func TestFetchDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(detailHTML))
	}))
	defer ts.Close()

	s := New(testConfig())
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	detail, err := s.FetchDetail(context.Background(), ts.URL+"/101-test-scene-one")
	if err != nil {
		t.Fatal(err)
	}

	if detail.Description != "Scene description for testing." {
		t.Errorf("description = %q", detail.Description)
	}
	if detail.Thumbnail != "https://cdn.example.com/og-thumb.jpg" {
		t.Errorf("thumbnail = %q", detail.Thumbnail)
	}
	if len(detail.Performers) != 2 || detail.Performers[0] != "Alice Test" || detail.Performers[1] != "Bob Example" {
		t.Errorf("performers = %v", detail.Performers)
	}
	if len(detail.Tags) != 3 || detail.Tags[0] != "4K" {
		t.Errorf("tags = %v", detail.Tags)
	}
}

func TestBuildScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(detailHTML))
	}))
	defer ts.Close()

	s := New(testConfig())
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	vid := makeTestVideos()[1]
	scene, err := s.BuildScene(context.Background(), ts.URL, vid)
	if err != nil {
		t.Fatal(err)
	}

	if scene.ID != "200" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "testsite" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "Test Scene Two" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 1200 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if scene.Width != 3840 || scene.Height != 2160 {
		t.Errorf("Resolution: %dx%d", scene.Width, scene.Height)
	}
	if scene.Resolution != "2160p" {
		t.Errorf("Resolution = %q", scene.Resolution)
	}
	if scene.Studio != "Test Studio" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Views != 300 {
		t.Errorf("Views = %d", scene.Views)
	}
	if scene.Description != "Scene description for testing." {
		t.Errorf("Description = %q", scene.Description)
	}
	if len(scene.Performers) != 2 {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if len(scene.Tags) != 3 {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Thumbnail != "https://cdn.example.com/og-thumb.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 24.99 {
		t.Errorf("PriceHistory = %v", scene.PriceHistory)
	}
}

func TestListScenes(t *testing.T) {
	videos := makeTestVideos()
	ts := newTestServer(videos)
	defer ts.Close()

	s := New(testConfig())
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.Run(context.Background(), ts.URL, scraper.ListOpts{Workers: 1}, out)
	}()

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	videos := makeTestVideos()
	ts := newTestServer(videos)
	defer ts.Close()

	s := New(testConfig())
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.Run(context.Background(), ts.URL, scraper.ListOpts{
			Workers:  1,
			KnownIDs: map[string]bool{"200": true},
		}, out)
	}()

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, out)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 || scenes[0].ID != "100" {
		t.Errorf("got %d scenes, want [100]", len(scenes))
	}
}

func TestListScenesSkipsUnpublished(t *testing.T) {
	videos := []APIVideo{
		{ID: 1, Title: "Published", IsPublished: true, PublishDate: "2026-01-01T00:00:00.000000Z", ContentMappingID: 1},
		{ID: 2, Title: "Unpublished", IsPublished: false, PublishDate: "2026-01-02T00:00:00.000000Z", ContentMappingID: 2},
	}
	ts := newTestServer(videos)
	defer ts.Close()

	s := New(testConfig())
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.Run(context.Background(), ts.URL, scraper.ListOpts{Workers: 1}, out)
	}()

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1 (unpublished should be skipped)", len(scenes))
	}
	if len(scenes) == 1 && scenes[0].Title != "Published" {
		t.Errorf("got %q, expected only Published", scenes[0].Title)
	}
}

func TestMultiPage(t *testing.T) {
	pageData := map[int][]APIVideo{
		1: {{ID: 1, Title: "Scene A", IsPublished: true, PublishDate: "2026-04-01T00:00:00.000000Z", ContentMappingID: 1}},
		2: {{ID: 2, Title: "Scene B", IsPublished: true, PublishDate: "2026-03-01T00:00:00.000000Z", ContentMappingID: 2}},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case apiPath:
			args := r.URL.Query().Get("args")
			page := 1
			for p := 1; p <= 3; p++ {
				expected, _ := json.Marshal([]any{[]string{fmt.Sprintf("page=%d", p)}})
				if args == string(expected) {
					page = p
					break
				}
			}
			videos := pageData[page]
			lastPage := 2
			if page > 2 {
				videos = nil
			}
			vp := VideosPage{
				CurrentPage: page,
				LastPage:    lastPage,
				Total:       2,
				PerPage:     1,
				Data:        videos,
			}
			outer := apiResponse{OK: true}
			outer.Data, _ = json.Marshal(vp)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(outer)
		default:
			_, _ = w.Write([]byte(detailHTML))
		}
	}))
	defer ts.Close()

	s := New(testConfig())
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.Run(context.Background(), ts.URL, scraper.ListOpts{Workers: 1}, out)
	}()

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
}

// --- golden fixture ----------------------------------------------------------
//
// This one covers four packages. `rubberpassion`, `rachelsteele` and
// `kingnoirexxx` each stand up a test server that marshals a
// **mymemberutil.VideosPage** built in Go — so all three share this package's
// struct tags on both sides of the round trip, and a renamed tag would go
// unnoticed in every one of them. Pinning the wire format here covers the util
// and its three wrappers at once.
//
// Byte-verbatim slice of a live
// https://rubber-passion.com/api/cancellable-request?functionName=fetchVideosApi
// response (first two of 526 videos; the page counters copied from the same
// body). No credential: the endpoint is open, and the `args` parameter is just a
// JSON-encoded `[["page=1"]]`.
//
// Shapes a hand-written fixture would have got wrong:
//   - the payload is **double-wrapped**: `{"ok":…,"data":{…VideosPage…}}`, and the
//     inner page has its own `data` array. fetchVideosPage unmarshals twice for
//     this reason, and the outer `ok` is checked before the inner decode.
//   - **`poster` is a bare filename while `poster_src` is the full CDN URL.**
//     BuildScene uses poster_src; taking `poster` yields a thumbnail that is not
//     a URL at all, and nothing downstream would reject it.
//   - **`publish_date` and `original_publish_date` are different dates**
//     (2026-07-25 vs 2024-03-11 for a re-published video). Scene.Date comes from
//     publish_date.
//   - both carry **six-digit microseconds** and a Z, which is why ParseDate falls
//     back to an explicit "2006-01-02T15:04:05.000000Z" layout after RFC3339Nano.
func TestGoldenVideosPage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "videos_page.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Decode exactly as fetchVideosPage does: envelope first, then the page.
	var outer apiResponse
	if err := json.Unmarshal(body, &outer); err != nil {
		t.Fatalf("decoding envelope: %v", err)
	}
	if !outer.OK {
		t.Fatal("envelope ok is false — fetchVideosPage would treat this as an error")
	}
	var page VideosPage
	if err := json.Unmarshal(outer.Data, &page); err != nil {
		t.Fatalf("decoding VideosPage from data: %v", err)
	}

	if len(page.Data) != 2 {
		t.Fatalf("decoded %d videos, want 2", len(page.Data))
	}
	if page.CurrentPage != 1 || page.LastPage != 18 || page.PerPage != 30 || page.Total != 526 {
		t.Errorf("paging = current %d, last %d, per_page %d, total %d; want 1/18/30/526",
			page.CurrentPage, page.LastPage, page.PerPage, page.Total)
	}

	v := page.Data[0]
	if v.ID != 15 {
		t.Errorf("ID = %d (id), want 15", v.ID)
	}
	if v.Title == "" {
		t.Error("Title is empty (title)")
	}
	if !v.IsPublished {
		t.Error("IsPublished is false (is_published) on a published video")
	}
	if v.Duration != 338 {
		t.Errorf("Duration = %d (duration), want 338 seconds", v.Duration)
	}
	if v.ContentMappingID != 20 {
		t.Errorf("ContentMappingID = %d (content_mapping_id), want 20 — the detail URL uses it", v.ContentMappingID)
	}

	// poster_src, not poster.
	if !strings.HasPrefix(v.PosterSrc, "https://") {
		t.Errorf("PosterSrc = %q (poster_src) — BuildScene uses this as Thumbnail and it must "+
			"be a URL; the sibling `poster` field is a bare filename", v.PosterSrc)
	}

	// the date field and its microsecond layout.
	if v.PublishDate != "2026-07-25T20:10:25.000000Z" {
		t.Errorf("PublishDate = %q (publish_date), want six-digit microseconds", v.PublishDate)
	}
	if d := ParseDate(v.PublishDate); d.IsZero() {
		t.Errorf("ParseDate(%q) returned zero — Scene.Date comes from it", v.PublishDate)
	}
}

// The two poster fields and the two date fields, asserted against the raw bytes
// so the distinction survives even if the struct stops decoding one of them.
func TestGoldenVideosPageHasBothPosterAndDateVariants(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "videos_page.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"poster":`, `"poster_src":`, `"publish_date":`, `"original_publish_date":`} {
		if !bytes.Contains(body, []byte(k)) {
			t.Errorf("fixture no longer carries %s — the wrong-pick trap it documents is gone", k)
		}
	}
	if !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Error(`fixture lost the outer {"ok":true,…} envelope`)
	}
}

// The platform's WAF answers 403 to navigation-shaped headers on the API
// endpoint — all three sites broke at once — so the listing fetch must present
// itself as the XHR it is.
func TestFetchPageSendsXHRHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true,"status":200,"data":{"current_page":1,"last_page":1,"data":[]}}`)
	}))
	defer srv.Close()

	s := New(SiteConfig{SiteID: "test", Domain: "example.test", StudioName: "Test"})
	s.Client = srv.Client()
	s.SiteBase = srv.URL

	if _, err := s.FetchPage(context.Background(), 1); err != nil {
		t.Fatalf("FetchPage: %v", err)
	}

	want := map[string]string{
		"Sec-Fetch-Dest": "empty",
		"Sec-Fetch-Mode": "cors",
		"Sec-Fetch-Site": "same-origin",
		"Accept":         "application/json, text/plain, */*",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
	if got.Get("Upgrade-Insecure-Requests") != "" {
		t.Error("navigation headers must not be sent on the API call")
	}
}

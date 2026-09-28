package d2passutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func testConfig() SiteConfig {
	return SiteConfig{SiteID: "1pondo", Domain: "www.1pondo.tv", StudioName: "1Pondo"}
}

func strp(s string) *string { return &s }

// ---- ToScene ----

func TestToScenePrefersEnglish(t *testing.T) {
	s := New(testConfig())
	now := time.Now().UTC()

	m := Movie{
		MovieID:     "072126_001",
		MetaMovieID: 35187,
		Title:       "あいか先生のエッチな実践授業",
		TitleEn:     "The Teacher's Lewd Hands-On Lesson",
		Desc:        "日本語の説明",
		DescEn:      "An English description.",
		Release:     "2026-07-21",
		Duration:    3655,
		ActressesJa: []string{"星野あいか"},
		ActressesEn: []string{"Aika Hoshino"},
		UCNAME:      []string{"AV女優", "中出し"},
		UCNAMEEn:    []string{"AV Idol", "Creampie"},
		SeriesEn:    strp("Some Series"),
		ThumbUltra:  "https://www.1pondo.tv/moviepages/072126_001/images/str.jpg",
	}

	got := s.ToScene("https://www.1pondo.tv", m, now)

	if got.ID != "072126_001" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.SiteID != "1pondo" {
		t.Errorf("SiteID = %q", got.SiteID)
	}
	if got.Studio != "1Pondo" {
		t.Errorf("Studio = %q", got.Studio)
	}
	if got.Title != "The Teacher's Lewd Hands-On Lesson" {
		t.Errorf("Title = %q, want the English title", got.Title)
	}
	if got.Description != "An English description." {
		t.Errorf("Description = %q", got.Description)
	}
	if got.URL != "https://www.1pondo.tv/movies/072126_001/" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Duration != 3655 {
		t.Errorf("Duration = %d", got.Duration)
	}
	if !slices.Equal(got.Performers, []string{"Aika Hoshino"}) {
		t.Errorf("Performers = %v", got.Performers)
	}
	if !slices.Equal(got.Tags, []string{"AV Idol", "Creampie"}) {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.Series != "Some Series" {
		t.Errorf("Series = %q", got.Series)
	}
	want := time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)
	if !got.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", got.Date, want)
	}
	if got.Thumbnail != m.ThumbUltra {
		t.Errorf("Thumbnail = %q", got.Thumbnail)
	}
}

// DescEn is frequently an empty string rather than absent, so the Japanese
// text has to be used instead — otherwise most scenes lose their description.
func TestToSceneFallsBackToJapanese(t *testing.T) {
	s := New(testConfig())

	m := Movie{
		MovieID:     "071926_1251",
		Title:       "制服娘を電車でイタズラ",
		TitleEn:     "",
		Desc:        "日本語の説明",
		DescEn:      "   ",
		ActressesEn: nil,
		ActressesJa: []string{"天野奈々"},
		UCNAMEEn:    []string{},
		UCNAME:      []string{"素人", "痴漢"},
	}

	got := s.ToScene("https://www.1pondo.tv", m, time.Now())

	if got.Title != "制服娘を電車でイタズラ" {
		t.Errorf("Title = %q, want the Japanese title", got.Title)
	}
	if got.Description != "日本語の説明" {
		t.Errorf("Description = %q, want the Japanese description", got.Description)
	}
	if !slices.Equal(got.Performers, []string{"天野奈々"}) {
		t.Errorf("Performers = %v, want the Japanese names", got.Performers)
	}
	if !slices.Equal(got.Tags, []string{"素人", "痴漢"}) {
		t.Errorf("Tags = %v, want the Japanese tags", got.Tags)
	}
}

func TestToSceneThumbnailPreference(t *testing.T) {
	s := New(testConfig())
	cases := []struct {
		name string
		m    Movie
		want string
	}{
		{"ultra wins", Movie{MovieID: "a", ThumbUltra: "u", ThumbHigh: "h", ThumbMed: "m", MovieThumb: "t"}, "u"},
		{"falls to high", Movie{MovieID: "a", ThumbHigh: "h", ThumbMed: "m"}, "h"},
		{"falls to med", Movie{MovieID: "a", ThumbMed: "m", MovieThumb: "t"}, "m"},
		{"falls to movie thumb", Movie{MovieID: "a", MovieThumb: "t"}, "t"},
		{"none", Movie{MovieID: "a"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.ToScene("x", c.m, time.Now()).Thumbnail; got != c.want {
				t.Errorf("Thumbnail = %q, want %q", got, c.want)
			}
		})
	}
}

func TestToSceneSeriesNull(t *testing.T) {
	s := New(testConfig())
	// Series is null on most records.
	if got := s.ToScene("x", Movie{MovieID: "a"}, time.Now()).Series; got != "" {
		t.Errorf("Series = %q, want empty", got)
	}
	if got := s.ToScene("x", Movie{MovieID: "a", Series: strp("Ja Series")}, time.Now()).Series; got != "Ja Series" {
		t.Errorf("Series = %q, want the Japanese series", got)
	}
}

func TestToSceneBadDate(t *testing.T) {
	s := New(testConfig())
	if got := s.ToScene("x", Movie{MovieID: "a", Release: "not-a-date"}, time.Now()); !got.Date.IsZero() {
		t.Errorf("Date = %v, want zero", got.Date)
	}
}

// ---- pagination ----

// fakeAPI serves the offset-keyed listing, and rejects any offset that is not a
// multiple of splitSize exactly as the real server does.
type fakeAPI struct {
	total     int
	splitSize int
	offsets   []int
	badOffset bool
}

func (f *fakeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/dyn/phpauto/movie_lists/list_newest_"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		offset, ok := ParseOffset(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		// The real API 404s on an offset that is not a multiple of SplitSize.
		if offset%f.splitSize != 0 {
			f.badOffset = true
			http.NotFound(w, r)
			return
		}
		f.offsets = append(f.offsets, offset)

		rows := []Movie{}
		for i := offset; i < offset+f.splitSize && i < f.total; i++ {
			rows = append(rows, Movie{
				MovieID:  fmt.Sprintf("id_%03d", i),
				TitleEn:  fmt.Sprintf("Scene %d", i),
				Release:  "2026-01-01",
				Duration: 60,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListPage{TotalRows: f.total, SplitSize: f.splitSize, Rows: rows})
	})
}

func newFake(t *testing.T, total, split int) (*fakeAPI, *Scraper) {
	t.Helper()
	f := &fakeAPI{total: total, splitSize: split}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)

	s := New(testConfig())
	s.Client = srv.Client()
	s.SiteBase = srv.URL
	return f, s
}

func collect(t *testing.T, s *Scraper, opts scraper.ListOpts) ([]models.Scene, int, bool) {
	t.Helper()
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.Run(context.Background(), s.SiteBase, opts, out)
	}()

	var scenes []models.Scene
	total := 0
	stopped := false
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindTotal:
			total = r.Total
		case scraper.KindStoppedEarly:
			stopped = true
		case scraper.KindError:
			t.Fatalf("scraper error: %v", r.Err)
		}
	}
	return scenes, total, stopped
}

// The API keys off a row offset, not a page number. Stepping by 1 would 404 on
// every request after the first.
func TestRunStepsByRowOffset(t *testing.T) {
	f, s := newFake(t, 120, 50)

	scenes, total, _ := collect(t, s, scraper.ListOpts{})

	if f.badOffset {
		t.Error("scraper requested an offset that was not a multiple of SplitSize")
	}
	if len(scenes) != 120 {
		t.Fatalf("got %d scenes, want 120", len(scenes))
	}
	if total != 120 {
		t.Errorf("total = %d, want 120", total)
	}
	if want := []int{0, 50, 100}; !slices.Equal(f.offsets, want) {
		t.Errorf("offsets = %v, want %v", f.offsets, want)
	}
}

// The scraper must adopt the SplitSize the server reports rather than assume 50.
func TestRunAdoptsServerSplitSize(t *testing.T) {
	f, s := newFake(t, 60, 20)

	scenes, _, _ := collect(t, s, scraper.ListOpts{})

	if f.badOffset {
		t.Error("scraper requested a misaligned offset")
	}
	if len(scenes) != 60 {
		t.Fatalf("got %d scenes, want 60", len(scenes))
	}
	if want := []int{0, 20, 40}; !slices.Equal(f.offsets, want) {
		t.Errorf("offsets = %v, want %v", f.offsets, want)
	}
}

// An exactly-full last page must not trigger an extra request past the end.
func TestRunStopsOnExactMultiple(t *testing.T) {
	f, s := newFake(t, 100, 50)

	scenes, _, _ := collect(t, s, scraper.ListOpts{})

	if len(scenes) != 100 {
		t.Fatalf("got %d scenes, want 100", len(scenes))
	}
	if want := []int{0, 50}; !slices.Equal(f.offsets, want) {
		t.Errorf("offsets = %v, want %v — no request past the end", f.offsets, want)
	}
}

func TestRunKnownIDsStopsEarly(t *testing.T) {
	_, s := newFake(t, 120, 50)

	scenes, _, stopped := collect(t, s, scraper.ListOpts{
		KnownIDs: map[string]bool{"id_002": true},
	})

	if !stopped {
		t.Error("expected a StoppedEarly signal")
	}
	// Rows are newest-first, so the walk stops at the end of the page holding
	// the known row — every other row on that page still comes back.
	if len(scenes) != 49 {
		t.Errorf("got %d scenes, want 49 (page one minus the known row)", len(scenes))
	}
}

func TestRunEmptyListing(t *testing.T) {
	_, s := newFake(t, 0, 50)

	scenes, _, _ := collect(t, s, scraper.ListOpts{})
	if len(scenes) != 0 {
		t.Errorf("got %d scenes, want 0", len(scenes))
	}
}

func TestRunContextCancellation(t *testing.T) {
	_, s := newFake(t, 500, 50)

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.Run(ctx, s.SiteBase, scraper.ListOpts{}, out)
	}()
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range out {
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestRunPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	s := New(testConfig())
	s.Client = srv.Client()
	s.SiteBase = srv.URL

	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.Run(context.Background(), srv.URL, scraper.ListOpts{}, out)
	}()

	sawErr := false
	for r := range out {
		if r.Kind == scraper.KindError {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("malformed JSON produced no error result")
	}
}

// ---- ParseOffset ----

func TestParseOffset(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"/dyn/phpauto/movie_lists/list_newest_0.json", 0, true},
		{"/dyn/phpauto/movie_lists/list_newest_150.json", 150, true},
		{"https://x/dyn/phpauto/movie_lists/list_newest_50.json", 50, true},
		{"/dyn/phpauto/movie_lists/list_oldest_50.json", 0, false},
		{"/nonsense", 0, false},
		{"/dyn/phpauto/movie_lists/list_newest_abc.json", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseOffset(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseOffset(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestMovieURL(t *testing.T) {
	s := New(testConfig())
	if got, want := s.MovieURL("072126_001"), "https://www.1pondo.tv/movies/072126_001/"; got != want {
		t.Errorf("MovieURL = %q, want %q", got, want)
	}
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests build Movie values in Go, so encode and decode share the struct
// tag and a renamed tag round-trips unnoticed. This one decodes a listing captured
// verbatim from 1pondo.tv and runs a row through ToScene.
//
// Notable shapes: the JSON keys are **PascalCase** (`MovieID`, `ActressesJa`), which
// is unusual enough that a lowercase guess would be silently wrong; credits and tags
// arrive as parallel Ja/En arrays that the scraper prefers between; and `Series` is a
// *string that is null on most rows.
func TestGoldenListNewest(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "list_newest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var page ListPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decoding captured payload: %v", err)
	}
	if page.TotalRows == 0 {
		t.Error("TotalRows is 0 (TotalRows) — pagination depends on it")
	}
	if page.SplitSize == 0 {
		t.Error("SplitSize is 0 (SplitSize)")
	}
	if len(page.Rows) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(page.Rows))
	}

	m := page.Rows[0]
	if m.MovieID != "072526_001" {
		t.Errorf("MovieID = %q (MovieID)", m.MovieID)
	}
	if m.Title == "" {
		t.Error("Title is empty (Title)")
	}
	if m.Release != "2026-07-25" {
		t.Errorf("Release = %q (Release)", m.Release)
	}
	if m.Duration != 3686 {
		t.Errorf("Duration = %d (Duration), want 3686", m.Duration)
	}
	if len(m.ActressesJa) == 0 {
		t.Error("ActressesJa is empty (ActressesJa)")
	}
	if len(m.UCNAME) == 0 {
		t.Error("UCNAME is empty (UCNAME) — the tag list")
	}
	if m.ThumbUltra == "" && m.ThumbHigh == "" && m.MovieThumb == "" {
		t.Error("no thumbnail decoded (ThumbUltra/ThumbHigh/MovieThumb)")
	}

	s := New(testConfig())
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	sc := s.ToScene("https://www.1pondo.tv/", m, now)

	if sc.ID != "072526_001" {
		t.Errorf("scene ID = %q", sc.ID)
	}
	if sc.Title == "" {
		t.Error("scene Title is empty")
	}
	if sc.Date.Format("2006-01-02") != "2026-07-25" {
		t.Errorf("scene Date = %v, want 2026-07-25", sc.Date)
	}
	if sc.Duration != 3686 {
		t.Errorf("scene Duration = %d, want 3686", sc.Duration)
	}
	if len(sc.Performers) == 0 {
		t.Error("scene Performers is empty")
	}
	if len(sc.Tags) == 0 {
		t.Error("scene Tags is empty")
	}
	if sc.Thumbnail == "" {
		t.Error("scene Thumbnail is empty")
	}
}

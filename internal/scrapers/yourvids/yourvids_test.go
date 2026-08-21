package yourvids

import (
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
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://yourvids.com/creators/rheasweet", true},
		{"https://www.yourvids.com/creators/rheasweet", true},
		{"https://yourvids.com/creators/some-name", true},
		{"https://yourvids.com/vids/some-video", false},
		{"https://example.com/creators/foo", false},

		// The site serves each creator at a bare vanity path too.
		{"https://yourvids.com/leina-sex", true},
		{"https://www.yourvids.com/leina-sex/", true},

		// Its own top-level pages are not creators.
		{"https://yourvids.com/help", false},
		{"https://yourvids.com/Boutique", false},
		{"https://yourvids.com/creators", false},
		{"https://yourvids.com/search", false},
		{"https://yourvids.com/vids", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"98:19", 5899},
		{"34:43", 2083},
		{"1:30:00", 5400},
		{"5:00", 300},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseutil.ParseDurationColon(c.in); got != c.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTitleCase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"dirty talk", "Dirty Talk"},
		{"creamy pussy", "Creamy Pussy"},
		{"affair", "Affair"},
		{"", ""},
	}
	for _, c := range cases {
		if got := titleCase(c.in); got != c.want {
			t.Errorf("titleCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanHTML(t *testing.T) {
	in := "First paragraph.<br>\n<br>\nSecond paragraph with <b>bold</b> and I&#039;m here.</p>"
	got := cleanHTML(in)
	if got != "First paragraph.\nSecond paragraph with bold and I'm here." {
		t.Errorf("cleanHTML = %q", got)
	}
}

func TestToScene(t *testing.T) {
	v := apiVideo{
		ID:            12345,
		Title:         "Test Video",
		Thumbnail:     "https://cdn.yourvids.com/thumb.webp",
		PreviewURL:    "https://cdn.yourvids.com/preview.mp4",
		CreatorName:   "TestCreator",
		Duration:      "34:43",
		Views:         100,
		Likes:         5,
		Price:         "15.00",
		OriginalPrice: json.RawMessage(`30`),
		VideoURL:      "https://yourvids.com/vids/test-video",
		CreatorURL:    "https://yourvids.com/creators/testcreator",
		IsOnSale:      true,
		IsFree:        false,
		IsHD:          true,
		Is4K:          false,
		CreatedAt:     "2026-04-15 20:15:46",
	}

	scene := toScene("https://yourvids.com/creators/testcreator", v, "Full description.", []string{"Tag1", "Tag2"}, fixedTime())

	if scene.ID != "12345" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Test Video" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 2083 {
		t.Errorf("Duration = %d, want 2083", scene.Duration)
	}
	if scene.Views != 100 {
		t.Errorf("Views = %d", scene.Views)
	}
	if scene.Resolution != "1080p" {
		t.Errorf("Resolution = %q", scene.Resolution)
	}
	if scene.Width != 1920 || scene.Height != 1080 {
		t.Errorf("Width/Height = %d/%d", scene.Width, scene.Height)
	}
	if scene.Description != "Full description." {
		t.Errorf("Description = %q", scene.Description)
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "Tag1" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "TestCreator" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 4 || scene.Date.Day() != 15 {
		t.Errorf("Date = %v", scene.Date)
	}
	if len(scene.PriceHistory) != 1 {
		t.Fatalf("PriceHistory len = %d", len(scene.PriceHistory))
	}
	snap := scene.PriceHistory[0]
	if !snap.IsOnSale {
		t.Error("expected IsOnSale=true")
	}
	if snap.Regular != 30 {
		t.Errorf("Regular = %f, want 30", snap.Regular)
	}
	if snap.Discounted != 15 {
		t.Errorf("Discounted = %f, want 15", snap.Discounted)
	}
	if snap.DiscountPercent != 50 {
		t.Errorf("DiscountPercent = %d, want 50", snap.DiscountPercent)
	}
}

func TestToScene_4K(t *testing.T) {
	v := apiVideo{
		ID: 1, Title: "4K Vid", CreatorName: "X", Duration: "5:00",
		Price: "10.00", VideoURL: "https://yourvids.com/vids/x",
		IsHD: true, Is4K: true, CreatedAt: "2026-01-01 00:00:00",
	}
	scene := toScene("https://yourvids.com/creators/x", v, "", nil, fixedTime())
	if scene.Resolution != "4K" || scene.Width != 3840 {
		t.Errorf("Resolution=%q Width=%d", scene.Resolution, scene.Width)
	}
}

func TestToScene_Free(t *testing.T) {
	v := apiVideo{
		ID: 2, Title: "Free Vid", CreatorName: "X", Duration: "1:00",
		Price: "0.00", VideoURL: "https://yourvids.com/vids/x",
		IsFree: true, CreatedAt: "2026-01-01 00:00:00",
	}
	scene := toScene("https://yourvids.com/creators/x", v, "", nil, fixedTime())
	if !scene.PriceHistory[0].IsFree {
		t.Error("expected IsFree=true")
	}
}

func TestListScenes(t *testing.T) {
	page1 := apiResponse{
		Success: true,
		Data: apiData{
			Videos: []apiVideo{
				{
					ID: 100, Title: "Video One", CreatorName: "Creator",
					Duration: "10:00", Price: "5.00", IsHD: true,
					VideoURL:  "%s/vids/video-one",
					CreatedAt: "2026-04-15 10:00:00",
				},
				{
					ID: 101, Title: "Video Two", CreatorName: "Creator",
					Duration: "20:00", Price: "10.00", IsHD: true,
					VideoURL:  "%s/vids/video-two",
					CreatedAt: "2026-04-14 10:00:00",
				},
			},
			Pagination: apiPagination{CurrentPage: 1, PerPage: 20, Total: 2, TotalPages: 1, HasMore: false},
		},
	}

	detailHTML := `<html><head><title>Video | YourVids</title></head><body>
<div class="rich-text-content text-sm text-gray-700 leading-relaxed mb-4">
A great description of the video.
</div>
<button data-filter="tag" data-tag="affair">Affair</button>
<button data-filter="tag" data-tag="dirty-talk">Dirty Talk</button>
</body></html>`

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/creators/testcreator/videos":
			p1 := page1
			for i := range p1.Data.Videos {
				p1.Data.Videos[i].VideoURL = fmt.Sprintf(p1.Data.Videos[i].VideoURL, ts.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(p1)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(detailHTML))
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client:  ts.Client(),
		apiBase: ts.URL,
	}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/creators/testcreator", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, s := range scenes {
		if s.Description != "A great description of the video." {
			t.Errorf("Description = %q", s.Description)
		}
		if len(s.Tags) != 2 {
			t.Errorf("Tags = %v", s.Tags)
		}
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	page1 := apiResponse{
		Success: true,
		Data: apiData{
			Videos: []apiVideo{
				{
					ID: 100, Title: "New", CreatorName: "C", Duration: "5:00",
					Price: "5.00", VideoURL: "%s/vids/new", CreatedAt: "2026-04-15 10:00:00",
				},
				{
					ID: 99, Title: "Known", CreatorName: "C", Duration: "5:00",
					Price: "5.00", VideoURL: "%s/vids/known", CreatedAt: "2026-04-14 10:00:00",
				},
			},
			Pagination: apiPagination{CurrentPage: 1, PerPage: 20, Total: 2, TotalPages: 1, HasMore: false},
		},
	}

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/creators/c/videos":
			p1 := page1
			for i := range p1.Data.Videos {
				p1.Data.Videos[i].VideoURL = fmt.Sprintf(p1.Data.Videos[i].VideoURL, ts.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(p1)
		default:
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), apiBase: ts.URL}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/creators/c", scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"99": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)

	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].Title != "New" {
		t.Errorf("got %d scenes, want [New]", len(scenes))
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
}

// --- golden fixture ----------------------------------------------------------
//
// The other tests marshal the same structs the scraper unmarshals, so a renamed
// json tag round-trips and passes. This one serves a creator page captured
// verbatim from the live API. Each assertion names the field it pins.
//
// yourvids is a priced storefront, so the money fields matter most: `price` and
// `original_price` arrive as *strings* and `original_price` is null when nothing
// is discounted — a shape easy to mis-model and impossible to catch with a
// struct-built fixture.
func TestGoldenCreatorVideos(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "creator_videos.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/api/creators/") {
			// Only page 1 has content; later pages are empty so the walk ends.
			// Serving the same page for every request would loop forever.
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"success":true,"data":{"videos":[]}}`))
				return
			}
			_, _ = w.Write(body)
			return
		}
		// Detail fetches: the scraper tolerates these failing.
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), apiBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/creators/bettie-bondage", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes from the captured page, want 2", len(scenes))
	}

	var sc *models.Scene
	for i := range scenes {
		if scenes[i].ID == "128709" {
			sc = &scenes[i]
		}
	}
	if sc == nil {
		t.Fatalf("scene 128709 missing (data.videos[].id); got %v", scenes)
	}
	if sc.Title != "Ending Your Semen Retention" {
		t.Errorf("Title = %q (data.videos[].title)", sc.Title)
	}
	if sc.Date.IsZero() {
		t.Error("Date is zero (data.videos[].created_at)")
	}
	// duration arrives as "43:43", not seconds.
	if sc.Duration != 43*60+43 {
		t.Errorf("Duration = %d (data.videos[].duration \"43:43\"), want %d", sc.Duration, 43*60+43)
	}
	if !strings.Contains(sc.URL, "ending-your-semen-retention") {
		t.Errorf("URL = %q (data.videos[].video_url)", sc.URL)
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail is empty (data.videos[].thumbnail)")
	}
	if sc.Views != 2345 || sc.Likes != 15 {
		t.Errorf("Views/Likes = %d/%d (data.videos[].views/.likes), want 2345/15", sc.Views, sc.Likes)
	}
	// price is a string; original_price is null here, so this must not be read
	// as a sale.
	if len(sc.PriceHistory) != 1 {
		t.Fatalf("PriceHistory = %+v, want one snapshot (data.videos[].price)", sc.PriceHistory)
	}
	p := sc.PriceHistory[0]
	if p.Regular != 33.99 {
		t.Errorf("Regular = %v (data.videos[].price \"33.99\")", p.Regular)
	}
	if p.IsOnSale {
		t.Errorf("IsOnSale = true, but original_price is null (data.videos[].is_on_sale)")
	}
}

func TestPreferredStudioURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want string
	}{
		{"https://yourvids.com/leina-sex", "https://yourvids.com/creators/leina-sex"},
		{"https://www.yourvids.com/leina-sex/", "https://yourvids.com/creators/leina-sex"},
		{"http://yourvids.com/creators/leina-sex", "https://yourvids.com/creators/leina-sex"},
		{"https://yourvids.com/help", ""},
		{"https://example.com/leina-sex", ""},
	}
	for _, c := range cases {
		if got := s.PreferredStudioURL(c.url); got != c.want {
			t.Errorf("PreferredStudioURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// A bare-slug URL may name a viewer account rather than a creator, which the
// API answers with a 404. The error has to say so, and must still mark the
// traversal incomplete so an authoritative Save cannot delete the catalogue.
func TestListScenesNotACreator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"message": ""}`)
	}))
	defer srv.Close()

	s := New()
	s.apiBase = srv.URL
	s.client = srv.Client()

	ch, err := s.ListScenes(context.Background(), "https://yourvids.com/whaleman2018", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var gotErr error
	for res := range ch {
		if res.Kind == scraper.KindError {
			gotErr = res.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error result, got none")
	}
	if !strings.Contains(gotErr.Error(), "no creator") {
		t.Errorf("error = %q, want it to name the missing creator", gotErr)
	}
	if !scraper.Classify(gotErr).MissingData() {
		t.Error("a missing catalogue must count as missing data, not as absent")
	}
}

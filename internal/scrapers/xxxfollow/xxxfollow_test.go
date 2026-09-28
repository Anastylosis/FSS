package xxxfollow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.xxxfollow.com/katanakombat/premium", true},
		{"https://www.xxxfollow.com/katanakombat", true},
		{"https://xxxfollow.com/katanakombat/", true},
		{"https://www.xxxfollow.com/katanakombat/premium/best-sellers", true},
		{"https://www.xfollow.com/katanakombat/premium", true},
		{"https://www.xxxfollow.com/some.creator_name-1", true},

		// A single premium post, not a listing.
		{"https://www.xxxfollow.com/katanakombat/premium/solo-masterbating", false},
		// Other profile tabs the scraper does not read.
		{"https://www.xxxfollow.com/katanakombat/most-likes", false},
		{"https://www.xxxfollow.com/katanakombat/subscribe", false},
		{"https://www.xxxfollow.com", false},
		{"https://www.manyvids.com/Profile/123", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// Creator profiles live at the site root, so site pages and locale
// subdirectories sit in the same namespace as usernames and must not be
// mistaken for creators.
func TestMatchesURLRejectsReservedSegments(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.xxxfollow.com/support",
		"https://www.xxxfollow.com/support/faq",
		"https://www.xxxfollow.com/login",
		"https://www.xxxfollow.com/tag",
		"https://www.xxxfollow.com/top",
		"https://www.xxxfollow.com/newest-creators",
		"https://www.xxxfollow.com/post/1164932",
		"https://www.xxxfollow.com/sitemap.xml",
		"https://www.xxxfollow.com/de",
		"https://www.xxxfollow.com/pt-br",
		"https://www.xxxfollow.com/Support",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

func TestParseURL(t *testing.T) {
	cases := []struct {
		url      string
		wantUser string
		wantSort string
	}{
		{"https://www.xxxfollow.com/katanakombat", "katanakombat", ""},
		{"https://www.xxxfollow.com/katanakombat/premium", "katanakombat", ""},
		{"https://www.xxxfollow.com/katanakombat/premium/", "katanakombat", ""},
		{"https://www.xxxfollow.com/katanakombat/premium/best-sellers", "katanakombat", "best-sellers"},
		{"https://www.xfollow.com/katanakombat/premium", "katanakombat", ""},
		// Test-server fallback: host is not xxxfollow.com.
		{"http://127.0.0.1:1234/katanakombat", "katanakombat", ""},
		{"http://127.0.0.1:1234/katanakombat/premium/best-sellers", "katanakombat", "best-sellers"},
		{"https://www.xxxfollow.com/", "", ""},
	}
	for _, c := range cases {
		user, sortBy := parseURL(c.url)
		if user != c.wantUser || sortBy != c.wantSort {
			t.Errorf("parseURL(%q) = (%q, %q), want (%q, %q)", c.url, user, sortBy, c.wantUser, c.wantSort)
		}
	}
}

// makePost builds one premium listing entry shaped like the live API's.
func makePost(id int, slug, text string, amount float64) listItem {
	it := listItem{LikeCount: 4, ViewCount: 9, CommentCount: 2}
	it.Post = post{
		ID:                 id,
		Access:             "paid",
		AmountUSD:          amount,
		CreatedAt:          "2026-06-04T19:14:04+0000",
		Slug:               slug,
		Text:               text,
		VideoDurationTotal: 140,
		DurationTotal:      140,
		Media: []media{{
			Type:           "video",
			DurationInSecs: 140,
			Width:          720,
			Height:         1280,
			BlurURL:        fmt.Sprintf("https://media.example/%d_blur.jpg", id),
			PreviewURL:     fmt.Sprintf("https://media.example/%d_preview.mp4", id),
		}},
	}
	it.Post.MediaCount.Video = 1
	it.Post.User.Username = "katanakombat"
	it.Post.User.DisplayName = "Katana Kombat"
	return it
}

func makePosts(n, startID int) []listItem {
	items := make([]listItem, n)
	for i := range items {
		id := startID + i
		items[i] = makePost(id, fmt.Sprintf("scene-%d", id), fmt.Sprintf("Scene %d", id), 10)
	}
	return items
}

// newTestServer serves the premium listing endpoint, one entry of pages per
// page number. It records the sort_by values it was asked for.
func newTestServer(t *testing.T, pages [][]listItem, sorts *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/media/public/premium") {
			http.NotFound(w, r)
			return
		}
		if sorts != nil {
			*sorts = append(*sorts, r.URL.Query().Get("sort_by"))
		}
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 {
			http.Error(w, "bad page", http.StatusBadRequest)
			return
		}
		var items []listItem
		if page <= len(pages) {
			items = pages[page-1]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listResponse{List: items})
	}))
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(t, [][]listItem{makePosts(3, 1)}, nil)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katanakombat/premium", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1" {
		t.Errorf("ID = %q, want %q", sc.ID, "1")
	}
	if sc.SiteID != "xxxfollow" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Title != "Scene 1" {
		t.Errorf("Title = %q", sc.Title)
	}
	if want := ts.URL + "/katanakombat/premium/scene-1"; sc.URL != want {
		t.Errorf("URL = %q, want %q", sc.URL, want)
	}
	assertUTC(t, sc.Date, time.Date(2026, 6, 4, 19, 14, 4, 0, time.UTC))
	if sc.Duration != 140 {
		t.Errorf("Duration = %d, want 140", sc.Duration)
	}
	if sc.Width != 720 || sc.Height != 1280 {
		t.Errorf("dimensions = %dx%d, want 720x1280", sc.Width, sc.Height)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Katana Kombat" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Likes != 4 || sc.Views != 9 || sc.Comments != 2 {
		t.Errorf("engagement = %d/%d/%d", sc.Likes, sc.Views, sc.Comments)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 10 {
		t.Errorf("PriceHistory = %+v", sc.PriceHistory)
	}
	if sc.LowestPrice != 10 {
		t.Errorf("LowestPrice = %v, want 10", sc.LowestPrice)
	}
}

func TestListScenesPagination(t *testing.T) {
	// A full page must be followed; the short page ends the walk.
	ts := newTestServer(t, [][]listItem{makePosts(perPage, 1), makePosts(5, 100)}, nil)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katanakombat/premium", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != perPage+5 {
		t.Fatalf("got %d scenes, want %d", len(scenes), perPage+5)
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := newTestServer(t, [][]listItem{makePosts(5, 1)}, nil)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katanakombat/premium", scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4 — the page is finished before the stop", len(scenes))
	}
}

// Only the default ordering is date-descending. Under best-sellers a known ID
// can sit above unseen older posts, so the early-stop hint must be dropped.
func TestBestSellersSortIgnoresKnownIDs(t *testing.T) {
	var sorts []string
	ts := newTestServer(t, [][]listItem{makePosts(5, 1)}, &sorts)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katanakombat/premium/best-sellers", scraper.ListOpts{
		KnownIDs: map[string]bool{"3": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if stoppedEarly {
		t.Error("best-sellers sort must not stop early on a known ID")
	}
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}
	if len(sorts) == 0 || sorts[0] != "best-sellers" {
		t.Errorf("sort_by sent = %v, want first request to carry best-sellers", sorts)
	}
}

// The default listing omits sort_by entirely, matching the site's own request.
func TestDefaultSortOmitsSortBy(t *testing.T) {
	var sorts []string
	ts := newTestServer(t, [][]listItem{makePosts(2, 1)}, &sorts)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katanakombat/premium", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	testutil.CollectScenes(t, ch)

	if len(sorts) == 0 || sorts[0] != "" {
		t.Errorf("sort_by sent = %v, want empty", sorts)
	}
}

// Premium posts are galleries and may hold pictures or audio rather than
// video; those are not scenes. A page that filters to zero must not be read as
// the end of the listing.
func TestSkipsNonVideoPosts(t *testing.T) {
	pictureOnly := makePost(1, "gallery", "Photo set", 5)
	pictureOnly.Post.MediaCount.Video = 0
	pictureOnly.Post.MediaCount.Picture = 12
	pictureOnly.Post.Media[0].Type = "picture"

	page1 := make([]listItem, 0, perPage)
	page1 = append(page1, pictureOnly)
	page1 = append(page1, makePosts(perPage-1, 10)...)
	// A whole page of galleries, which must not terminate the walk.
	page2 := make([]listItem, perPage)
	for i := range page2 {
		p := makePost(200+i, fmt.Sprintf("gallery-%d", i), "Photo set", 5)
		p.Post.MediaCount.Video = 0
		p.Post.MediaCount.Picture = 3
		page2[i] = p
	}
	page3 := makePosts(2, 300)

	ts := newTestServer(t, [][]listItem{page1, page2, page3}, nil)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katanakombat/premium", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != (perPage-1)+2 {
		t.Fatalf("got %d scenes, want %d", len(scenes), (perPage-1)+2)
	}
	for _, sc := range scenes {
		if strings.HasPrefix(sc.Title, "Photo set") {
			t.Errorf("picture-only post leaked through: %q", sc.Title)
		}
	}
}

func TestToSceneThumbnailFallsBackToBlur(t *testing.T) {
	// Most premium posts carry no preview image; the blurred still shipped
	// with the locked video is all a signed-out client can see.
	it := makePost(7, "slug", "Text", 9.99)
	sc := toScene(defaultBase, "https://www.xxxfollow.com/katanakombat/premium", "katanakombat", it)
	if sc.Thumbnail != "https://media.example/7_blur.jpg" {
		t.Errorf("Thumbnail = %q, want the blur still", sc.Thumbnail)
	}
	if sc.Preview != "https://media.example/7_preview.mp4" {
		t.Errorf("Preview = %q", sc.Preview)
	}

	it.Post.Preview = &struct {
		URL      string `json:"url"`
		ThumbURL string `json:"thumb_url"`
	}{URL: "https://media.example/7_full.jpg"}
	sc = toScene(defaultBase, "https://www.xxxfollow.com/katanakombat/premium", "katanakombat", it)
	if sc.Thumbnail != "https://media.example/7_full.jpg" {
		t.Errorf("Thumbnail = %q, want the post preview to win", sc.Thumbnail)
	}
}

func TestTitleFallbacks(t *testing.T) {
	tagged := post{ID: 5}
	tagged.Tags = []struct {
		Tag string `json:"tag"`
	}{{Tag: "solo"}, {Tag: "blonde"}, {Tag: "toys"}, {Tag: "ignored"}}

	cases := []struct {
		name string
		in   post
		want string
	}{
		{"caption", post{ID: 1, Text: "  sold these panties  "}, "sold these panties"},
		{"tags", tagged, "solo, blonde, toys"},
		{"neither", post{ID: 42}, "Video 42"},
	}
	for _, c := range cases {
		if got := title(c.in); got != c.want {
			t.Errorf("%s: title() = %q, want %q", c.name, got, c.want)
		}
	}
}

// One live post carries an empty slug, which has no address under
// /{user}/premium/. /post/{id} resolves for every post.
func TestSceneURLSluglessPost(t *testing.T) {
	if got := sceneURL(defaultBase, "katanakombat", post{ID: 99}); got != defaultBase+"/post/99" {
		t.Errorf("sceneURL() = %q", got)
	}
	if got := sceneURL(defaultBase, "katanakombat", post{ID: 99, Slug: "s"}); got != defaultBase+"/katanakombat/premium/s" {
		t.Errorf("sceneURL() = %q", got)
	}
}

func TestFreePostPrice(t *testing.T) {
	it := makePost(3, "free-one", "Freebie", 0)
	it.Post.Access = "free"
	sc := toScene(defaultBase, defaultBase+"/katanakombat/premium", "katanakombat", it)
	if len(sc.PriceHistory) != 1 || !sc.PriceHistory[0].IsFree {
		t.Fatalf("PriceHistory = %+v, want one free snapshot", sc.PriceHistory)
	}
	if sc.LowestPrice != 0 || sc.LowestPriceDate == nil {
		t.Errorf("LowestPrice = %v (date %v), want a recorded zero", sc.LowestPrice, sc.LowestPriceDate)
	}
}

// A cancelled scrape must stop, not keep walking pages nobody reads. The
// server below never runs out of pages, so the only thing that ends the walk
// is the context.
func TestCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		// Unique IDs per page so Paginate's repeat-page guard never trips.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listResponse{List: makePosts(perPage, page*1000)})
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	testutil.AssertCancellable(t, s, ts.URL+"/katanakombat/premium", scraper.ListOpts{})
}

// --- golden fixture ----------------------------------------------------------
//
// Every other test in this file builds its server response by marshalling the
// same structs the scraper unmarshals, so encode and decode share the struct
// tag and a wrong tag round-trips perfectly. This one serves a payload captured
// verbatim from the live API, so a renamed or mistyped `json:"..."` tag fails
// here even though it would pass everywhere else.

func TestGoldenPremiumPage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "premium_page.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/katanakombat/premium", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	sc := scenes[0]
	// Each assertion pins one json tag against the real payload.
	if sc.ID != "1261687" {
		t.Errorf("ID = %q (post.id)", sc.ID)
	}
	if sc.Title != "sold these panties and bra" {
		t.Errorf("Title = %q (post.text)", sc.Title)
	}
	if want := ts.URL + "/katanakombat/premium/sold-these-panties-and-bra"; sc.URL != want {
		t.Errorf("URL = %q (post.slug), want %q", sc.URL, want)
	}
	assertUTC(t, sc.Date, time.Date(2026, 6, 4, 19, 14, 4, 0, time.UTC))
	if sc.Duration != 140 {
		t.Errorf("Duration = %d (post.video_duration_total)", sc.Duration)
	}
	if sc.Width != 720 || sc.Height != 1280 {
		t.Errorf("dimensions = %dx%d (media[].width/height)", sc.Width, sc.Height)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Katana Kombat" {
		t.Errorf("Performers = %v (post.user.display_name)", sc.Performers)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 7 {
		t.Errorf("PriceHistory = %+v (post.amount_usd)", sc.PriceHistory)
	}
	if sc.Thumbnail == "" {
		t.Error("Thumbnail is empty (post.preview.url / media[].blur_url)")
	}
	if sc.Preview == "" {
		t.Error("Preview is empty (media[].preview_url)")
	}
	// media_count.video drives the videos-only filter; if its tag broke, both
	// fixture posts would have been dropped and the count check above would
	// already have failed.
	if scenes[1].ID != "1164932" {
		t.Errorf("second scene ID = %q, want 1164932", scenes[1].ID)
	}
}

// assertUTC checks both the instant and the location.
//
// A layout ending in a bare "Z" does NOT assert UTC — in a Go layout that Z is
// a literal, not a zone verb (only Z07:00/Z0700 are), so it prints the wall
// clock plus a hard-coded Z whatever the zone is. time.Equal alone is no better:
// it compares instants and ignores location. Only Location() catches a dropped
// .UTC().
func assertUTC(t *testing.T, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("Date = %v, want %v", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("Date location = %v, want UTC", got.Location())
	}
}

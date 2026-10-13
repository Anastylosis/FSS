package zexyvr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs []error) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return scenes, errs
}

type recorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *recorder) add(p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, p)
}

func newServer(t *testing.T, rec *recorder) *httptest.Server {
	t.Helper()
	listing := fixture(t, "listing_page1.html")
	model := fixture(t, "model.html")
	detail := fixture(t, "detail.html")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.RequestURI())
		switch {
		case strings.HasPrefix(r.URL.Path, "/models/blossom/"):
			_, _ = w.Write([]byte(model))
		case strings.Count(r.URL.Path, "/") == 3 && strings.HasPrefix(r.URL.Path, "/videos/"):
			_, _ = w.Write([]byte(detail))
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(listing))
		default:
			_, _ = w.Write([]byte("<html><body>nothing</body></html>"))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func newTestScraper(ts *httptest.Server) *Scraper {
	s := New()
	s.client = ts.Client()
	s.base = ts.URL
	return s
}

func TestListScenesCatalogue(t *testing.T) {
	rec := &recorder{}
	ts := newServer(t, rec)
	s := newTestScraper(ts)

	studioURL := "https://zexyvr.com/videos"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs := collect(ch)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	sc := scenes[0]
	if sc.ID != "2279" || sc.SiteID != "zexyvr" || sc.Studio != "ZexyVR" {
		t.Errorf("identity: %+v", sc)
	}
	if sc.Title != "Red Hot" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.StudioURL != studioURL {
		t.Errorf("StudioURL = %q", sc.StudioURL)
	}
	if want := ts.URL + "/videos/louise+p+red+hot/2279"; sc.URL != want {
		t.Errorf("URL = %q, want %q", sc.URL, want)
	}
	if want := time.Date(2024, 12, 6, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Duration != 12*60+40 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Louise P" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if want := "https://c73d2da614.mjedge.net/2279/o/artwork/art.jpg?w=450&h=300"; sc.Thumbnail != want {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt unset")
	}

	// The fixture detail page enriches every scene.
	if !strings.HasPrefix(sc.Description, "In today’s update, Blossom struts") {
		t.Errorf("description = %q", sc.Description)
	}
	wantTags := []string{"22-29", "5.7K", "8K", "Belgian", "Black", "Brunette", "English", "Green", "Kitchen", "Medium", "Natural", "Secretary", "Shaved", "Tattoos"}
	if len(sc.Tags) != len(wantTags) {
		t.Fatalf("tags = %v", sc.Tags)
	}
	for i, tg := range wantTags {
		if sc.Tags[i] != tg {
			t.Errorf("tag %d = %q, want %q", i, sc.Tags[i], tg)
		}
	}

	var pages []string
	for _, p := range rec.paths {
		if strings.HasPrefix(p, "/videos?") {
			pages = append(pages, p)
		}
	}
	if len(pages) != 2 || pages[0] != "/videos?order=&page=1" || pages[1] != "/videos?order=&page=2" {
		t.Errorf("listing requests = %v", pages)
	}
}

func TestListScenesKnownIDsSkipsDetail(t *testing.T) {
	rec := &recorder{}
	ts := newServer(t, rec)
	s := newTestScraper(ts)

	known := map[string]bool{"2279": true, "2229": true, "2234": true}
	ch, err := s.ListScenes(context.Background(), "https://zexyvr.com/videos", scraper.ListOpts{KnownIDs: known})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _ := collect(ch)
	if len(scenes) != 0 {
		t.Errorf("got %d scenes, want 0", len(scenes))
	}
	for _, p := range rec.paths {
		if strings.Count(strings.SplitN(p, "?", 2)[0], "/") == 3 {
			t.Errorf("fetched detail %q for a known scene", p)
		}
	}
	if len(rec.paths) != 1 {
		t.Errorf("requests = %v, want only page 1", rec.paths)
	}
}

func TestListScenesTagPage(t *testing.T) {
	rec := &recorder{}
	ts := newServer(t, rec)
	s := newTestScraper(ts)

	studioURL := "https://zexyvr.com/videos/22-29-vr-videos-118?order=&page=3"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs := collect(ch)
	if len(errs) != 0 || len(scenes) != 3 {
		t.Fatalf("scenes=%d errs=%v", len(scenes), errs)
	}
	if scenes[0].StudioURL != studioURL {
		t.Errorf("StudioURL = %q", scenes[0].StudioURL)
	}
	if rec.paths[0] != "/videos/22-29-vr-videos-118?order=&page=1" {
		t.Errorf("first request = %q", rec.paths[0])
	}
}

func TestListScenesModelPage(t *testing.T) {
	rec := &recorder{}
	ts := newServer(t, rec)
	s := newTestScraper(ts)

	studioURL := "https://zexyvr.com/models/blossom/122"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs := collect(ch)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (only the model's own videos)", len(scenes))
	}
	if scenes[0].ID != "2218" || scenes[0].Title != "Devilish Delights" {
		t.Errorf("first = %+v", scenes[0])
	}
	if len(scenes[0].Performers) != 1 || scenes[0].Performers[0] != "Blossom" {
		t.Errorf("performers = %v", scenes[0].Performers)
	}
	if scenes[0].StudioURL != studioURL {
		t.Errorf("StudioURL = %q", scenes[0].StudioURL)
	}
	if rec.paths[0] != "/models/blossom/122" {
		t.Errorf("first request = %q", rec.paths[0])
	}
}

func TestEmptyFirstPageIsParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>redesigned</body></html>"))
	}))
	defer ts.Close()
	s := newTestScraper(ts)

	ch, _ := s.ListScenes(context.Background(), "https://zexyvr.com/videos", scraper.ListOpts{})
	scenes, errs := collect(ch)
	if len(scenes) != 0 || len(errs) != 1 {
		t.Fatalf("scenes=%d errs=%v", len(scenes), errs)
	}
	if got := scraper.Classify(errs[0]); got != scraper.FailureParse {
		t.Errorf("failure kind = %v, want parse", got)
	}
}

func TestParseListing(t *testing.T) {
	cards := parseListing(fixture(t, "listing_page1.html"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards", len(cards))
	}
	if cards[1].id != "2229" || cards[1].title != "Undress For You" || cards[1].path != "/videos/blossom+undress+for+you/2229" {
		t.Errorf("card 1 = %+v", cards[1])
	}
	if cards[1].duration != 7*60+43 {
		t.Errorf("duration = %d", cards[1].duration)
	}
}

func TestApplyDetail(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, fixture(t, "detail.html"))
	if !strings.HasSuffix(sc.Description, "this fantasy will soon be a reality!") {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Tags) != 14 || sc.Tags[0] != "22-29" {
		t.Errorf("tags = %v", sc.Tags)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://zexyvr.com", true},
		{"https://zexyvr.com/", true},
		{"https://www.zexyvr.com/videos?order=&page=2", true},
		{"https://zexyvr.com/models/blossom/122", true},
		{"https://zexyvr.com/videos/22-29-vr-videos-118", true},
		{"https://zexyvr.com.evil.net/", false},
		{"https://notzexyvr.com/", false},
		{"https://example.com/zexyvr.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestResolveListing(t *testing.T) {
	tests := []struct {
		url   string
		path  string
		model bool
	}{
		{"https://zexyvr.com/", "/videos", false},
		{"https://zexyvr.com/videos", "/videos", false},
		{"https://zexyvr.com/videos?order=&page=4", "/videos", false},
		{"https://zexyvr.com/videos/22-29-vr-videos-118", "/videos/22-29-vr-videos-118", false},
		{"https://zexyvr.com/videos/22-29-vr-videos-118/?order=&page=2", "/videos/22-29-vr-videos-118", false},
		{"https://zexyvr.com/models/blossom/122", "/models/blossom/122", true},
		{"https://zexyvr.com/models/molly+m/167/", "/models/molly+m/167", true},
		{"https://zexyvr.com/models", "/videos", false},
	}
	for _, tt := range tests {
		got := resolveListing(tt.url)
		if got.path != tt.path || got.model != tt.model {
			t.Errorf("resolveListing(%q) = %+v, want %q/%v", tt.url, got, tt.path, tt.model)
		}
	}
}

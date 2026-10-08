package gayerdar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newServer(t *testing.T, details *atomic.Int32) *httptest.Server {
	t.Helper()
	page1, page6 := fixture(t, "page1.json"), fixture(t, "page6.json")
	detail, model := fixture(t, "detail.json"), fixture(t, "model.json")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v2/videos" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write(page1)
		case r.URL.Path == "/api/v2/videos" && r.URL.Query().Get("page") == "2":
			_, _ = w.Write(page6)
		case r.URL.Path == "/api/v2/videos":
			_, _ = w.Write([]byte(`{"status":true,"data":{"list":[],"pagination":{"total_records":3,"total_pages":2}}}`))
		case strings.HasPrefix(r.URL.Path, "/api/v2/videos/"):
			details.Add(1)
			_, _ = w.Write(detail)
		case r.URL.Path == "/api/v2/models/116":
			_, _ = w.Write(model)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), api: ts.URL, web: ts.URL}
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	var errs []error
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

func TestListScenesCatalogue(t *testing.T) {
	var details atomic.Int32
	ts := newServer(t, &details)
	defer ts.Close()

	scenes, errs := collect(t, newTestScraper(ts), "https://gayerdar.com/en-US", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if details.Load() != 3 {
		t.Errorf("detail fetches = %d, want 3", details.Load())
	}
	sc := scenes[0]
	if sc.ID != "GDSR-006-2" || sc.SiteID != "gayerdar" || sc.Studio != "Gayerdar" {
		t.Errorf("identity: %+v", sc)
	}
	if sc.Title != "Monkey King EP2: Fertilize Both the King & Prince" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.StudioURL != "https://gayerdar.com/en-US" {
		t.Errorf("studio URL = %q", sc.StudioURL)
	}
	if sc.URL != ts.URL+"/en-US/videos/GDSR-006-2" {
		t.Errorf("url = %q", sc.URL)
	}
	if want := time.UnixMilli(1791962700000).UTC(); !sc.Date.Equal(want) {
		t.Errorf("date = %v, want %v", sc.Date, want)
	}
	if sc.Duration != 1805 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if got := strings.Join(sc.Performers, ","); got != "Yan Jia You,Luke,Diwu" {
		t.Errorf("performers = %q", got)
	}
	if got := strings.Join(sc.Tags, ","); got != "Tight Bussy,Asian,Anal" {
		t.Errorf("tags = %q", got)
	}
	if sc.Thumbnail == "" || sc.Description == "" || sc.ScrapedAt.IsZero() {
		t.Errorf("missing media/description: %+v", sc)
	}
}

func TestListScenesKnownIDsSkipDetail(t *testing.T) {
	var details atomic.Int32
	ts := newServer(t, &details)
	defer ts.Close()

	known := map[string]bool{"GDSR-006-2": true, "GDSR-006-1": true}
	_, errs := collect(t, newTestScraper(ts), "https://gayerdar.com/", scraper.ListOpts{KnownIDs: known})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if details.Load() != 0 {
		t.Errorf("detail fetches = %d, want 0 for a fully known page", details.Load())
	}
}

func TestListScenesModel(t *testing.T) {
	var details atomic.Int32
	ts := newServer(t, &details)
	defer ts.Close()

	url := "https://gayerdar.com/en-US/models/116"
	scenes, errs := collect(t, newTestScraper(ts), url, scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].ID != "GD-037" || scenes[0].StudioURL != url {
		t.Errorf("scene = %+v", scenes[0])
	}
	if len(scenes[0].Performers) == 0 {
		t.Error("model scene should be enriched with its cast")
	}
}

func TestListScenesDetailError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/videos" {
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write(fixture(t, "page6.json"))
			} else {
				_, _ = w.Write([]byte(`{"status":true,"data":{"list":[]}}`))
			}
			return
		}
		http.Error(w, "boom", http.StatusNotFound)
	}))
	defer ts.Close()

	scenes, errs := collect(t, newTestScraper(ts), "https://gayerdar.com/", scraper.ListOpts{})
	if len(scenes) != 1 || len(errs) != 1 {
		t.Fatalf("scenes=%d errs=%d, want 1 and 1", len(scenes), len(errs))
	}
}

func TestListScenesBadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":true,"data":"nope"}`))
	}))
	defer ts.Close()

	scenes, errs := collect(t, newTestScraper(ts), "https://gayerdar.com/", scraper.ListOpts{})
	if len(scenes) != 0 || len(errs) == 0 {
		t.Fatalf("scenes=%d errs=%d, want a parse error", len(scenes), len(errs))
	}
	if scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Errorf("kind = %v, want parse", scraper.Classify(errs[0]))
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://gayerdar.com",
		"https://gayerdar.com/en-US",
		"https://www.gayerdar.com/zh-TW/models/116",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://notgayerdar.com/",
		"https://gayerdar.com.evil.example/",
		"https://modelmediaasia.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestModelID(t *testing.T) {
	cases := map[string]string{
		"https://gayerdar.com/en-US/models/116":  "116",
		"https://gayerdar.com/models/7/":         "7",
		"https://gayerdar.com/en-US/videos/GD-1": "",
		"https://gayerdar.com/en-US":             "",
		"https://gayerdar.com/en-US/models/abc":  "",
	}
	for in, want := range cases {
		if got := modelID(in); got != want {
			t.Errorf("modelID(%q) = %q, want %q", in, got, want)
		}
	}
}

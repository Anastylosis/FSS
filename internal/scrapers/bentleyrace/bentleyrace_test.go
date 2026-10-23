package bentleyrace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type testServer struct {
	*httptest.Server
	mu      sync.Mutex
	queries []string
}

// newTestServer serves posts.json as a two-page REST listing (two posts, then
// one), the include= endpoint, term lookups and the model archive pages.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	var posts []json.RawMessage
	if err := json.Unmarshal(readFixture(t, "posts.json"), &posts); err != nil {
		t.Fatal(err)
	}
	byID := map[string]json.RawMessage{}
	for _, p := range posts {
		var head struct {
			ID int `json:"id"`
		}
		_ = json.Unmarshal(p, &head)
		byID[strconv.Itoa(head.ID)] = p
	}

	ts := &testServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		ts.queries = append(ts.queries, r.URL.Path+"?"+r.URL.RawQuery)
		ts.mu.Unlock()
		q := r.URL.Query()
		switch r.URL.Path {
		case "/wp-json/wp/v2/posts":
			w.Header().Set("Content-Type", "application/json")
			if inc := q.Get("include"); inc != "" {
				var out []json.RawMessage
				for _, id := range strings.Split(inc, ",") {
					if p, ok := byID[id]; ok {
						out = append(out, p)
					}
				}
				_ = json.NewEncoder(w).Encode(out)
				return
			}
			w.Header().Set("X-WP-Total", "3")
			w.Header().Set("X-WP-TotalPages", "2")
			switch q.Get("page") {
			case "1":
				_ = json.NewEncoder(w).Encode(posts[:2])
			case "2":
				_ = json.NewEncoder(w).Encode(posts[2:])
			default:
				http.Error(w, `{"code":"rest_post_invalid_page_number"}`, http.StatusBadRequest)
			}
		case "/wp-json/wp/v2/categories":
			switch q.Get("slug") {
			case "duo-videos":
				_, _ = w.Write([]byte(`[{"id":23,"name":"Duo Videos","slug":"duo-videos","parent":10}]`))
			case "photo-galleries":
				_, _ = w.Write([]byte(`[{"id":3,"name":"Photo Galleries","slug":"photo-galleries","parent":0}]`))
			default:
				_, _ = w.Write([]byte(`[]`))
			}
		case "/wp-json/wp/v2/tags":
			_, _ = w.Write([]byte(`[{"id":2196,"name":"Fleshlight","slug":"fleshlight","parent":0}]`))
		case "/model/nicolas-leone":
			_, _ = w.Write(readFixture(t, "model_p1.html"))
		case "/model/nicolas-leone/page/2":
			_, _ = w.Write(readFixture(t, "model_p2.html"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *testServer) sawQuery(sub string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, q := range ts.queries {
		if strings.Contains(q, sub) {
			return true
		}
	}
	return false
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
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

func newTestScraper(ts *testServer) *Scraper {
	s := New()
	s.client = ts.Client()
	s.base = ts.URL
	return s
}

func TestListing(t *testing.T) {
	ts := newTestServer(t)
	studioURL := "https://bentleyrace.com/"
	scenes, errs := collect(t, newTestScraper(ts), studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if !ts.sawQuery("categories=10&") {
		t.Error("listing was not filtered to the Videos category")
	}

	sc := scenes[0]
	if sc.ID != "33511" || sc.SiteID != siteID || sc.StudioURL != studioURL || sc.Studio != "Bentley Race" {
		t.Errorf("identity: %+v", sc)
	}
	if sc.Title != "Fleshlight fucking with my cute mate Nicolas" {
		t.Errorf("title %q", sc.Title)
	}
	if sc.URL != ts.URL+"/fleshlight-fucking-with-my-cute-mate-nicolas" {
		t.Errorf("url %q", sc.URL)
	}
	if got := sc.Date.Format("2006-01-02"); got != "2026-10-23" {
		t.Errorf("date %s", got)
	}
	if sc.Thumbnail != "https://bentleyrace.com/public_images/nicolasleoneonsievideo4.jpg" {
		t.Errorf("thumbnail %q", sc.Thumbnail)
	}
	if !strings.HasPrefix(sc.Description, "In this video I had a lot of fun") || strings.Contains(sc.Description, "<p>") {
		t.Errorf("description %q", sc.Description)
	}
	if !slices.Equal(sc.Performers, []string{"Bentley Race", "Nicolas Leone"}) {
		t.Errorf("performers %v", sc.Performers)
	}
	if slices.Contains(sc.Tags, "Nicolas Leone") || slices.Contains(sc.Tags, "Bentley Race") {
		t.Errorf("performer names left in tags: %v", sc.Tags)
	}
	if !slices.Contains(sc.Tags, "Fleshlight") || !slices.Contains(sc.Tags, "rimming") {
		t.Errorf("tags %v", sc.Tags)
	}
	if !slices.Equal(sc.Categories, []string{"Duo Videos"}) {
		t.Errorf("categories %v", sc.Categories)
	}
	if !slices.Equal(scenes[2].Performers, []string{"Andrew Tran"}) {
		t.Errorf("solo performers %v", scenes[2].Performers)
	}
}

func TestKnownIDsStopsAfterPage(t *testing.T) {
	ts := newTestServer(t)
	scenes, _ := collect(t, newTestScraper(ts), "https://bentleyrace.com/",
		scraper.ListOpts{KnownIDs: map[string]bool{"33448": true}})
	if len(scenes) != 1 || scenes[0].ID != "33511" {
		t.Fatalf("got %v", scenes)
	}
	if ts.sawQuery("page=2") {
		t.Error("walked past the page holding a known scene")
	}
}

func TestCategoryAndTagModes(t *testing.T) {
	ts := newTestServer(t)
	s := newTestScraper(ts)
	if _, errs := collect(t, s, "https://bentleyrace.com/category/videos/duo-videos", scraper.ListOpts{}); len(errs) > 0 {
		t.Fatal(errs)
	}
	if !ts.sawQuery("categories=23&") {
		t.Error("category mode did not filter on the category id")
	}
	if _, errs := collect(t, s, "https://bentleyrace.com/tag/fleshlight/", scraper.ListOpts{}); len(errs) > 0 {
		t.Fatal(errs)
	}
	if !ts.sawQuery("categories=10&tags=2196&") {
		t.Error("tag mode did not filter on the tag id within Videos")
	}
	scenes, errs := collect(t, s, "https://bentleyrace.com/category/photo-galleries", scraper.ListOpts{})
	if len(scenes) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "not a video category") {
		t.Errorf("photo category: scenes=%d errs=%v", len(scenes), errs)
	}
}

func TestModelMode(t *testing.T) {
	ts := newTestServer(t)
	studioURL := "https://bentleyrace.com/model/nicolas-leone"
	scenes, errs := collect(t, newTestScraper(ts), studioURL, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var ids []string
	for _, sc := range scenes {
		ids = append(ids, sc.ID)
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL %q", sc.StudioURL)
		}
	}
	if !slices.Equal(ids, []string{"33511", "33420"}) {
		t.Errorf("ids %v, want the video posts across both pages", ids)
	}
	if ts.sawQuery("page/3") {
		t.Error("walked past the last model page")
	}
}

func TestParseModelPage(t *testing.T) {
	ids, n := parseModelPage(readFixture(t, "model_p1.html"))
	if n != 2 || !slices.Equal(ids, []string{"33511"}) {
		t.Errorf("ids %v, articles %d", ids, n)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for u, want := range map[string]bool{
		"https://bentleyrace.com/":                        true,
		"https://www.bentleyrace.com":                     true,
		"http://bentleyrace.com/model/troy-king":          true,
		"https://bentleyrace.com/category/videos/solo":    true,
		"https://notbentleyrace.com/":                     false,
		"https://bentleyrace.com.evil.example/model/x":    false,
		"https://www.thebestporn.com/review/bentleyrace/": false,
	} {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v", u, got)
		}
	}
}

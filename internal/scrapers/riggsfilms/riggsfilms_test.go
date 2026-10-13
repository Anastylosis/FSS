package riggsfilms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newServer serves the fixtures the way the WP REST API does: filtered by the
// categories/tags parameters and paginated two to a page.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	rewrite := func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), "https://riggsfilms.com", srv.URL))
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/categories":
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write(fixture(t, "categories.json"))
		case "/wp-json/wp/v2/tags":
			w.Header().Set("X-WP-TotalPages", "1")
			_, _ = w.Write(fixture(t, "tags.json"))
		case "/wp-json/wp/v2/posts":
			var all []map[string]json.RawMessage
			if err := json.Unmarshal(fixture(t, "posts.json"), &all); err != nil {
				t.Fatal(err)
			}
			q := r.URL.Query()
			var match []map[string]json.RawMessage
			for _, p := range all {
				var cats, tags []int
				_ = json.Unmarshal(p["categories"], &cats)
				_ = json.Unmarshal(p["tags"], &tags)
				if c, _ := strconv.Atoi(q.Get("categories")); c != 0 && !hasInt(cats, c) {
					continue
				}
				if tg, _ := strconv.Atoi(q.Get("tags")); tg != 0 && !hasInt(tags, tg) {
					continue
				}
				match = append(match, p)
			}
			const per = 2
			page, _ := strconv.Atoi(q.Get("page"))
			lo, hi := (page-1)*per, page*per
			if lo > len(match) {
				lo = len(match)
			}
			if hi > len(match) {
				hi = len(match)
			}
			w.Header().Set("X-WP-Total", strconv.Itoa(len(match)))
			w.Header().Set("X-WP-TotalPages", strconv.Itoa((len(match)+per-1)/per))
			b, _ := json.Marshal(match[lo:hi])
			_, _ = w.Write(rewrite(b))
		default:
			_, _ = w.Write(fixture(t, "detail.html"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) ([]string, map[string]scraper.SceneResult, []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	scenes := map[string]scraper.SceneResult{}
	var errs []error
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			order = append(order, r.Scene.ID)
			scenes[r.Scene.ID] = r
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return order, scenes, errs
}

func testScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.client = srv.Client()
	s.base = srv.URL
	return s
}

func TestListScenesWholeCatalogue(t *testing.T) {
	srv := newServer(t)
	s := testScraper(srv)
	studioURL := srv.URL + "/"
	order, scenes, errs := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if strings.Join(order, ",") != "932,47,60" {
		t.Fatalf("scene order/filter = %v, want [932 47 60] (photo post 974 must be excluded)", order)
	}
	sc := scenes["932"].Scene
	if sc.Title != "Katija Cortez & Coco Bae Get Hot & Wet in the Shower" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.SiteID != "riggsfilms" || sc.Studio != "Riggs Films" || sc.StudioURL != studioURL {
		t.Errorf("site/studio fields: %+v", sc)
	}
	if sc.URL != srv.URL+"/katija-cortez-coco-bae-fucking-each-other-with-a-dildo/" {
		t.Errorf("url = %q", sc.URL)
	}
	if sc.Date.Format("2006-01-02") != "2026-10-03" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Duration != 17*60+26 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if !strings.HasSuffix(sc.Thumbnail, "95.-Thumbnail-SFW-Katija-Coco-GG.jpg") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if strings.Join(sc.Performers, ",") != "Coco Bae,Katija Cortez" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if !strings.Contains(sc.Description, "girl girl action in the shower") {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.Tags) == 0 {
		t.Error("no tags")
	}
}

func TestListScenesCategoryAndTagFilters(t *testing.T) {
	srv := newServer(t)
	s := testScraper(srv)

	order, _, errs := collect(t, s, srv.URL+"/category/jack-wanton/", scraper.ListOpts{})
	if len(errs) != 0 || strings.Join(order, ",") != "60" {
		t.Errorf("category filter: order=%v errs=%v", order, errs)
	}
	order, _, _ = collect(t, s, srv.URL+"/coco-bae/", scraper.ListOpts{})
	if strings.Join(order, ",") != "932" {
		t.Errorf("model filter: order=%v", order)
	}

	// "dildo" is on the 932 shower scene only.
	order, _, _ = collect(t, s, srv.URL+"/tag/dildo/", scraper.ListOpts{})
	if strings.Join(order, ",") != "932" {
		t.Errorf("tag filter: order=%v", order)
	}
}

func TestListScenesUnknownFilter(t *testing.T) {
	srv := newServer(t)
	order, _, errs := collect(t, testScraper(srv), srv.URL+"/category/nobody/", scraper.ListOpts{})
	if len(order) != 0 || len(errs) != 1 {
		t.Errorf("order=%v errs=%v", order, errs)
	}
}

func TestListScenesKnownIDsStopsAfterPage(t *testing.T) {
	srv := newServer(t)
	_, scenes, _ := collect(t, testScraper(srv), srv.URL+"/", scraper.ListOpts{KnownIDs: map[string]bool{"47": true}})
	if _, ok := scenes["932"]; !ok {
		t.Error("new scene 932 missing")
	}
	if _, ok := scenes["60"]; ok {
		t.Error("walk should stop after the page holding the known scene")
	}
}

func TestParseVideoObject(t *testing.T) {
	v, err := parseVideoObject(string(fixture(t, "detail.html")))
	if err != nil {
		t.Fatal(err)
	}
	if v.duration != 1046 || !strings.Contains(v.thumbnail, "Thumbnail-SFW") {
		t.Errorf("got %+v", v)
	}
	if _, err := parseVideoObject("<html></html>"); err == nil {
		t.Error("expected error without a VideoObject")
	}
}

func TestFirstString(t *testing.T) {
	if got := firstString(json.RawMessage(`"a"`)); got != "a" {
		t.Errorf("string: %q", got)
	}
	if got := firstString(json.RawMessage(`["b","c"]`)); got != "b" {
		t.Errorf("array: %q", got)
	}
	if got := firstString(nil); got != "" {
		t.Errorf("nil: %q", got)
	}
}

func TestParseFilter(t *testing.T) {
	cases := map[string]filter{
		"https://riggsfilms.com":                         {},
		"https://riggsfilms.com/":                        {},
		"https://riggsfilms.com/category/katija-cortez/": {category: "katija-cortez"},
		"https://riggsfilms.com/tag/lesbian":             {tag: "lesbian"},
		"https://riggsfilms.com/coco-bae/":               {category: "coco-bae"},
	}
	for u, want := range cases {
		if got := parseFilter(u); got != want {
			t.Errorf("parseFilter(%q) = %+v, want %+v", u, got, want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://riggsfilms.com", "https://www.riggsfilms.com/category/x/", "http://riggsfilms.com/tag/y/"} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %q", u)
		}
	}
	for _, u := range []string{"https://riggsfilms.vip", "https://notriggsfilms.com", "https://example.com/riggsfilms.com"} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %q", u)
		}
	}
}

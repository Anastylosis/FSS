package nanairo

import (
	"context"
	"encoding/json"
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

func loadItems(t *testing.T) []item {
	t.Helper()
	data, err := os.ReadFile("testdata/videos.json")
	if err != nil {
		t.Fatal(err)
	}
	var items []item
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	return items
}

// fakeSite serves /ja/videos.json from a table of item id -> release date,
// honouring the text, date and page parameters the way nanairo does.
type fakeSite struct {
	items []item
	dates map[int]string
	mu    sync.Mutex
	hits  int
}

func (f *fakeSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ja/videos.json" {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.hits++
	f.mu.Unlock()
	q := r.URL.Query()
	var from, to string
	if d := q.Get("date"); d != "" {
		from, to, _ = strings.Cut(d, ",")
	}
	var match []item
	for _, it := range f.items {
		if txt := q.Get("text"); txt != "" && !strings.Contains(it.Subject, txt) {
			continue
		}
		if d := f.dates[it.ID]; from != "" && (d < from || d > to) {
			continue
		}
		match = append(match, it)
	}
	page := 1
	if p := q.Get("page"); p != "" {
		page = int(p[0] - '0')
	}
	start := (page - 1) * pageSize
	res := []item{}
	if start < len(match) {
		res = match[start:min(start+pageSize, len(match))]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func newTestScraper(srv *httptest.Server) *Scraper {
	s := New()
	s.client = srv.Client()
	s.base = srv.URL
	s.now = func() time.Time { return time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC) }
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, bool) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var scenes []models.Scene
	stopped := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			t.Fatalf("scrape error: %v", r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return scenes, stopped
}

func testDates() map[int]string {
	return map[int]string{
		6217: "2026-10-12",
		6216: "2026-10-12",
		6011: "2026-10-09",
		131:  "2016-02-01",
	}
}

func TestCatalogueWalk(t *testing.T) {
	site := &fakeSite{items: loadItems(t), dates: testDates()}
	srv := httptest.NewServer(site)
	defer srv.Close()
	s := newTestScraper(srv)
	const studioURL = "https://nanairo.co"

	scenes, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4", len(scenes))
	}
	// Newest day first; order within one day is the site's.
	if scenes[2].ID != "6011" || scenes[3].ID != "131" {
		t.Errorf("order = %s, %s; want 6011, 131", scenes[2].ID, scenes[3].ID)
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL = %q", sc.StudioURL)
		}
		if sc.Studio != "S-Cute" || sc.SiteID != "nanairo" {
			t.Errorf("studio/site = %q/%q", sc.Studio, sc.SiteID)
		}
	}
	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}
	k := byID["6011"]
	if k.Title != "Karin & Kaho #1-1" || k.Date.Format("2006-01-02") != "2026-10-09" {
		t.Errorf("6011 = %q %s", k.Title, k.Date)
	}
	if strings.Join(k.Performers, "|") != "Karin|Kaho" {
		t.Errorf("performers = %v", k.Performers)
	}
	if k.Description != "美女二人で金玉からっぽになるか試してみた" {
		t.Errorf("description = %q", k.Description)
	}
	if !strings.HasPrefix(k.Thumbnail, "https://d30tjfgbbtn4lr.cloudfront.net/") {
		t.Errorf("thumbnail = %q", k.Thumbnail)
	}
	if k.URL != srv.URL+"/ja/videos/6011" {
		t.Errorf("url = %q", k.URL)
	}
	if byID["131"].Date.Format("2006-01-02") != "2016-02-01" {
		t.Errorf("131 date = %s", byID["131"].Date)
	}
}

func TestCatalogueWalkStopsOnKnownDay(t *testing.T) {
	site := &fakeSite{items: loadItems(t), dates: testDates()}
	srv := httptest.NewServer(site)
	defer srv.Close()
	s := newTestScraper(srv)

	scenes, stopped := collect(t, s, "https://nanairo.co", scraper.ListOpts{KnownIDs: map[string]bool{"6216": true}})
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 1 || scenes[0].ID != "6217" {
		t.Errorf("got %v, want only 6217", scenes)
	}
	// 14, 13, 12 October: three day requests and no more.
	if site.hits != 3 {
		t.Errorf("hits = %d, want 3", site.hits)
	}
}

func TestCatalogueWalkPagesBusyDay(t *testing.T) {
	var items []item
	dates := map[int]string{}
	for i := 1; i <= 30; i++ {
		items = append(items, item{ID: i, Subject: "Yuka #1-1"})
		dates[i] = "2026-10-13"
	}
	site := &fakeSite{items: items, dates: dates}
	srv := httptest.NewServer(site)
	defer srv.Close()
	s := newTestScraper(srv)
	scenes, _ := collect(t, s, "https://nanairo.co", scraper.ListOpts{})
	if len(scenes) != 30 {
		t.Fatalf("got %d scenes, want 30", len(scenes))
	}
}

func TestSearchBisection(t *testing.T) {
	site := &fakeSite{items: loadItems(t), dates: testDates()}
	srv := httptest.NewServer(site)
	defer srv.Close()
	s := newTestScraper(srv)
	const studioURL = "https://nanairo.co/ja/search?text=Kaho"

	scenes, _ := collect(t, s, studioURL, scraper.ListOpts{})
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if scenes[0].ID != "6217" || scenes[0].Date.Format("2006-01-02") != "2026-10-12" {
		t.Errorf("first = %s %s", scenes[0].ID, scenes[0].Date)
	}
	if scenes[1].ID != "6011" || scenes[1].Date.Format("2006-01-02") != "2026-10-09" {
		t.Errorf("second = %s %s", scenes[1].ID, scenes[1].Date)
	}
	for _, sc := range scenes {
		if sc.StudioURL != studioURL {
			t.Errorf("StudioURL = %q", sc.StudioURL)
		}
	}
	// A bisection is far cheaper than walking ~9,800 days.
	if site.hits > 60 {
		t.Errorf("hits = %d", site.hits)
	}
}

func TestSearchSkipsKnown(t *testing.T) {
	site := &fakeSite{items: loadItems(t), dates: testDates()}
	srv := httptest.NewServer(site)
	defer srv.Close()
	s := newTestScraper(srv)
	scenes, stopped := collect(t, s, "https://nanairo.co/ja/search?text=Kaho", scraper.ListOpts{KnownIDs: map[string]bool{"6011": true}})
	if !stopped || len(scenes) != 1 || scenes[0].ID != "6217" {
		t.Errorf("stopped=%v scenes=%v", stopped, scenes)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://nanairo.co",
		"https://nanairo.co/",
		"https://nanairo.co/ja/",
		"https://nanairo.co/en/videos",
		"https://nanairo.co/ja/videos?text=Kaho",
		"https://nanairo.co/zh-hant/search?text=Kaho",
		"https://www.s-cute.com/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %s", u)
		}
	}
	for _, u := range []string{
		"https://nanairo.co/ja/girls",
		"https://nanairo.co/ja/videos/5919",
		"https://nanairo.co/ja/search",
		"https://www.s-cute.com/contents/123",
		"https://example.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %s", u)
		}
	}
}

func TestPreferredStudioURL(t *testing.T) {
	s := New()
	for in, want := range map[string]string{
		"https://www.s-cute.com/":                "https://nanairo.co",
		"https://nanairo.co/ja/videos":           "https://nanairo.co",
		"https://nanairo.co/ja/search?text=Kaho": "",
		"https://nanairo.co/ja/videos?text=Kaho": "",
		"https://example.com/":                   "",
	} {
		if got := s.PreferredStudioURL(in); got != want {
			t.Errorf("PreferredStudioURL(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestPerformers(t *testing.T) {
	for in, want := range map[string]string{
		"Kaho #1-1":         "Kaho",
		"Karin & Kaho #1-1": "Karin|Kaho",
		"Ayumi #3":          "Ayumi",
		"No number here":    "",
	} {
		if got := strings.Join(performers(in), "|"); got != want {
			t.Errorf("performers(%q) = %q, want %q", in, got, want)
		}
	}
}

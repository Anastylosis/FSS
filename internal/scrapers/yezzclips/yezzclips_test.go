package yezzclips

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.yezzclips.com/store_view.php?id=1054", true},
		{"http://www.yezzclips.com/store_view.php?id=1054", true},
		{"https://yezzclips.com/store_view.php?id=1054", true},
		{"https://www.yezzclips.com/store_view.php?id=1054&page=3", true},
		{"https://www.yezzclips.com/store_view.php?id=2404&item=216986", true},
		// main.php indexes every store on the site, so it is not one studio.
		{"https://www.yezzclips.com/main.php", false},
		{"https://www.yezzclips.com/store_view.php", false},
		{"https://www.yezzclips.com/store_view.php?id=abc", false},
		{"https://www.yezzclips.com/results.php?searchby=category&cat=136", false},
		{"https://example.com/store_view.php?id=1054", false},
	}
	s := New()
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// A single clip, a deep listing page and the bare store URL are one studio.
func TestPreferredStudioURL(t *testing.T) {
	want := "https://www.yezzclips.com/store_view.php?id=1054"
	cases := []string{
		"https://www.yezzclips.com/store_view.php?id=1054",
		"https://www.yezzclips.com/store_view.php?id=1054&page=7",
		"https://www.yezzclips.com/store_view.php?id=1054&item=216466",
		"http://yezzclips.com/store_view.php?id=1054",
	}
	s := New()
	for _, u := range cases {
		if got := s.PreferredStudioURL(u); got != want {
			t.Errorf("PreferredStudioURL(%q) = %q, want %q", u, got, want)
		}
	}
	if got := s.PreferredStudioURL("https://example.com/x"); got != "" {
		t.Errorf("unclaimed URL: got %q, want empty", got)
	}
}

func TestParseLength(t *testing.T) {
	cases := map[string]int{
		"41min.":     41 * 60,
		" 9min. ":    9 * 60,
		"1h 5min":    3900,
		"2h":         7200,
		"30sec":      30,
		"1min 30sec": 90,
		"":           0,
	}
	for in, want := range cases {
		if got := parseLength(in); got != want {
			t.Errorf("parseLength(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParsePrice(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"19,99", 19.99, true},
		{"9,99", 9.99, true},
		{"1.234,56", 1234.56, true},
		{"", 0, false},
		{"free", 0, false},
	}
	for _, c := range cases {
		got, ok := parsePrice(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parsePrice(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// storeServer serves the fixture for the store's pages and redirects anything
// else — a page past the end, or an unknown store id — to the age-gate splash,
// exactly as the live site does.
func storeServer(t *testing.T) *httptest.Server {
	t.Helper()
	page1, err := os.ReadFile("testdata/store_page.html")
	if err != nil {
		t.Fatal(err)
	}
	gate, err := os.ReadFile("testdata/agegate.html")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/store_view.php", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page := q.Get("page")
		if q.Get("id") != "1054" || (page != "" && page != "1") {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(page1)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(gate)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
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

func TestScrapeStore(t *testing.T) {
	srv := storeServer(t)
	s := New()
	s.client = srv.Client()

	studioURL := srv.URL + "/store_view.php?id=1054"
	scenes, errs := collect(t, s, studioURL, scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// The fixture is a short page (3 < pageSize), so the walk stops after it.
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}

	sc := scenes[0]
	if sc.ID != "216466" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "yezzclips" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.Title != "Cory Chase - Taking Anal and Swallowing Pee" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Studio != "One Two Pee" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if sc.StudioURL != studioURL {
		t.Errorf("StudioURL = %q", sc.StudioURL)
	}
	if want := srv.URL + "/store_view.php?id=1054&item=216466"; sc.URL != want {
		t.Errorf("URL = %q, want %q", sc.URL, want)
	}
	if sc.Duration != 41*60 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.Format != "MP4" {
		t.Errorf("Format = %q", sc.Format)
	}
	if sc.Width != 1920 || sc.Height != 1080 || sc.Resolution != "1920x1080" {
		t.Errorf("resolution = %dx%d %q", sc.Width, sc.Height, sc.Resolution)
	}
	if len(sc.Categories) != 1 || sc.Categories[0] != "Peeing / Pissing" {
		t.Errorf("Categories = %v", sc.Categories)
	}
	if sc.Thumbnail != "https://static.yezzclips.com/item_previews/previews_static/216466.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.Preview != "https://static.yezzclips.com/item_previews/previews_animated_mp4/216466.mp4" {
		t.Errorf("Preview = %q", sc.Preview)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 20.99 {
		t.Errorf("PriceHistory = %+v", sc.PriceHistory)
	}
	// The site publishes no release date anywhere on a store page.
	if !sc.Date.IsZero() {
		t.Errorf("Date = %v, want zero", sc.Date)
	}

	// cp1252 smart quotes survive decoding rather than arriving as U+FFFD.
	if !strings.Contains(sc.Description, "‘I need to do a thorough physical exam,’") {
		t.Errorf("Description lost its cp1252 quotes: %q", sc.Description)
	}
	if strings.ContainsRune(sc.Description, '�') {
		t.Errorf("Description has replacement chars: %q", sc.Description)
	}

	if scenes[2].PriceHistory[0].Regular != 1234.56 {
		t.Errorf("thousands-separated price = %+v", scenes[2].PriceHistory)
	}
	if scenes[1].Format != "WMV" {
		t.Errorf("second clip Format = %q", scenes[1].Format)
	}
}

// A store id the site does not serve redirects to the age gate. That must be a
// loud error, not a silent empty catalogue that an authoritative Save would
// act on.
func TestUnknownStoreIsAnError(t *testing.T) {
	srv := storeServer(t)
	s := New()
	s.client = srv.Client()

	scenes, errs := collect(t, s, srv.URL+"/store_view.php?id=999999", scraper.ListOpts{})
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes, want 0", len(scenes))
	}
	if len(errs) == 0 {
		t.Fatal("want an error for an unknown store, got none")
	}
	if k := scraper.Classify(errs[0]); !k.MissingData() {
		t.Errorf("Classify = %v, want a kind that marks the run incomplete", k)
	}
}

// An item URL scrapes just that clip, picked by id rather than by position:
// the page also carries the store's first listing page.
func TestScrapeSingleItem(t *testing.T) {
	srv := storeServer(t)
	s := New()
	s.client = srv.Client()

	scenes, errs := collect(t, s, srv.URL+"/store_view.php?id=1054&item=214937", scraper.ListOpts{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].ID != "214937" {
		t.Errorf("ID = %q, want 214937", scenes[0].ID)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	srv := storeServer(t)
	s := New()
	s.client = srv.Client()

	scenes, _ := collect(t, s, srv.URL+"/store_view.php?id=1054",
		scraper.ListOpts{KnownIDs: map[string]bool{"214937": true}})
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 before the known id", len(scenes))
	}
	if scenes[0].ID != "216466" {
		t.Errorf("ID = %q", scenes[0].ID)
	}
}

func TestListScenesRejectsURLWithoutStoreID(t *testing.T) {
	if _, err := New().ListScenes(context.Background(), "https://www.yezzclips.com/main.php", scraper.ListOpts{}); err == nil {
		t.Fatal("want an error for a URL naming no store")
	}
}

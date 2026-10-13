package onnow_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/onnow"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type irmaosServer struct {
	*httptest.Server
	mu       sync.Mutex
	listings []string
	details  []string
}

// newIrmaosServer serves the Irmãos Dotados fixtures. As on the live site, a
// page past a category's last one answers with the unfiltered catalogue, so a
// scraper that trusted "empty page means done" would never stop.
func newIrmaosServer(t *testing.T) *irmaosServer {
	t.Helper()
	list1, list2 := fixture(t, "irmaos_list1.html"), fixture(t, "irmaos_list2.html")
	nopager, movies1 := fixture(t, "irmaos_list_nopager.html"), fixture(t, "irmaos_list_movies1.html")
	detail, sitemap := fixture(t, "irmaos_detail.html"), fixture(t, "irmaos_sitemap.xml")
	s := &irmaosServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		p := r.URL.Path
		var body string
		switch {
		case p == "/sitemap.xml":
			body = sitemap
		case strings.HasPrefix(p, "/video/"):
			s.details = append(s.details, strings.TrimPrefix(p, "/video/"))
			body = detail
		case p == "/videos":
			s.listings = append(s.listings, p)
			body = list1
		case p == "/videos/solo":
			s.listings = append(s.listings, p)
			body = nopager
		case p == "/videos/movies":
			s.listings = append(s.listings, p)
			body = movies1
		case strings.HasPrefix(p, "/videos/"):
			s.listings = append(s.listings, p)
			body = list2
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func irmaosScraper(srv *irmaosServer) *onnow.Scraper {
	cfg := onnow.Sites()[0]
	cfg.BaseURL = srv.URL
	s := onnow.New(cfg)
	s.Client = srv.Client()
	return s
}

func TestListScenesWalksCatalogue(t *testing.T) {
	srv := newIrmaosServer(t)
	studio := srv.URL + "/videos"
	ch, err := irmaosScraper(srv).ListScenes(context.Background(), studio, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 7 {
		t.Fatalf("got %d scenes, want 7: 5 listed over two pages plus 111 and 748 only in the sitemap", len(scenes))
	}
	if want := "/videos,/videos/2"; strings.Join(srv.listings, ",") != want {
		t.Errorf("listing requests = %v, want %s", srv.listings, want)
	}
	byID := map[string]int{}
	for i, sc := range scenes {
		byID[sc.ID] = i
	}
	sc := scenes[byID["765"]]
	if sc.Title != "DESEJO DISFARÇADO" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.Duration != 28*60+24 {
		t.Errorf("Duration = %d, want 1704", sc.Duration)
	}
	if sc.URL != srv.URL+"/video/765" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.StudioURL != studio || sc.SiteID != "irmaosdotados" || sc.Studio != "Irmãos Dotados" {
		t.Errorf("StudioURL/SiteID/Studio = %q/%q/%q", sc.StudioURL, sc.SiteID, sc.Studio)
	}
	if !strings.HasPrefix(sc.Description, "Otávio Andreassa e Felipe Marino dividem o mesmo ambiente") ||
		!strings.Contains(sc.Description, "\nA tensão cresce") {
		t.Errorf("Description = %q", sc.Description)
	}
	if !strings.HasPrefix(sc.Thumbnail, "https://id.onvicdn.com/id/thumbs/765/03.jpg") {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt unset")
	}
	if got := scenes[byID["743"]].Duration; got != 36*60+8 {
		t.Errorf("743 duration = %d, want 2168", got)
	}
	hidden, ok := byID["111"]
	if !ok {
		t.Fatal("scene 111, present only in the sitemap, was not collected")
	}
	if h := scenes[hidden]; h.Title != "DESEJO DISFARÇADO" || h.Duration != 28*60+24 || h.URL != srv.URL+"/video/111" {
		t.Errorf("sitemap-only scene = %q / %d / %q", h.Title, h.Duration, h.URL)
	}
}

func TestListScenesCategoryWithoutPagerIsOnePage(t *testing.T) {
	srv := newIrmaosServer(t)
	studio := srv.URL + "/videos/solo"
	ch, err := irmaosScraper(srv).ListScenes(context.Background(), studio, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	if scenes[0].StudioURL != studio {
		t.Errorf("StudioURL = %q", scenes[0].StudioURL)
	}
	if len(srv.listings) != 1 || srv.listings[0] != "/videos/solo" {
		t.Errorf("listing requests = %v, want only /videos/solo", srv.listings)
	}
}

func TestListScenesCategoryStopsAtPagerEnd(t *testing.T) {
	srv := newIrmaosServer(t)
	ch, err := irmaosScraper(srv).ListScenes(context.Background(), srv.URL+"/videos/movies", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}
	if want := "/videos/movies,/videos/movies/2"; strings.Join(srv.listings, ",") != want {
		t.Errorf("listing requests = %v, want %s", srv.listings, want)
	}
	if len(srv.details) != 5 {
		t.Errorf("a category walk must not pull in the sitemap: %d detail fetches", len(srv.details))
	}
}

func TestKnownIDsStopsAfterPageAndSkipsDetails(t *testing.T) {
	srv := newIrmaosServer(t)
	opts := scraper.ListOpts{KnownIDs: map[string]bool{"764": true}, Workers: 1}
	ch, err := irmaosScraper(srv).ListScenes(context.Background(), srv.URL+"/videos", opts)
	if err != nil {
		t.Fatal(err)
	}
	scenes, stopped := testutil.CollectScenesWithStop(t, ch)
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (765 and 763)", len(scenes))
	}
	for _, d := range srv.details {
		if d == "764" {
			t.Errorf("fetched the detail of known scene %s", d)
		}
	}
	if len(srv.listings) != 1 {
		t.Errorf("listing requests = %v, want one page", srv.listings)
	}
}

func TestEmptyFirstPageIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()
	cfg := onnow.Sites()[0]
	cfg.BaseURL = srv.URL
	s := onnow.New(cfg)
	s.Client = srv.Client()
	ch, err := s.ListScenes(context.Background(), srv.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var errs int
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs++
			if scraper.Classify(r.Err) != scraper.FailureParse {
				t.Errorf("error %v not classified as a parse failure", r.Err)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestCancellable(t *testing.T) {
	srv := newIrmaosServer(t)
	testutil.AssertCancellable(t, irmaosScraper(srv), srv.URL+"/videos", scraper.ListOpts{Workers: 1})
}

func TestMatchesURL(t *testing.T) {
	var s *onnow.Scraper
	for _, c := range onnow.Sites() {
		if c.ID == "irmaosdotados" {
			s = onnow.New(c)
		}
	}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://irmaosdotados.com.br", true},
		{"https://irmaosdotados.com.br/", true},
		{"https://www.irmaosdotados.com.br/videos", true},
		{"https://irmaosdotados.com.br/videos/2", true},
		{"http://irmaosdotados.com.br/videos/solo", true},
		{"https://irmaosdotados.com.br.evil.test/", false},
		{"https://notirmaosdotados.com.br/", false},
		{"https://hardbrazil.com.br/videos", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSiteTableIntegrity(t *testing.T) {
	cfgs := onnow.Sites()
	rows := make([]testutil.SiteRow, 0, len(cfgs))
	for _, c := range cfgs {
		rows = append(rows, testutil.SiteRow{
			ID: c.ID, Base: c.BaseURL, Studio: c.SiteName,
			Patterns: c.Patterns, MatchRe: c.MatchRe,
		})
		if c.CardRe == nil || c.CardRe.NumSubexp() < 1 {
			t.Errorf("%s: CardRe needs an id capture group", c.ID)
		}
		if c.TitleRe == nil || c.DurationRe == nil {
			t.Errorf("%s: TitleRe and DurationRe are required", c.ID)
		}
	}
	testutil.CheckSiteTable(t, rows)
	testutil.CheckSiteTableDomains(t, rows)
}

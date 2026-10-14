package giga

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

type gateServer struct {
	*httptest.Server
	mu        sync.Mutex
	queries   []string
	handshake int
}

func newServer(t *testing.T) *gateServer {
	t.Helper()
	g := &gateServer{}
	pages := map[string]string{
		"":  fixture(t, "list_page1.html"),
		"1": fixture(t, "list_page1.html"),
		"2": fixture(t, "list_page2.html"),
	}
	empty := fixture(t, "list_empty.html")
	detail := fixture(t, "detail.html")
	mux := http.NewServeMux()
	mux.HandleFunc("/cookie_set.php", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.handshake++
		g.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "old_check", Value: "yes", Path: "/"})
		http.Redirect(w, r, "/top.php", http.StatusFound)
	})
	mux.HandleFunc("/top.php", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>top</html>"))
	})
	gated := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie("old_check")
			if err != nil || c.Value != "yes" || !strings.HasSuffix(r.Referer(), "/top.php") {
				http.Redirect(w, r, "/top.php", http.StatusFound)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/search/index.php", gated(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.queries = append(g.queries, r.URL.RawQuery)
		g.mu.Unlock()
		body, ok := pages[r.URL.Query().Get("count")]
		if !ok {
			body = empty
		}
		_, _ = w.Write([]byte(body))
	}))
	mux.HandleFunc("/product/index.php", gated(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(detail))
	}))
	g.Server = httptest.NewServer(mux)
	t.Cleanup(g.Close)
	return g
}

func (g *gateServer) scraper() *Scraper {
	s := New()
	s.base = g.URL
	return s
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) ([]models.Scene, []error) {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
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

func TestListScenesEndToEnd(t *testing.T) {
	g := newServer(t)
	s := g.scraper()
	studioURL := "https://www.giga-web.jp/"
	scenes, errs := collect(t, s, studioURL, scraper.ListOpts{Workers: 2})
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(scenes) != 5 {
		t.Fatalf("got %d scenes, want 5", len(scenes))
	}

	sc := scenes[0]
	if sc.ID != "THPA-17" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "giga" || sc.Studio != "GIGA" || sc.StudioURL != studioURL {
		t.Errorf("site/studio/studioURL = %q %q %q", sc.SiteID, sc.Studio, sc.StudioURL)
	}
	if sc.Title != "スーパーヒロイン危機一髪!!Vol.117 鮮光戦隊サイリュウジャー" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.URL != g.URL+"/product/index.php?product_id=7784" {
		t.Errorf("URL = %q", sc.URL)
	}
	if want := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Thumbnail != "https://www.giga-web.jp/db_titles/thpa/thpa17_xpi17f/pac_s.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "巴ひかり" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Director != "特異点心" {
		t.Errorf("Director = %q", sc.Director)
	}
	if sc.Duration != 65*60 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if !strings.HasPrefix(sc.Description, "スパンデクサー・コスモエンジェルは襲いくる異星人から地球を守っていた。") || strings.Contains(sc.Description, "もっと見る") {
		t.Errorf("Description = %q", sc.Description)
	}
	if len(sc.Tags) == 0 || sc.Tags[0] != "ドミネーション" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt zero")
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.handshake != 1 {
		t.Errorf("age-gate handshake ran %d times, want 1", g.handshake)
	}
	if len(g.queries) != 3 || g.queries[0] != "" || g.queries[1] != "count=2" || g.queries[2] != "count=3" {
		t.Errorf("listing queries = %q", g.queries)
	}
}

func TestListScenesFilter(t *testing.T) {
	g := newServer(t)
	s := g.scraper()
	url := "https://www.giga-web.jp/search/index.php?actor_id=4014&sort=0&cp=5&count=9"
	scenes, errs := collect(t, s, url, scraper.ListOpts{Workers: 1})
	if len(errs) != 0 || len(scenes) == 0 {
		t.Fatalf("scenes=%d errs=%v", len(scenes), errs)
	}
	if scenes[0].StudioURL != url {
		t.Errorf("StudioURL = %q", scenes[0].StudioURL)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.queries[0] != "actor_id=4014" || g.queries[1] != "actor_id=4014&count=2" {
		t.Errorf("queries = %q", g.queries)
	}
}

func TestListScenesKnownIDsStops(t *testing.T) {
	g := newServer(t)
	s := g.scraper()
	scenes, _ := collect(t, s, "https://www.giga-web.jp/", scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"SPSF-76": true},
	})
	g.mu.Lock()
	n := len(g.queries)
	g.mu.Unlock()
	if n != 1 {
		t.Errorf("fetched %d listing pages, want 1", n)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want the 2 unknown ones on page 1", len(scenes))
	}
	for _, sc := range scenes {
		if len(sc.Performers) == 0 {
			t.Errorf("%s not enriched", sc.ID)
		}
	}
}

func TestGateWithoutSessionIsError(t *testing.T) {
	g := newServer(t)
	s := g.scraper()
	s.session = true // skip the handshake so the listing bounces to the gate
	_, errs := collect(t, s, "https://www.giga-web.jp/", scraper.ListOpts{})
	if len(errs) == 0 {
		t.Fatal("expected an error when the gate is not passed")
	}
}

func TestParseListingSkipsCommentedCast(t *testing.T) {
	cards := parseListing(fixture(t, "list_page1.html"))
	if len(cards) != 3 {
		t.Fatalf("got %d cards", len(cards))
	}
	c := cards[1]
	if c.id != "7785" || c.code != "SPSF-76" {
		t.Errorf("card = %+v", c)
	}
	if c.title != "憑依変身ヒロイン 蒼き聖女サフィール" {
		t.Errorf("title = %q", c.title)
	}
	old := parseListing(fixture(t, "list_page2.html"))
	if len(old) != 2 || old[0].code != "TNI-04" || old[0].date.Year() != 2000 {
		t.Errorf("old cards = %+v", old)
	}
}

func TestApplyDetailRuntimeFallback(t *testing.T) {
	body := `<li><dl><dt>収録時間</dt><dd>120分</dd></dl></li><li><dl><dt>DVDリリース日</dt><dd>2020/03/04</dd></dl></li>`
	sc := models.Scene{}
	applyDetail(&sc, body)
	if sc.Duration != 7200 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if sc.Date.Format("2006-01-02") != "2020-03-04" {
		t.Errorf("Date = %v", sc.Date)
	}
}

func TestDetailCodeFillsMissingID(t *testing.T) {
	sc := models.Scene{}
	applyDetail(&sc, `<li><dl><dt>作品番号</dt><dd>SPSE-59</dd></dl></li>`)
	if sc.ID != "SPSE-59" {
		t.Errorf("ID = %q", sc.ID)
	}
}

func TestFilterQuery(t *testing.T) {
	cases := map[string]string{
		"https://www.giga-web.jp/":                                           "",
		"https://www.giga-web.jp/search/index.php":                           "",
		"https://www.giga-web.jp/search/index.php?label_id=3&cp=2&sort=1":    "label_id=3",
		"https://www.giga-web.jp/search/index.php?&actor_id=3581":            "actor_id=3581",
		"https://www.giga-web.jp/search/index.php?year=2024&month=5&count=3": "month=5&year=2024",
	}
	for in, want := range cases {
		if got := filterQuery(in).Encode(); got != want {
			t.Errorf("filterQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://www.giga-web.jp",
		"https://www.giga-web.jp/",
		"http://giga-web.jp/search/index.php?actor_id=1",
		"https://www.giga-web.jp/product/index.php?product_id=7624",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("should match %q", u)
		}
	}
	for _, u := range []string{
		"https://www.giga-web.jp.evil.com/",
		"https://notgiga-web.jp/",
		"https://www.akiba-web.com/",
		"https://example.com/giga-web.jp",
	} {
		if s.MatchesURL(u) {
			t.Errorf("should not match %q", u)
		}
	}
}

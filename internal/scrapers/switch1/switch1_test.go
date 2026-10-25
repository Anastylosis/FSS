package switch1

import (
	"context"
	"fmt"
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

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://switch1.jp/",
		"https://switch1.jp",
		"http://www.switch1.jp/web/",
		"https://switch1.jp/web/bbs/board.php?bo_table=works&sca=2025%E5%B9%B4",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://switch1.jp.evil.test/",
		"https://example.com/switch1.jp",
		"https://switch.jp/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestPatternsCoverFilteredURLs(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://switch1.jp/web/",
		"https://switch1.jp/web/bbs/board.php?bo_table=works",
		"https://switch1.jp/web/bbs/board.php?bo_table=works&sca=2024%E5%B9%B4",
	} {
		if scraper.URLLooksUnhandled(s, u) {
			t.Errorf("URLLooksUnhandled(%q) = true", u)
		}
	}
}

func TestCategory(t *testing.T) {
	cases := map[string]string{
		"https://switch1.jp/": "",
		"https://switch1.jp/web/bbs/board.php?bo_table=works":                   "",
		"https://switch1.jp/web/bbs/board.php?bo_table=works&sca=2025%E5%B9%B4": "2025年",
		"https://switch1.jp/web/bbs/board.php?bo_table=works&sca=2023年&page=2":  "2023年",
	}
	for u, want := range cases {
		if got := category(u); got != want {
			t.Errorf("category(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestParseMeta(t *testing.T) {
	m := parseMeta("○発売日： \t2023/02/23　○収録時間：210分　○出演者： 児玉れな　○監督：ライト藤真　○品番：SW-883\t\t")
	want := time.Date(2023, 2, 23, 0, 0, 0, 0, time.UTC)
	if !m.date.Equal(want) || m.duration != 210*60 || m.director != "ライト藤真" || m.code != "SW-883" {
		t.Errorf("parseMeta = %+v", m)
	}
	if len(m.performers) != 1 || m.performers[0] != "児玉れな" {
		t.Errorf("performers = %v", m.performers)
	}

	m = parseMeta("○発売日：2026/03/5　○収録時間：180分　○出演者： ---　○監督：\tよっちゃん　○品番：SW-1042")
	if m.date.Day() != 5 || m.director != "よっちゃん" || len(m.performers) != 0 {
		t.Errorf("parseMeta single-digit day / placeholder cast = %+v", m)
	}
}

func TestParseListing(t *testing.T) {
	body := fixture(t, "list1.html")
	cards := parseListing(body)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].wrID != "203" || !strings.HasSuffix(cards[0].title, "…") || !strings.Contains(cards[0].thumb, "/data/file/works/thumb-") {
		t.Errorf("card 0 = %+v", cards[0])
	}
	if got := lastPage(body); got != 10 {
		t.Errorf("lastPage(page 1) = %d, want 10", got)
	}
	if got := lastPage(fixture(t, "list10.html")); got != 9 {
		t.Errorf("lastPage(page 10) = %d, want 9", got)
	}
}

func TestApplyDetail(t *testing.T) {
	sc := models.Scene{URL: "https://switch1.jp/x", Title: "truncated"}
	if err := applyDetail(&sc, fixture(t, "detail203.html")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(sc.Title, "元気になりました！") {
		t.Errorf("title = %q", sc.Title)
	}
	if !strings.HasPrefix(sc.Description, "引きこもりになった僕を心配して") {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Director != "ライト藤真" || sc.Duration != 160*60 || sc.Date.Format("2006-01-02") != "2026-11-19" {
		t.Errorf("meta = %q %d %v", sc.Director, sc.Duration, sc.Date)
	}
	if strings.Contains(sc.Thumbnail, "thumb-") || !strings.HasSuffix(sc.Thumbnail, ".jpg") {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}

	if err := applyDetail(&sc, "<html>redesigned</html>"); scraper.Classify(err) != scraper.FailureParse {
		t.Errorf("missing title block: err = %v", err)
	}
}

func newServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	list1, list10, detail := fixture(t, "list1.html"), fixture(t, "list10.html"), fixture(t, "detail203.html")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/web/bbs/board.php" || r.URL.Query().Get("bo_table") != "works" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if id := q.Get("wr_id"); id != "" {
			if hits != nil {
				hits.Add(1)
			}
			if id == "203" {
				_, _ = fmt.Fprint(w, detail)
				return
			}
			_, _ = fmt.Fprintf(w, `<h2 id="bo_v_title"><div>Work %s</div></h2><div class="sbjB1">○監督：X</div><ul><li>Story %s</li></ul>`, id, id)
			return
		}
		switch q.Get("page") {
		case "", "1":
			_, _ = fmt.Fprint(w, list1)
		case "10":
			_, _ = fmt.Fprint(w, list10)
		default:
			_, _ = fmt.Fprint(w, `<nav class="pg_wrap"><a href="?bo_table=works&amp;page=10" class="pg_page">10</a></nav>`)
		}
	}))
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

func TestListScenes(t *testing.T) {
	srv := newServer(t, nil)
	defer srv.Close()
	s := New()
	s.base = srv.URL
	s.client = srv.Client()

	const studio = "https://switch1.jp/"
	scenes, errs := collect(t, s, studio, scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	// Page 1 has two works and links on to page 10; page 2 has none, which
	// ends the walk.
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	first := scenes[0]
	if first.ID != "SW-1076" || first.SiteID != siteID || first.StudioURL != studio || first.Studio != studioName {
		t.Errorf("identity = %q %q %q %q", first.ID, first.SiteID, first.StudioURL, first.Studio)
	}
	if first.URL != srv.URL+"/web/bbs/board.php?bo_table=works&wr_id=203" {
		t.Errorf("url = %q", first.URL)
	}
	if !strings.HasSuffix(first.Title, "元気になりました！") || first.Description == "" {
		t.Errorf("detail not applied: %q / %q", first.Title, first.Description)
	}
	if scenes[1].ID != "SW-1075" || scenes[1].Title != "Work 202" || scenes[1].Duration != 217*60 {
		t.Errorf("second = %+v", scenes[1])
	}
}

func TestListScenesWalksToLastPage(t *testing.T) {
	list10 := fixture(t, "list10.html")
	var pages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("wr_id") != "" {
			_, _ = fmt.Fprint(w, `<h2 id="bo_v_title"><div>Work</div></h2>`)
			return
		}
		pages.Add(1)
		if q.Get("sca") != "2023年" {
			t.Errorf("category not forwarded: %q", r.URL.RawQuery)
		}
		_, _ = fmt.Fprint(w, list10)
	}))
	defer srv.Close()
	s := New()
	s.base = srv.URL
	s.client = srv.Client()

	scenes, errs := collect(t, s, "https://switch1.jp/web/bbs/board.php?bo_table=works&sca=2023%E5%B9%B4", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	// Every page serves list10, whose bar links up to page 9: the repeated
	// work is sent once, and the walk ends at page 9.
	if n := pages.Load(); n != 9 {
		t.Errorf("fetched %d listing pages, want 9", n)
	}
	if len(scenes) != 1 || scenes[0].ID != "SW-888" || scenes[0].Date.Format("2006-01-02") != "2023-04-06" {
		t.Errorf("scenes = %+v", scenes)
	}
}

func TestKnownIDsSkipDetailFetch(t *testing.T) {
	var hits atomic.Int32
	srv := newServer(t, &hits)
	defer srv.Close()
	s := New()
	s.base = srv.URL
	s.client = srv.Client()

	scenes, _ := collect(t, s, "https://switch1.jp/", scraper.ListOpts{KnownIDs: map[string]bool{"SW-1075": true}})
	if len(scenes) != 1 || scenes[0].ID != "SW-1076" {
		t.Errorf("scenes = %v", scenes)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("detail fetches = %d, want 1", n)
	}
}

func TestEmptyFirstPageIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html>redesigned</html>")
	}))
	defer srv.Close()
	s := New()
	s.base = srv.URL
	s.client = srv.Client()

	_, errs := collect(t, s, "https://switch1.jp/", scraper.ListOpts{})
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Errorf("errs = %v", errs)
	}
}

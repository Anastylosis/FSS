package rebecca

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

func collect(t *testing.T, s *Scraper, u string) ([]models.Scene, []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := s.ListScenes(ctx, u, scraper.ListOpts{Workers: 2})
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

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"http://rebecca-web.com/", true},
		{"https://www.rebecca-web.com/search.php?label_id=79", true},
		{"http://rebecca-web.com/actor.php?id=3541", true},
		{"http://rebecca-web.com", true},
		{"http://rebecca-web.com.evil.org/", false},
		{"http://rebecca-web.jp/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestPatternsCoverModes(t *testing.T) {
	s := New()
	for _, u := range []string{
		"http://rebecca-web.com/search.php?label_id=79",
		"http://rebecca-web.com/search.php?series_id=1",
		"http://rebecca-web.com/search.php?costume_id=81",
		"http://rebecca-web.com/actor.php?id=3541",
	} {
		if scraper.URLLooksUnhandled(s, u) {
			t.Errorf("%s flagged as unhandled", u)
		}
	}
}

func TestParseListing(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://rebecca-web.com/", "http://x/search.php"},
		{"http://rebecca-web.com/search.php?page=4", "http://x/search.php"},
		{"http://rebecca-web.com/search.php?label_id=79&page=2", "http://x/search.php?label_id=79"},
		{"http://rebecca-web.com/actor.php?id=3541", "http://x/actor.php?id=3541"},
	}
	for _, c := range cases {
		if got := parseListing(c.in).url("http://x", 1); got != c.want {
			t.Errorf("parseListing(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := parseListing("http://rebecca-web.com/search.php?series_id=1").url("http://x", 3); got != "http://x/search.php?page=3&series_id=1" {
		t.Errorf("page url = %q", got)
	}
	if got := parseListing("http://rebecca-web.com/actor.php?id=7").url("http://x", 3); got != "http://x/actor.php?id=7" {
		t.Errorf("actor url = %q", got)
	}
}

func TestParseCards(t *testing.T) {
	cards := parseCards(fixture(t, "search_page1.html"))
	if len(cards) != 3 {
		t.Fatalf("cards = %d, want 3", len(cards))
	}
	if cards[0].id != "grace_036" || cards[0].title != "Honey＆Pretty アリスの蜜愛ヌード 釈アリス" || cards[0].performer != "釈アリス" {
		t.Errorf("card 0 = %+v", cards[0])
	}
	if cards[0].thumb != "https://file.rebecca-web.com/media/videos/dl03/grace_036/1_pc2.jpg" {
		t.Errorf("thumb = %q", cards[0].thumb)
	}
	if cards[1].id != "rebd_1082" || cards[1].thumb != "" {
		t.Errorf("placeholder thumb kept: %+v", cards[1])
	}
}

func TestParseDetailCatalogueNumber(t *testing.T) {
	c := card{id: "rebd_1085", title: "Ui2 南風に微笑んで", performer: "天神羽衣"}
	sc, err := parseDetail(fixture(t, "detail_rebd_1085.html"), c, "http://x/detail.php?id=rebd_1085", "http://x/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "REBD-1085" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.Date.Format("2006-01-02") != "2026-11-19" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.Duration != 84*60 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "天神羽衣" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Series != "S級アイドル" || len(sc.Categories) != 1 || sc.Categories[0] != "S級アイドル" {
		t.Errorf("Series/Categories = %q %v", sc.Series, sc.Categories)
	}
	if !strings.HasPrefix(sc.Description, "本作の主演は天神羽衣ちゃん!!\n清楚な") || strings.Contains(sc.Description, "<br>") {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Title != "Ui2 南風に微笑んで" || sc.Studio != "REbecca" {
		t.Errorf("Title/Studio = %q %q", sc.Title, sc.Studio)
	}
}

func TestParseDetailWithoutCode(t *testing.T) {
	c := card{id: "grace_036", title: "Honey＆Pretty", thumb: "https://file.rebecca-web.com/media/videos/dl03/grace_036/1_pc2.jpg"}
	sc, err := parseDetail(fixture(t, "detail_grace_036.html"), c, "http://x/detail.php?id=grace_036", "http://x/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ID != "grace_036" {
		t.Errorf("ID = %q, want shop id fallback", sc.ID)
	}
	if sc.Duration != 133*60 || sc.Date.Format("2006-01-02") != "2026-08-29" {
		t.Errorf("Duration/Date = %d %v", sc.Duration, sc.Date)
	}
	if len(sc.Tags) != 3 || sc.Tags[0] != "スレンダー" {
		t.Errorf("Tags = %v", sc.Tags)
	}
}

func TestParseDetailWithoutModelProfile(t *testing.T) {
	body := `<div class="specbox"><ul><li><div class="label">収録時間</div><div class="data">90分</div></li></ul></div><div class="sampleimagebox"></div>`
	sc, err := parseDetail(body, card{id: "kyiz_004", title: "巨乳の泉2"}, "u", "s")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Duration != 90*60 {
		t.Errorf("Duration = %d", sc.Duration)
	}
}

func TestParseDetailNoSpec(t *testing.T) {
	if _, err := parseDetail("<html></html>", card{id: "x", title: "t"}, "u", "s"); err == nil {
		t.Error("expected error")
	}
}

func TestListScenesFixtures(t *testing.T) {
	var searchHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.php":
			searchHits.Add(1)
			_, _ = fmt.Fprint(w, fixture(t, "search_page1.html"))
		case "/detail.php":
			if r.URL.Query().Get("id") == "grace_036" {
				_, _ = fmt.Fprint(w, fixture(t, "detail_grace_036.html"))
			} else {
				_, _ = fmt.Fprint(w, fixture(t, "detail_rebd_1085.html"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	studio := "http://rebecca-web.com/search.php?label_id=79"
	res, errs := collect(t, s, studio)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(res) != 3 {
		t.Fatalf("scenes = %d, want 3", len(res))
	}
	if searchHits.Load() != 1 {
		t.Errorf("search hits = %d, want 1 (short page ends walk)", searchHits.Load())
	}
	sc := res[0]
	if sc.ID != "grace_036" || sc.StudioURL != studio || !strings.HasPrefix(sc.URL, srv.URL+"/detail.php?id=grace_036") {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Thumbnail != "https://file.rebecca-web.com/media/videos/dl03/grace_036/1_pc2.jpg" {
		t.Errorf("thumb = %q", sc.Thumbnail)
	}
}

func TestPaginationStopsOnRepeatedDefaultPage(t *testing.T) {
	cardsHTML := func(from, n int) string {
		var sb strings.Builder
		sb.WriteString(`<ul class="commonlist">`)
		for i := from; i < from+n; i++ {
			fmt.Fprintf(&sb, `<li><a href="detail.php?id=w_%d"><div class="thumb"><img src="/p.png"></div><div class="title">T%d</div><div class="actress">A%d</div></a></li>`, i, i, i)
		}
		sb.WriteString(`</ul>`)
		return sb.String()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.php":
			switch r.URL.Query().Get("page") {
			case "", "1":
				_, _ = fmt.Fprint(w, cardsHTML(0, 30))
			case "2":
				_, _ = fmt.Fprint(w, cardsHTML(30, 30))
			default:
				_, _ = fmt.Fprint(w, cardsHTML(0, 12))
			}
		case "/detail.php":
			_, _ = fmt.Fprint(w, `<div class="specbox"><ul><li><div class="label">収録時間</div><div class="data">10分</div></li></ul></div><div class="modelprofielbox"></div>`)
		}
	}))
	defer srv.Close()

	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	res, errs := collect(t, s, "http://rebecca-web.com/")
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(res) != 60 {
		t.Errorf("scenes = %d, want 60", len(res))
	}
}

func TestFirstPageWithoutWorksIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html></html>")
	}))
	defer srv.Close()
	s := New()
	s.Client = srv.Client()
	s.base = srv.URL
	res, errs := collect(t, s, "http://rebecca-web.com/")
	if len(res) != 0 || len(errs) != 1 {
		t.Fatalf("scenes=%d errs=%v", len(res), errs)
	}
	if scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Errorf("kind = %v", scraper.Classify(errs[0]))
	}
}

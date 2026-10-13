package aromaplanning

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
		"https://www.aroma-p.com/",
		"https://aroma-p.com",
		"http://www.aroma-p.com/works/list/label/1/",
		"https://www.aroma-p.com/works/list/date/20260922/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://www.aroma-p.com.evil.test/",
		"https://example.com/aroma-p.com",
		"https://www.aroma.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestResolveTarget(t *testing.T) {
	cases := map[string]target{
		"https://www.aroma-p.com/":                          {},
		"https://www.aroma-p.com/works/list/label/2/":       {kind: "label", id: "2"},
		"https://www.aroma-p.com/works/list/genre/23/3/":    {kind: "genre", id: "23"},
		"https://www.aroma-p.com/works/list/date/20260922/": {kind: "date", date: "20260922"},
	}
	for u, want := range cases {
		if got := resolveTarget(u); got != want {
			t.Errorf("resolveTarget(%q) = %+v, want %+v", u, got, want)
		}
	}
}

func TestCatalogueID(t *testing.T) {
	for in, want := range map[string]string{"aarm367": "AARM-367", "armg001": "ARMG-001", "odd-slug": "ODD-SLUG"} {
		if got := catalogueID(in); got != want {
			t.Errorf("catalogueID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDates(t *testing.T) {
	body := `<a href="/works/list/date/20080119/">x</a><a href="/works/list/date/20260922/">y</a><a href="/works/list/date/20080119/">dup</a>`
	got := parseDates(body)
	if len(got) != 2 || got[0] != "20260922" || got[1] != "20080119" {
		t.Errorf("parseDates = %v", got)
	}
}

func TestParseListing(t *testing.T) {
	items := parseListing(fixture(t, "date.html"))
	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].slug != "aarm367" || items[0].title != "唾液エンドレスのじゅるじゅる乳首舐め×トルネード寸止めオイル手コキ6" {
		t.Errorf("item 0 = %+v", items[0])
	}
	if items[0].thumb != "/contents/works/aarm/aarm367/aarm367-pm.jpg" {
		t.Errorf("thumb = %q", items[0].thumb)
	}
	if items[1].slug != "aarm368" || items[1].title != "オフィスでおしゃぶり" {
		t.Errorf("item 1 = %+v", items[1])
	}
}

func TestApplyDetail(t *testing.T) {
	s := New()
	sc := s.baseScene(item{slug: "aarm367", title: "x"}, "AARM-367", "u", time.Now())
	applyDetail(&sc, fixture(t, "detail_aarm367.html"))

	if sc.Title != "唾液エンドレスのじゅるじゅる乳首舐め×トルネード寸止めオイル手コキ6" {
		t.Errorf("title = %q", sc.Title)
	}
	if got := strings.Join(sc.Performers, ","); got != "もなみ鈴,二宮もも,菜月ひかる,辻井ほのか,花衣つばき,依本しおり" {
		t.Errorf("performers = %q", got)
	}
	if sc.Director != "角脇しげお" {
		t.Errorf("director = %q", sc.Director)
	}
	if got := strings.Join(sc.Tags, ","); got != "乳首責め,手コキ,寸止め" {
		t.Errorf("tags = %q", got)
	}
	if got := strings.Join(sc.Categories, ","); got != "アロマ" {
		t.Errorf("categories = %q", got)
	}
	if sc.Duration != 7200 {
		t.Errorf("duration = %d", sc.Duration)
	}
	if sc.Date.Format("2006-01-02") != "2026-09-22" {
		t.Errorf("date = %v", sc.Date)
	}
	if !strings.HasPrefix(sc.Description, "唾液たっぷりの乳首舐めに") {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://www.aroma-p.com/contents/works/aarm/aarm367/aarm367-pm.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if !strings.HasSuffix(sc.Preview, "aarm00367_dm_w.mp4") {
		t.Errorf("preview = %q", sc.Preview)
	}
}

const searchPage = `<html><body>
<a href="/works/list/date/20080119/">1/19</a>
<a href="/works/list/date/20260922/">9/22</a>
</body></html>`

const oldPage = `<div class="wrap-lst-work"><ul class="lst-works">
<li class="c-list-works-item "><div class="wrap-works-item">
<a class="bx-works-package" href="/works/detail/armg001/"><img class="img-works-package" src="/contents/works/armg/armg001/armg001-pm.jpg" alt="Old"></a>
<div class="wrap-ttl-works"><a class="ttl-works" href="/works/detail/armg001/">Old Title</a></div></div></li></ul></div>`

func newScraper(t *testing.T) *Scraper {
	t.Helper()
	date := fixture(t, "date.html")
	detail := fixture(t, "detail_aarm367.html")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/":
			_, _ = fmt.Fprint(w, searchPage)
		case "/works/list/date/20260922/":
			_, _ = fmt.Fprint(w, date)
		case "/works/list/date/20080119/":
			_, _ = fmt.Fprint(w, oldPage)
		case "/works/list/label/1/":
			_, _ = fmt.Fprint(w, date+`<link rel="next" href="/works/list/label/1/2/">`)
		case "/works/list/label/1/2/":
			_, _ = fmt.Fprint(w, oldPage)
		case "/works/detail/aarm367/", "/works/detail/aarm368/", "/works/detail/armg001/":
			slug := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[2]
			_, _ = fmt.Fprint(w, strings.ReplaceAll(detail, "aarm367", slug))
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	s := New()
	s.base = srv.URL
	s.client = srv.Client()
	return s
}

type collected struct {
	ids     []string
	scenes  []scraper.SceneResult
	errs    []string
	stopped bool
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) collected {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	var c collected
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			c.scenes = append(c.scenes, r)
			c.ids = append(c.ids, r.Scene.ID)
		case scraper.KindError:
			c.errs = append(c.errs, r.Err.Error())
		case scraper.KindStoppedEarly:
			c.stopped = true
		}
	}
	return c
}

func TestListScenesWholeCatalogue(t *testing.T) {
	s := newScraper(t)
	c := collect(t, s, "https://www.aroma-p.com/", scraper.ListOpts{})
	if len(c.errs) != 0 {
		t.Fatalf("errors: %v", c.errs)
	}
	if got := strings.Join(c.ids, ","); got != "AARM-367,AARM-368,ARMG-001" {
		t.Fatalf("ids = %s", got)
	}
	first := c.scenes[0].Scene
	if first.SiteID != siteID || first.StudioURL != "https://www.aroma-p.com/" || first.Studio != studioName {
		t.Errorf("first = %+v", first)
	}
	if first.URL != s.base+"/works/detail/aarm367/" {
		t.Errorf("url = %q", first.URL)
	}
	if len(first.Performers) != 6 || first.Date.Format("2006-01-02") != "2026-09-22" {
		t.Errorf("first fields = %+v", first)
	}
	if last := c.scenes[2].Scene; last.Title != "唾液エンドレスのじゅるじゅる乳首舐め×トルネード寸止めオイル手コキ6" {
		t.Errorf("detail title should win over card title, got %q", last.Title)
	}
}

func TestListScenesKnownIDsStopsAfterThePageOfDays(t *testing.T) {
	s := newScraper(t)
	c := collect(t, s, "https://www.aroma-p.com/", scraper.ListOpts{KnownIDs: map[string]bool{"AARM-367": true}})
	if got := strings.Join(c.ids, ","); got != "AARM-368,ARMG-001" || !c.stopped {
		t.Errorf("ids = %s stopped = %v", got, c.stopped)
	}
}

func TestListScenesDateMode(t *testing.T) {
	s := newScraper(t)
	url := "https://www.aroma-p.com/works/list/date/20260922/"
	c := collect(t, s, url, scraper.ListOpts{})
	if len(c.errs) != 0 || len(c.scenes) != 2 {
		t.Fatalf("scenes = %d errs = %v", len(c.scenes), c.errs)
	}
	if sc := c.scenes[1].Scene; sc.ID != "AARM-368" || sc.StudioURL != url {
		t.Errorf("scene = %+v", sc)
	}
}

func TestListScenesLabelPagination(t *testing.T) {
	s := newScraper(t)
	c := collect(t, s, "https://www.aroma-p.com/works/list/label/1/", scraper.ListOpts{})
	if len(c.errs) != 0 {
		t.Fatalf("errors: %v", c.errs)
	}
	if got := strings.Join(c.ids, ","); got != "AARM-367,AARM-368,ARMG-001" {
		t.Errorf("ids = %s", got)
	}
}

func TestListScenesNoDatesIsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html></html>")
	}))
	defer srv.Close()
	s := New()
	s.base = srv.URL
	s.client = srv.Client()

	c := collect(t, s, "https://www.aroma-p.com/", scraper.ListOpts{})
	if len(c.scenes) != 0 || len(c.errs) != 1 {
		t.Errorf("scenes = %d errs = %v", len(c.scenes), c.errs)
	}
}

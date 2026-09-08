package maxing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/text/encoding/japanese"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.maxing.jp/", true},
		{"http://maxing.jp/shop/", true},
		{"https://www.maxing.jp/shop/ac/ACT10364.html", true},
		{"https://maxing.jp.evil.org/", false},
		{"https://maxing.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestFilterPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://www.maxing.jp/", ""},
		{"https://www.maxing.jp/shop/", ""},
		{"https://www.maxing.jp/shop/src/page/3.html", ""},
		{"https://www.maxing.jp/shop/ac/ACT10364.html", "/shop/ac/ACT10364.html"},
		{"https://www.maxing.jp/shop/sr/243.html", "/shop/sr/243.html"},
		{"https://www.maxing.jp/shop/la/13.html", "/shop/la/13.html"},
	}
	for _, c := range cases {
		if got := filterPath(c.in); got != c.want {
			t.Errorf("filterPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTrimSiteSuffix(t *testing.T) {
	if got := trimSiteSuffix("イッた直後の感度100% 松丸香澄★マキシング"); got != "イッた直後の感度100% 松丸香澄" {
		t.Errorf("trimSiteSuffix = %q", got)
	}
	if got := trimSiteSuffix("No suffix here"); got != "No suffix here" {
		t.Errorf("trimSiteSuffix = %q", got)
	}
}

const productPage = `<html><head>
<meta http-equiv="Content-Type" content="text/html; charset=EUC-JP">
<title>イッた直後の感度100%のおま●こ 松丸香澄★マキシング</title>
</head><body>
<a href="https://www.maxing.jp/com/img_popup.php?img_url=../product_img/MXGS-1408/P0001.jpg" class="thickbox">
<img src="https://www.maxing.jp/product_img/MXGS-1408/P0002.jpg" alt="cover" /></a>
<dl class="pDetailDl">
	<dt class="noneline">品番</dt><dd class="noneline">MXGS-1408</dd>
	<dt>監督</dt><dd>馨&nbsp;</dd>
	<dt>収録時間</dt><dd>125&nbsp;</dd>
	<dt>発売日</dt><dd>2025年12月16日</dd>
	<dt>メーカー</dt><dd><a href="https://www.maxing.jp/shop/mk/1.html">MAXING</a>&nbsp;</dd>
	<dt>レーベル</dt><dd><a href="https://www.maxing.jp/shop/la/13.html">MAXING</a>&nbsp;</dd>
	<dt>シリーズ</dt><dd><a href="https://www.maxing.jp/shop/sr/243.html">無限ループピストン</a>&nbsp;</dd>
	<dt>女優</dt><dd><a href="https://www.maxing.jp/shop/ac/ACT10364.html">松丸香澄</a>&nbsp;</dd>
	<dt>ジャンル</dt><dd>&nbsp;</dd>
	<dt>内容</dt><dd>１回イったあとの敏感おま〇こ。&nbsp;</dd>
</dl>
</body></html>`

func TestParseProduct(t *testing.T) {
	s := New()
	sc, err := s.parseProduct([]byte(productPage), "5536",
		"https://www.maxing.jp/shop/pid/5536.html", "https://www.maxing.jp/")
	if err != nil {
		t.Fatal(err)
	}
	// The catalogue number identifies a release everywhere else, so it is the
	// id rather than the shop's internal product number.
	if sc.ID != "MXGS-1408" {
		t.Errorf("id = %q", sc.ID)
	}
	if sc.Title != "イッた直後の感度100%のおま●こ 松丸香澄" {
		t.Errorf("title = %q — the site's own ★suffix should be gone", sc.Title)
	}
	if sc.Director != "馨" {
		t.Errorf("director = %q", sc.Director)
	}
	// 収録時間 is stated in whole minutes.
	if sc.Duration != 125*60 {
		t.Errorf("duration = %d, want 7500", sc.Duration)
	}
	if sc.Date.Format("2006-01-02") != "2025-12-16" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Series != "無限ループピストン" {
		t.Errorf("series = %q", sc.Series)
	}
	if !slices.Equal(sc.Performers, []string{"松丸香澄"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.Description != "１回イったあとの敏感おま〇こ。" {
		t.Errorf("description = %q", sc.Description)
	}
	// The label equals the studio on most releases and is not worth storing;
	// the cover is linked twice and only the <img src> is a usable image URL.
	if len(sc.Categories) != 0 {
		t.Errorf("categories = %v, want none", sc.Categories)
	}
	if sc.Thumbnail != "https://www.maxing.jp/product_img/MXGS-1408/P0002.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
}

// The table omits a row entirely when a release has no director or series, so
// fields are matched by their label rather than by position.
func TestParseProductWithMissingRows(t *testing.T) {
	page := `<html><head><title>Bare Release★マキシング</title></head><body>
	<dl class="pDetailDl">
		<dt>品番</dt><dd>MXBD-001</dd>
		<dt>発売日</dt><dd>2020年1月5日</dd>
		<dt>女優</dt><dd><a href="/shop/ac/ACT1.html">A</a><a href="/shop/ac/ACT2.html">B</a></dd>
	</dl></body></html>`
	s := New()
	sc, err := s.parseProduct([]byte(page), "1", "https://www.maxing.jp/shop/pid/1.html", "https://www.maxing.jp/")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Director != "" || sc.Series != "" || sc.Duration != 0 {
		t.Errorf("absent rows produced values: %+v", sc)
	}
	if sc.Date.Format("2006-01-02") != "2020-01-05" {
		t.Errorf("date = %v", sc.Date)
	}
	if !slices.Equal(sc.Performers, []string{"A", "B"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
}

func TestParseProductWithoutADetailBlockIsAParseError(t *testing.T) {
	s := New()
	_, err := s.parseProduct([]byte(`<html><body><p>nothing</p></body></html>`), "1",
		"https://www.maxing.jp/shop/pid/1.html", "https://www.maxing.jp/")
	if err == nil {
		t.Fatal("want an error")
	}
	if k := scraper.Classify(err); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	pages   int
	perPage int

	mu   sync.Mutex
	hits []string
}

func (f *fakeSite) hit(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, p)
}

func (f *fakeSite) hitList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hits)
}

// eucjp encodes a fixture the way the site serves it. A scraper that read the
// bytes as UTF-8 would turn every Japanese field into replacement characters.
func eucjp(t *testing.T, s string) []byte {
	t.Helper()
	b, err := japanese.EUCJP.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	return b
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.hit(r.URL.Path)
		w.Header().Set("Content-Type", "text/html; charset=EUC-JP")
		if strings.HasPrefix(r.URL.Path, "/shop/pid/") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/shop/pid/"), ".html")
			_, _ = w.Write(eucjp(t, strings.ReplaceAll(productPage, "MXGS-1408", "MX-"+id)))
			return
		}
		var page int
		switch {
		case strings.HasPrefix(r.URL.Path, "/shop/ac/"):
			page = 1
		default:
			if _, err := fmt.Sscanf(r.URL.Path, "/shop/src/page/%d.html", &page); err != nil {
				http.NotFound(w, r)
				return
			}
			if page > f.pages {
				_, _ = w.Write(eucjp(t, `<html><body>該当する商品はありません</body></html>`))
				return
			}
		}
		var b strings.Builder
		b.WriteString(`<html><body>`)
		for i := range f.perPage {
			fmt.Fprintf(&b, `<a href="https://www.maxing.jp/shop/pid/%d%d.html">x</a>`, page, i)
		}
		b.WriteString(`</body></html>`)
		_, _ = w.Write(eucjp(t, b.String()))
	}
}

func newTestScraper(t *testing.T, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	return &Scraper{Client: ts.Client(), base: ts.URL}
}

func collect(t *testing.T, s *Scraper, studioURL string) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 500)
	go s.run(context.Background(), studioURL, scraper.ListOpts{}, out)
	var ids []string
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	slices.Sort(ids)
	return ids, errs
}

func TestRunWalksToTheLastPage(t *testing.T) {
	f := &fakeSite{pages: 3, perPage: 2}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://www.maxing.jp/")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	want := []string{"MX-10", "MX-11", "MX-20", "MX-21", "MX-30", "MX-31"}
	if !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if !slices.Contains(f.hitList(), "/shop/src/page/4.html") {
		t.Errorf("hits = %v, want the walk to probe past the end", f.hitList())
	}
}

// The site is EUC-JP and says so only in a meta tag; decoding must not be left
// to the default.
func TestRunDecodesJapanese(t *testing.T) {
	f := &fakeSite{pages: 1, perPage: 1}
	s := newTestScraper(t, f)

	out := make(chan scraper.SceneResult, 50)
	go s.run(context.Background(), "https://www.maxing.jp/", scraper.ListOpts{}, out)
	var got string
	for r := range out {
		if r.Kind == scraper.KindScene {
			got = r.Scene.Performers[0]
		}
	}
	if got != "松丸香澄" {
		t.Errorf("performer = %q, want the decoded Japanese name", got)
	}
}

func TestRunActressPageIsASingleRequest(t *testing.T) {
	f := &fakeSite{pages: 3, perPage: 2}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, s.base+"/shop/ac/ACT10364.html")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !slices.Equal(ids, []string{"MX-10", "MX-11"}) {
		t.Errorf("ids = %v", ids)
	}
	for _, h := range f.hitList() {
		if strings.HasPrefix(h, "/shop/src/") {
			t.Errorf("an actress walk requested catalogue pages: %v", f.hitList())
			break
		}
	}
}

func TestRunReportsAnEmptyFirstPage(t *testing.T) {
	f := &fakeSite{pages: 0, perPage: 0}
	s := newTestScraper(t, f)

	ids, errs := collect(t, s, "https://www.maxing.jp/")
	if len(ids) != 0 {
		t.Errorf("got %d scenes, want none", len(ids))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

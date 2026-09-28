package faleno

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://falenogroup.com/", true},
		{"https://www.falenogroup.com/", true},
		{"https://falenogroup.com/work/", true},
		{"https://falenogroup.com/makers/clover/", true},
		{"https://falenogroup.com/botan/", true},
		{"https://falenogroup.com/noskn/", true},
		{"https://dahlia-av.jp/", true},
		{"https://dahlia-av.jp/work/", true},
		{"https://dahlia-av.jp/actress/some-name/", true},
		{"https://example.com/", false},
		{"https://faleno.jp/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestCodeFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://falenogroup.com/works/fthtd-211/", "fthtd-211"},
		{"https://dahlia-av.jp/works/dldss517/", "dldss517"},
		{"https://falenogroup.com/works/scbb-001/", "scbb-001"},
	}
	for _, c := range cases {
		if got := codeFromURL(c.url); got != c.want {
			t.Errorf("codeFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestParseDetailPage(t *testing.T) {
	html := `<html><body>
<h1>テスト作品タイトル</h1>
<div class="box_works01_img">
  <img src="https://cdn.faleno.net/group/wp-content/uploads/2024/01/FTHTD-100.jpg">
</div>
<div class="box_works01_text">
  <p>作品の説明文です。</p>
</div>
<div class="box_works01_list clearfix">
<ul>
<li class="clearfix"><span>出演女優</span><p>女優A、女優B</p></li>
<li class="clearfix"><span>収録時間</span><p>138分</p></li>
<li class="clearfix"><span>監督</span><p>監督名</p></li>
</ul>
<ul>
<li class="clearfix"><span>メーカー</span><p>FALENO TUBE</p></li>
<li class="clearfix"><span>配信開始日</span><p>2024/5/29</p></li>
<li class="clearfix"><span>発売日</span><p>2024/6/15</p></li>
</ul>
</div>
<a href="/genre/%E4%B8%AD%E5%87%BA%E3%81%97/">中出し</a>
<a href="/genre/%E3%83%8A%E3%83%B3%E3%83%91/">ナンパ</a>
</body></html>`

	d := parseDetailPage([]byte(html))

	if d.title != "テスト作品タイトル" {
		t.Errorf("title = %q", d.title)
	}
	if d.cover != "https://cdn.faleno.net/group/wp-content/uploads/2024/01/FTHTD-100.jpg" {
		t.Errorf("cover = %q", d.cover)
	}
	if d.description != "作品の説明文です。" {
		t.Errorf("description = %q", d.description)
	}
	if len(d.performers) != 2 || d.performers[0] != "女優A" || d.performers[1] != "女優B" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.duration != 138*60 {
		t.Errorf("duration = %d, want %d", d.duration, 138*60)
	}
	if d.director != "監督名" {
		t.Errorf("director = %q", d.director)
	}
	if d.maker != "FALENO TUBE" {
		t.Errorf("maker = %q", d.maker)
	}
	if d.date.Format("2006-01-02") != "2024-05-29" {
		t.Errorf("date = %v", d.date)
	}
	if len(d.genres) != 2 {
		t.Errorf("genres = %v", d.genres)
	}
}

func TestParseDurationMin(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"138分", 138 * 60},
		{"60分", 3600},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseDurationMin(c.input); got != c.want {
			t.Errorf("parseDurationMin(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestParseFalenoDate(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"2024/5/29", "2024-05-29"},
		{"2024/05/29", "2024-05-29"},
		{"2026/1/1", "2026-01-01"},
		{"", "0001-01-01"},
	}
	for _, c := range cases {
		got := parseFalenoDate(c.input)
		if got.Format("2006-01-02") != c.want {
			t.Errorf("parseFalenoDate(%q) = %v, want %s", c.input, got, c.want)
		}
	}
}

func TestSplitPerformers(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"女優A、女優B", 2},
		{"女優A,女優B", 2},
		{"ソロ", 1},
		{"", 0},
	}
	for _, c := range cases {
		got := splitPerformers(c.input)
		if len(got) != c.want {
			t.Errorf("splitPerformers(%q) len = %d, want %d (got %v)", c.input, len(got), c.want, got)
		}
	}
}

func TestParseWorkURLs(t *testing.T) {
	html := `<a href="https://falenogroup.com/works/fthtd-100/">
<a href="https://falenogroup.com/works/fthtd-100/">
<a href="https://falenogroup.com/works/fthtd-101/">`
	urls := parseWorkURLs([]byte(html), "https://falenogroup.com")
	if len(urls) != 2 {
		t.Fatalf("got %d urls, want 2 (deduped)", len(urls))
	}
}

func TestParseLastPage(t *testing.T) {
	cases := []struct {
		name string
		html string
		want int
	}{
		{
			"falenogroup last link",
			`<a class="last" aria-label="Last Page" href="https://falenogroup.com/work/page/88/">`,
			88,
		},
		{
			"dahlia pages span",
			`<span class='pages'>2 / 23</span>`,
			23,
		},
		{
			"no pagination",
			`<div>no page nav</div>`,
			0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseLastPage([]byte(c.html))
			if got != c.want {
				t.Errorf("parseLastPage = %d, want %d", got, c.want)
			}
		})
	}
}

func detailHTML(code, title, performer, maker string) string {
	return fmt.Sprintf(`<html><body>
<h1>%s</h1>
<div class="box_works01_img"><img src="https://cdn.faleno.net/%s.jpg"></div>
<div class="box_works01_text"><p>Description</p></div>
<div class="box_works01_list clearfix">
<ul>
<li class="clearfix"><span>出演女優</span><p>%s</p></li>
<li class="clearfix"><span>収録時間</span><p>120分</p></li>
</ul><ul>
<li class="clearfix"><span>メーカー</span><p>%s</p></li>
<li class="clearfix"><span>配信開始日</span><p>2024/6/1</p></li>
</ul></div>
</body></html>`, title, code, performer, maker)
}

func listingHTMLWithBase(codes []string, lastPage int, base string) string {
	var sb string
	for _, c := range codes {
		sb += fmt.Sprintf(`<div class="text_name"><a href="%s/works/%s/">Title %s</a></div>`+"\n", base, c, c)
	}
	if lastPage > 0 {
		sb += fmt.Sprintf(`<a class="last" aria-label="Last Page" href="%s/work/page/%d/">&gt;&gt;</a>`, base, lastPage)
	}
	return sb
}

func newTestServer(t *testing.T, listings map[string][]string, details map[string]string, lastPage int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		base := "http://" + r.Host

		for prefix, codes := range listings {
			if r.URL.Path == prefix || r.URL.Path == prefix+"/" {
				_, _ = fmt.Fprint(w, listingHTMLWithBase(codes, lastPage, base))
				return
			}
		}

		for code, html := range details {
			if r.URL.Path == "/works/"+code+"/" {
				_, _ = fmt.Fprint(w, html)
				return
			}
		}

		http.NotFound(w, r)
	}))
}

func TestListScenesMakerPage(t *testing.T) {
	details := map[string]string{
		"fthtd-100": detailHTML("FTHTD-100", "Scene One", "Performer A", "FALENO TUBE"),
		"fthtd-101": detailHTML("FTHTD-101", "Scene Two", "Performer B", "FALENO TUBE"),
	}
	listings := map[string][]string{
		"/makers/falenotube": {"fthtd-100", "fthtd-101"},
	}

	ts := newTestServer(t, listings, details, 0)
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/makers/falenotube/", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Duration != 7200 {
		t.Errorf("duration = %d, want 7200", results[0].Duration)
	}
}

// The /work/ listing is grouped by label, not by release date, so a stored code
// in the middle must be skipped rather than end the walk.
func TestListScenesKnownIDs(t *testing.T) {
	details := map[string]string{
		"fthtd-100": detailHTML("FTHTD-100", "New", "A", "FALENO TUBE"),
		"fthtd-101": detailHTML("FTHTD-101", "Known", "B", "FALENO TUBE"),
	}
	details["fthtd-102"] = detailHTML("FTHTD-102", "Also New", "C", "FALENO TUBE")
	listings := map[string][]string{
		"/makers/falenotube": {"fthtd-100", "fthtd-101", "fthtd-102"},
	}

	ts := newTestServer(t, listings, details, 0)
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/makers/falenotube/", scraper.ListOpts{
		KnownIDs: map[string]bool{"FTHTD-101": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	ids := map[string]bool{}
	for _, sc := range results {
		ids[sc.ID] = true
	}
	if len(results) != 2 || !ids["FTHTD-100"] || !ids["FTHTD-102"] {
		t.Fatalf("scene IDs = %v, want FTHTD-100 and the code behind the stored one", ids)
	}
}

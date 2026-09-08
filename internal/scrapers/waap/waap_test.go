package waap

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.waap.co.jp/", true},
		{"https://www.waap.co.jp/work/search.php?serch=8&onrls=new&tlname=cobra&pg=1", true},
		{"https://waap.co.jp/work/item.php?itemcode=ECB167", true},
		{"http://www.dt01.co.jp/", true},
		{"https://dt01.co.jp/foo", true},
		{"https://example.com/waap", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseListingPage(t *testing.T) {
	body := `
<div>検索結果：42件</div>
<ul class="item_list">
<li class="list_img_jacket"><a href="item.php?itemcode=ECB167"><img src='../hskhsk/itsmall125177/ECB167.jpg' alt='Title One／Performer A' width='125' class='over' /></a></li>
<li class="list_cmt"><a href="item.php?itemcode=ECB167"><span>【最新作】</span>Title One</a></li>
</ul>
<ul class="item_list">
<li class="list_img_jacket"><a href="item.php?itemcode=DFE456"><img src='../hskhsk/itsmall125177/DFE456.jpg' alt='Title Two' /></a></li>
<li class="list_cmt"><a href="item.php?itemcode=DFE456">Title Two</a></li>
</ul>
`
	items, total := parseListingPage(body)
	if total != 42 {
		t.Errorf("total = %d, want 42", total)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].code != "ECB167" {
		t.Errorf("items[0].code = %q", items[0].code)
	}
	if items[1].code != "DFE456" {
		t.Errorf("items[1].code = %q", items[1].code)
	}
}

func TestParseDetailPage(t *testing.T) {
	body := `
<meta name="twitter:title" content="痴女る女と犯ラレル男／鈴木真夕／水端あさみ" />
<meta name="twitter:image" content="http://www.waap.co.jp/hskhsk/itsub/ECB167.jpg" />
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd">&nbsp;発売月&nbsp;：&nbsp;&nbsp;2026年06月</span></li>
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd">&nbsp;出演者&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='../work/search.php?serch=2&onrls=new&tlname=test&pg=1'>鈴木真夕</a>&nbsp;<a href='../work/search.php?serch=2&onrls=new&tlname=test2&pg=1'>水端あさみ</a>&nbsp;</span></li>
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd02">&nbsp;ジャンル&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='../work/search.php?serch=3&onrls=new&tlname=test&pg=1'>アダルト</a>&nbsp;<a href='../work/search.php?serch=3&onrls=new&tlname=test2&pg=1'>痴女</a>&nbsp;</span></li>
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd">&nbsp;レーベル&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='../work/search.php?serch=8&onrls=new&tlname=cobra&pg=1'>cobra</a>&nbsp;</span></li>
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd">&nbsp;メーカー&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='../work/search.php?serch=10&onrls=new&tlname=test&pg=1'>ワープエンタテインメント</a>&nbsp;</span></li>
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd">&nbsp;シリーズ&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='../work/search.php?serch=4&onrls=new&tlname=test&pg=1'>痴女る女</a>&nbsp;</a></span></li>
<div class="update_counts">収録時間：<span>137分</span></div>
<div id="title_cmt_all">A test description here.</div>
`
	d := parseDetailPage(body)

	if d.title != "痴女る女と犯ラレル男" {
		t.Errorf("title = %q", d.title)
	}
	if d.thumbnail != "https://www.waap.co.jp/hskhsk/itsub/ECB167.jpg" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
	if d.duration != 137*60 {
		t.Errorf("duration = %d, want %d", d.duration, 137*60)
	}
	if d.date.Year() != 2026 || d.date.Month() != 6 {
		t.Errorf("date = %v", d.date)
	}
	if len(d.performers) != 2 || d.performers[0] != "鈴木真夕" || d.performers[1] != "水端あさみ" {
		t.Errorf("performers = %v", d.performers)
	}
	if len(d.tags) != 2 || d.tags[0] != "アダルト" || d.tags[1] != "痴女" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.studio != "cobra" {
		t.Errorf("studio = %q", d.studio)
	}
	if d.series != "痴女る女" {
		t.Errorf("series = %q", d.series)
	}
	if d.description != "A test description here." {
		t.Errorf("description = %q", d.description)
	}
}

func TestParseDetailPageMakerFallback(t *testing.T) {
	body := `
<meta name="twitter:title" content="No Label Scene／Performer" />
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd">&nbsp;メーカー&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='../work/search.php?serch=10&onrls=new&tlname=test&pg=1'>ドリームチケット</a>&nbsp;</span></li>
`
	d := parseDetailPage(body)
	if d.studio != "ドリームチケット" {
		t.Errorf("studio = %q, want ドリームチケット (maker fallback)", d.studio)
	}
}

func TestSetPageParam(t *testing.T) {
	cases := []struct {
		url  string
		page int
		want string
	}{
		{"https://www.waap.co.jp/work/search.php?serch=5&onrls=new&limit=45&pg=1", 3,
			"https://www.waap.co.jp/work/search.php?serch=5&onrls=new&limit=45&pg=3"},
		{"https://www.waap.co.jp/work/search.php?serch=5", 2,
			"https://www.waap.co.jp/work/search.php?serch=5&pg=2"},
	}
	for _, c := range cases {
		if got := setPageParam(c.url, c.page); got != c.want {
			t.Errorf("setPageParam(%q, %d) = %q, want %q", c.url, c.page, got, c.want)
		}
	}
}

func TestResolveListingURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.waap.co.jp/work/search.php?serch=8&onrls=new&tlname=cobra&pg=1",
			"https://www.waap.co.jp/work/search.php?serch=8&onrls=new&tlname=cobra&pg=1"},
		{"https://www.waap.co.jp/",
			siteBase + "/work/search.php?serch=5&onrls=new&limit=45&pg=1"},
		{"http://www.dt01.co.jp/",
			siteBase + "/work/search.php?serch=5&onrls=new&limit=45&pg=1"},
	}
	for _, c := range cases {
		if got := resolveListingURL(c.url); got != c.want {
			t.Errorf("resolveListingURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

const listingTpl = `<div>検索結果：%d件</div>%s`

const itemTpl = `<ul class="item_list">
<li class="list_img_jacket"><a href="item.php?itemcode=%s"><img src='thumb.jpg' alt='Title' /></a></li>
</ul>`

const detailTpl = `
<meta name="twitter:title" content="Test Title／Model A" />
<meta name="twitter:image" content="http://www.waap.co.jp/hskhsk/itsub/%s.jpg" />
<div class="update_counts">収録時間：<span>90分</span></div>
<li class="wkact_ser_maker02"><span class="wkact_ser_txtmd">&nbsp;発売月&nbsp;：&nbsp;&nbsp;2026年01月</span></li>
<li class="wkact_ser_maker02"><span>&nbsp;出演者&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='search.php?serch=2&onrls=new&tlname=a&pg=1'>Model A</a>&nbsp;</span></li>
<li class="wkact_ser_maker02"><span>&nbsp;レーベル&nbsp;：&nbsp;</span><span id="act_name_color" class="wkact_ser_txtbox"><a href='search.php?serch=8&onrls=new&tlname=cobra&pg=1'>cobra</a>&nbsp;</span></li>
`

func newTestServer(codes []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		switch r.URL.Path {
		case "/work/search.php":
			pg := r.URL.Query().Get("pg")
			if pg != "" && pg != "1" {
				_, _ = fmt.Fprintf(w, listingTpl, len(codes), "")
				return
			}
			var items string
			for _, code := range codes {
				items += fmt.Sprintf(itemTpl, code)
			}
			_, _ = fmt.Fprintf(w, listingTpl, len(codes), items)

		case "/work/item.php":
			code := r.URL.Query().Get("itemcode")
			_, _ = fmt.Fprintf(w, detailTpl, code)

		default:
			_, _ = fmt.Fprint(w, `<div>empty</div>`)
		}
	}))
}

func TestRun(t *testing.T) {
	ts := newTestServer([]string{"ABC001", "DEF002"})
	defer ts.Close()

	s := &Scraper{client: ts.Client()}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/work/search.php?serch=5&onrls=new&limit=45&pg=1", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Title != "Test Title" && results[1].Title != "Test Title" {
		t.Errorf("unexpected titles: %q, %q", results[0].Title, results[1].Title)
	}
}

func TestRunKnownIDs(t *testing.T) {
	ts := newTestServer([]string{"ABC001", "DEF002", "GHI003"})
	defer ts.Close()

	s := &Scraper{client: ts.Client()}

	ch, err := s.ListScenes(context.Background(), ts.URL+"/work/search.php?serch=5&onrls=new&limit=45&pg=1", scraper.ListOpts{
		KnownIDs: map[string]bool{"DEF002": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(results) != 1 {
		t.Fatalf("got %d scenes, want 1", len(results))
	}
}

// The charset used to be taken from the Content-Type header alone, and this
// site labels most of its pages only in a <meta> tag — so those decoded as
// UTF-8 and every Japanese title came out as replacement characters.
func TestDecodeShiftJIS(t *testing.T) {
	const jp = "美少女とデート"
	sjis, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(jp))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		body        []byte
		contentType string
		want        string
	}{
		{
			name:        "header names the charset",
			body:        sjis,
			contentType: "text/html; charset=Shift_JIS",
			want:        jp,
		},
		{
			name:        "header is silent, the page declares it",
			body:        append([]byte(`<meta http-equiv="Content-Type" content="text/html; charset=Shift_JIS">`), sjis...),
			contentType: "text/html",
			want:        jp,
		},
		{
			name:        "nothing declares it and the bytes are not UTF-8",
			body:        sjis,
			contentType: "text/html",
			want:        jp,
		},
		{
			name:        "a genuine UTF-8 page is left alone",
			body:        []byte(jp),
			contentType: "text/html; charset=UTF-8",
			want:        jp,
		},
		{
			name:        "an undeclared UTF-8 page is left alone",
			body:        []byte(jp),
			contentType: "text/html",
			want:        jp,
		},
		{
			name:        "ASCII is untouched",
			body:        []byte("<html><body>plain</body></html>"),
			contentType: "text/html",
			want:        "<html><body>plain</body></html>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeShiftJIS(c.body, c.contentType)
			if !strings.Contains(got, c.want) {
				t.Errorf("decodeShiftJIS = %q, want it to contain %q", got, c.want)
			}
			if strings.Contains(got, "�") {
				t.Errorf("decodeShiftJIS produced replacement characters: %q", got)
			}
		})
	}
}

package deeps

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

type testItem struct {
	code string
}

func listingPageHTML(items []testItem, totalCount int, maxPage int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	fmt.Fprintf(&sb, `<h1>作品一覧<span>全 %d タイトル</span></h1>`, totalCount)
	sb.WriteString(`<ul class="list_box">`)
	for _, item := range items {
		prefix := strings.SplitN(item.code, "-", 2)[0]
		fmt.Fprintf(&sb, `<li class="shinsaku"><a href="detail.php?%s"><figure><img src="img/%s/%s.jpg" alt="%s：ジャケット"></figure>`, item.code, prefix, item.code, item.code)
		fmt.Fprintf(&sb, `<div class="mask"><div class="caption">Description for %s</div><div class="btn_more pc"></div></div></a></li>`, item.code)
	}
	sb.WriteString(`</ul>`)
	if maxPage > 0 {
		sb.WriteString(`<p class="footbango">`)
		for i := 1; i <= maxPage; i++ {
			fmt.Fprintf(&sb, `<a href="index.php?0__50_%d" class="navbango">%d</a>`, i, i)
		}
		sb.WriteString(`</p>`)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func filterListingPageHTML(items []testItem, filterName string, filterCount int, maxPage int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	sb.WriteString(`<h1>作品一覧<span>全 2615 タイトル</span></h1>`)
	fmt.Fprintf(&sb, `このうち「%s」関連の作品は %d タイトルあります。`, filterName, filterCount)
	sb.WriteString(`<ul class="list_box">`)
	for _, item := range items {
		prefix := strings.SplitN(item.code, "-", 2)[0]
		fmt.Fprintf(&sb, `<li><a href="detail.php?%s"><figure><img src="img/%s/%s.jpg" alt=""></figure>`, item.code, prefix, item.code)
		sb.WriteString(`<div class="mask"><div class="caption">Desc</div></div></a></li>`)
	}
	sb.WriteString(`</ul>`)
	if maxPage > 0 {
		sb.WriteString(`<p class="footbango">`)
		for i := 1; i <= maxPage; i++ {
			fmt.Fprintf(&sb, `<a href="index.php?w_test_50_%d" class="navbango">%d</a>`, i, i)
		}
		sb.WriteString(`</p>`)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func detailPageHTML(code, title, desc, date, duration, director, price, cast, series, categories string) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><main><div class="main_column" id="item"><section><div class="inner">`)
	fmt.Fprintf(&sb, `<figure class="jacket"><img class="pc pc222" src="img/%s/%s.jpg" alt="%s"></figure>`, strings.SplitN(code, "-", 2)[0], code, code)
	fmt.Fprintf(&sb, `<h1 class="shinsaku">%s</h1>`, title)
	sb.WriteString(`<table class="t1"><tbody>`)
	fmt.Fprintf(&sb, `<tr><th>発売日</th><td>%s</td></tr>`, date)
	fmt.Fprintf(&sb, `<tr><th>収録時間</th><td>%s</td></tr>`, duration)
	sb.WriteString(`</tbody></table>`)
	sb.WriteString(`<table class="t2"><tbody>`)
	fmt.Fprintf(&sb, `<tr><th>監督</th><td>%s</td></tr>`, director)
	fmt.Fprintf(&sb, `<tr><th>品番/価格</th><td>%s</td></tr>`, price)
	sb.WriteString(`</tbody></table>`)
	sb.WriteString(`<table class="t3"><tbody>`)
	fmt.Fprintf(&sb, `<tr><th>出演</th><td>%s</td></tr>`, cast)
	fmt.Fprintf(&sb, `<tr><th>シリーズ</th><td>%s</td></tr>`, series)
	fmt.Fprintf(&sb, `<tr><th>カテゴリ</th><td>%s</td></tr>`, categories)
	sb.WriteString(`</tbody></table>`)
	fmt.Fprintf(&sb, `<div class="item_content"><p>%s</p></div>`, desc)
	sb.WriteString(`</div></section></div></main></body></html>`)
	return sb.String()
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://deeps.net/", true},
		{"https://deeps.net", true},
		{"https://www.deeps.net/", true},
		{"https://deeps.net/item/", true},
		{"https://deeps.net/item", true},
		{"https://deeps.net/item/index.php", true},
		{"https://deeps.net/item/index.php?w_test", true},
		{"https://deeps.net/item/?s_test", true},
		// Detail pages should NOT match
		{"https://deeps.net/item/detail.php?dvmm-382", false},
		// Other sections should NOT match
		{"https://deeps.net/board/", false},
		{"https://deeps.net/offering/", false},
		// Other sites
		{"https://example.com/item/", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestResolveListingURL ----

func TestResolveListingURL(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://deeps.net/", "https://deeps.net/item/"},
		{"https://deeps.net", "https://deeps.net/item/"},
		{"https://deeps.net/item/", "https://deeps.net/item/"},
		{"https://deeps.net/item/index.php?w_test", "https://deeps.net/item/index.php?w_test"},
	}
	for _, c := range cases {
		got := resolveListingURL(c.input)
		if got != c.want {
			t.Errorf("resolveListingURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---- TestExtractFilter ----

func TestExtractFilter(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://deeps.net/item/", "0_"},
		{"https://deeps.net/item", "0_"},
		{"https://deeps.net/item/index.php", "0_"},
		{"https://deeps.net/item/index.php?w_test", "w_test"},
		{"https://deeps.net/item/?s_series", "s_series"},
	}
	for _, c := range cases {
		got := extractFilter(c.input)
		if got != c.want {
			t.Errorf("extractFilter(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---- TestBuildPageURL ----

func TestBuildPageURL(t *testing.T) {
	cases := []struct {
		studioURL string
		page      int
		want      string
	}{
		{"https://deeps.net/item/", 1, "https://deeps.net/item/"},
		{"https://deeps.net/item/", 2, "https://deeps.net/item/index.php?0__50_2"},
		{"https://deeps.net/item/", 52, "https://deeps.net/item/index.php?0__50_52"},
		{"https://deeps.net/item/index.php?w_test", 1, "https://deeps.net/item/index.php?w_test"},
		{"https://deeps.net/item/index.php?w_test", 2, "https://deeps.net/item/index.php?w_test_50_2"},
	}
	for _, c := range cases {
		got := buildPageURL(c.studioURL, c.page)
		if got != c.want {
			t.Errorf("buildPageURL(%q, %d) = %q, want %q", c.studioURL, c.page, got, c.want)
		}
	}
}

// ---- TestBuildDetailURL ----

func TestBuildDetailURL(t *testing.T) {
	cases := []struct {
		studioURL string
		code      string
		want      string
	}{
		{"https://deeps.net/item/", "dvmm-382", "https://deeps.net/item/detail.php?dvmm-382"},
		{"http://localhost:12345/item/", "dvrt-073", "http://localhost:12345/item/detail.php?dvrt-073"},
	}
	for _, c := range cases {
		got := buildDetailURL(c.studioURL, c.code)
		if got != c.want {
			t.Errorf("buildDetailURL(%q, %q) = %q, want %q", c.studioURL, c.code, got, c.want)
		}
	}
}

// ---- TestThumbPath ----

func TestThumbPath(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"dvmm-382", "img/dvmm/dvmm-382.jpg"},
		{"dvrt-073", "img/dvrt/dvrt-073.jpg"},
		{"gdjp-009", "img/gdjp/gdjp-009.jpg"},
	}
	for _, c := range cases {
		got := thumbPath(c.code)
		if got != c.want {
			t.Errorf("thumbPath(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// ---- TestParseListingItems ----

func TestParseListingItems(t *testing.T) {
	items := []testItem{{"dvmm-382"}, {"dvrt-073"}, {"gdjp-009"}}
	body := []byte(listingPageHTML(items, 2615, 52))
	got := parseListingItems(body)

	if len(got) != 3 {
		t.Fatalf("parseListingItems returned %d items, want 3", len(got))
	}
	for i, want := range items {
		if got[i].code != want.code {
			t.Errorf("item[%d].code = %q, want %q", i, got[i].code, want.code)
		}
		if got[i].thumb != thumbPath(want.code) {
			t.Errorf("item[%d].thumb = %q, want %q", i, got[i].thumb, thumbPath(want.code))
		}
	}
}

func TestParseListingItemsDedup(t *testing.T) {
	items := []testItem{{"dvmm-382"}, {"dvmm-382"}}
	body := []byte(listingPageHTML(items, 100, 0))
	got := parseListingItems(body)
	if len(got) != 1 {
		t.Errorf("parseListingItems returned %d items, want 1 (dedup)", len(got))
	}
}

// ---- TestExtractTotal ----

func TestExtractTotal(t *testing.T) {
	body := []byte(listingPageHTML([]testItem{{"dvmm-382"}}, 2615, 52))
	got := extractTotal(body)
	if got != 2615 {
		t.Errorf("extractTotal = %d, want 2615", got)
	}
}

func TestExtractTotalFilter(t *testing.T) {
	body := []byte(filterListingPageHTML([]testItem{{"dvmm-382"}}, "葉山さゆり", 2, 0))
	got := extractTotal(body)
	if got != 2 {
		t.Errorf("extractTotal (filter) = %d, want 2", got)
	}
}

// ---- TestMaxNavPage ----

func TestMaxNavPage(t *testing.T) {
	body := []byte(listingPageHTML([]testItem{{"dvmm-382"}}, 2615, 52))
	got := maxNavPage(body)
	if got != 52 {
		t.Errorf("maxNavPage = %d, want 52", got)
	}
}

func TestMaxNavPageNone(t *testing.T) {
	body := []byte(listingPageHTML([]testItem{{"dvmm-382"}}, 10, 0))
	got := maxNavPage(body)
	if got != 0 {
		t.Errorf("maxNavPage = %d, want 0", got)
	}
}

// ---- TestParseDetail ----

func TestParseDetail(t *testing.T) {
	body := []byte(detailPageHTML(
		"dvmm-382",
		"テスト作品タイトル 葉山さゆり",
		"テスト作品の説明文です。",
		"2026.4.21",
		"125分",
		`<a href="index.php?d_ビバ☆ゴンゾ">ビバ☆ゴンゾ</a>`,
		"DVMM-382 / 3,180 円（税抜）",
		`<a href="index.php?w_葉山さゆり">葉山さゆり</a>`,
		`<a href="index.php?s_テストシリーズ">テストシリーズ</a>`,
		`<a href="index.php?c_中出し">中出し</a>　<a href="index.php?c_巨乳">巨乳</a>`,
	))

	item := listingItem{code: "dvmm-382", thumb: "img/dvmm/dvmm-382.jpg"}
	scene := parseDetail(body, "https://deeps.net/item/", item, "https://deeps.net/item/detail.php?dvmm-382")

	if scene.ID != "DVMM-382" {
		t.Errorf("ID = %q, want %q", scene.ID, "DVMM-382")
	}
	if scene.SiteID != "deeps" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "deeps")
	}
	if scene.Studio != "DEEP'S" {
		t.Errorf("Studio = %q, want %q", scene.Studio, "DEEP'S")
	}
	if scene.Title != "テスト作品タイトル 葉山さゆり" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Thumbnail != "https://deeps.net/item/img/dvmm/dvmm-382.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Description != "テスト作品の説明文です。" {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 4 || scene.Date.Day() != 21 {
		t.Errorf("Date = %v, want 2026-04-21", scene.Date)
	}
	if scene.Duration != 7500 {
		t.Errorf("Duration = %d, want 7500 (125*60)", scene.Duration)
	}
	if scene.Director != "ビバ☆ゴンゾ" {
		t.Errorf("Director = %q, want %q", scene.Director, "ビバ☆ゴンゾ")
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "葉山さゆり" {
		t.Errorf("Performers = %v, want [葉山さゆり]", scene.Performers)
	}
	if scene.Series != "テストシリーズ" {
		t.Errorf("Series = %q, want %q", scene.Series, "テストシリーズ")
	}
	if len(scene.Tags) != 2 || scene.Tags[0] != "中出し" || scene.Tags[1] != "巨乳" {
		t.Errorf("Tags = %v, want [中出し 巨乳]", scene.Tags)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 3180 {
		t.Errorf("PriceHistory = %v, want [{Regular:3180}]", scene.PriceHistory)
	}
}

func TestParseDetailNoOptionalFields(t *testing.T) {
	body := []byte(detailPageHTML(
		"dvmm-100",
		"Minimal Title",
		"",
		"2025.1.5",
		"90分",
		"",
		"DVMM-100",
		"",
		"",
		"",
	))

	item := listingItem{code: "dvmm-100", thumb: "img/dvmm/dvmm-100.jpg"}
	scene := parseDetail(body, "https://deeps.net/item/", item, "https://deeps.net/item/detail.php?dvmm-100")

	if scene.ID != "DVMM-100" {
		t.Errorf("ID = %q, want %q", scene.ID, "DVMM-100")
	}
	if scene.Title != "Minimal Title" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Director != "" {
		t.Errorf("Director should be empty, got %q", scene.Director)
	}
	if len(scene.Performers) != 0 {
		t.Errorf("Performers should be empty, got %v", scene.Performers)
	}
	if scene.Series != "" {
		t.Errorf("Series should be empty, got %q", scene.Series)
	}
	if len(scene.Tags) != 0 {
		t.Errorf("Tags should be empty, got %v", scene.Tags)
	}
	if len(scene.PriceHistory) != 0 {
		t.Errorf("PriceHistory should be empty, got %v", scene.PriceHistory)
	}
}

func TestParseDetailCoverFallback(t *testing.T) {
	body := []byte(`<html><body><h1>Title</h1></body></html>`)
	item := listingItem{code: "dvmm-100", thumb: "img/dvmm/dvmm-100.jpg"}
	scene := parseDetail(body, "https://deeps.net/item/", item, "https://deeps.net/item/detail.php?dvmm-100")

	if scene.Thumbnail != "https://deeps.net/item/img/dvmm/dvmm-100.jpg" {
		t.Errorf("Thumbnail fallback = %q, want listing thumb URL", scene.Thumbnail)
	}
}

// ---- TestListScenes ----

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/item/":
			items := []testItem{{"dvmm-382"}, {"dvrt-073"}}
			_, _ = fmt.Fprint(w, listingPageHTML(items, 2, 0))
		case "/item/detail.php":
			switch r.URL.RawQuery {
			case "dvmm-382":
				_, _ = fmt.Fprint(w, detailPageHTML("dvmm-382", "Title One", "Desc one", "2026.4.21", "125分",
					`<a href="index.php?d_Dir">Dir</a>`, "DVMM-382 / 3,180 円（税抜）",
					`<a href="index.php?w_Perf1">Perf1</a>`, "", `<a href="index.php?c_Tag1">Tag1</a>`))
			case "dvrt-073":
				_, _ = fmt.Fprint(w, detailPageHTML("dvrt-073", "Title Two", "Desc two", "2026.3.10", "100分",
					"", "DVRT-073 / 2,980 円（税抜）", "", "", ""))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/item/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	got := map[string]string{}
	for _, sc := range scenes {
		got[sc.ID] = sc.Title
	}
	if got["DVMM-382"] != "Title One" {
		t.Errorf("DVMM-382 title = %q", got["DVMM-382"])
	}
	if got["DVRT-073"] != "Title Two" {
		t.Errorf("DVRT-073 title = %q", got["DVRT-073"])
	}
}

// ---- TestListScenesKnownIDs ----

// Releases arrive in same-date batches ordered by product code, so a title
// added to a batch already half-stored sits behind a known sibling. The stop
// therefore waits for the end of the page: dvmm-384 must still be collected.
func TestListScenesKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/item/":
			items := []testItem{{"dvmm-382"}, {"dvmm-383"}, {"dvmm-384"}}
			_, _ = fmt.Fprint(w, listingPageHTML(items, 3, 0))
		case "/item/detail.php":
			switch r.URL.RawQuery {
			case "dvmm-382":
				_, _ = fmt.Fprint(w, detailPageHTML("dvmm-382", "Title One", "", "2026.4.21", "90分", "", "", "", "", ""))
			case "dvmm-384":
				_, _ = fmt.Fprint(w, detailPageHTML("dvmm-384", "Title Three", "", "2026.4.21", "90分", "", "", "", "", ""))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/item/", scraper.ListOpts{
		KnownIDs: map[string]bool{"DVMM-383": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes, stopped := testutil.CollectScenesWithStop(t, ch)

	if !stopped {
		t.Error("expected StoppedEarly signal")
	}
	ids := map[string]bool{}
	for _, sc := range scenes {
		ids[sc.ID] = true
	}
	if len(scenes) != 2 || !ids["DVMM-382"] || !ids["DVMM-384"] {
		t.Errorf("scene IDs = %v, want DVMM-382 and DVMM-384 — the page is finished before the stop", ids)
	}
}

// ---- TestListScenesPagination ----

func TestListScenesPagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/item/":
			items := []testItem{{"dvmm-382"}}
			_, _ = fmt.Fprint(w, listingPageHTML(items, 2, 2))
		case "/item/index.php":
			if r.URL.RawQuery == "0__50_2" {
				items := []testItem{{"dvrt-073"}}
				_, _ = fmt.Fprint(w, listingPageHTML(items, 2, 2))
			} else {
				_, _ = fmt.Fprint(w, listingPageHTML(nil, 0, 0))
			}
		case "/item/detail.php":
			switch r.URL.RawQuery {
			case "dvmm-382":
				_, _ = fmt.Fprint(w, detailPageHTML("dvmm-382", "Title One", "", "2026.4.21", "90分", "", "", "", "", ""))
			case "dvrt-073":
				_, _ = fmt.Fprint(w, detailPageHTML("dvrt-073", "Title Two", "", "2026.3.10", "100分", "", "", "", "", ""))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/item/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	got := map[string]bool{}
	for _, sc := range scenes {
		got[sc.ID] = true
	}
	if !got["DVMM-382"] || !got["DVRT-073"] {
		t.Errorf("missing expected scenes: got %v", got)
	}
}

// ---- TestListScenesBareDomain ----

func TestListScenesBareDomain(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/item/":
			items := []testItem{{"dvmm-382"}}
			_, _ = fmt.Fprint(w, listingPageHTML(items, 1, 0))
		case "/item/detail.php":
			_, _ = fmt.Fprint(w, detailPageHTML("dvmm-382", "Title", "", "2026.4.21", "90分", "", "", "", "", ""))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if scenes[0].ID != "DVMM-382" {
		t.Errorf("scene ID = %q, want %q", scenes[0].ID, "DVMM-382")
	}
}

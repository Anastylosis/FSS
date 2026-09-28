package kmproduce

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

func listingPageHTML(codes []string, totalCount int, hasNext bool) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><section id="list">`)
	fmt.Fprintf(&sb, `<div class="listbar col"><div class="total"><p>全  %d タイトル</p></div></div>`, totalCount)
	sb.WriteString(`<ul class="worklist">`)
	for _, code := range codes {
		fmt.Fprintf(&sb, `<li><article class="post">
<h3><a href="./works/%s">Test Title</a></h3>
<p class="jk"><a href="./works/%s"><img src="/img/title0/%s.jpg" alt="Full Title for %s"></a></p>
</article></li>`, code, code, code, code)
	}
	sb.WriteString(`</ul>`)
	if hasNext {
		sb.WriteString(`<div class="pager col"><div class="number"><a href="/page/999" class="next">&rsaquo;</a></div></div>`)
	}
	sb.WriteString(`</section></body></html>`)
	return sb.String()
}

func detailPageHTML(code, title, desc string, performers []string, director string, genres []string, durationMin int, date string, price int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><article id="single">`)
	fmt.Fprintf(&sb, `<div class="pagettl"><h1>%s</h1></div>`, title)
	fmt.Fprintf(&sb, `<p id="fulljk" class="fulljk"><a href="/img/title1/%s.jpg"><img src="/img/title1/%s.jpg"></a></p>`, code, code)
	fmt.Fprintf(&sb, `<div class="information"><p class="intro">%s</p></div>`, desc)

	sb.WriteString(`<dl class="first">`)
	if len(performers) > 0 {
		sb.WriteString(`<dt>出演女優</dt><dd class="act"><ul>`)
		for _, p := range performers {
			fmt.Fprintf(&sb, `<li><a href="/works/category/%s">%s</a></li>`, p, p)
		}
		sb.WriteString(`</ul></dd>`)
	}
	if director != "" {
		fmt.Fprintf(&sb, `<dt>監督</dt><dd><ul><li><a href="/?s=%s">%s</a></li></ul></dd>`, director, director)
	}
	if len(genres) > 0 {
		sb.WriteString(`<dt>ジャンル</dt><dd><ul>`)
		for _, g := range genres {
			fmt.Fprintf(&sb, `<li><a href="/works/tag/%s" rel="tag">%s</a></li>`, g, g)
		}
		sb.WriteString(`</ul></dd>`)
	}
	sb.WriteString(`</dl>`)

	sb.WriteString(`<dl class="second">`)
	fmt.Fprintf(&sb, `<dt>発売日</dt><dd>%s</dd>`, date)
	fmt.Fprintf(&sb, `<dt>品番</dt><dd>%s</dd>`, strings.ToUpper(code))
	fmt.Fprintf(&sb, `<dt>収録時間</dt><dd>%d分</dd>`, durationMin)
	fmt.Fprintf(&sb, `<dt>定価</dt><dd>%d円</dd>`, price)
	sb.WriteString(`</dl>`)

	sb.WriteString(`</article></body></html>`)
	return sb.String()
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://www.km-produce.com/", true},
		{"https://www.km-produce.com", true},
		{"https://km-produce.com/", true},
		{"https://www.km-produce.com/works", true},
		{"https://www.km-produce.com/works/", true},
		{"https://www.km-produce.com/works-vr", true},
		{"https://www.km-produce.com/works-vr/", true},
		{"https://www.km-produce.com/works-sell", true},
		{"https://www.km-produce.com/works-vr/page/3/", true},
		{"https://www.km-produce.com/works/tag/%e7%be%8e%e4%b9%b3", true},
		{"https://www.km-produce.com/works/category/%e9%80%a2%e6%b2%a2%e3%81%bf%e3%82%86", true},
		{"https://www.km-produce.com/nanase_alice", true},
		{"https://www.km-produce.com/nanase_alice/", true},
		{"https://www.km-produce.com/label?works=kmp-vr", true},
		// Detail pages should NOT match
		{"https://www.km-produce.com/works/vrkm-1828", false},
		{"https://www.km-produce.com/works/mkmp-728", false},
		// Other sites
		{"https://example.com/works-vr/", false},
		{"", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestNormalizeListURL ----

func TestNormalizeListURL(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://www.km-produce.com/works-vr", "https://www.km-produce.com/works-vr/"},
		{"https://www.km-produce.com/works-vr/", "https://www.km-produce.com/works-vr/"},
		{"https://www.km-produce.com/works-vr/page/3/", "https://www.km-produce.com/works-vr/"},
		{"https://www.km-produce.com/works/tag/%E7%BE%8E%E4%B9%B3/page/2/", "https://www.km-produce.com/works/tag/%E7%BE%8E%E4%B9%B3/"},
		{"https://www.km-produce.com/label/?works=kmp-vr", "https://www.km-produce.com/label/?works=kmp-vr"},
	}
	for _, c := range cases {
		got := normalizeListURL(c.input)
		if got != c.want {
			t.Errorf("normalizeListURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---- TestBuildPageURL ----

func TestBuildPageURL(t *testing.T) {
	cases := []struct {
		base string
		page int
		want string
	}{
		{"https://www.km-produce.com/works-vr/", 1, "https://www.km-produce.com/works-vr/"},
		{"https://www.km-produce.com/works-vr/", 2, "https://www.km-produce.com/works-vr/page/2/"},
		{"https://www.km-produce.com/works/tag/%E7%BE%8E%E4%B9%B3/", 3, "https://www.km-produce.com/works/tag/%E7%BE%8E%E4%B9%B3/page/3/"},
		{"https://www.km-produce.com/label/?works=kmp-vr", 2, "https://www.km-produce.com/label/page/2/?works=kmp-vr"},
	}
	for _, c := range cases {
		got := buildPageURL(c.base, c.page)
		if got != c.want {
			t.Errorf("buildPageURL(%q, %d) = %q, want %q", c.base, c.page, got, c.want)
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
		{"https://www.km-produce.com/works-vr/", "vrkm-1828", "https://www.km-produce.com/works/vrkm-1828"},
		{"https://www.km-produce.com/nanase_alice", "mkmp-728", "https://www.km-produce.com/works/mkmp-728"},
		{"http://localhost:12345/works-vr/", "vrkm-001", "http://localhost:12345/works/vrkm-001"},
	}
	for _, c := range cases {
		got := buildDetailURL(c.studioURL, c.code)
		if got != c.want {
			t.Errorf("buildDetailURL(%q, %q) = %q, want %q", c.studioURL, c.code, got, c.want)
		}
	}
}

// ---- TestResolveListingURLs ----

func TestResolveListingURLs(t *testing.T) {
	cases := []struct {
		input     string
		wantURLs  []string
		wantPaged bool
	}{
		{
			"https://www.km-produce.com/",
			[]string{"https://www.km-produce.com/works-vr/", "https://www.km-produce.com/works-sell/"},
			true,
		},
		{
			"https://www.km-produce.com",
			[]string{"https://www.km-produce.com/works-vr/", "https://www.km-produce.com/works-sell/"},
			true,
		},
		{
			"https://www.km-produce.com/works",
			[]string{"https://www.km-produce.com/works-vr/", "https://www.km-produce.com/works-sell/"},
			true,
		},
		{
			"https://www.km-produce.com/works-vr",
			[]string{"https://www.km-produce.com/works-vr/"},
			true,
		},
		{
			"https://www.km-produce.com/works/tag/%E7%BE%8E%E4%B9%B3",
			[]string{"https://www.km-produce.com/works/tag/%E7%BE%8E%E4%B9%B3/"},
			true,
		},
		{
			"https://www.km-produce.com/nanase_alice",
			[]string{"https://www.km-produce.com/nanase_alice"},
			false,
		},
		{
			"https://www.km-produce.com/label?works=kmp-vr",
			[]string{"https://www.km-produce.com/label/?works=kmp-vr"},
			true,
		},
	}
	for _, c := range cases {
		gotURLs, gotPaged := resolveListingURLs(c.input)
		if len(gotURLs) != len(c.wantURLs) {
			t.Errorf("resolveListingURLs(%q): got %d URLs, want %d", c.input, len(gotURLs), len(c.wantURLs))
			continue
		}
		for i, want := range c.wantURLs {
			if gotURLs[i] != want {
				t.Errorf("resolveListingURLs(%q)[%d] = %q, want %q", c.input, i, gotURLs[i], want)
			}
		}
		if gotPaged != c.wantPaged {
			t.Errorf("resolveListingURLs(%q) paginated = %v, want %v", c.input, gotPaged, c.wantPaged)
		}
	}
}

// ---- TestParseListingItems ----

func TestParseListingItems(t *testing.T) {
	body := []byte(listingPageHTML([]string{"vrkm-1828", "mkmp-728", "mdvr-357"}, 100, false))
	items := parseListingItems(body)
	if len(items) != 3 {
		t.Fatalf("parseListingItems returned %d items, want 3", len(items))
	}
	want := []string{"vrkm-1828", "mkmp-728", "mdvr-357"}
	for i, item := range items {
		if item.code != want[i] {
			t.Errorf("item[%d].code = %q, want %q", i, item.code, want[i])
		}
		if item.thumb != "/img/title0/"+want[i]+".jpg" {
			t.Errorf("item[%d].thumb = %q", i, item.thumb)
		}
	}
}

func TestParseListingItemsDedup(t *testing.T) {
	body := []byte(`<img src="/img/title0/vrkm-1828.jpg" alt="Test">
		<img src="/img/title0/vrkm-1828.jpg" alt="Test">`)
	items := parseListingItems(body)
	if len(items) != 1 {
		t.Errorf("parseListingItems returned %d items, want 1 (dedup)", len(items))
	}
}

func TestParseListingItemsEmpty(t *testing.T) {
	body := []byte(`<html><body><ul class="worklist"></ul></body></html>`)
	items := parseListingItems(body)
	if len(items) != 0 {
		t.Errorf("parseListingItems returned %d items, want 0", len(items))
	}
}

// ---- TestExtractTotal ----

func TestExtractTotal(t *testing.T) {
	body := []byte(listingPageHTML([]string{"vrkm-1828"}, 4222, false))
	got := extractTotal(body)
	if got != 4222 {
		t.Errorf("extractTotal = %d, want 4222", got)
	}
}

func TestExtractTotalNotPresent(t *testing.T) {
	body := []byte(`<html><body>no total here</body></html>`)
	got := extractTotal(body)
	if got != 0 {
		t.Errorf("extractTotal = %d, want 0", got)
	}
}

// ---- TestHasNextPage ----

func TestHasNextPage(t *testing.T) {
	withNext := []byte(listingPageHTML([]string{"vrkm-1828"}, 100, true))
	if !hasNextPage(withNext) {
		t.Error("hasNextPage should be true when next link is present")
	}

	withoutNext := []byte(listingPageHTML([]string{"vrkm-1828"}, 100, false))
	if hasNextPage(withoutNext) {
		t.Error("hasNextPage should be false when no next link")
	}
}

// ---- TestParseDetail ----

func TestParseDetail(t *testing.T) {
	body := []byte(detailPageHTML(
		"vrkm-1828",
		"スローセックスＶＲ",
		"Test description text.",
		[]string{"逢沢みゆ", "橋本れいか"},
		"宮迫メンバー",
		[]string{"8K", "中出し", "主観"},
		83,
		"2026/6/20",
		1780,
	))

	item := listingItem{code: "vrkm-1828", thumb: "/img/title0/vrkm-1828.jpg"}
	scene := parseDetail(body, "https://www.km-produce.com/works-vr/", item, "https://www.km-produce.com/works/vrkm-1828")

	if scene.ID != "vrkm-1828" {
		t.Errorf("ID = %q, want %q", scene.ID, "vrkm-1828")
	}
	if scene.SiteID != "kmproduce" {
		t.Errorf("SiteID = %q, want %q", scene.SiteID, "kmproduce")
	}
	if scene.Title != "スローセックスＶＲ" {
		t.Errorf("Title = %q, want %q", scene.Title, "スローセックスＶＲ")
	}
	if scene.Thumbnail != "https://www.km-produce.com/img/title1/vrkm-1828.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Description != "Test description text." {
		t.Errorf("Description = %q, want %q", scene.Description, "Test description text.")
	}
	if len(scene.Performers) != 2 {
		t.Fatalf("Performers = %v, want 2", scene.Performers)
	}
	if scene.Performers[0] != "逢沢みゆ" || scene.Performers[1] != "橋本れいか" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Director != "宮迫メンバー" {
		t.Errorf("Director = %q, want %q", scene.Director, "宮迫メンバー")
	}
	if len(scene.Tags) != 3 {
		t.Fatalf("Tags = %v, want 3", scene.Tags)
	}
	if scene.Tags[0] != "8K" || scene.Tags[1] != "中出し" || scene.Tags[2] != "主観" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Duration != 4980 {
		t.Errorf("Duration = %d, want 4980 (83min)", scene.Duration)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 6 || scene.Date.Day() != 20 {
		t.Errorf("Date = %v, want 2026-06-20", scene.Date)
	}
	if scene.Studio != "KM Produce" {
		t.Errorf("Studio = %q, want %q", scene.Studio, "KM Produce")
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 1780 {
		t.Errorf("PriceHistory = %v", scene.PriceHistory)
	}
}

func TestParseDetailFallbackThumbnail(t *testing.T) {
	body := []byte(`<html><body><article id="single">
		<div class="pagettl"><h1>Test</h1></div>
		<dl class="second"><dt>発売日</dt><dd>2026/1/1</dd></dl>
		</article></body></html>`)

	item := listingItem{code: "vrkm-001", thumb: "/img/title0/vrkm-001.jpg"}
	scene := parseDetail(body, "https://www.km-produce.com/works-vr/", item, "https://www.km-produce.com/works/vrkm-001")

	if scene.Thumbnail != "https://www.km-produce.com/img/title0/vrkm-001.jpg" {
		t.Errorf("Thumbnail fallback = %q, want listing thumb", scene.Thumbnail)
	}
}

// ---- TestListScenes ----

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works-vr/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"vrkm-1828", "mdvr-357"}, 2, false))
		case "/works/vrkm-1828":
			_, _ = fmt.Fprint(w, detailPageHTML("vrkm-1828", "Title One", "Desc one", []string{"Actress A"}, "Dir A", []string{"Tag1"}, 83, "2026/6/20", 1780))
		case "/works/mdvr-357":
			_, _ = fmt.Fprint(w, detailPageHTML("mdvr-357", "Title Two", "Desc two", []string{"Actress B"}, "Dir B", []string{"Tag2"}, 120, "2026/5/15", 2480))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/works-vr/", scraper.ListOpts{})
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
	if got["vrkm-1828"] != "Title One" {
		t.Errorf("vrkm-1828 title = %q, want %q", got["vrkm-1828"], "Title One")
	}
	if got["mdvr-357"] != "Title Two" {
		t.Errorf("mdvr-357 title = %q, want %q", got["mdvr-357"], "Title Two")
	}
}

// ---- TestListScenesKnownIDs ----

func TestListScenesKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works-vr/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"vrkm-1828", "mdvr-357", "vrkm-1827"}, 3, false))
		case "/works/vrkm-1828":
			_, _ = fmt.Fprint(w, detailPageHTML("vrkm-1828", "Title One", "", []string{"A"}, "", nil, 60, "2026/1/1", 1000))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/works-vr/", scraper.ListOpts{
		KnownIDs: map[string]bool{"mdvr-357": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes, stopped := testutil.CollectScenesWithStop(t, ch)

	if !stopped {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
	if len(scenes) > 0 && scenes[0].ID != "vrkm-1828" {
		t.Errorf("scene ID = %q, want %q", scenes[0].ID, "vrkm-1828")
	}
}

// ---- TestListScenesPagination ----

func TestListScenesPagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works-vr/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"vrkm-1828"}, 2, true))
		case "/works-vr/page/2/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"mdvr-357"}, 2, false))
		case "/works/vrkm-1828":
			_, _ = fmt.Fprint(w, detailPageHTML("vrkm-1828", "Title One", "", []string{"A"}, "", nil, 60, "2026/1/1", 1000))
		case "/works/mdvr-357":
			_, _ = fmt.Fprint(w, detailPageHTML("mdvr-357", "Title Two", "", []string{"B"}, "", nil, 90, "2026/2/1", 2000))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/works-vr/", scraper.ListOpts{})
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
	if !got["vrkm-1828"] || !got["mdvr-357"] {
		t.Errorf("missing expected scenes: got %v", got)
	}
}

// ---- TestListScenesMainPage ----

func TestListScenesMainPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works-vr/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"vrkm-1828"}, 1, false))
		case "/works-sell/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"mkmp-728"}, 1, false))
		case "/works/vrkm-1828":
			_, _ = fmt.Fprint(w, detailPageHTML("vrkm-1828", "VR Title", "", []string{"A"}, "", nil, 60, "2026/1/1", 1000))
		case "/works/mkmp-728":
			_, _ = fmt.Fprint(w, detailPageHTML("mkmp-728", "DVD Title", "", []string{"B"}, "", nil, 120, "2026/2/1", 2000))
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

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	got := map[string]bool{}
	for _, sc := range scenes {
		got[sc.ID] = true
	}
	if !got["vrkm-1828"] || !got["mkmp-728"] {
		t.Errorf("missing expected scenes from VR+sell: got %v", got)
	}
}

// Scraping the bare host walks /works-vr/ and /works-sell/ in turn. The two are
// separate catalogues on disjoint label prefixes (savr-* vs mkmp-/nask-/…), each
// strictly date-descending, so an early stop on one must end that list only —
// not the walk.
func TestListScenesMainPageStopsPerList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works-vr/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"savr-1132", "savr-1100"}, 2, false))
		case "/works-sell/":
			_, _ = fmt.Fprint(w, listingPageHTML([]string{"mkmp-728"}, 1, false))
		case "/works/savr-1132":
			_, _ = fmt.Fprint(w, detailPageHTML("savr-1132", "VR Title", "", []string{"A"}, "", nil, 60, "2026/1/1", 1000))
		case "/works/mkmp-728":
			_, _ = fmt.Fprint(w, detailPageHTML("mkmp-728", "DVD Title", "", []string{"B"}, "", nil, 120, "2026/2/1", 2000))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"savr-1100": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes, stopped := testutil.CollectScenesWithStop(t, ch)

	if !stopped {
		t.Error("expected StoppedEarly from the VR list")
	}
	got := map[string]bool{}
	for _, sc := range scenes {
		got[sc.ID] = true
	}
	if !got["savr-1132"] || !got["mkmp-728"] || len(got) != 2 {
		t.Errorf("scenes = %v, want savr-1132 plus the whole sell list", got)
	}
}

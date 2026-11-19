package next11

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
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
		{"https://next11.co.jp/", true},
		{"https://www.next11.co.jp/products/list.php", true},
		{"https://next11.co.jp/products/detail.php?product_id=123", true},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseListingPage(t *testing.T) {
	body := []byte(`<p class="pagenumber_count"><span class="pagenumber">6,788</span>件の商品がございます。</p>
<ul class="listarea">
<li>
<div class="listphoto"><a href="products/detail.php?product_id=100"><img src="x.jpg"></a></div>
<div class="listrightblock">
<dl class="listproducts">
<div><dt>商品：</dt><dd>ABC-001</dd></div>
</dl>
</div>
</li>
<li>
<div class="listphoto"><a href="products/detail.php?product_id=200"><img src="y.jpg"></a></div>
<div class="listrightblock">
<dl class="listproducts">
<div><dt>女優：</dt><dd><a href="products/list.php?category_id=1">Someone</a></dd></div>
<div><dt>商品：</dt><dd>DEF-002</dd></div>
</dl>
</div>
</li>`)

	items, total := parseListingPage(body)
	if total != 6788 {
		t.Errorf("total = %d, want 6788 (thousands separator)", total)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].productID != "100" || items[0].code != "ABC-001" {
		t.Errorf("items[0] = %+v", items[0])
	}
	if items[1].productID != "200" || items[1].code != "DEF-002" {
		t.Errorf("items[1] = %+v", items[1])
	}
}

func TestParseLiveListing(t *testing.T) {
	b, err := os.ReadFile("testdata/listing.html")
	if err != nil {
		t.Fatal(err)
	}
	items, total := parseListingPage(b)
	if total != 6788 || len(items) != 3 || items[0].productID != "13827" || items[0].code != "COU-024" {
		t.Errorf("total = %d, items = %+v", total, items)
	}
}

func TestParseLiveDetail(t *testing.T) {
	b, err := os.ReadFile("testdata/detail.html")
	if err != nil {
		t.Fatal(err)
	}
	d := parseDetailPage(b)
	if d.code != "EMBM-056" || !strings.HasPrefix(d.title, "ド素人応募妻") || strings.HasPrefix(d.title, "[") {
		t.Errorf("code = %q, title = %q", d.code, d.title)
	}
	if d.date.Format("2006-01-02") != "2026-10-20" || d.duration != 126*60 {
		t.Errorf("date = %v, duration = %d", d.date, d.duration)
	}
	if len(d.performers) != 1 || d.performers[0] != "葉月英里" || len(d.tags) != 7 || d.label != "エンブレム" || d.series != "ド素人応募妻" {
		t.Errorf("detail = %+v", d)
	}
	if !strings.HasPrefix(d.description, "非日常体験に興奮する") {
		t.Errorf("description = %q (comes from the JSON-LD Movie block)", d.description)
	}
	if d.thumbnail != "upload/save_image/jacket/EMBM-056.jpg" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
}

const detailHTML = `<script type="application/ld+json">{"@type":"Movie","description":"A synopsis."}</script>
<div class="detail1"><a href="upload/save_image/jacket/DOKI-033.jpg"><img src="x"></a></div>
<dl class="detail2">
<dt>タイトル</dt><dd>Test Title</dd>
<dt>出演</dt><dd><a href="products/list.php?category_id=4504">Performer One</a><a href="products/list.php?category_id=4615">Performer Two</a></dd>
<dt>ジャンル</dt><dd><a href="products/list.php?category_id=14">Genre1</a><a href="products/list.php?category_id=53">Genre2</a></dd>
<dt>レーベル</dt><dd><a href="products/list.php?category_id=4357">DOKI!</a></dd>
<dt>シリーズ</dt><dd><a href="products/list.php?category_id=1234">Test Series</a></dd>
<dt>収録時間</dt><dd>130分</dd>
<dt>発売日</dt><dd><time datetime="2026-03-20">2026-03-20</time></dd>
<dt>品番</dt><dd>DOKI-033</dd>
</dl>`

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(detailHTML))

	if d.title != "Test Title" {
		t.Errorf("title = %q", d.title)
	}
	if d.code != "DOKI-033" {
		t.Errorf("code = %q", d.code)
	}
	if d.description != "A synopsis." {
		t.Errorf("description = %q", d.description)
	}
	if d.date.Format("2006-01-02") != "2026-03-20" {
		t.Errorf("date = %v", d.date)
	}
	if d.duration != 7800 {
		t.Errorf("duration = %d, want 7800", d.duration)
	}
	if len(d.performers) != 2 || d.performers[0] != "Performer One" || d.performers[1] != "Performer Two" {
		t.Errorf("performers = %v", d.performers)
	}
	if len(d.tags) != 2 || d.tags[0] != "Genre1" || d.tags[1] != "Genre2" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.label != "DOKI!" {
		t.Errorf("label = %q", d.label)
	}
	if d.series != "Test Series" {
		t.Errorf("series = %q", d.series)
	}
	if d.thumbnail != "upload/save_image/jacket/DOKI-033.jpg" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
}

func TestParseDetailPageMissingFields(t *testing.T) {
	body := []byte(`<dt>タイトル</dt><dd>Title Only</dd>`)
	d := parseDetailPage(body)
	if d.title != "Title Only" {
		t.Errorf("title = %q", d.title)
	}
	if !d.date.IsZero() {
		t.Errorf("expected zero date, got %v", d.date)
	}
	if len(d.performers) != 0 {
		t.Errorf("expected no performers, got %v", d.performers)
	}
}

// newTestServer serves the product listing, paginating `pages` by the `pageno`
// query parameter the way the rebuilt site does. A POSTed pageno is ignored
// and answers page one, as on the real site, so a regression to the old POST
// walk would return duplicates and fail.
func newTestServer(pages [][]listingItem) *httptest.Server {
	total := 0
	for _, p := range pages {
		total += len(p)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		switch r.URL.Path {
		case "/products/list.php":
			page := 1
			if r.Method == http.MethodGet {
				page, _ = strconv.Atoi(r.URL.Query().Get("pageno"))
				if page < 1 {
					page = 1
				}
			}
			var items []listingItem
			if page <= len(pages) {
				items = pages[page-1]
			}
			var listing string
			for _, item := range items {
				listing += fmt.Sprintf(`<li>
<div class="listphoto"><a href="products/detail.php?product_id=%s"><img/></a></div>
<div class="listrightblock"><dl><div><dt>商品：</dt><dd>%s</dd></div></dl></div>
</li>`, item.productID, item.code)
			}
			_, _ = fmt.Fprintf(w, `<p><span class="pagenumber">%d</span>件</p>%s`, total, listing)
		default:
			// Serve a detail page whose product code matches the requested
			// product. A single shared fixture would give every scene the same
			// ID, hiding the fact that the ID comes from the detail page.
			body := detailHTML
			pid := r.URL.Query().Get("product_id")
			for _, page := range pages {
				for _, item := range page {
					if item.productID == pid {
						body = strings.ReplaceAll(detailHTML, "DOKI-033", item.code)
					}
				}
			}
			_, _ = fmt.Fprint(w, body)
		}
	}))
}

func TestRun(t *testing.T) {
	items := []listingItem{
		{productID: "100", code: "ABC-001"},
		{productID: "200", code: "DEF-002"},
	}
	ts := newTestServer([][]listingItem{items})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	// The ID comes from the detail page's productID, so each product must
	// yield its own — a shared detail fixture previously gave both the same.
	gotIDs := map[string]bool{scenes[0].ID: true, scenes[1].ID: true}
	if !gotIDs["ABC-001"] || !gotIDs["DEF-002"] {
		t.Errorf("scene IDs = %q/%q, want ABC-001 and DEF-002", scenes[0].ID, scenes[1].ID)
	}

	for _, sc := range scenes {
		if sc.SiteID != siteID {
			t.Errorf("SiteID = %q", sc.SiteID)
		}
		if sc.Studio != studioName {
			t.Errorf("Studio = %q", sc.Studio)
		}
		if sc.Title == "" {
			t.Error("empty title")
		}
	}
}

func TestRunKnownIDs(t *testing.T) {
	items := []listingItem{
		{productID: "100", code: "ABC-001"},
		{productID: "200", code: "DEF-002"},
		{productID: "300", code: "GHI-003"},
	}
	ts := newTestServer([][]listingItem{items})
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"DEF-002": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
}

// The regression test for the pagination bug: the listing paginates by POSTed
// form fields, not by `?pageno=`. Before the fix the scraper GET-walked every
// page and the site returned page one each time, so a --full run emitted the
// same products repeatedly — they collapsed at Save (keyed on id, site_id) and
// the store kept only the first page.
func TestRunWalksAllPages(t *testing.T) {
	// The page count comes from the site's reported total divided by pageSize,
	// so a multi-page fixture needs a full first page — anything smaller is
	// legitimately one page and would prove nothing.
	mkPage := func(prefix string, base, n int) []listingItem {
		out := make([]listingItem, n)
		for i := range out {
			// productID must be numeric — pidRe is product_id=(\d+).
			out[i] = listingItem{
				productID: fmt.Sprintf("%d%03d", base, i),
				code:      fmt.Sprintf("%s-%03d", prefix, i),
			}
		}
		return out
	}
	pages := [][]listingItem{mkPage("AAA", 1, pageSize), mkPage("BBB", 2, 3)}
	ts := newTestServer(pages)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)
	// CollectScenes also fails on duplicate IDs, so a scraper that re-read page
	// one would trip that as well as the count.
	want := pageSize + 3
	if len(scenes) != want {
		t.Fatalf("got %d scenes, want %d across 2 pages", len(scenes), want)
	}
	got := map[string]bool{}
	for _, sc := range scenes {
		got[sc.ID] = true
	}
	// One from each page: page 2 is only reached if pagination works.
	for _, id := range []string{"AAA-000", "BBB-000", "BBB-002"} {
		if !got[id] {
			t.Errorf("scene %q missing — the page walk did not reach it", id)
		}
	}
}

// The listing is paged by GET `pageno`; a POST is ignored by the site.
func TestListingIsFetchedByGet(t *testing.T) {
	var queries []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/products/list.php" {
			queries = append(queries, r.Method+" "+r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<span class="pagenumber">0</span>件`)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	if len(queries) != 1 || queries[0] != "GET pageno=1" {
		t.Errorf("listing requests = %v, want one GET with pageno=1", queries)
	}
}

// An empty first page means the template changed, which must not read as an
// empty catalogue.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	ts := newTestServer(nil)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs = append(errs, r.Err)
		}
	}
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse {
		t.Errorf("errors = %v, want one parse failure", errs)
	}
}

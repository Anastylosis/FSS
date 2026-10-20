package uptimelyutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// ---- fixtures ----

func listingPageHTML(items []listingItem, total int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	if total > 0 {
		fmt.Fprintf(&sb, `<div class="swiper-pagination-02">全%d作品中 1 〜 %d タイトルを表示</div>`, total, len(items))
	}
	for _, item := range items {
		fmt.Fprintf(&sb, `<div class="item"><div class="c-card">`+
			`<a class="img hover" href="https://example.com/works/detail/%s?page_from=series">`+
			`<img class="c-main-bg lazyload" data-src="%s" alt=""/>`+
			`<div class="hover__child"><p class="text">Title for %s</p></div>`+
			`</a></div></div>`, item.code, item.thumb, item.code)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func actressListingPageHTML(items []listingItem, total int) string {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	if total > 0 {
		fmt.Fprintf(&sb, `<div class="swiper-pagination-02">全%d作品中 1 〜 %d タイトルを表示</div>`, total, len(items))
	}
	for _, item := range items {
		fmt.Fprintf(&sb, `<a class="item" href="https://example.com/works/detail/%s?page_from=actress">`+
			`<div class="c-card"><div class="img hover">`+
			`<img class="c-main-bg lazyload" data-src="%s" alt="" />`+
			`<div class="hover__child"><p class="text">Title for %s</p></div>`+
			`</div></div></a>`, item.code, item.thumb, item.code)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

func detailPageHTML(code, title, desc string, performers []string, director string, genres []string, series string, durationMin int, date string) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><div class="p-workPage l-wrap">`)
	fmt.Fprintf(&sb, `<h2 class="p-workPage__title">  %s  </h2>`, title)
	fmt.Fprintf(&sb, `<p class="p-workPage__text">%s</p>`, desc)
	sb.WriteString(`<div class="p-workPage__block"><div class="p-workPage__table">`)

	sb.WriteString(`<div class="item"><div class="th">女優</div><div class="td">`)
	for _, p := range performers {
		fmt.Fprintf(&sb, `<div class="item"><a class="c-tag c-main-bg-hover c-main-font c-main-bd" href="https://example.com/actress/detail/123">%s</a></div>`, p)
	}
	sb.WriteString(`</div></div>`)

	fmt.Fprintf(&sb, `<div class="item"><div class="th">発売日</div><div class="td"><div class="item">`+
		`<a class="c-tag c-main-bg-hover c-main-font c-main-bd" href="https://example.com/works/list/date/%s">%s</a>`+
		`</div></div></div>`, date, date)

	if series != "" {
		fmt.Fprintf(&sb, `<div class="item"><div class="th">シリーズ</div><div class="item">`+
			`<a class="c-tag c-main-bg-hover c-main-font c-main-bd" href="https://example.com/works/list/series/1">%s</a>`+
			`</div><div class="td"></div></div>`, series)
	}

	if len(genres) > 0 {
		sb.WriteString(`<div class="item"><div class="th">ジャンル</div><div class="td">`)
		for _, g := range genres {
			fmt.Fprintf(&sb, `<div class="item"><a class="c-tag c-main-bg-hover c-main-font c-main-bd" href="https://example.com/works/list/genre/1">%s</a></div>`, g)
		}
		sb.WriteString(`</div></div>`)
	}

	if director != "" {
		fmt.Fprintf(&sb, `<div class="item"><div class="th">監督</div><div class="td"><div class="item"><p>%s</p></div></div></div>`, director)
	}

	fmt.Fprintf(&sb, `<div class="item"><div class="th">品番</div><div class="td"><div class="item -minW">`+
		`<p><span class="c-tag02 c-main-bg-hover c-main-bg">DVD</span>%s</p></div></div></div>`, code)
	fmt.Fprintf(&sb, `<div class="item"><div class="th">収録時間</div><div class="td"><div class="item -minW">`+
		`<p><span class="c-tag02 c-main-bg-hover c-main-bg">DVD</span>%d分</p></div></div></div>`, durationMin)

	sb.WriteString(`</div><div class="p-workPage__side"></div></div></div></body></html>`)
	return sb.String()
}

var testCfg = SiteConfig{
	ID:      "testsite",
	Studio:  "TESTSITE",
	Domain:  "example.com",
	MatchRe: regexp.MustCompile(`^https?://(?:www\.)?example\.com/(?:works/list/|actress/detail/)`),
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		ID:      "testsite",
		MatchRe: regexp.MustCompile(`^https?://(?:www\.)?example\.com/(?:works/list/|actress/detail/)`),
	})
	cases := []struct {
		url   string
		match bool
	}{
		{"https://example.com/works/list/series/3482", true},
		{"https://example.com/works/list/release", true},
		{"https://example.com/works/list/date/2026-04-21", true},
		{"https://example.com/works/list/genre/126", true},
		{"https://example.com/works/list/label/5046", true},
		{"https://example.com/actress/detail/701326", true},
		{"https://www.example.com/works/list/series/3482", true},
		{"https://example.com/works/detail/MIAD491", false},
		{"https://example.com/", false},
		{"https://other.com/works/list/series/1", false},
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
		{"https://example.com/actress/detail/700115", "https://example.com/actress/detail/700115"},
		{"https://example.com/actress/detail/700115?page=3", "https://example.com/actress/detail/700115"},
		{"https://example.com/works/list/series/3482?page=2", "https://example.com/works/list/series/3482"},
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
		{"https://example.com/actress/detail/700115", 1, "https://example.com/actress/detail/700115"},
		{"https://example.com/actress/detail/700115", 2, "https://example.com/actress/detail/700115?page=2"},
		{"https://example.com/works/list/series/3482", 3, "https://example.com/works/list/series/3482?page=3"},
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
		domain    string
		want      string
	}{
		{"https://example.com/actress/detail/700115", "MIAD491", "example.com", "https://example.com/works/detail/MIAD491"},
		{"https://example.com/works/list/series/3482", "MDVR418", "example.com", "https://example.com/works/detail/MDVR418"},
		{"http://localhost:12345/works/list/release", "TEST001", "example.com", "http://localhost:12345/works/detail/TEST001"},
	}
	for _, c := range cases {
		got := buildDetailURL(c.studioURL, c.code, c.domain)
		if got != c.want {
			t.Errorf("buildDetailURL(%q, %q, %q) = %q, want %q", c.studioURL, c.code, c.domain, got, c.want)
		}
	}
}

// ---- TestParseListingItems ----

func TestParseListingItems(t *testing.T) {
	items := []listingItem{
		{code: "MIAD491", thumb: "https://cdn.example.com/MIAD491_1.jpg"},
		{code: "MIAD469", thumb: "https://cdn.example.com/MIAD469_1.jpg"},
	}
	body := []byte(listingPageHTML(items, 11))
	got := parseListingItems(body)
	if len(got) != 2 {
		t.Fatalf("parseListingItems returned %d items, want 2", len(got))
	}
	if got[0].code != "MIAD491" || got[1].code != "MIAD469" {
		t.Errorf("codes = [%s, %s], want [MIAD491, MIAD469]", got[0].code, got[1].code)
	}
	if got[0].thumb != "https://cdn.example.com/MIAD491_1.jpg" {
		t.Errorf("thumb = %q", got[0].thumb)
	}
}

func TestParseListingItemsActressFormat(t *testing.T) {
	items := []listingItem{
		{code: "MIBD804", thumb: "https://cdn.example.com/MIBD804_1.jpg"},
		{code: "MIBD749", thumb: "https://cdn.example.com/MIBD749_1.jpg"},
	}
	body := []byte(actressListingPageHTML(items, 3))
	got := parseListingItems(body)
	if len(got) != 2 {
		t.Fatalf("parseListingItems returned %d items, want 2", len(got))
	}
	if got[0].code != "MIBD804" || got[1].code != "MIBD749" {
		t.Errorf("codes = [%s, %s]", got[0].code, got[1].code)
	}
}

func TestParseListingItemsDedup(t *testing.T) {
	body := []byte(`<a href="/works/detail/MIAD491"><img data-src="a.jpg"/></a>` +
		`<a href="/works/detail/MIAD491"><img data-src="b.jpg"/></a>`)
	got := parseListingItems(body)
	if len(got) != 1 {
		t.Errorf("parseListingItems returned %d items, want 1 (dedup)", len(got))
	}
}

// ---- TestExtractTotal ----

func TestExtractTotal(t *testing.T) {
	body := []byte(`<div class="swiper-pagination-02">全152作品中 1 〜 12 タイトルを表示</div>`)
	got := extractTotal(body)
	if got != 152 {
		t.Errorf("extractTotal = %d, want 152", got)
	}
}

func TestExtractTotalNone(t *testing.T) {
	got := extractTotal([]byte(`<html><body>no total here</body></html>`))
	if got != 0 {
		t.Errorf("extractTotal = %d, want 0", got)
	}
}

// ---- TestParseDetail ----

func TestParseDetail(t *testing.T) {
	body := []byte(detailPageHTML(
		"MIAD491",
		"超絶品ボディ",
		"JULIAの肉体の素晴らしさ",
		[]string{"JULIA"},
		"[Jo]Style",
		[]string{"パイズリ", "潮吹き", "巨乳"},
		"超絶品ボディ",
		120,
		"2011-01-13",
	))

	item := listingItem{code: "MIAD491", thumb: "https://cdn.example.com/MIAD491_1.jpg"}
	scene := parseDetail(body, "testsite", "TESTSITE", "https://example.com/works/list/series/3482", item, "https://example.com/works/detail/MIAD491")

	if scene.ID != "MIAD491" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "testsite" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Title != "超絶品ボディ" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Description != "JULIAの肉体の素晴らしさ" {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Thumbnail != "https://cdn.example.com/MIAD491_1.jpg" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "JULIA" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Director != "[Jo]Style" {
		t.Errorf("Director = %q", scene.Director)
	}
	if len(scene.Tags) != 3 {
		t.Fatalf("Tags = %v, want 3", scene.Tags)
	}
	if scene.Series != "超絶品ボディ" {
		t.Errorf("Series = %q", scene.Series)
	}
	if scene.Duration != 7200 {
		t.Errorf("Duration = %d, want 7200", scene.Duration)
	}
	if scene.Date.Year() != 2011 || scene.Date.Month() != 1 || scene.Date.Day() != 13 {
		t.Errorf("Date = %v", scene.Date)
	}
	if scene.Studio != "TESTSITE" {
		t.Errorf("Studio = %q", scene.Studio)
	}
}

// ---- TestListScenes ----

func TestListScenes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works/list/series/100":
			if r.URL.Query().Get("page") != "" {
				_, _ = fmt.Fprint(w, listingPageHTML(nil, 0))
				return
			}
			items := []listingItem{
				{code: "MIAD491", thumb: "/img/MIAD491_1.jpg"},
				{code: "MIAD469", thumb: "/img/MIAD469_1.jpg"},
			}
			_, _ = fmt.Fprint(w, listingPageHTML(items, 2))
		case "/works/detail/MIAD491":
			_, _ = fmt.Fprint(w, detailPageHTML("MIAD491", "Title One", "Desc one", []string{"JULIA"}, "Dir A", []string{"Tag1"}, "Series1", 120, "2011-01-13"))
		case "/works/detail/MIAD469":
			_, _ = fmt.Fprint(w, detailPageHTML("MIAD469", "Title Two", "Desc two", []string{"Actress B"}, "Dir B", []string{"Tag2"}, "", 90, "2010-12-01"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{cfg: testCfg, Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/works/list/series/100", scraper.ListOpts{})
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
	if got["MIAD491"] != "Title One" {
		t.Errorf("MIAD491 title = %q", got["MIAD491"])
	}
	if got["MIAD469"] != "Title Two" {
		t.Errorf("MIAD469 title = %q", got["MIAD469"])
	}
}

// ---- TestListScenesKnownIDs ----

func TestListScenesKnownIDs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actress/detail/100":
			if r.URL.Query().Get("page") != "" {
				_, _ = fmt.Fprint(w, actressListingPageHTML(nil, 0))
				return
			}
			items := []listingItem{
				{code: "MIAD491", thumb: "/img/a.jpg"},
				{code: "MIAD469", thumb: "/img/b.jpg"},
				{code: "MIDD633", thumb: "/img/c.jpg"},
			}
			_, _ = fmt.Fprint(w, actressListingPageHTML(items, 3))
		case "/works/detail/MIAD491":
			_, _ = fmt.Fprint(w, detailPageHTML("MIAD491", "Title One", "", []string{"A"}, "", nil, "", 60, "2011-01-01"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{cfg: testCfg, Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/actress/detail/100", scraper.ListOpts{
		KnownIDs: map[string]bool{"MIAD469": true},
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
	if len(scenes) > 0 && scenes[0].ID != "MIAD491" {
		t.Errorf("scene ID = %q, want MIAD491", scenes[0].ID)
	}
}

// ---- TestListScenesPagination ----

func TestListScenesPagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actress/detail/100":
			page := r.URL.Query().Get("page")
			switch page {
			case "", "1":
				_, _ = fmt.Fprint(w, actressListingPageHTML([]listingItem{
					{code: "MIAD491", thumb: "/img/a.jpg"},
				}, 2))
			case "2":
				_, _ = fmt.Fprint(w, actressListingPageHTML([]listingItem{
					{code: "MIAD469", thumb: "/img/b.jpg"},
				}, 2))
			default:
				_, _ = fmt.Fprint(w, actressListingPageHTML(nil, 0))
			}
		case "/works/detail/MIAD491":
			_, _ = fmt.Fprint(w, detailPageHTML("MIAD491", "T1", "", []string{"A"}, "", nil, "", 60, "2011-01-01"))
		case "/works/detail/MIAD469":
			_, _ = fmt.Fprint(w, detailPageHTML("MIAD469", "T2", "", []string{"B"}, "", nil, "", 90, "2010-12-01"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := &Scraper{cfg: testCfg, Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/actress/detail/100", scraper.ListOpts{})
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
	if !got["MIAD491"] || !got["MIAD469"] {
		t.Errorf("missing expected scenes: got %v", got)
	}
}

// ---- catalogue mode ----

// hitCounter records request paths. The detail pool runs several goroutines
// against the test server at once, so the map needs a lock of its own.
type hitCounter struct {
	mu sync.Mutex
	n  map[string]int
}

func (h *hitCounter) add(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n[path]++
}

func (h *hitCounter) get(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n[path]
}

func genreIndexHTML(ids ...string) string {
	var sb strings.Builder
	sb.WriteString(`<html><body><ul class="p-genreList">`)
	for _, id := range ids {
		fmt.Fprintf(&sb, `<li><a href="https://example.com/works/list/genre/%s">Genre %s</a></li>`, id, id)
	}
	sb.WriteString(`</ul></body></html>`)
	return sb.String()
}

// catalogueServer models the shape that made the release listing useless:
// /works/list/release is one unpaginated page of the newest works, while the
// genre listings paginate properly and between them hold the whole catalogue.
func catalogueServer(t *testing.T) (*httptest.Server, *hitCounter) {
	t.Helper()
	hits := &hitCounter{n: map[string]int{}}
	genres := map[string][][]listingItem{
		"10": {
			{{code: "AAA001", thumb: "/img/1.jpg"}, {code: "AAA002", thumb: "/img/2.jpg"}},
			{{code: "AAA003", thumb: "/img/3.jpg"}},
		},
		// Overlaps genre 10 — a work carries several genres, so the union has
		// to dedupe rather than fetch AAA003 twice.
		"11": {{{code: "AAA003", thumb: "/img/3.jpg"}, {code: "AAA004", thumb: "/img/4.jpg"}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.add(r.URL.Path)
		switch {
		case r.URL.Path == "/works/genre":
			_, _ = fmt.Fprint(w, genreIndexHTML("10", "11"))
		case r.URL.Path == "/works/list/release":
			// Never paginates: page 2 re-serves page 1 verbatim.
			_, _ = fmt.Fprint(w, listingPageHTML([]listingItem{{code: "AAA004", thumb: "/img/4.jpg"}}, 1))
		case strings.HasPrefix(r.URL.Path, "/works/list/genre/"):
			id := strings.TrimPrefix(r.URL.Path, "/works/list/genre/")
			pages := genres[id]
			page := 1
			if p := r.URL.Query().Get("page"); p != "" {
				_, _ = fmt.Sscanf(p, "%d", &page)
			}
			if page < 1 || page > len(pages) {
				_, _ = fmt.Fprint(w, listingPageHTML(nil, 0))
				return
			}
			_, _ = fmt.Fprint(w, listingPageHTML(pages[page-1], 0))
		case strings.HasPrefix(r.URL.Path, "/works/detail/"):
			code := strings.TrimPrefix(r.URL.Path, "/works/detail/")
			_, _ = fmt.Fprint(w, detailPageHTML(code, "Title "+code, "Desc", []string{"Hester Prynne"}, "Dir", []string{"Tag"}, "", 60, "2026-02-03"))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, hits
}

// A bare host is the whole-catalogue request. It must reach every genre rather
// than the release page, which shows only the newest works and does not
// paginate.
func TestCatalogueModeUnionsTheGenreListings(t *testing.T) {
	ts, hits := catalogueServer(t)
	defer ts.Close()

	s := &Scraper{cfg: testCfg, Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 4 {
		t.Fatalf("got %d scenes, want 4 (the genre union)", len(scenes))
	}
	got := map[string]bool{}
	for _, sc := range scenes {
		if got[sc.ID] {
			t.Errorf("%s emitted twice — the union did not dedupe", sc.ID)
		}
		got[sc.ID] = true
	}
	for _, want := range []string{"AAA001", "AAA002", "AAA003", "AAA004"} {
		if !got[want] {
			t.Errorf("missing %s", want)
		}
	}
	if hits.get("/works/list/release") != 0 {
		t.Error("catalogue mode fetched the release listing, which does not paginate")
	}
	if hits.get("/works/detail/AAA003") != 1 {
		t.Errorf("AAA003 fetched %d times, want 1", hits.get("/works/detail/AAA003"))
	}
}

// A /works/list/ URL still addresses that one view, unchanged.
func TestListingURLStillScrapesJustThatListing(t *testing.T) {
	ts, hits := catalogueServer(t)
	defer ts.Close()

	s := &Scraper{cfg: testCfg, Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/works/list/genre/11", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 from genre 11 alone", len(scenes))
	}
	if hits.get("/works/genre") != 0 {
		t.Error("a single-listing URL consulted the genre index")
	}
}

// The union is in genre order, not date order, so a known code must not stop
// the walk — everything after it in genre order would be lost. Known works are
// skipped instead, and the run reports itself as stopped early so an
// authoritative save cannot read the smaller result as a deletion.
func TestCatalogueModeSkipsKnownIDsWithoutTruncating(t *testing.T) {
	ts, hits := catalogueServer(t)
	defer ts.Close()

	s := &Scraper{cfg: testCfg, Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), ts.URL+"/", scraper.ListOpts{
		KnownIDs: map[string]bool{"AAA001": true, "AAA003": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var ids []string
	stoppedEarly := false
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			ids = append(ids, res.Scene.ID)
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", res.Err)
		}
	}

	if len(ids) != 2 {
		t.Fatalf("got %v, want just the two unknown works", ids)
	}
	if !stoppedEarly {
		t.Error("skipping known works did not report StoppedEarly")
	}
	// AAA004 lives in the last genre walked and follows the known AAA003. A
	// truncating early-stop would have dropped it.
	var sawAAA004 bool
	for _, id := range ids {
		if id == "AAA004" {
			sawAAA004 = true
		}
	}
	if !sawAAA004 {
		t.Error("AAA004 was lost — the walk truncated at a known code")
	}
	if hits.get("/works/detail/AAA001") != 0 {
		t.Error("a known work was re-fetched")
	}
}

// One unreachable genre must not end the traversal: bailing there would hand
// an authoritative --full save a catalogue missing everything after it.
func TestCatalogueModeContinuesPastAFailedGenre(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/works/genre":
			_, _ = fmt.Fprint(w, genreIndexHTML("10", "11"))
		case r.URL.Path == "/works/list/genre/10":
			http.Error(w, "boom", http.StatusInternalServerError)
		case r.URL.Path == "/works/list/genre/11":
			if r.URL.Query().Get("page") != "" {
				_, _ = fmt.Fprint(w, listingPageHTML(nil, 0))
				return
			}
			_, _ = fmt.Fprint(w, listingPageHTML([]listingItem{{code: "BBB001"}}, 1))
		case strings.HasPrefix(r.URL.Path, "/works/detail/"):
			code := strings.TrimPrefix(r.URL.Path, "/works/detail/")
			_, _ = fmt.Fprint(w, detailPageHTML(code, "Title", "Desc", nil, "", nil, "", 60, "2026-02-03"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := &Scraper{cfg: testCfg, Client: srv.Client()}
	ch, err := s.ListScenes(context.Background(), srv.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenes []string
	var errs int
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene.ID)
		case scraper.KindError:
			errs++
		}
	}
	if errs == 0 {
		t.Error("the failed genre was not reported")
	}
	if len(scenes) != 1 || scenes[0] != "BBB001" {
		t.Errorf("got %v, want the surviving genre's work", scenes)
	}
}

// A genre index that parses to nothing is a template change, not a label with
// no works, so it is reported rather than returned as an empty success.
func TestCatalogueModeReportsAnUnparseableGenreIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>no genres here</body></html>")
	}))
	defer srv.Close()

	s := &Scraper{cfg: testCfg, Client: srv.Client()}
	ch, err := s.ListScenes(context.Background(), srv.URL+"/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var errs []error
	for res := range ch {
		if res.Kind == scraper.KindError {
			errs = append(errs, res.Err)
		}
		if res.Kind == scraper.KindScene {
			t.Error("scene emitted from an empty genre index")
		}
	}
	if len(errs) == 0 {
		t.Fatal("an unparseable genre index reported no error")
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

func TestIsCatalogueURL(t *testing.T) {
	cases := map[string]bool{
		"https://example.com":                          true,
		"https://example.com/":                         true,
		"https://example.com/top":                      true,
		"https://example.com/works/list/release":       false,
		"https://example.com/works/list/genre/113":     false,
		"https://example.com/actress/detail/700115":    false,
		"https://example.com/works/detail/MIAD491":     false,
		"https://www.example.com/works/list/series/12": false,
	}
	for u, want := range cases {
		if got := isCatalogueURL(u); got != want {
			t.Errorf("isCatalogueURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestParseGenreIDsDedupes(t *testing.T) {
	got := parseGenreIDs([]byte(genreIndexHTML("10", "11", "10")))
	if len(got) != 2 || got[0] != "10" || got[1] != "11" {
		t.Errorf("parseGenreIDs = %v, want [10 11] in index order", got)
	}
}

// A listing URL whose first page parses to nothing is reported rather than
// returned as an empty success — the widened host match means a /works/detail/
// URL lands here too, and silence would look like a catalogue that vanished.
func TestEmptyFirstListingPageIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, listingPageHTML(nil, 0))
	}))
	defer srv.Close()

	s := &Scraper{cfg: testCfg, Client: srv.Client()}
	ch, err := s.ListScenes(context.Background(), srv.URL+"/works/detail/MIAD491", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var errs []error
	for res := range ch {
		if res.Kind == scraper.KindError {
			errs = append(errs, res.Err)
		}
		if res.Kind == scraper.KindScene {
			t.Error("unexpected scene")
		}
	}
	if len(errs) == 0 {
		t.Fatal("an empty first page reported no error")
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

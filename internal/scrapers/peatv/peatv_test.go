package peatv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

const fixtureListing = `
<html><body>
<div style="text-align:right">93 件中 1 ～ 25 件目を表示中</div>
<div style="text-align:right;margin-bottom:10px">
  <span style="font-weight:normal;">1</span>&nbsp;
  <a href="/search.php?b=1&amp;p=2" title="page 2">2</a>&nbsp;
  <a href="/search.php?b=1&amp;p=4" title="last page">【最後へ】</a>
</div>
<div class="row featurette">
<div class="hori5">
<a href="https://pea-tv.jp/monthly_detail.php?code=WA-582"><img class="featurette-image img-responsive prod_img" src="./pic_base/product/WA-582/wa-582_pickup.jpg" alt="素人妻ナンパ 全員生中出し ５時間 セレブＤＸ１０４"></a>
<img src="pic_base/icon/icon_hd.gif" class="pea_icon">
<h4 style="height:2em;"><a href="https://pea-tv.jp/monthly_detail.php?code=WA-582">素人妻ナンパ 全員生中出し ...</a></h4>
<p>■----</p><p>■WA-582</p><p>■300分</p>
</div>
<div class="hori5">
<a href="https://pea-tv.jp/monthly_detail.php?code=ZEX-337"><img class="featurette-image img-responsive prod_img" src="./pic_base/product/ZEX-337/zex-337_pickup.jpg" alt="某１８禁動画サイトで話題の乳首舐め"></a>
<h4 style="height:2em;"><a href="https://pea-tv.jp/monthly_detail.php?code=ZEX-337">某１８禁動画サイトで...</a></h4>
<p>■----</p><p>■ZEX-337</p><p>■120分</p>
</div>
</div>
</body></html>
`

func TestParseListingPage(t *testing.T) {
	items, total, lastPage := parseListingPage([]byte(fixtureListing))

	if total != 93 {
		t.Errorf("total = %d, want 93", total)
	}
	if lastPage != 4 {
		t.Errorf("lastPage = %d, want 4", lastPage)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	c := items[0]
	if c.code != "WA-582" {
		t.Errorf("code = %q", c.code)
	}
	if c.title != "素人妻ナンパ 全員生中出し ５時間 セレブＤＸ１０４" {
		t.Errorf("title = %q", c.title)
	}
	if c.thumbnail != "https://pea-tv.jp/pic_base/product/WA-582/wa-582_pickup.jpg" {
		t.Errorf("thumbnail = %q", c.thumbnail)
	}
	if c.duration != 300*60 {
		t.Errorf("duration = %d, want %d", c.duration, 300*60)
	}

	c2 := items[1]
	if c2.code != "ZEX-337" {
		t.Errorf("code = %q", c2.code)
	}
	if c2.duration != 120*60 {
		t.Errorf("duration = %d, want %d", c2.duration, 120*60)
	}
}

const fixtureDetail = `
<html><head>
<title>[HD高画質]素人妻ナンパ...(WA-582) -  -  - AV動画 - PEA-TV【ピー・ティーヴィ】</title>
</head><body>
<table class="table">
<tr><td colspan="1">品番</td><td colspan="3">WA-582</td></tr>
<tr><td colspan="1">配信開始日</td><td colspan="3">2026年4月24日</td></tr>
<tr><td colspan="1">通販開始日</td><td colspan="3">2026年4月24日</td></tr>
<tr><td colspan="1">再生時間</td><td colspan="3">300分</td></tr>
<tr><td colspan="1">レーベル</td><td colspan="3">----</td></tr>
<tr><td colspan="1">シリーズ</td><td colspan="3">----</td></tr>
<tr><td colspan="4"><p class="text-justify">素人妻ナンパの人気シリーズ。今回もセレブな奥様たちを街でナンパ。</p></td></tr>
</table>
</body></html>
`

func TestParseDetailDate(t *testing.T) {
	d, ok := parseDetailDate([]byte(fixtureDetail))
	if !ok {
		t.Fatal("parseDetailDate returned false")
	}
	want := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	if d != want {
		t.Errorf("date = %v, want %v", d, want)
	}
}

func TestParseDetailDateMailOrderFallback(t *testing.T) {
	html := `<table>
<tr><td colspan="1">配信開始日</td><td colspan="3">-</td></tr>
<tr><td colspan="1">通販開始日</td><td colspan="3">2025年12月5日</td></tr>
</table>`
	d, ok := parseDetailDate([]byte(html))
	if !ok {
		t.Fatal("parseDetailDate returned false")
	}
	want := time.Date(2025, 12, 5, 0, 0, 0, 0, time.UTC)
	if d != want {
		t.Errorf("date = %v, want %v", d, want)
	}
}

func TestParseJPDate(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2026年4月24日", time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC), true},
		{"2020年12月1日", time.Date(2020, 12, 1, 0, 0, 0, 0, time.UTC), true},
		{"-", time.Time{}, false},
		{"", time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := parseJPDate(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseJPDate(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestBuildListingURL(t *testing.T) {
	cases := []struct {
		studioURL string
		want      string
	}{
		{"https://pea-tv.jp/", "https://pea-tv.jp/search.php"},
		{"https://pea-tv.jp/top_index.php", "https://pea-tv.jp/search.php"},
		{"https://pea-tv.jp/search.php?b=1", "https://pea-tv.jp/search.php?b=1"},
		{"https://pea-tv.jp/search.php?b=7&p=3", "https://pea-tv.jp/search.php?b=7"},
		{"https://pea-tv.jp/search.php", "https://pea-tv.jp/search.php"},
	}
	for _, c := range cases {
		got := buildListingURL(siteBase, c.studioURL)
		if got != c.want {
			t.Errorf("buildListingURL(%q) = %q, want %q", c.studioURL, got, c.want)
		}
	}
}

func TestPageURL(t *testing.T) {
	cases := []struct {
		base string
		page int
		want string
	}{
		{"https://pea-tv.jp/search.php", 1, "https://pea-tv.jp/search.php"},
		{"https://pea-tv.jp/search.php", 2, "https://pea-tv.jp/search.php?p=2"},
		{"https://pea-tv.jp/search.php?b=1", 3, "https://pea-tv.jp/search.php?b=1&p=3"},
	}
	for _, c := range cases {
		got := pageURL(c.base, c.page)
		if got != c.want {
			t.Errorf("pageURL(%q, %d) = %q, want %q", c.base, c.page, got, c.want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://pea-tv.jp/", true},
		{"https://pea-tv.jp/search.php?b=1", true},
		{"https://pea-tv.jp/top_index.php", true},
		{"http://www.pea-tv.jp/", true},
		{"https://example.com/", false},
		{"https://pea-tvfake.jp/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// --- end to end ---------------------------------------------------------------
//
// The tests above call the parsers directly, which left ListScenes, run,
// fetchDetail and fetchHTML at 0% and the package at 39.3%. The base is now a
// field, so the search-listing -> detail walk runs offline.
func TestListScenesEndToEnd(t *testing.T) {
	// Handlers run on separate goroutines (Workers > 1), so this must be atomic.
	var detailHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search.php"):
			if r.URL.Query().Get("p") != "" && r.URL.Query().Get("p") != "1" {
				_, _ = fmt.Fprint(w, `<html><body>0 件中</body></html>`)
				return
			}
			// The listing's detail hrefs are absolute live URLs, but the scraper
			// only takes the ?code= from them and rebuilds the URL from the base,
			// so serving the fixture verbatim keeps the fetches on this server.
			_, _ = fmt.Fprint(w, fixtureListing)
		case strings.HasPrefix(r.URL.Path, "/monthly_detail.php"):
			detailHits.Add(1)
			_, _ = fmt.Fprint(w, fixtureDetail)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New()
	s.client = ts.Client()
	s.base = ts.URL

	ch, err := s.ListScenes(context.Background(), ts.URL+"/search.php?b=1", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) == 0 {
		t.Fatal("no scenes returned")
	}
	if detailHits.Load() == 0 {
		t.Error("no detail pages were fetched")
	}
	for _, sc := range scenes {
		// fetchDetail fetches scene.URL, so it must stay on the test server.
		if !strings.HasPrefix(sc.URL, ts.URL) {
			t.Errorf("scene %q URL %q escaped the test server", sc.ID, sc.URL)
		}
		if sc.ID == "" || sc.Title == "" {
			t.Errorf("scene has empty ID/Title: %+v", sc)
		}
	}
}

// H-misc / peatv:152: a cancelled scrape must not report StoppedEarly.
//
// StoppedEarly means something specific — "an incremental run reached scenes it already
// had, so this result is complete" — and the cmd layer treats it as a successful run.
// Folding cancellation into the same `hitKnown` flag made an interrupted scrape claim to
// be a finished one.
//
// Forcing the producer to actually block is the whole difficulty. `work` is buffered to
// Workers, so with a fast detail handler the producer never reaches the `case
// <-ctx.Done()` branch and the test passes against the bug. Here the single worker is
// parked inside a detail fetch, the buffer fills, and the producer is provably blocked on
// `work <- item` at the moment of cancellation.
//
// It also guards the correction to a first fix attempt of mine: `close(work)` and
// `wg.Wait()` follow the paging loop, so bailing out with `return` instead of `break`
// leaves the workers blocked forever and then panics them on the closed out channel.
// Draining with a deadline is what catches that.
func TestCancelledScrapeDoesNotReportStoppedEarly(t *testing.T) {
	release := make(chan struct{})
	detailHit := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if strings.HasPrefix(r.URL.Path, "/search.php") {
			// Never runs out of pages: only the context may end this walk.
			_, _ = fmt.Fprint(w, fixtureListing)
			return
		}
		select {
		case detailHit <- struct{}{}:
		default:
		}
		<-release // park the worker so the producer fills the buffer and blocks
		_, _ = fmt.Fprint(w, fixtureDetail)
	}))
	defer ts.Close()
	defer close(release)

	s := New()
	s.client = ts.Client()
	s.base = ts.URL

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, ts.URL, scraper.ListOpts{KnownIDs: map[string]bool{}, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	var stoppedEarly atomic.Int32
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for r := range ch {
			if r.Kind == scraper.KindStoppedEarly {
				stoppedEarly.Add(1)
			}
		}
	}()

	// Wait until a worker is parked in a detail fetch; the producer is then
	// filling the buffer and will block on the next send.
	select {
	case <-detailHit:
	case <-time.After(10 * time.Second):
		t.Fatal("no detail fetch within 10s; the walk never reached the worker")
	}
	time.Sleep(200 * time.Millisecond) // let the producer reach its blocked send
	cancel()

	select {
	case <-drained:
	case <-time.After(15 * time.Second):
		t.Fatal("channel still open 15s after cancellation — the producer skipped " +
			"close(work)/wg.Wait() and the workers are blocked")
	}

	if n := stoppedEarly.Load(); n > 0 {
		t.Errorf("cancelled scrape emitted %d StoppedEarly result(s); that tells the "+
			"caller the run finished normally", n)
	}
}

// fixtureShutdown is the complete 165-byte body every pea-tv.jp page has
// served since the service closed on 2026-09-01, byte for byte.
const fixtureShutdown = "ＰＥＡーＴＶは２０２６年９月１日をもってサービスを終了いたしました。長らくのご愛顧誠にありがとうございました。"

func TestIsShutdownNotice(t *testing.T) {
	if len(fixtureShutdown) != 165 {
		t.Fatalf("fixture is %d bytes, want the live notice's 165", len(fixtureShutdown))
	}
	if !isShutdownNotice([]byte(fixtureShutdown)) {
		t.Error("the closure notice was not recognised")
	}
	if isShutdownNotice([]byte(fixtureListing)) {
		t.Error("a real listing was taken for the closure notice")
	}
}

// An empty first listing page must surface as a parse error, never as a
// silent 0-scene success: FailureParse keeps the run incomplete, so an
// authoritative --full/--refresh Save cannot delete the stored catalogue.
func TestEmptyFirstPageReportsParseError(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantService bool
	}{
		{"shutdown notice", fixtureShutdown, true},
		{"empty listing", `<html><body>0 件中</body></html>`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var detailHits atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/monthly_detail.php") {
					detailHits.Add(1)
				}
				_, _ = fmt.Fprint(w, c.body)
			}))
			defer ts.Close()

			s := New()
			s.client = ts.Client()
			s.base = ts.URL

			ch, err := s.ListScenes(context.Background(), ts.URL+"/search.php?b=1", scraper.ListOpts{Workers: 2})
			if err != nil {
				t.Fatal(err)
			}
			var errs []error
			scenes := 0
			for r := range ch {
				switch r.Kind {
				case scraper.KindScene:
					scenes++
				case scraper.KindError:
					errs = append(errs, r.Err)
				}
			}
			if scenes != 0 || detailHits.Load() != 0 {
				t.Errorf("got %d scenes and %d detail fetches, want none", scenes, detailHits.Load())
			}
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
			}
			if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
				t.Errorf("Classify = %v, want FailureParse", k)
			}
			if got := errors.Is(errs[0], errServiceEnded); got != c.wantService {
				t.Errorf("errors.Is(err, errServiceEnded) = %v, want %v (err: %v)", got, c.wantService, errs[0])
			}
		})
	}
}

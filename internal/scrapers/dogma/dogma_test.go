package dogma

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"http://www.dogma.co.jp/", "https://dogma.co.jp", "http://www.dogma.co.jp/12-dvd"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"http://dogma.jp/", "https://example.com/dogma.co.jp", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

const productPage = `<html><head>
<meta property="og:image" content="http://www.dogma.co.jp/19464-large_default/bbtu-120.jpg">
</head><body>
<h1 itemprop="name">おっぱい保育園 - 吉根ゆりあ</h1>
<p id="product_reference"><span class="editable" itemprop="sku">bbtu-120/bbtu-120d</span></p>
<ul id="features">
	<li>女優 : <a class="actress" href="/search?id_feature=5&search_feature=x">吉根ゆりあ</a> </li>
	<li>監督 : <a class="director" href="/search?id_feature=6&search_feature=TENUN">TENUN</a> </li>
	<li>シリーズ : <a class="series" href="/search?s=1">おっぱい保育園</a></li>
	<li>ジャンル : <a class="tag" href="/search?tag=巨乳">巨乳 </a><a class="tag" href="/search?tag=パイズリ">パイズリ </a></li>
	<li>収録時間 : 101分</li>
	<li>配信開始日 : <a class="delivery" href="/x">2026/09/11</a></li>
	<li>DVD発売日 : <a class="dvd" href="/y">2026/09/15</a></li>
</ul>
<div class="rte">説明 &amp; more</div>
<div class="button_block_wrapper">商品番号 : bbtu-120<br> 定価 : <strong class="proper-price">¥ 4,400</strong><br> 販売価格 : <strong>¥ 3,120</strong></div>
</body></html>`

func TestParseProduct(t *testing.T) {
	scene, err := parseProduct(productPage, productRef{id: "16497", slug: "bbtu-120"}, "http://www.dogma.co.jp/", time.Now().UTC())
	if err != nil {
		t.Fatalf("parseProduct: %v", err)
	}
	// The SKU is the catalogue code and is what survives a re-listing.
	if scene.ID != "bbtu-120" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "おっぱい保育園 - 吉根ゆりあ" {
		t.Errorf("Title = %q", scene.Title)
	}
	if strings.Join(scene.Performers, "|") != "吉根ゆりあ" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Director != "TENUN" {
		t.Errorf("Director = %q", scene.Director)
	}
	if scene.Series != "おっぱい保育園" {
		t.Errorf("Series = %q", scene.Series)
	}
	if strings.Join(scene.Tags, "|") != "巨乳|パイズリ" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	// 101分 is whole minutes.
	if scene.Duration != 101*60 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	// The DVD date is the release proper; the streaming date is the fallback.
	if got := scene.Date.Format("2006-01-02"); got != "2026-09-15" {
		t.Errorf("Date = %s", got)
	}
	if len(scene.PriceHistory) != 1 || scene.PriceHistory[0].Regular != 3120 {
		t.Errorf("PriceHistory = %+v", scene.PriceHistory)
	}
	if scene.Description != "説明 & more" {
		t.Errorf("Description = %q", scene.Description)
	}
}

// A download-only release has no DVD date and falls back to the streaming one.
func TestParseProductFallsBackToTheStreamingDate(t *testing.T) {
	page := strings.Replace(productPage, `<li>DVD発売日 : <a class="dvd" href="/y">2026/09/15</a></li>`, "", 1)
	scene, err := parseProduct(page, productRef{id: "1", slug: "x"}, "u", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := scene.Date.Format("2006-01-02"); got != "2026-09-11" {
		t.Errorf("Date = %s, want the streaming date", got)
	}
}

func TestParseProductWithoutTitleIsAnError(t *testing.T) {
	if _, err := parseProduct("<html></html>", productRef{id: "1", slug: "x"}, "u", time.Now()); err == nil {
		t.Error("want an error when the product title is gone")
	}
}

// Every page is the age gate until its form has been posted.
func TestListScenesPassesTheAgeGateFirst(t *testing.T) {
	var gatePosts, listings int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/":
			gatePosts++
			_, _ = fmt.Fprint(w, "ok")
		case strings.HasPrefix(r.URL.Path, "/home/"):
			_, _ = fmt.Fprint(w, productPage)
		case r.URL.Path == categoryPath:
			listings++
			if r.URL.Query().Get("p") == "" {
				_, _ = fmt.Fprint(w, `<a href="/home/16497-bbtu-120.html">x</a><a href="/home/16497-bbtu-120.html">dupe</a>`)
				return
			}
			_, _ = fmt.Fprint(w, `<html>no products</html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client.Transport = srv.Client().Transport
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes int
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			scenes++
		}
	}
	if gatePosts != 1 {
		t.Errorf("age gate posted %d times, want once", gatePosts)
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1 (the repeated card is one product)", scenes)
	}
	if listings != 2 {
		t.Errorf("fetched %d listing pages, want 2", listings)
	}
}

// A listing that is still the age gate yields no products; that must be a
// parse failure, not an empty catalogue.
func TestGatedListingIsAParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = fmt.Fprint(w, "ok")
			return
		}
		_, _ = fmt.Fprint(w, `<form method="post"><input type=hidden name="chk" value="1"></form>`)
	}))
	defer srv.Close()

	s := New()
	s.client.Transport = srv.Client().Transport
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	var errs []error
	for r := range ch {
		if r.Kind == scraper.KindError {
			errs = append(errs, r.Err)
		}
	}
	if len(errs) != 1 || scraper.Classify(errs[0]) != scraper.FailureParse || !strings.Contains(errs[0].Error(), "age gate") {
		t.Fatalf("errors = %v, want one parse failure naming the age gate", errs)
	}
	if s.gatePassed {
		t.Error("a gated listing should make the next run post the gate again")
	}
}

// The walk reports the advertised total, stops once it has listed that many
// without requesting the page past the end (which redirects to page 1), and
// a stored product ends it at the end of its page without paying for its
// detail page.
func TestListScenesTotalAndKnownIDs(t *testing.T) {
	var details, listings []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			_, _ = fmt.Fprint(w, "ok")
		case strings.HasPrefix(r.URL.Path, "/home/"):
			details = append(details, r.URL.Path)
			_, _ = fmt.Fprint(w, productPage)
		case r.URL.Path == categoryPath:
			listings = append(listings, r.URL.RawQuery)
			if r.URL.Query().Get("p") == "" {
				_, _ = fmt.Fprint(w, `<div class="product-count">3 件中 1 件目から 2 件を表示しています</div>`+
					`<a href="/home/3-ccc-003.html">c</a><a href="/home/2-bbb-002.html">b</a>`)
				return
			}
			_, _ = fmt.Fprint(w, `<a href="/home/1-aaa-001.html">a</a>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	run := func(opts scraper.ListOpts) (total, scenes int, stopped bool) {
		s := New()
		s.client.Transport = srv.Client().Transport
		s.base = srv.URL
		ch, _ := s.ListScenes(context.Background(), srv.URL, opts)
		for r := range ch {
			switch r.Kind {
			case scraper.KindTotal:
				total = r.Total
			case scraper.KindScene:
				scenes++
			case scraper.KindStoppedEarly:
				stopped = true
			case scraper.KindError:
				t.Errorf("unexpected error: %v", r.Err)
			}
		}
		return
	}

	total, scenes, stopped := run(scraper.ListOpts{Workers: 1})
	if total != 3 || scenes != 3 || stopped {
		t.Errorf("full walk: total=%d scenes=%d stopped=%v", total, scenes, stopped)
	}
	if len(listings) != 2 {
		t.Errorf("listing requests = %v, want 2 (no request past the advertised total)", listings)
	}

	details, listings = nil, nil
	_, scenes, stopped = run(scraper.ListOpts{Workers: 1, KnownIDs: map[string]bool{"bbb-002": true}})
	if scenes != 1 || !stopped {
		t.Errorf("incremental: scenes=%d stopped=%v, want 1 and an early stop", scenes, stopped)
	}
	if len(details) != 1 || len(listings) != 1 {
		t.Errorf("incremental fetched details %v and listings %v; the known product must not be fetched", details, listings)
	}
}

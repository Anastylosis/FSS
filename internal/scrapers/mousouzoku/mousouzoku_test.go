package mousouzoku

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

const fixtureDateArchive = `<html><body>
<ul class="area-list lst-media-date">
<li class="c-media-item"><a href="/works/list/date/20260505"><p class="ttl-media">2026年5月5日</p></a></li>
<li class="c-media-item"><a href="/works/list/date/20260428"><p class="ttl-media">2026年4月28日</p></a></li>
</ul>
</body></html>`

const fixtureDateListing = `<html><body>
<p class="tx-paging"> 全2作品中 1～2作品表示</p>
<ul class="c-list-works">
<li class="c-list-works-item">
  <a class="bx-works-detail" href="/works/detail/fjin140/">
    <img class="works-detail-img" src="/contents/works/fjin/fjin140/fjin140pt.jpg" alt="First Title">
    <p class="works-detail-title">First Title</p>
  </a>
</li>
<li class="c-list-works-item">
  <a class="bx-works-detail" href="/works/detail/ydns009/">
    <img class="works-detail-img" src="/contents/works/ydns/ydns009/ydns009pt.jpg" alt="Second Title">
    <p class="works-detail-title">Second Title</p>
  </a>
</li>
</ul>
</body></html>`

const fixtureMakerListing = `<html><body>
<p class="tx-paging"> 全10作品中 1～8作品表示</p>
<ul class="c-list-works">
<li class="c-list-works-item">
  <a class="bx-works-detail" href="/works/detail/abc001/">
    <p class="works-detail-title">Maker Scene</p>
  </a>
</li>
</ul>
</body></html>`

const fixtureDetail1 = `<html><body>
<h1 class="ttl-works">触手組織に囚われた捜査官</h1>
<p class="tx-intro">A great description of this scene.</p>
<div class="bx-pake"><img src="/contents/works/fjin/fjin140/fjin140pl.jpg?1234"></div>
<dl class="bx-info">
<dt>メーカー</dt><dd><a class="btn-inner" href="/works/list/maker/462/"><p class="tx-btn">FunCity/妄想族</p></a></dd>
<dt>発売日</dt><dd><a href="/works/list/date/20260505/">2026年5月5日</a></dd>
<dt>収録時間</dt><dd><p class="tx-info">118分</p></dd>
<dt>品番</dt><dd><p class="tx-info">fjin00140</p></dd>
<dt>出演者</dt><dd><ul><li class="item-info is-inactive"><p class="tx-btn">Melody Marks</p></li></ul></dd>
<dt>ジャンル</dt><dd><ul><li class="item-info"><p class="tx-btn">触手</p></li><li class="item-info"><p class="tx-btn">調教</p></li></ul></dd>
</dl>
</body></html>`

const fixtureDetail2 = `<html><body>
<h1 class="ttl-works">Second Scene Title</h1>
<p class="tx-intro">Another scene description.</p>
<div class="bx-pake"><img src="/contents/works/ydns/ydns009/ydns009pl.jpg?5678"></div>
<dl class="bx-info">
<dt>メーカー</dt><dd><a class="btn-inner" href="/works/list/maker/100/"><p class="tx-btn">Other Maker</p></a></dd>
<dt>発売日</dt><dd><a href="/works/list/date/20260428/">2026年4月28日</a></dd>
<dt>収録時間</dt><dd><p class="tx-info">95分</p></dd>
<dt>品番</dt><dd><p class="tx-info">ydns00009</p></dd>
<dt>出演者</dt><dd><ul><li class="item-info is-inactive"><p class="tx-btn">-</p></li></ul></dd>
<dt>ジャンル</dt><dd><ul><li class="item-info"><p class="tx-btn">中出し</p></li></ul></dd>
</dl>
</body></html>`

const fixtureDetailMaker = `<html><body>
<h1 class="ttl-works">Maker Scene Title</h1>
<dl class="bx-info">
<dt>メーカー</dt><dd><p class="tx-btn">TestMaker</p></dd>
<dt>発売日</dt><dd><a href="/works/list/date/20260505/">2026年5月5日</a></dd>
<dt>収録時間</dt><dd><p class="tx-info">60分</p></dd>
<dt>品番</dt><dd><p class="tx-info">abc00001</p></dd>
</dl>
</body></html>`

const fixtureEmpty = `<html><body></body></html>`

func newTestServer(dateArchive string, datePages map[string]string, details map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/works/date/":
			_, _ = fmt.Fprint(w, dateArchive)
		case datePages != nil && datePages[path] != "":
			_, _ = fmt.Fprint(w, datePages[path])
		case details != nil && details[path] != "":
			_, _ = fmt.Fprint(w, details[path])
		default:
			_, _ = fmt.Fprint(w, fixtureEmpty)
		}
	}))
}

func collect(ch <-chan scraper.SceneResult) []scraper.SceneResult {
	var results []scraper.SceneResult
	for r := range ch {
		results = append(results, r)
	}
	return results
}

func TestParseListingSlugs(t *testing.T) {
	slugs := parseListingSlugs(fixtureDateListing)
	if len(slugs) != 2 {
		t.Fatalf("got %d slugs, want 2", len(slugs))
	}
	if slugs[0] != "fjin140" {
		t.Errorf("slug[0] = %q, want fjin140", slugs[0])
	}
	if slugs[1] != "ydns009" {
		t.Errorf("slug[1] = %q, want ydns009", slugs[1])
	}
}

func TestParseTotal(t *testing.T) {
	total := parseTotal(fixtureDateListing)
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
}

func TestParseTotalComma(t *testing.T) {
	total := parseTotal(`<p class="tx-paging"> 全1,234作品中 1～8作品表示</p>`)
	if total != 1234 {
		t.Errorf("total = %d, want 1234", total)
	}
}

func TestParseDetail(t *testing.T) {
	sc := parseDetail("fjin140", fixtureDetail1, "https://www.mousouzoku-av.com", "https://www.mousouzoku-av.com/works/list/maker/462/")

	if sc.StudioURL != "https://www.mousouzoku-av.com/works/list/maker/462/" {
		t.Errorf("StudioURL = %q", sc.StudioURL)
	}
	if sc.ID != "fjin140" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.Title != "触手組織に囚われた捜査官" {
		t.Errorf("title = %q", sc.Title)
	}
	if sc.Description != "A great description of this scene." {
		t.Errorf("description = %q", sc.Description)
	}
	if sc.Duration != 7080 {
		t.Errorf("duration = %d, want 7080 (118 min)", sc.Duration)
	}
	if sc.Date.Format("2006-01-02") != "2026-05-05" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Studio != "FunCity/妄想族" {
		t.Errorf("studio = %q", sc.Studio)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Melody Marks" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if len(sc.Tags) != 2 || sc.Tags[0] != "触手" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Thumbnail != "https://www.mousouzoku-av.com/contents/works/fjin/fjin140/fjin140pl.jpg" {
		t.Errorf("thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != "https://www.mousouzoku-av.com/works/detail/fjin140/" {
		t.Errorf("url = %q", sc.URL)
	}
}

func TestParseDetailNoPerformer(t *testing.T) {
	sc := parseDetail("ydns009", fixtureDetail2, "https://www.mousouzoku-av.com", "https://www.mousouzoku-av.com")
	if len(sc.Performers) != 0 {
		t.Errorf("performers should be empty for '-', got %v", sc.Performers)
	}
	if sc.Duration != 5700 {
		t.Errorf("duration = %d, want 5700 (95 min)", sc.Duration)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.mousouzoku-av.com/works/list/release/", true},
		{"https://mousouzoku-av.com/works/detail/fjin140/", true},
		{"https://www.mousouzoku-av.com", true},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(
		fixtureDateArchive,
		map[string]string{
			"/works/list/date/20260505/": fixtureDateListing,
		},
		map[string]string{
			"/works/detail/fjin140/": fixtureDetail1,
			"/works/detail/ydns009/": fixtureDetail2,
		},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseURL: ts.URL}
	studioURL := ts.URL + "/works/"
	ch, err := s.ListScenes(context.Background(), studioURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes int
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.StudioURL != studioURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, studioURL)
			}
			if r.Scene.ID == "fjin140" && r.Scene.Studio != "FunCity/妄想族" {
				t.Errorf("studio = %q", r.Scene.Studio)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer(
		fixtureDateArchive,
		map[string]string{
			"/works/list/date/20260505/": fixtureDateListing,
		},
		map[string]string{
			"/works/detail/fjin140/": fixtureDetail1,
		},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseURL: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"ydns009": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes, stopped int
	for _, r := range results {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

func TestMakerMode(t *testing.T) {
	ts := newTestServer(
		"",
		map[string]string{
			"/works/list/maker/462/": fixtureMakerListing,
		},
		map[string]string{
			"/works/detail/abc001/": fixtureDetailMaker,
		},
	)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), baseURL: ts.URL}
	makerURL := ts.URL + "/works/list/maker/462/"
	ch, err := s.ListScenes(context.Background(), makerURL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}

	results := collect(ch)
	var scenes int
	for _, r := range results {
		if r.Kind == scraper.KindScene {
			scenes++
			if r.Scene.StudioURL != makerURL {
				t.Errorf("StudioURL = %q, want %q", r.Scene.StudioURL, makerURL)
			}
			if r.Scene.ID != "abc001" {
				t.Errorf("ID = %q, want abc001", r.Scene.ID)
			}
		}
	}
	if scenes != 1 {
		t.Errorf("got %d scenes, want 1", scenes)
	}
}

// H-misc / mousouzoku:251: a cancelled detail fetch must never leave a zero-value
// result behind.
//
// `results` is index-addressed and pre-sized, so a goroutine that returns without
// writing its slot leaves `sceneResult{}` there. The consumer reads `err == nil` as
// success and appends the empty models.Scene — a scene with no ID, title or URL enters
// the store as if it had been scraped. There is no ID guard on that path, which is what
// separates this package (and rawfuckclub) from the ~19 others with the same goroutine
// shape but an `item.id == ""` check downstream.
//
// Two paths leave a slot unwritten: the delay select's cancel case, and the outer loop
// breaking before it has launched every goroutine. An already-cancelled context
// exercises the second deterministically.
func TestFetchDetailsNeverLeavesZeroValueResults(t *testing.T) {
	s := New()
	slugs := []string{"a", "b", "c", "d", "e"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the first launch

	results := s.fetchDetails(ctx, "https://example.com/studio", slugs, 0, 2)

	if len(results) != len(slugs) {
		t.Fatalf("got %d results, want %d", len(results), len(slugs))
	}
	for i, r := range results {
		if r.err == nil && r.scene.ID == "" {
			t.Errorf("result %d is a zero value: err is nil and Scene.ID is empty, so the "+
				"consumer would append an empty scene to the store", i)
		}
	}
}

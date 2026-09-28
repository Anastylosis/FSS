package hmp

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
		{"https://www.hmp.jp/", true},
		{"https://hmp.jp/portal/top/", true},
		{"https://www.hmp.jp/portal/catalog/?scd=10", true},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestParseListingPage(t *testing.T) {
	body := []byte(`<li class="srch-total">合計<span>42</span>件</li>
<a href="https://www.hmp.jp/portal/catalog/goods/HODV-22078/">Title</a>
<a href="https://www.hmp.jp/portal/catalog/goods/HODV-22078/">Title</a>
<a href="https://www.hmp.jp/portal/catalog/goods/HOMA-00165/">Title</a>`)

	items, total := parseListingPage(body)
	if total != 42 {
		t.Errorf("total = %d, want 42", total)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (deduped)", len(items))
	}
	if items[0].code != "HODV-22078" {
		t.Errorf("items[0].code = %q", items[0].code)
	}
	if items[1].code != "HOMA-00165" {
		t.Errorf("items[1].code = %q", items[1].code)
	}
}

const detailHTML = `<h1 id="itemTitle">
				Test Title Here
				</h1>
<div id="itemMainPhoto"><a href="full.jpg" class="imp1"><img src="https://www.hmp.jp/images/item/0039/HODV-22078/p14m_260528122350.jpg" alt="Test" /></a></div>
<div id="itemSpec">
<table>
<tr><th>出演：</th>
<td><a href="/portal/actress/detail/J912/">Performer One</a>、<a href="/portal/actress/detail/J913/">Performer Two</a>、Plain Name、</td></tr>
<tr><th>発売日：</th>
<td>2026.06.26</td></tr>
<tr><th>品番：</th>
<td>HODV-22078</td></tr>
<tr><th>時間：</th>
<td>140 分</td></tr>
<tr><th>レーベル：</th>
<td><a href="?bt=001_001&amp;scd=10">h.m.p</a></td></tr>
<tr><th>税込金額：</th>
<td>3,498円</td></tr>
<tr><th>ジャンル：</th>
<td><a href="?g=003&amp;scd=10">巨乳</a>、<a href="?g=020&amp;scd=10">痴女</a></td></tr>
<tr><th>シリーズ：</th>
<td><a href="?s=109&amp;scd=10">大人のAV</a></td></tr>
</table></div>
<p id="explain">Description text here.</p>`

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(detailHTML))

	if d.title != "Test Title Here" {
		t.Errorf("title = %q", d.title)
	}
	if d.date.Format("2006-01-02") != "2026-06-26" {
		t.Errorf("date = %v", d.date)
	}
	if d.duration != 8400 {
		t.Errorf("duration = %d, want 8400", d.duration)
	}
	if len(d.performers) != 3 || d.performers[0] != "Performer One" || d.performers[1] != "Performer Two" || d.performers[2] != "Plain Name" {
		t.Errorf("performers = %v", d.performers)
	}
	if len(d.tags) != 2 || d.tags[0] != "巨乳" || d.tags[1] != "痴女" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.series != "大人のAV" {
		t.Errorf("series = %q", d.series)
	}
	if d.label != "h.m.p" {
		t.Errorf("label = %q", d.label)
	}
	if d.price != 3498 {
		t.Errorf("price = %d", d.price)
	}
	if d.description != "Description text here." {
		t.Errorf("description = %q", d.description)
	}
	if d.thumbnail != "https://www.hmp.jp/images/item/0039/HODV-22078/p14m_260528122350.jpg" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
}

func TestParseDetailPageMissingFields(t *testing.T) {
	body := []byte(`<h1 id="itemTitle">Title Only</h1>`)
	d := parseDetailPage(body)
	if d.title != "Title Only" {
		t.Errorf("title = %q", d.title)
	}
	if !d.date.IsZero() {
		t.Errorf("expected zero date, got %v", d.date)
	}
	if d.duration != 0 {
		t.Errorf("expected 0 duration, got %d", d.duration)
	}
	if len(d.performers) != 0 {
		t.Errorf("expected no performers, got %v", d.performers)
	}
}

func newTestServer(codes []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		switch r.URL.Path {
		case "/portal/catalog/":
			var links string
			for _, code := range codes {
				links += fmt.Sprintf(`<a href="/portal/catalog/goods/%s/">Title</a>`, code)
			}
			_, _ = fmt.Fprintf(w, `<li class="srch-total">合計<span>%d</span>件</li>%s`, len(codes), links)
		default:
			_, _ = fmt.Fprint(w, detailHTML)
		}
	}))
}

func TestRun(t *testing.T) {
	codes := []string{"HODV-11111", "HODV-22222"}
	ts := newTestServer(codes)
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
		if sc.ID != "HODV-11111" && sc.ID != "HODV-22222" {
			t.Errorf("unexpected ID = %q", sc.ID)
		}
	}
}

// Releases arrive in same-date batches ordered by product code, so a title
// added to a half-stored batch sits behind a known sibling: the page is
// finished before the stop and HODV-33333 must still be collected.
func TestRunKnownIDs(t *testing.T) {
	codes := []string{"HODV-11111", "HODV-22222", "HODV-33333"}
	ts := newTestServer(codes)
	defer ts.Close()

	s := &Scraper{client: ts.Client(), siteBase: ts.URL}
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"HODV-22222": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
	ids := map[string]bool{}
	for _, sc := range scenes {
		ids[sc.ID] = true
	}
	if len(scenes) != 2 || !ids["HODV-11111"] || !ids["HODV-33333"] {
		t.Fatalf("scene IDs = %v, want HODV-11111 and HODV-33333", ids)
	}
}

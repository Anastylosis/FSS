package maturenl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.mature.nl/en/updates", true},
		{"https://www.mature.nl/en/updates/3", true},
		{"https://mature.nl/en/model/9071", true},
		{"https://www.mature.nl/en/model/9071/some-name", true},
		{"https://www.mature.nl/en/niche/570/1/4k", true},
		{"https://www.mature.nl/en/niche/570", true},
		{"https://www.mature.nl/en/home", false},
		{"https://example.com/en/updates", false},
		{"https://mature.nl/de/updates", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestClassifyURL(t *testing.T) {
	tests := []struct {
		url      string
		wantKind urlKind
		wantID   string
	}{
		{"https://www.mature.nl/en/updates", kindUpdates, ""},
		{"https://www.mature.nl/en/updates/3", kindUpdates, ""},
		{"https://www.mature.nl/en/model/9071", kindModel, "9071"},
		{"https://www.mature.nl/en/model/9071/some-name", kindModel, "9071"},
		{"https://www.mature.nl/en/niche/570/1/4k", kindNiche, "570"},
	}
	for _, tt := range tests {
		kind, id := classifyURL(tt.url)
		if kind != tt.wantKind || id != tt.wantID {
			t.Errorf("classifyURL(%q) = (%v, %q), want (%v, %q)", tt.url, kind, id, tt.wantKind, tt.wantID)
		}
	}
}

const listingHTML = `<html><body>
<div class="page-nav">
  <a href="/en/updates/1" class="active">1</a>
  <a href="/en/updates/2" class="">2</a>
  <a href="/en/updates/5" class="page-nav-hide-tn">
    <span class="material-icons">&#xE5DD;</span>
  </a>
</div>
<div class="grid-layout">
  <div class="grid-item">
    <div class="card">
      <div class="card-img shine-overlay">
        <a href="/en/update/14672/a-pussy-licking-affair">
          <img src="/ui/v3/img/cs_default.png" class="lazy"
               data-src="https://s.cdn.mature.nl/update_support/2/14672/cs_en.jpg?validfrom=123&amp;validto=456&amp;hash=abc">
        </a>
      </div>
      <div class="card-cnt">
        <div class="card-title">
          <a href="/en/update/14672/a-pussy-licking-affair">A pussy licking affair</a><br>
        </div>
        <div class="card-subtitle">
          <a href="/en/model/9606/isabella-de-laa">Isabella De Laa</a> (20),
          <a href="/en/model/8901/lena-love">Lena Love</a> (39)<br>
        </div>
        <div class="card-text">
          <div class="overflow">
            <a href="/en/niche/1/1/lesbian">Lesbian</a>, <a href="/en/niche/2/1/milf">MILF</a>
          </div>
        </div>
        <div class="card-text fs-small">
          <div class="overflow">
            MAE &bull; 4-11-2022<br>
          </div>
        </div>
      </div>
    </div>
  </div>
  <div class="grid-item">
    <div class="card">
      <div class="card-img shine-overlay">
        <a href="/en/update/14500/hot-granny-fun">
          <img src="/ui/v3/img/cs_default.png" class="lazy"
               data-src="https://s.cdn.mature.nl/update_support/2/14500/cs_en.jpg?validfrom=123&amp;validto=456&amp;hash=def">
        </a>
      </div>
      <div class="card-cnt">
        <div class="card-title">
          <a href="/en/update/14500/hot-granny-fun">Hot granny fun</a><br>
        </div>
        <div class="card-subtitle">
          <a href="/en/model/5555/anna-b">Anna B.</a> (65)<br>
        </div>
        <div class="card-text">
          <div class="overflow">
            <a href="/en/niche/3/1/granny">Granny</a>
          </div>
        </div>
        <div class="card-text fs-small">
          <div class="overflow">
            ProVid &bull; 15-10-2022<br>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>
</body></html>`

func TestParseListingCards(t *testing.T) {
	cards := parseListingCards([]byte(listingHTML))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}

	c := cards[0]
	if c.id != "14672" {
		t.Errorf("id = %q, want 14672", c.id)
	}
	if c.title != "A pussy licking affair" {
		t.Errorf("title = %q", c.title)
	}
	if c.url != siteBase+"/en/update/14672" {
		t.Errorf("url = %q", c.url)
	}
	if len(c.performers) != 2 || c.performers[0] != "Isabella De Laa" || c.performers[1] != "Lena Love" {
		t.Errorf("performers = %v", c.performers)
	}
	if len(c.tags) != 2 || c.tags[0] != "Lesbian" || c.tags[1] != "MILF" {
		t.Errorf("tags = %v", c.tags)
	}
	if c.producer != "MAE" {
		t.Errorf("producer = %q, want MAE", c.producer)
	}
	wantDate := time.Date(2022, 11, 4, 0, 0, 0, 0, time.UTC)
	if !c.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", c.date, wantDate)
	}
	if c.thumbnail == "" {
		t.Error("thumbnail is empty")
	}

	c2 := cards[1]
	if c2.id != "14500" {
		t.Errorf("card 2 id = %q, want 14500", c2.id)
	}
	if len(c2.performers) != 1 || c2.performers[0] != "Anna B." {
		t.Errorf("card 2 performers = %v", c2.performers)
	}
}

func TestEstimateTotal(t *testing.T) {
	total := estimateTotal([]byte(listingHTML), 2)
	if total != 10 {
		t.Errorf("estimateTotal = %d, want 10 (5 pages * 2 cards)", total)
	}
}

func TestEstimateTotal_noNav(t *testing.T) {
	total := estimateTotal([]byte(`<html><body>no nav</body></html>`), 60)
	if total != 60 {
		t.Errorf("estimateTotal = %d, want 60", total)
	}
}

const detailHTML = `<html><body>
<h1>A pussy licking affair between hot teeny and MILF</h1>
<span title="Release date">&#xe935;</span> <span class="val-m">4-11-2022</span>
<span title="Video length">&#xe039;</span> <span class="val-m">27:47</span>
<span title="Photos">&#xe410;</span> <span class="val-m">249</span>
<span class="col-accent">Starring:</span> Isabella De Laa (20) &amp; Lena Love (39)<br>
<span class="col-accent">Synopsis:</span> Two beautiful women explore each other<br>
<span class="col-accent">Producer:</span> MAE<br>
<div class="tag-list">
  <a href="/en/niche/1/1/lesbian" class="tag">Lesbian</a>
  <a href="/en/niche/2/1/milf" class="tag">MILF</a>
  <a href="/en/niche/570/1/4k" class="tag">4K</a>
</div>
<video poster="https://s.cdn.mature.nl/update_support/2/14672/cs_wide.jpg?v=1">
  <source src="https://l.cdn.mature.nl/update_support/2/14672/trailer_soft.mp4?v=1" type="video/mp4">
</video>
</body></html>`

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(detailHTML))

	if d.title != "A pussy licking affair between hot teeny and MILF" {
		t.Errorf("title = %q", d.title)
	}
	wantDate := time.Date(2022, 11, 4, 0, 0, 0, 0, time.UTC)
	if !d.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", d.date, wantDate)
	}
	if d.duration != 27*60+47 {
		t.Errorf("duration = %d, want %d", d.duration, 27*60+47)
	}
	if len(d.performers) != 2 || d.performers[0] != "Isabella De Laa" || d.performers[1] != "Lena Love" {
		t.Errorf("performers = %v", d.performers)
	}
	if d.description != "Two beautiful women explore each other" {
		t.Errorf("description = %q", d.description)
	}
	if d.producer != "MAE" {
		t.Errorf("producer = %q", d.producer)
	}
	if len(d.tags) != 3 {
		t.Errorf("tags = %v, want 3", d.tags)
	}
	if d.thumbnail == "" {
		t.Error("thumbnail is empty")
	}
	if d.preview == "" {
		t.Error("preview is empty")
	}
}

func TestParseModelUpdateIDs(t *testing.T) {
	body := []byte(`
		<a href="/en/update/14672/slug-a"></a>
		<a href="/en/update/14500/slug-b"></a>
		<a href="/en/update/14672/slug-a"></a>
	`)
	ids := parseModelUpdateIDs(body)
	if len(ids) != 2 {
		t.Fatalf("got %d ids, want 2 (deduped)", len(ids))
	}
	if ids[0] != "14672" || ids[1] != "14500" {
		t.Errorf("ids = %v", ids)
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"4-11-2022", time.Date(2022, 11, 4, 0, 0, 0, 0, time.UTC)},
		{"27-4-2026", time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)},
		{"15-10-2022", time.Date(2022, 10, 15, 0, 0, 0, 0, time.UTC)},
		{"bad", time.Time{}},
	}
	for _, tt := range tests {
		got := parseDate(tt.in)
		if !got.Equal(tt.want) {
			t.Errorf("parseDate(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"27:47", 27*60 + 47},
		{"1:05:30", 1*3600 + 5*60 + 30},
		{"0:45", 45},
	}
	for _, tt := range tests {
		got := parseutil.ParseDurationColon(tt.in)
		if got != tt.want {
			t.Errorf("parseutil.ParseDurationColon(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestPaginatedScrape(t *testing.T) {
	page1 := listingHTML
	page2 := `<html><body><div class="grid-layout"></div></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/updates/1":
			_, _ = fmt.Fprint(w, page1)
		default:
			_, _ = fmt.Fprint(w, page2)
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Scraper{client: ts.Client()}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runPaginated(ctx, ts.URL+"/en/updates", scraper.ListOpts{Workers: 1}, out, func(page int) string {
			return fmt.Sprintf("%s/en/updates/%d", ts.URL, page)
		})
	}()

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
}

// TestPaginatedScrape404PastLastPage pins the end-of-list rule: the listing
// 404s past its last page, and that must end the walk quietly rather than
// report a failure that demotes a --full run to non-authoritative.
func TestPaginatedScrape404PastLastPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/en/updates/1" {
			_, _ = fmt.Fprint(w, listingHTML)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Scraper{client: ts.Client()}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runPaginated(ctx, ts.URL+"/en/updates", scraper.ListOpts{Workers: 1}, out, func(page int) string {
			return fmt.Sprintf("%s/en/updates/%d", ts.URL, page)
		})
	}()

	scenes, errs := 0, 0
	for res := range out {
		switch res.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
			t.Logf("error: %v", res.Err)
		}
	}
	if scenes != 2 {
		t.Errorf("got %d scenes, want 2", scenes)
	}
	if errs != 0 {
		t.Errorf("got %d errors, want 0 — a 404 past the last page is the end of the listing", errs)
	}
}

// A 404 on page 1 is a bad niche or model slug, not the end of a listing, and
// must still be reported.
func TestPaginatedScrape404OnFirstPageStillErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Scraper{client: ts.Client()}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runPaginated(ctx, ts.URL+"/en/niche/nope", scraper.ListOpts{}, out, func(page int) string {
			return fmt.Sprintf("%s/en/niche/nope/%d", ts.URL, page)
		})
	}()

	errs := 0
	for res := range out {
		if res.Kind == scraper.KindError {
			errs++
		}
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listingHTML)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Scraper{client: ts.Client()}
	out := make(chan scraper.SceneResult)
	opts := scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"14500": true},
	}
	go func() {
		defer close(out)
		s.runPaginated(ctx, ts.URL+"/en/updates", opts, out, func(page int) string {
			return fmt.Sprintf("%s/en/updates/%d", ts.URL, page)
		})
	}()

	scenes, stoppedEarly := testutil.CollectScenesWithStop(t, out)
	if len(scenes) != 1 {
		t.Errorf("got %d scenes before known ID, want 1", len(scenes))
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

// TestRunModelKnownIDsNoDeadlock regresses AUDIT_PLAN B1b: a model page whose
// updates are all already known makes every worker early-stop on its first
// item. The producer must notice the workers have stopped and quit enqueuing
// instead of blocking forever on a full work buffer that nobody drains.
func TestRunModelKnownIDsNoDeadlock(t *testing.T) {
	var links strings.Builder
	known := map[string]bool{}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("%d", 20000+i)
		fmt.Fprintf(&links, "<a href=\"/en/update/%s/slug-%d\"></a>\n", id, i)
		known[id] = true
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "<html><body>%s</body></html>", links.String())
	}))
	defer ts.Close()

	old := siteBase
	siteBase = ts.URL
	defer func() { siteBase = old }()

	s := &Scraper{client: ts.Client()}
	out := make(chan scraper.SceneResult, 64)
	done := make(chan struct{})
	go func() {
		defer close(out)
		defer close(done)
		// Small worker count so the buffer (== Workers) fills well before the
		// 30 ids are enqueued.
		s.runModel(context.Background(), siteBase+"/en/model/9071", "9071",
			scraper.ListOpts{Workers: 2, KnownIDs: known}, out)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runModel deadlocked on an all-known model page")
	}
	for range out {
	}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = (*Scraper)(nil)
}

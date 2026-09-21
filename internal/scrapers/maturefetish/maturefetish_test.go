package maturefetish

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
		{"https://maturefetish.com/en/updates", true},
		{"https://maturefetish.com/en/updates/3", true},
		{"https://www.maturefetish.com/en/updates", true},
		{"https://maturefetish.com/en/model/10500", true},
		{"https://maturefetish.com/en/model/10500/1/cresina", true},
		{"https://maturefetish.com/en/niche/67/1/facesitting", true},
		{"https://maturefetish.com/en/niche/67", true},
		{"https://maturefetish.com/en/home", false},
		{"https://example.com/en/updates", false},
		{"https://mature.nl/en/updates", false},
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
		{"https://maturefetish.com/en/updates", kindUpdates, ""},
		{"https://maturefetish.com/en/updates/3", kindUpdates, ""},
		{"https://maturefetish.com/en/model/10500", kindModel, "10500"},
		{"https://maturefetish.com/en/model/10500/1/cresina", kindModel, "10500"},
		{"https://maturefetish.com/en/niche/67/1/facesitting", kindNiche, "67"},
	}
	for _, tt := range tests {
		kind, id := classifyURL(tt.url)
		if kind != tt.wantKind || id != tt.wantID {
			t.Errorf("classifyURL(%q) = (%v, %q), want (%v, %q)", tt.url, kind, id, tt.wantKind, tt.wantID)
		}
	}
}

const listingHTML = `<html><body>
<div class="grid gap-4 sm:grid-cols-2 md:grid-cols-3">
  <div class="update svelte-8bkqp6">
    <a href="/en/update/16395/cresina-and-limma-sweet-have-a-very-special-welcome-for-their-toyboy">
      <noscript><img src="https://s.cdn.mature.nl/update_support/2/16395/ts_hard.jpg?v=1" alt="Title cover"/></noscript>
    </a>
    <div class="title svelte-8bkqp6">
      <a href="/en/update/16395/cresina-and-limma-sweet-have-a-very-special-welcome-for-their-toyboy">Cresina and Limma Sweet Have a Very Special Welcome for Their Toyboy</a>
    </div>
    <div class="stats svelte-8bkqp6">
      <div class="svelte-8bkqp6"><span class="mat-ico"></span> 19:07</div>
      <div class="svelte-8bkqp6"><span class="mat-ico"></span> 117</div>
    </div>
  </div>
  <div class="update svelte-8bkqp6">
    <a href="/en/update/16783/kinky-facesitting-fetish">
      <noscript><img src="https://s.cdn.mature.nl/update_support/2/16783/ts_hard.jpg?v=1" alt="Title cover"/></noscript>
    </a>
    <div class="title svelte-8bkqp6">
      <a href="/en/update/16783/kinky-facesitting-fetish">Kinky Facesitting Fetish</a>
    </div>
  </div>
</div>
<a href="/en/updates/1" class="svelte-1exw4ui active">1</a>
<a href="/en/updates/2" class="svelte-1exw4ui">2</a>
<a href="/en/updates/9" class="svelte-1exw4ui">9</a>
</body></html>`

func TestParseListingIDs(t *testing.T) {
	ids := parseListingIDs([]byte(listingHTML))
	if len(ids) != 2 {
		t.Fatalf("got %d IDs, want 2", len(ids))
	}
	if ids[0] != "16395" || ids[1] != "16783" {
		t.Errorf("ids = %v, want [16395, 16783]", ids)
	}
}

func TestParseLastPage(t *testing.T) {
	lastPage := parseLastPage([]byte(listingHTML))
	if lastPage != 9 {
		t.Errorf("lastPage = %d, want 9", lastPage)
	}
}

func TestEstimateTotal(t *testing.T) {
	total := estimateTotal([]byte(listingHTML), 2)
	if total != 18 {
		t.Errorf("estimateTotal = %d, want 18 (9 pages * 2)", total)
	}
}

func TestEstimateTotal_noNav(t *testing.T) {
	total := estimateTotal([]byte(`<html><body>no nav</body></html>`), 6)
	if total != 6 {
		t.Errorf("estimateTotal = %d, want 6", total)
	}
}

const detailHTML = `<html><head>
<meta name="description" content="68-year-old busty granny and 41-year-old MILF share their toyboy.">
</head><body>
<h1>Cresina and Limma Sweet Have a Very Special Welcome for Their Toyboy</h1>
<div class="text-page-text-alt flex items-center gap-1 mt-1">
  <span class="mat-ico"></span> 6-5-2026
  <span class="mat-ico ml-3"></span> 19:07
  <span class="mat-ico ml-3"></span> 117
</div>
<a href="/en/model/10391/1/bruno-baxter">Bruno Baxter</a>
<a href="/en/model/10500/1/cresina">Cresina</a>
<a href="/en/model/10772/1/limma-sweet">Limma Sweet</a>
<a href="/en/niche/465/1/40-plus" class="btn btn-filter svelte-1iyow1u">40 Plus</a>
<a href="/en/niche/67/1/facesitting" class="btn btn-filter svelte-1iyow1u">Facesitting</a>
<a href="/en/niche/118/1/ass" class="btn btn-filter svelte-1iyow1u">Ass</a>
<video poster="https://s.cdn.mature.nl/update_support/2/16395/tl_hard.jpg?v=1">
  <source src="https://l.cdn.mature.nl/update_support/2/16395/trailer_soft.mp4?v=1" type="video/mp4">
</video>
</body></html>`

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(detailHTML))

	if d.title != "Cresina and Limma Sweet Have a Very Special Welcome for Their Toyboy" {
		t.Errorf("title = %q", d.title)
	}
	wantDate := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	if !d.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", d.date, wantDate)
	}
	if d.duration != 19*60+7 {
		t.Errorf("duration = %d, want %d", d.duration, 19*60+7)
	}
	if len(d.performers) != 3 || d.performers[0] != "Bruno Baxter" || d.performers[1] != "Cresina" || d.performers[2] != "Limma Sweet" {
		t.Errorf("performers = %v", d.performers)
	}
	if len(d.tags) != 3 || d.tags[0] != "40 Plus" || d.tags[1] != "Facesitting" {
		t.Errorf("tags = %v", d.tags)
	}
	if d.description != "68-year-old busty granny and 41-year-old MILF share their toyboy." {
		t.Errorf("description = %q", d.description)
	}
	if d.thumbnail != "https://s.cdn.mature.nl/update_support/2/16395/tl_hard.jpg?v=1" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
	if d.preview != "https://l.cdn.mature.nl/update_support/2/16395/trailer_soft.mp4?v=1" {
		t.Errorf("preview = %q", d.preview)
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"6-5-2026", time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)},
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
		{"19:07", 19*60 + 7},
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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/en/update/"):
			_, _ = fmt.Fprint(w, detailHTML)
		case r.URL.Path == "/en/updates/1":
			_, _ = fmt.Fprint(w, listingHTML)
		default:
			_, _ = fmt.Fprint(w, `<html><body></body></html>`)
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runPaginated(ctx, ts.URL+"/en/updates", scraper.ListOpts{Workers: 2}, out, func(page int) string {
			return fmt.Sprintf("%s/en/updates/%d", ts.URL, page)
		})
	}()

	scenes := testutil.CollectScenes(t, out)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if scenes[0].SiteID != "maturefetish" {
		t.Errorf("SiteID = %q, want maturefetish", scenes[0].SiteID)
	}
	// Offline means offline: every scene URL must point at the test server.
	// Before base was overridable these came from the siteBase const, so the
	// detail fetches silently scraped production.
	for _, sc := range scenes {
		if !strings.HasPrefix(sc.URL, ts.URL) {
			t.Errorf("scene %q URL %q escaped the test server", sc.ID, sc.URL)
		}
	}
	// The fixture is what should have been parsed, not live markup.
	if scenes[0].Title == "" {
		t.Error("scene has empty Title — detail fixture was not parsed")
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/en/update/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			_, _ = fmt.Fprint(w, listingHTML)
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult)
	opts := scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"16783": true},
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

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = (*Scraper)(nil)
}

// Worker-pool shape: a cancelled scrape must stop the page walk and let the
// detail workers exit, rather than keep fetching for a reader that has gone.
func TestCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/en/update/") {
			_, _ = fmt.Fprint(w, detailHTML)
			return
		}
		// Never runs out of listing pages, so only the context ends the walk.
		_, _ = fmt.Fprint(w, listingHTML)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	testutil.AssertCancellable(t, s, ts.URL+"/en/updates", scraper.ListOpts{Workers: 2})
}

// A d-m-Y or m:ss pattern elsewhere on the page — a sidebar promo, the
// player's markup — must not be read as the scene's date or runtime.
func TestParseDetailPageIgnoresDatesOutsideTheInfoLine(t *testing.T) {
	page := `<html><body>
<div class="promo">Members since 1-1-2001 &middot; 99:99 of new video weekly</div>
<h1>Some Scene</h1>
<div class="text-page-text-alt flex items-center gap-1 mt-1"><span class="mat-ico"></span> 10-8-2026 <span class="mat-ico ml-3"></span> 15:02 <span class="mat-ico ml-3"></span> 118</div>
</body></html>`
	d := parseDetailPage([]byte(page))
	if got := d.date.Format("2006-01-02"); got != "2026-08-10" {
		t.Errorf("date = %s, want 2026-08-10", got)
	}
	if d.duration != 902 {
		t.Errorf("duration = %d, want 902", d.duration)
	}
}

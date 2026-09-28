package julesjordanutil

import (
	"context"
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

// ========== JJ Template Fixtures ==========

const jjListingFixture = `
<div class="jj-content-card">
<a href="/scenes/Scene-One_vids.html" class="jj-card-thumb">
<img id="set-target-100" class="jj-thumb-img stdimage" src="/thumbs/100.jpg" /></a>
<div class="jj-card-body">
<h2 class="jj-card-title">Scene One Title</h2>
<div class="jj-card-meta">Starring: <span class="update_models">
<a href="/models/alice.html">Alice</a>, <a href="/models/bob.html">Bob</a></span></div>
<div class="jj-card-date">Released: April 28, 2026</div>
</div></div>

<div class="jj-content-card">
<a href="/scenes/Scene-Two_vids.html" class="jj-card-thumb">
<img id="set-target-200" class="jj-thumb-img" src="/thumbs/200.jpg" /></a>
<div class="jj-card-body">
<h2 class="jj-card-title">Scene Two Title</h2>
<div class="jj-card-meta">Starring: <span class="update_models">
<a href="/models/charlie.html">Charlie</a></span></div>
<div class="jj-card-date">Released: April 20, 2026</div>
</div></div>`

const jjDetailFixture = `
<h1 class="scene-title">Detail Scene Title</h1>
<div class="scene-meta">
<div class="meta-item"><div class="lbl">Starring</div>
<div class="val"><span class="update_models">
<a href="/models/alice.html">Alice</a>, <a href="/models/bob.html">Bob</a></span></div></div>
<div class="meta-item"><div class="lbl">Released</div>
<div class="val">April 28, 2026</div></div>
</div>
<div class="scene-desc">A great scene description with &amp; special chars.</div>
<div class="scene-cats"><span class="cats-lbl">Categories</span>
<a href="category.php?id=11" class="cat-tag">Anal</a><a href="category.php?id=29" class="cat-tag">Blowjobs</a></div>`

// ========== Classic Template Fixtures ==========

const classicListingFixture = `
<div class="update_details" data-setid="13101">
<a href="/scenes/Classic-Scene-One_vids.html">
<img id="set-target-13101" class="update_thumb thumbs hideMobile stdimage" src="/thumbs/13101.jpg" />
</a>
<a href="/scenes/Classic-Scene-One_vids.html">
Classic Scene One
</a>
<span class="update_models">
<a href="/models/alice.html">Alice</a>, <a href="/models/bob.html">Bob</a>
</span>
<div class="cell update_date">09/21/2022</div>
</div>
<div class="update_details" data-setid="13100">
<a href="/scenes/Classic-Scene-Two_vids.html">
<img id="set-target-13100" class="update_thumb thumbs" src="/thumbs/13100.jpg" />
</a>
<a href="/scenes/Classic-Scene-Two_vids.html">
Classic Scene Two
</a>
<span class="update_models">
<a href="/models/charlie.html">Charlie</a>
</span>
<div class="cell update_date">09/15/2022</div>
</div>`

const classicDetailFixture = `
<span class="title_bar_hilite">Classic Detail Title</span>
<span class="update_models">
<a href="/models/alice.html">Alice</a>, <a href="/models/bob.html">Bob</a>
</span>
<div class="cell update_date">09/21/2022</div>
<span class="update_description">A classic description.</span>
<span class="update_tags">Tags:&nbsp;<a href="/categories/anal.html">Anal</a>, <a href="/categories/blowjob.html">Blowjob</a></span>
<span class="update_dvds">Movie: <a href="/dvds/test-dvd.html">Test DVD</a></span>`

const classicDetailWithHRFixture = `
<span class="title_bar_hilite"><hr>Title With HR Tags<hr></span>
<div class="cell update_date">Release Date:&nbsp;07/15/2020</div>
<span class="update_description">GirlGirl description.</span>
<span class="update_tags">Tags:&nbsp;<a href="/categories/lesbian.html">Lesbian</a></span>`

// ========== Modern Template Fixtures ==========

const modernListingFixture = `
<div class="grid-item">
<a href="/scenes/Modern-Scene-One_vids.html">
<img id="set-target-16041" alt="Modern Scene One" class="update_thumb thumbs" src="/thumbs/16041.jpg"/>
</a>
<div class="overlay-text">
Modern Scene One<br>
Starring:
<span class="update_models">
<a href="/models/alice.html">Alice</a>
</span><br>
</div>
</div>
<div class="grid-item">
<a href="/scenes/Modern-Scene-Two_vids.html">
<img id="set-target-16040" alt="Modern Scene Two" class="update_thumb thumbs" src="/thumbs/16040.jpg"/>
</a>
<div class="overlay-text">
Modern Scene Two<br>
Starring:
<span class="update_models">
<a href="/models/bob.html">Bob</a>
</span><br>
</div>
</div>`

const modernDetailFixture = `
<div class="movie_title">Modern Detail Title</div>
<div class="player-scene-description"><span style="font-weight: bold;">Starring:</span>
<span class="update_models">
<a href="/models/alice.html">Alice</a>, <a href="/models/bob.html">Bob</a>
</span>
</div>
<div class="player-scene-description"><span style="font-weight: bold;">Date:</span>
2025-04-30
</div>
<div class="player-scene-description"><span style="font-weight: bold;">Description:</span>
A modern description.
</div>
<span class="player-scene-description">
Tags:&nbsp;<a href="/categories/anal.html">Anal</a>, <a href="/categories/hardcore.html">Hardcore</a>
</span>`

// ========== Listing Tests ==========

func TestParseListingJJ(t *testing.T) {
	items := parseListingJJ([]byte(jjListingFixture), "https://test.local")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	s := items[0]
	if s.slug != "Scene-One" {
		t.Errorf("slug = %q", s.slug)
	}
	if s.title != "Scene One Title" {
		t.Errorf("title = %q", s.title)
	}
	if len(s.performers) != 2 || s.performers[0] != "Alice" {
		t.Errorf("performers = %v", s.performers)
	}
	if !s.date.Equal(time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", s.date)
	}
	if s.thumb != "/thumbs/100.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
}

func TestParseListingClassic(t *testing.T) {
	items := parseListingClassic([]byte(classicListingFixture), "https://test.local")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	s := items[0]
	if s.slug != "Classic-Scene-One" {
		t.Errorf("slug = %q", s.slug)
	}
	if s.title != "Classic Scene One" {
		t.Errorf("title = %q", s.title)
	}
	if len(s.performers) != 2 || s.performers[0] != "Alice" {
		t.Errorf("performers = %v", s.performers)
	}
	if !s.date.Equal(time.Date(2022, 9, 21, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", s.date)
	}
	if s.thumb != "/thumbs/13101.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
}

func TestParseListingModern(t *testing.T) {
	items := parseListingModern([]byte(modernListingFixture), "https://test.local")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	s := items[0]
	if s.slug != "Modern-Scene-One" {
		t.Errorf("slug = %q", s.slug)
	}
	if s.title != "Modern Scene One" {
		t.Errorf("title = %q", s.title)
	}
	if len(s.performers) != 1 || s.performers[0] != "Alice" {
		t.Errorf("performers = %v", s.performers)
	}
	if !s.date.IsZero() {
		t.Errorf("expected zero date on modern listing, got %v", s.date)
	}
	if s.thumb != "/thumbs/16041.jpg" {
		t.Errorf("thumb = %q", s.thumb)
	}
}

// ========== Detail Tests ==========

func TestParseDetailJJ(t *testing.T) {
	d := parseDetailJJ([]byte(jjDetailFixture))
	if d.title != "Detail Scene Title" {
		t.Errorf("title = %q", d.title)
	}
	if d.description != "A great scene description with & special chars." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 2 || d.tags[0] != "Anal" {
		t.Errorf("tags = %v", d.tags)
	}
	if len(d.performers) != 2 || d.performers[0] != "Alice" {
		t.Errorf("performers = %v", d.performers)
	}
	if !d.date.Equal(time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", d.date)
	}
}

func TestParseDetailClassic(t *testing.T) {
	d := parseDetailClassic([]byte(classicDetailFixture))
	if d.title != "Classic Detail Title" {
		t.Errorf("title = %q", d.title)
	}
	if d.description != "A classic description." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 2 || d.tags[0] != "Anal" {
		t.Errorf("tags = %v", d.tags)
	}
	if len(d.performers) != 2 || d.performers[0] != "Alice" {
		t.Errorf("performers = %v", d.performers)
	}
	if !d.date.Equal(time.Date(2022, 9, 21, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", d.date)
	}
	if d.series != "Test DVD" {
		t.Errorf("series = %q", d.series)
	}
}

func TestParseDetailClassicWithHR(t *testing.T) {
	d := parseDetailClassic([]byte(classicDetailWithHRFixture))
	if d.title != "Title With HR Tags" {
		t.Errorf("title = %q", d.title)
	}
	if !d.date.Equal(time.Date(2020, 7, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", d.date)
	}
	if len(d.tags) != 1 || d.tags[0] != "Lesbian" {
		t.Errorf("tags = %v", d.tags)
	}
}

func TestParseDetailModern(t *testing.T) {
	d := parseDetailModern([]byte(modernDetailFixture))
	if d.title != "Modern Detail Title" {
		t.Errorf("title = %q", d.title)
	}
	if d.description != "A modern description." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 2 || d.tags[0] != "Anal" {
		t.Errorf("tags = %v", d.tags)
	}
	if len(d.performers) != 2 || d.performers[0] != "Alice" {
		t.Errorf("performers = %v", d.performers)
	}
	if !d.date.Equal(time.Date(2025, 4, 30, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v", d.date)
	}
}

// ========== DVD Tests ==========

func TestParseDVDListingJJ(t *testing.T) {
	body := []byte(`
<a href="/dvds/DVD-One.html" class="dvd-listing-card">
<div class="dvd-listing-bar"><span class="dvd-listing-name">DVD One Title</span></div></a>
<a href="/dvds/DVD-Two.html" class="dvd-listing-card">
<div class="dvd-listing-bar"><span class="dvd-listing-name">DVD Two Title</span></div></a>`)

	entries := parseDVDListingJJ(body)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].name != "DVD One Title" {
		t.Errorf("name = %q", entries[0].name)
	}
}

func TestExtractDVDSceneURLs(t *testing.T) {
	body := []byte(`
<a href="/scenes/Scene-A_vids.html">Watch</a>
<a href="/scenes/Scene-A_vids.html"><img /></a>
<a href="/scenes/Scene-B_vids.html">Watch</a>`)

	urls := extractDVDSceneURLs(body)
	if len(urls) != 2 {
		t.Fatalf("got %d URLs, want 2", len(urls))
	}
}

// ========== Max Page Tests ==========

func TestExtractMaxPageJJ(t *testing.T) {
	body := []byte(`<a href="movies_1_d.html">1</a> <a href="movies_5_d.html">5</a> <a href="movies_3_d.html">3</a>`)
	if got := extractMaxPageJJ(body); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestExtractMaxPageClassic(t *testing.T) {
	body := []byte(`<div class="page_totals">Page 1 of 23</div>`)
	if got := extractMaxPageClassic(body); got != 23 {
		t.Errorf("got %d, want 23", got)
	}
}

// ========== End-to-End Tests ==========

const listingCardTpl = `<div class="jj-content-card">
<a href="%s/scenes/scene-%d_vids.html" class="jj-card-thumb">
<img id="set-target-%d" class="jj-thumb-img" src="/thumbs/%d.jpg" /></a>
<div class="jj-card-body">
<h2 class="jj-card-title">Scene %d</h2>
<div class="jj-card-meta">Starring: <span class="update_models">
<a href="/models/test.html">Test Model</a></span></div>
<div class="jj-card-date">Released: January 15, 2026</div>
</div></div>`

func buildListingPage(base string, ids []int) []byte {
	var sb string
	for _, id := range ids {
		sb += fmt.Sprintf(listingCardTpl, base, id, id, id, id)
	}
	return []byte(sb)
}

const detailTpl = `
<h1 class="scene-title">Detail Title</h1>
<div class="scene-meta">
<div class="meta-item"><div class="lbl">Starring</div>
<div class="val"><span class="update_models"><a href="/models/test.html">Test Model</a></span></div></div>
<div class="meta-item"><div class="lbl">Released</div>
<div class="val">January 15, 2026</div></div></div>
<div class="scene-desc">Test description.</div>
<div class="scene-cats"><a class="cat-tag">TestTag</a></div>`

func newTestServer(pages [][]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case r.URL.Path == "/models/test-model.html":
			_, _ = w.Write(buildListingPage("", pages[0]))
		case strings.HasPrefix(r.URL.Path, "/scenes/"):
			_, _ = fmt.Fprint(w, detailTpl)
		case r.URL.Path == "/dvds/dvds.html":
			_, _ = fmt.Fprint(w, `<a href="/dvds/test-dvd.html" class="dvd-listing-card">
<div class="dvd-listing-bar"><span class="dvd-listing-name">Test DVD</span></div></a>`)
		case r.URL.Path == "/dvds/test-dvd.html":
			_, _ = fmt.Fprint(w, `<a href="/scenes/dvd-scene-1_vids.html">Watch</a>
<a href="/scenes/dvd-scene-2_vids.html">Watch</a>`)
		default:
			pageNum := 0
			_, _ = fmt.Sscanf(r.URL.Path, "/categories/movies_%d_d.html", &pageNum)
			if pageNum == 0 {
				pageNum = 1
			}
			idx := pageNum - 1
			if idx >= 0 && idx < len(pages) {
				_, _ = w.Write(buildListingPage("", pages[idx]))
			} else {
				_, _ = fmt.Fprint(w, `<div>empty</div>`)
			}
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{
		client: ts.Client(),
		base:   ts.URL,
		cfg:    SiteConfig{SiteID: "test-jj", Domain: "test.local", StudioName: "Test Studio", Template: TemplateJJ},
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer([][]int{{100, 200}})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies.html", scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Description != "Test description." {
		t.Errorf("description = %q", results[0].Description)
	}
	if len(results[0].Tags) != 1 || results[0].Tags[0] != "TestTag" {
		t.Errorf("tags = %v", results[0].Tags)
	}
}

func TestListScenesKnownIDs(t *testing.T) {
	ts := newTestServer([][]int{{1, 2, 3, 4}})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/categories/movies.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"scene-3": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if !stoppedEarly {
		t.Error("expected StoppedEarly")
	}
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
}

func TestListScenesModelPage(t *testing.T) {
	ts := newTestServer([][]int{{10, 20, 30}})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/test-model.html", scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 3 {
		t.Fatalf("got %d scenes, want 3", len(results))
	}
}

func TestListScenesDVDMode(t *testing.T) {
	ts := newTestServer([][]int{{100}})
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/dvds/dvds.html", scraper.ListOpts{
		Delay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := testutil.CollectScenes(t, ch)
	if len(results) != 2 {
		t.Fatalf("got %d scenes, want 2", len(results))
	}
	if results[0].Series != "Test DVD" {
		t.Errorf("series = %q", results[0].Series)
	}
}

// TestListScenesCancelRace exercises the B1 worker-pool cancellation path: the
// producer goroutine sends to out (errors) and feeds the work channel while the
// caller cancels mid-stream. If the producer were not part of the WaitGroup,
// run() could close(out) while the producer is still selecting on a send to it,
// panicking on a closed channel. Run with -race to surface the data race too.
func TestListScenesCancelRace(t *testing.T) {
	// The server serves a full page 1 (so workers start sending to out), then
	// errors on every later page so the producer repeatedly takes its
	// `out <- scraper.Error(...)` branch — the exact send that races close(out)
	// when the producer is not in the WaitGroup.
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if strings.HasPrefix(r.URL.Path, "/scenes/") {
			_, _ = fmt.Fprint(w, detailTpl)
			return
		}
		page := 1
		_, _ = fmt.Sscanf(r.URL.Path, "/categories/movies_%d_d.html", &page)
		if page <= 1 {
			_, _ = w.Write(buildListingPage("", []int{1, 2, 3, 4, 5, 6, 7, 8}))
			return
		}
		atomic.AddInt32(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	for i := 0; i < 100; i++ {
		s := newTestScraper(ts)
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := s.ListScenes(ctx, ts.URL+"/categories/movies.html", scraper.ListOpts{
			Workers: 4,
		})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		// Read one result, then cancel while the producer is still walking pages
		// and taking its out<- error branch. With the producer outside the
		// WaitGroup this races close(out); the producer must be joined first.
		for range ch {
			cancel()
			break
		}
		// Drain to completion; the channel must close without panicking.
		for range ch {
		}
		cancel()
	}
	_ = atomic.LoadInt32(&hits)
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{SiteID: "julesjordan", Domain: "julesjordan.com", StudioName: "Jules Jordan"})
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.julesjordan.com/trial/", true},
		{"https://julesjordan.com/trial/categories/movies.html", true},
		{"https://example.com/julesjordan", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestBasePath(t *testing.T) {
	cases := []struct {
		name     string
		basePath string
		wantBase string
		wantPat0 string
	}{
		{"default trial", "", "https://www.julesjordan.com/trial", "julesjordan.com/trial/"},
		{"explicit trial", "/trial", "https://www.julesjordan.com/trial", "julesjordan.com/trial/"},
		{"root", "/", "https://www.julesjordan.com", "julesjordan.com/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(SiteConfig{SiteID: "x", Domain: "julesjordan.com", BasePath: c.basePath})
			if s.base != c.wantBase {
				t.Errorf("base = %q, want %q", s.base, c.wantBase)
			}
			if got := s.Patterns()[0]; got != c.wantPat0 {
				t.Errorf("Patterns()[0] = %q, want %q", got, c.wantPat0)
			}
		})
	}
}

// A DVD is a compilation: it reissues scenes that shipped on earlier discs, so
// the first stored scene turns up long before the newest disc is reached. The
// walk must skip it and keep discovering later DVDs.
func TestListScenesDVDModeKnownIDDoesNotAbortTheWalk(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/dvds/dvds.html":
			_, _ = fmt.Fprint(w, `<a href="/dvds/old.html" class="dvd-listing-card">
<div class="dvd-listing-bar"><span class="dvd-listing-name">Old DVD</span></div></a>
<a href="/dvds/new.html" class="dvd-listing-card">
<div class="dvd-listing-bar"><span class="dvd-listing-name">New DVD</span></div></a>`)
		case "/dvds/old.html":
			_, _ = fmt.Fprint(w, `<a href="/scenes/stored-scene_vids.html">Watch</a>`)
		case "/dvds/new.html":
			_, _ = fmt.Fprint(w, `<a href="/scenes/fresh-scene_vids.html">Watch</a>`)
		case "/dvds/dvds_page_2.html":
			_, _ = fmt.Fprint(w, `<div>empty</div>`)
		default:
			if strings.HasPrefix(r.URL.Path, "/scenes/") {
				_, _ = fmt.Fprint(w, detailTpl)
				return
			}
			t.Errorf("unexpected fetch %s", r.URL)
		}
	}))
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/dvds/dvds.html", scraper.ListOpts{
		KnownIDs: map[string]bool{"stored-scene": true},
		Delay:    time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	results, stoppedEarly := testutil.CollectScenesWithStop(t, ch)
	if len(results) != 1 {
		t.Fatalf("got %d scenes, want 1 — the later DVD must still be walked", len(results))
	}
	if results[0].ID != "fresh-scene" {
		t.Errorf("scene ID = %q, want fresh-scene", results[0].ID)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly to report the skipped scene")
	}
}

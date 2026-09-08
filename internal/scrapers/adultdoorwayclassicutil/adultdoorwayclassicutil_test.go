package adultdoorwayclassicutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// Fixtures derived from real blackpayback.com markup.

const listingHTML = `<html><body>

<!-- Flexslider banner — must NOT be parsed as a card -->
<div class="flexslider">
  <ul class="slides">
    <li><a href="https://blackpayback.com/tour/trailers/banner-only.html" title="banner">
      <img src="https://example.com/banner.jpg" />
    </a></li>
  </ul>
</div>

<div class="item-thumb">
  <a href="https://blackpayback.com/tour/trailers/extra-mayo.html" title="Extra Mayo">
    <img id="set-target-141" alt="Extra Mayo" class="mainThumb thumbs stdimage"
         src0_1x="https://cdn77.blackpayback.com/tour/content/contentthumbs/15/53/1553-1x.jpg"
         src0_2x="https://cdn77.blackpayback.com/tour/content/contentthumbs/15/53/1553-2x.jpg" />
  </a>
</div>
<div class="item-info clear">
  <h4><a href="https://blackpayback.com/tour/trailers/extra-mayo.html" title="Extra Mayo">Extra Mayo</a></h4>
</div>

<div class="item-thumb">
  <a href="https://blackpayback.com/tour/trailers/asian-persuasion.html" title="Asian Persuasion">
    <img id="set-target-140" alt="Asian Persuasion" class="mainThumb thumbs stdimage"
         src0_1x="https://cdn77.blackpayback.com/tour/content/contentthumbs/15/52/1552-1x.jpg" />
  </a>
</div>
<div class="item-info clear">
  <h4><a href="https://blackpayback.com/tour/trailers/asian-persuasion.html" title="Asian Persuasion">Asian Persuasion</a></h4>
</div>

<ul class="pagination">
  <li class="active"><a href="/tour/categories/movies/1/latest/">1</a></li>
  <li><a href="/tour/categories/movies/2/latest/">2</a></li>
  <li><a href="/tour/categories/movies/19/latest/">19</a></li>
</ul>
</body></html>`

const detailHTML = `<html><body>
<h1>Extra Mayo</h1>
<p>We had fun with this one. She's a kooky, quirky broad who is definitely hungry. Goldey put her on her knees and she went to boppin.</p>
<div class="videoInfo clear">
  <p>868&nbsp;Photos, 57&nbsp;min&nbsp;of&nbsp;video</p>
  <p><span>Rating:</span> 4.9/5.0</p>
</div>
<div class="featuring clear">
  <ul>
    <li class="label">Tags:</li>
    <li><a href="https://blackpayback.com/tour/categories/black-owned-business/1/latest/">Black Owned Business</a></li>
    <li><a href="https://blackpayback.com/tour/categories/blondes/1/latest/">Blondes</a></li>
    <li><a href="https://blackpayback.com/tour/categories/deep-throat/1/latest/">Deep Throat</a></li>
  </ul>
</div>
</body></html>`

const detailHTMLColonRuntime = `<html><body>
<h1>Long Form Scene</h1>
<p>Description here.</p>
<div class="videoInfo clear">
  <p>1200&nbsp;Photos, 01:02:47&nbsp;of&nbsp;video</p>
</div>
<div class="featuring clear">
  <ul>
    <li class="label">Tags:</li>
    <li><a href="https://blackpayback.com/tour/categories/anal/1/latest/">Anal</a></li>
  </ul>
</div>
</body></html>`

const emptyListingHTML = `<html><body><div class="content">No scenes.</div></body></html>`

func TestParseListing_skipsBannerFlexslider(t *testing.T) {
	items := parseListing([]byte(listingHTML))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (banner must be skipped)", len(items))
	}

	// Confirm "banner-only" was excluded.
	for _, it := range items {
		if it.id == "banner-only" {
			t.Error("flexslider banner was picked up as a card")
		}
	}

	first := items[0]
	if first.id != "extra-mayo" {
		t.Errorf("ID = %q, want extra-mayo", first.id)
	}
	if first.title != "Extra Mayo" {
		t.Errorf("Title = %q", first.title)
	}
	if first.url != "https://blackpayback.com/tour/trailers/extra-mayo.html" {
		t.Errorf("URL = %q", first.url)
	}
	wantThumb := "https://cdn77.blackpayback.com/tour/content/contentthumbs/15/53/1553-1x.jpg"
	if first.thumb != wantThumb {
		t.Errorf("Thumb = %q, want %q", first.thumb, wantThumb)
	}
}

func TestParseListing_dedupesRepeatedCards(t *testing.T) {
	doubled := listingHTML + listingHTML
	items := parseListing([]byte(doubled))
	if len(items) != 2 {
		t.Fatalf("dedup failed: got %d, want 2", len(items))
	}
}

func TestEnrichFromDetail(t *testing.T) {
	var item sceneItem
	item.id = "extra-mayo"
	item.title = "Extra Mayo"
	enrichFromDetail([]byte(detailHTML), &item)

	if item.title != "Extra Mayo" {
		t.Errorf("Title = %q", item.title)
	}
	if !strings.HasPrefix(item.description, "We had fun with this one") {
		t.Errorf("Description prefix wrong: %q", item.description)
	}
	// 57 minutes = 3420s
	if item.duration != 3420 {
		t.Errorf("Duration = %d, want 3420", item.duration)
	}
	want := []string{"Black Owned Business", "Blondes", "Deep Throat"}
	if len(item.tags) != len(want) {
		t.Fatalf("Tags = %v, want %v", item.tags, want)
	}
	for i, w := range want {
		if item.tags[i] != w {
			t.Errorf("Tags[%d] = %q, want %q", i, item.tags[i], w)
		}
	}
}

func TestEnrichFromDetail_colonRuntime(t *testing.T) {
	var item sceneItem
	enrichFromDetail([]byte(detailHTMLColonRuntime), &item)
	// 01:02:47 = 3767s
	if item.duration != 3767 {
		t.Errorf("Duration = %d, want 3767 (HH:MM:SS form)", item.duration)
	}
}

func TestEstimateTotal(t *testing.T) {
	got := estimateTotal([]byte(listingHTML), 2)
	if got != 38 {
		t.Errorf("estimateTotal = %d, want 38 (max page 19 × 2 items)", got)
	}
}

func TestParseStudioURL(t *testing.T) {
	tests := []struct {
		url      string
		wantMode listMode
		wantSlug string
	}{
		{"https://blackpayback.com/", modeFullCatalog, ""},
		{"https://blackpayback.com/tour/", modeFullCatalog, ""},
		{"https://blackpayback.com/tour/categories/movies/1/latest/", modeFullCatalog, ""},
		{"https://blackpayback.com/tour/categories/movies/5/latest/", modeFullCatalog, ""},
		{"https://blackpayback.com/tour/categories/blondes/1/latest/", modeCategory, "blondes"},
		{"https://blackpayback.com/tour/categories/deep-throat/3/latest/", modeCategory, "deep-throat"},
	}
	for _, c := range tests {
		t.Run(c.url, func(t *testing.T) {
			got := parseStudioURL(c.url)
			if got.mode != c.wantMode || got.slug != c.wantSlug {
				t.Errorf("got {mode=%d, slug=%q}, want {mode=%d, slug=%q}", got.mode, got.slug, c.wantMode, c.wantSlug)
			}
		})
	}
}

func TestListConfig_pageURL(t *testing.T) {
	tests := []struct {
		lc         listConfig
		tourPrefix string
		page       int
		want       string
	}{
		{listConfig{mode: modeFullCatalog}, "/tour", 1, "https://example.com/tour/categories/movies/1/latest/"},
		{listConfig{mode: modeFullCatalog}, "/tour", 19, "https://example.com/tour/categories/movies/19/latest/"},
		{listConfig{mode: modeCategory, slug: "blondes"}, "/tour", 2, "https://example.com/tour/categories/blondes/2/latest/"},
		// Sites without /tour/ prefix (babearchives-style).
		{listConfig{mode: modeFullCatalog}, "", 1, "https://example.com/categories/movies/1/latest/"},
		{listConfig{mode: modeCategory, slug: "blondes"}, "", 3, "https://example.com/categories/blondes/3/latest/"},
	}
	for _, c := range tests {
		got := c.lc.pageURL("https://example.com", c.tourPrefix, c.page)
		if got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{
		ID:       "blackpayback",
		SiteBase: "https://blackpayback.com",
		Studio:   "Black Payback",
		MatchRe:  regexp.MustCompile(`^https?://(?:[a-z0-9-]+\.)?blackpayback\.com`),
	})
	cases := []struct {
		url   string
		match bool
	}{
		{"https://blackpayback.com/tour/", true},
		{"https://www.blackpayback.com", true},
		{"https://t5m.blackpayback.com/track/...", true},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			if got := s.MatchesURL(c.url); got != c.match {
				t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
			}
		})
	}
}

// TestListScenes_endToEnd exercises the full flow against an in-process server.
func TestListScenes_endToEnd(t *testing.T) {
	// hits maps are written from multiple worker goroutines via the
	// httptest handler — guard with a mutex so -race stays clean.
	var (
		hitsMu  sync.Mutex
		listing = map[string]int{}
		detail  = map[string]int{}
	)

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Rewrite the absolute blackpayback.com URLs in the fixture to the
		// test server so the worker pool stays in-process.
		rewrite := func(s string) string {
			return strings.ReplaceAll(s, "https://blackpayback.com", ts.URL)
		}
		bump := func(m map[string]int, k string) {
			hitsMu.Lock()
			m[k]++
			hitsMu.Unlock()
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/tour/categories/movies/1/latest"):
			bump(listing, r.URL.Path)
			_, _ = fmt.Fprint(w, rewrite(listingHTML))
		case strings.HasPrefix(r.URL.Path, "/tour/categories/movies/"):
			bump(listing, r.URL.Path)
			_, _ = fmt.Fprint(w, emptyListingHTML)
		case strings.HasPrefix(r.URL.Path, "/tour/trailers/"):
			bump(detail, r.URL.Path)
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:         "blackpayback",
		SiteBase:   ts.URL,
		Studio:     "Black Payback",
		TourPrefix: "/tour",
		MatchRe:    regexp.MustCompile(`.*`),
	})

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}

	var (
		scenes   int
		titles   []string
		sawTotal bool
	)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			titles = append(titles, r.Scene.Title)
			if r.Scene.Description == "" {
				t.Errorf("scene %q has empty description — detail fetch didn't enrich", r.Scene.Title)
			}
			if r.Scene.Duration == 0 {
				t.Errorf("scene %q has zero duration", r.Scene.Title)
			}
			if len(r.Scene.Tags) == 0 {
				t.Errorf("scene %q has no tags", r.Scene.Title)
			}
		case scraper.KindTotal:
			sawTotal = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if scenes != 2 {
		t.Errorf("got %d scenes, want 2 (titles=%v)", scenes, titles)
	}
	if !sawTotal {
		t.Error("expected a Progress message")
	}
	hitsMu.Lock()
	detailHits := detail["/tour/trailers/extra-mayo.html"]
	hitsMu.Unlock()
	if detailHits == 0 {
		t.Error("detail page never fetched")
	}
}

func TestListScenes_knownIDsStopsEarly(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		rewrite := func(s string) string {
			return strings.ReplaceAll(s, "https://blackpayback.com", ts.URL)
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/tour/categories/movies/1/latest"):
			_, _ = fmt.Fprint(w, rewrite(listingHTML))
		case strings.HasPrefix(r.URL.Path, "/tour/trailers/"):
			_, _ = fmt.Fprint(w, detailHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:         "blackpayback",
		SiteBase:   ts.URL,
		Studio:     "Black Payback",
		TourPrefix: "/tour",
		MatchRe:    regexp.MustCompile(`.*`),
	})

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		Workers:  1,
		KnownIDs: map[string]bool{"asian-persuasion": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		scenes       int
		stoppedEarly bool
	)
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindStoppedEarly:
			stoppedEarly = true
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if scenes != 1 {
		t.Errorf("got %d scenes, want 1 (stopped before known ID)", scenes)
	}
	if !stoppedEarly {
		t.Error("expected StoppedEarly signal")
	}
}

// The CMS serves its database-error page with HTTP 200 and a short body. Left
// undetected it parses to zero scenes, so a site-side outage reads as an empty
// catalogue rather than a failure — which is exactly the shape that trips the
// destructive-save path on --full/--refresh.
func TestCheckCMSErrorDetectsTheDBOutagePage(t *testing.T) {
	outage := []byte(`<!-- AUTOSEO v2.0: API unreachable -->
<title>blackpayback.com - tour</title>
<span style="color:red;"><B>Error</B></span> connecting to database server: <b>stagingbp_ex@cs1913lan</b>.<br /><B>PDO Error Message</B>: SQLSTATE[HY000] [2002] Connection refused<br />`)

	if err := checkCMSError(outage); err == nil {
		t.Fatal("the DB-error page must be reported as an error, not parsed as an empty listing")
	}

	// A normal listing must not trip it.
	for _, ok := range [][]byte{
		[]byte(`<html><body><div class="update_details" data-setid="1">fine</div></body></html>`),
		[]byte(``),
		// A scene description mentioning a database is not an outage.
		[]byte(`<html><p>She works as a database administrator.</p></html>`),
	} {
		if err := checkCMSError(ok); err != nil {
			t.Errorf("false positive on %q: %v", ok, err)
		}
	}
}

// The separator between "of" and "video" is a plain space on some sites in
// this template and an &nbsp; on others. Requiring the entity lost the
// duration wherever a space appeared.
func TestDurationAcceptsMixedSeparators(t *testing.T) {
	cases := []struct {
		info string
		want int
	}{
		{`<div class="videoInfo clear"><p>868&nbsp;Photos, 57&nbsp;min&nbsp;of&nbsp;video</p></div>`, 57 * 60},
		{`<div class="videoInfo clear"><p>48&nbsp;min&nbsp;of video</p></div>`, 48 * 60},
		{`<div class="videoInfo clear"><p>30 min of video</p></div>`, 30 * 60},
	}
	for _, c := range cases {
		var item sceneItem
		enrichFromDetail([]byte(c.info), &item)
		if item.duration != c.want {
			t.Errorf("duration for %q = %d, want %d", c.info, item.duration, c.want)
		}
	}
}

// Dreamnet's build of the template titles the scene with an <h3> inside a
// videoDetails block and prints a publication date the Adult Doorway sites
// do not have.
func TestDreamnetDetailShape(t *testing.T) {
	body := []byte(`<div class="videoDetails clear">
		<h3>OMG 14 Loads on the Face</h3>
		<p>Cute petite blonde Charlie takes 14 facials!</p>
	</div>
	<div class="videoInfo clear">
		<p><span>Date Added:</span> August 12, 2026</p>
		<p>48&nbsp;min&nbsp;of video</p>
	</div>`)
	var item sceneItem
	enrichFromDetail(body, &item)
	if item.title != "OMG 14 Loads on the Face" {
		t.Errorf("title = %q", item.title)
	}
	if item.description != "Cute petite blonde Charlie takes 14 facials!" {
		t.Errorf("description = %q", item.description)
	}
	if item.date.Format("2006-01-02") != "2026-08-12" {
		t.Errorf("date = %v", item.date)
	}
	if item.duration != 48*60 {
		t.Errorf("duration = %d", item.duration)
	}
}

func TestAbsURL(t *testing.T) {
	cases := []struct{ base, in, want string }{
		{"https://www.blowbanggirls.com", "/v3/content/x.jpg", "https://www.blowbanggirls.com/v3/content/x.jpg"},
		{"https://blackpayback.com", "https://cdn77.blackpayback.com/x.jpg", "https://cdn77.blackpayback.com/x.jpg"},
		{"https://x.com", "", ""},
	}
	for _, c := range cases {
		if got := absURL(c.base, c.in); got != c.want {
			t.Errorf("absURL(%q,%q) = %q, want %q", c.base, c.in, got, c.want)
		}
	}
}

// Brand New Amateurs writes a trailing space inside the href attribute
// (`…​.html "`). Requiring the closing quote right after `.html` matched no
// card on that site at all.
func TestCardHrefWithTrailingSpace(t *testing.T) {
	body := []byte(`<div class="item-thumb">
		<a href="https://brandnewamateurs.com/trailers/Amanda-Feisty.html " title="Amanda Feisty!!" class="409vids">
			<img class="mainThumb thumbs stdimage" src0_1x="/content//contentthumbs/38/31/23831-1x.jpg" />
		</a>
	</div>`)
	items := parseListing(body)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].id != "Amanda-Feisty" {
		t.Errorf("id = %q", items[0].id)
	}
	if items[0].url != "https://brandnewamateurs.com/trailers/Amanda-Feisty.html" {
		t.Errorf("url = %q — the trailing space must not survive into the URL", items[0].url)
	}
	if items[0].title != "Amanda Feisty!!" {
		t.Errorf("title = %q", items[0].title)
	}
}

// Dreamnet's slugs are mixed case; a lowercase-only pattern matched no card.
func TestCardSlugIsCaseInsensitive(t *testing.T) {
	body := []byte(`<div class="item-thumb"><a href="https://www.blowbanggirls.com/v3/trailers/OMG-14-Loads.html" title="OMG 14 Loads"><img src0_1x="/v3/content/x-1x.jpg" /></a></div>`)
	items := parseListing(body)
	if len(items) != 1 || items[0].id != "OMG-14-Loads" {
		t.Fatalf("items = %+v", items)
	}
}

// The listing has no page count to end on, and parseListing deduplicates only
// within a page. An origin that clamps `?page=N` back to page 1 — or a tour
// that repeats its last page rather than 404ing — used to loop forever,
// appending the same cards until the process ran out of memory.
func TestCollectListingStopsWhenPagingIsClamped(t *testing.T) {
	var mu sync.Mutex
	var pages int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		mu.Lock()
		pages++
		mu.Unlock()
		// Every page is page 1, forever.
		_, _ = fmt.Fprint(w, listingHTML)
	}))
	defer ts.Close()

	s := New(SiteConfig{
		ID:       "blackpayback",
		SiteBase: ts.URL,
		Studio:   "Black Payback",
		Patterns: []string{"blackpayback.com"},
		MatchRe:  regexp.MustCompile(`.*`),
	})

	out := make(chan scraper.SceneResult, 200)
	done := make(chan struct{})
	var items []sceneItem
	go func() {
		defer close(done)
		items, _ = s.collectListing(context.Background(), parseStudioURL(ts.URL), scraper.ListOpts{}, out)
		close(out)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("collectListing did not terminate against a clamped pager")
	}
	for range out {
	}

	want := len(parseListing([]byte(listingHTML)))
	if len(items) != want {
		t.Errorf("collected %d items, want the %d distinct cards once each", len(items), want)
	}
	mu.Lock()
	defer mu.Unlock()
	if pages > 2 {
		t.Errorf("fetched %d listing pages, want the walk to stop on the first repeat", pages)
	}
}

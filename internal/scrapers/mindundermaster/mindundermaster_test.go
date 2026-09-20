package mindundermaster

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func cardHTML(slug, id, title, thumb, text string) string {
	return fmt.Sprintf(`<!-- item -->
<a href="https://www.mindundermaster.com/video/%s-%s.html" title="%s" class="videos__element">
    <div class="videos__thumbnail">
        <img data-mb="shuffle-thumbs" src="%s" alt="%s" >
        <i class="fa-regular fa-circle-play"></i>
    </div>
    <div class="videos__wrapper">
        <h2 class="videos__title">%s</h2>
        <p class="videos__text">
            %s        </p>
    </div>
</a>
<!-- item END -->`, slug, id, title, thumb, title, title, text)
}

const detailPage = `<html><body>
<div class="video__wrapper">
    <h1 class="video__title">Magic Ring - Payton Preslee</h1>
    <div class="video__tags">
        <span>Tags:</span>
        <a href="/search/home-wrecker/" title="Home Wrecker" class="tag">Home Wrecker</a>, <a href="/search/titjob/" title="Titjob" class="tag">Titjob</a>
    </div>
    <p class="video__desc">
        The full description &amp; then some.<br />
More of it.
    </p>
    <div class="video__tokens">
        <span class="quantity">Price:</span>
        <span>$19.99</span>
    </div>
    <a href="/login" class="video__buy">ADD TO CART</a>
</div>
<div class="video__categories">
    <h3 class="video__catTitle">POPULAR CATEGORIES</h3>
    <a href="/channels/78/asmr/" class="video__category">ASMR</a>
</div>
</body></html>`

const rssFeedXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<item>
  <title><![CDATA[ Sorority Harem ]]></title>
  <link>https://www.mindundermaster.com/video/sorority-harem-342.html</link>
  <pubDate>Fri, 24 Jul 2026 05:28:18 EDT</pubDate>
</item>
<item>
  <title><![CDATA[ A Gallery ]]></title>
  <link>https://www.mindundermaster.com/galleries/some-gallery-12.html</link>
  <pubDate>Fri, 7 Aug 2026 03:38:13 EDT</pubDate>
</item>
</channel></rss>`

func modelPage(name string, cards ...string) string {
	return `<html><body><section class="videos">
<div class="videos__top"><h1 class="videos__topTitle">` + name + `</h1></div>
<section class="videos"><div class="videos__top"><h2 class="videos__topTitle">` + name + ` videos</h2></div>
<div class="videos__container">` + strings.Join(cards, "\n") + `</div></section>
</section></body></html>`
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	listing := `<html><body><div class="videos__container">` +
		cardHTML("sorority-harem", "342", "Sorority Harem", "https://cdn.example.com/342.jpg", "The sorority&#39;s new house.") +
		cardHTML("magic-ring-payton-preslee", "267", "Magic Ring - Payton Preslee", "https://cdn.example.com/267.jpg", "Holden &amp; the ring.") +
		`</div>
<a href="page2.html">2</a></body></html>`

	empty := `<html><body><div class="videos__container"></div></body></html>`

	modelIndex := `<html><body>
<a href="https://www.mindundermaster.com/models/vienna-rose-90.html">Vienna Rose</a>
<a href="https://www.mindundermaster.com/models/payton-preslee-91.html">Payton Preslee</a>
<a href="https://www.mindundermaster.com/models/page2.html">2</a>
</body></html>`

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos/page1.html":
			_, _ = fmt.Fprint(w, listing)
		case "/rss":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, rssFeedXML)
		case "/models/page1.html":
			_, _ = fmt.Fprint(w, modelIndex)
		case "/models/vienna-rose-90.html":
			_, _ = fmt.Fprint(w, modelPage("Vienna Rose",
				cardHTML("sorority-harem", "342", "Sorority Harem", "t.jpg", "x")))
		case "/models/payton-preslee-91.html":
			_, _ = fmt.Fprint(w, modelPage("Payton Preslee",
				cardHTML("magic-ring-payton-preslee", "267", "Magic Ring - Payton Preslee", "t.jpg", "x"),
				cardHTML("sorority-harem", "342", "Sorority Harem", "t.jpg", "x")))
		case "/video/sorority-harem-342.html", "/video/magic-ring-payton-preslee-267.html":
			_, _ = fmt.Fprint(w, detailPage)
		case "/videos/page2.html", "/models/page2.html":
			_, _ = fmt.Fprint(w, empty)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestScraper(ts *httptest.Server) *Scraper {
	return &Scraper{client: ts.Client(), base: ts.URL}
}

func collect(ch <-chan scraper.SceneResult) (scenes []models.Scene, errs int, stopped int) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs++
		case scraper.KindStoppedEarly:
			stopped++
		}
	}
	return scenes, errs, stopped
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.mindundermaster.com", true},
		{"https://mindundermaster.com/videos/page2.html", true},
		{"http://www.mindundermaster.com/channels/78/asmr/", true},
		{"https://www.mindundermaster.com.evil.com/", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestResolveMode(t *testing.T) {
	tests := []struct {
		url    string
		prefix string
		single bool
	}{
		{"https://www.mindundermaster.com/", "/videos/", false},
		{"https://www.mindundermaster.com", "/videos/", false},
		{"https://www.mindundermaster.com/videos/page3.html", "/videos/", false},
		{"https://www.mindundermaster.com/channels/78/asmr/", "/channels/78/asmr/", false},
		{"https://www.mindundermaster.com/channels/78/asmr/page2.html", "/channels/78/asmr/", false},
		{"https://www.mindundermaster.com/search/stripper/", "/search/stripper/", false},
		{"https://www.mindundermaster.com/models/vienna-rose-90.html", "/models/vienna-rose-90.html", true},
	}
	for _, tt := range tests {
		got := resolveMode(tt.url)
		if got.prefix != tt.prefix || got.single != tt.single {
			t.Errorf("resolveMode(%q) = %+v, want prefix %q single %v", tt.url, got, tt.prefix, tt.single)
		}
	}
}

func TestParseListing(t *testing.T) {
	page := cardHTML("sorority-harem", "342", "Sorority Harem &amp; Friends",
		"https://cdn.example.com/342.jpg", "A &quot;quoted&quot; blurb &amp; more.")
	cards := parseListing(page)
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	c := cards[0]
	if c.id != "342" {
		t.Errorf("id = %q, want 342", c.id)
	}
	if c.path != "/video/sorority-harem-342.html" {
		t.Errorf("path = %q", c.path)
	}
	if c.title != "Sorority Harem & Friends" {
		t.Errorf("title = %q", c.title)
	}
	if c.thumb != "https://cdn.example.com/342.jpg" {
		t.Errorf("thumb = %q", c.thumb)
	}
	if c.text != `A "quoted" blurb & more.` {
		t.Errorf("text = %q", c.text)
	}
}

// Adjacent cards are separate anchors; a terminator-consuming pattern would
// drop every second one.
func TestParseListingKeepsAdjacentCards(t *testing.T) {
	page := cardHTML("one", "1", "One", "a.jpg", "first") +
		cardHTML("two", "2", "Two", "b.jpg", "second") +
		cardHTML("three", "3", "Three", "c.jpg", "third")
	cards := parseListing(page)
	if len(cards) != 3 {
		t.Fatalf("got %d cards, want 3", len(cards))
	}
	for i, want := range []string{"1", "2", "3"} {
		if cards[i].id != want {
			t.Errorf("card %d id = %q, want %q", i, cards[i].id, want)
		}
	}
}

func TestApplyDetail(t *testing.T) {
	sc := models.Scene{Description: "truncated listing blurb"}
	applyDetail(&sc, detailPage)

	if len(sc.Tags) != 2 || sc.Tags[0] != "Home Wrecker" || sc.Tags[1] != "Titjob" {
		t.Errorf("tags = %v", sc.Tags)
	}
	if sc.Description != "The full description & then some. More of it." {
		t.Errorf("description = %q", sc.Description)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 19.99 {
		t.Errorf("price history = %v", sc.PriceHistory)
	}
}

// The "POPULAR CATEGORIES" block is site chrome, not scene metadata.
func TestApplyDetailIgnoresPopularCategories(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, detailPage)
	if len(sc.Categories) != 0 {
		t.Errorf("categories = %v, want none", sc.Categories)
	}
}

func TestListScenes(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}

	harem, ok := byID["342"]
	if !ok {
		t.Fatal("scene 342 missing")
	}
	if harem.Studio != studioName || harem.SiteID != siteID {
		t.Errorf("studio/site = %q/%q", harem.Studio, harem.SiteID)
	}
	if !strings.HasPrefix(harem.URL, ts.URL) {
		t.Errorf("URL = %q, want the test server host", harem.URL)
	}
	if harem.Date.Format("2006-01-02") != "2026-07-24" {
		t.Errorf("date = %v, want 2026-07-24", harem.Date)
	}
	if len(harem.Performers) != 2 || harem.Performers[0] != "Payton Preslee" || harem.Performers[1] != "Vienna Rose" {
		t.Errorf("performers = %v", harem.Performers)
	}
	if len(harem.Tags) != 2 {
		t.Errorf("tags = %v", harem.Tags)
	}
	if harem.Thumbnail != "https://cdn.example.com/342.jpg" {
		t.Errorf("thumbnail = %q", harem.Thumbnail)
	}

	ring := byID["267"]
	if len(ring.Performers) != 1 || ring.Performers[0] != "Payton Preslee" {
		t.Errorf("267 performers = %v", ring.Performers)
	}
	// Absent from the feed, so it carries no date rather than a wrong one.
	if !ring.Date.IsZero() {
		t.Errorf("267 date = %v, want zero", ring.Date)
	}
}

func TestListScenesModelPage(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL+"/models/payton-preslee-91.html", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collect(ch)
	if errs != 0 {
		t.Errorf("got %d errors, want 0", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
}

func TestKnownIDsStopEarly(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := newTestScraper(ts)
	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"267": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, stopped := collect(ch)
	if len(scenes) != 1 || scenes[0].ID != "342" {
		t.Errorf("scenes = %v, want only 342", scenes)
	}
	if stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
}

// An incremental run whose first page is entirely known must not pay for the
// RSS feed or the ~100-page model directory.
func TestKnownFirstPageSkipsIndexes(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	var mu sync.Mutex
	extra := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rss" || strings.HasPrefix(r.URL.Path, "/models/") {
			mu.Lock()
			extra++
			mu.Unlock()
		}
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, ts.URL+r.URL.RequestURI(), nil)
		resp, err := ts.Client().Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	s := newTestScraper(proxy)
	ch, err := s.ListScenes(context.Background(), proxy.URL, scraper.ListOpts{
		KnownIDs: map[string]bool{"342": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, stopped := collect(ch); stopped != 1 {
		t.Errorf("got %d stoppedEarly, want 1", stopped)
	}
	mu.Lock()
	defer mu.Unlock()
	if extra != 0 {
		t.Errorf("fetched %d index pages, want 0", extra)
	}
}

// The model directory's pager links live in the same directory as the models.
func TestModelPathsSkipsPagerLinks(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := newTestScraper(ts)
	paths := s.modelPaths(context.Background(), scraper.ListOpts{})
	want := []string{"/models/vienna-rose-90.html", "/models/payton-preslee-91.html"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestFetchDatesSkipsNonVideoItems(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	s := newTestScraper(ts)
	dates := s.fetchDates(context.Background())
	if len(dates) != 1 {
		t.Fatalf("dates = %v, want one entry", dates)
	}
	if got := dates["342"].Format("2006-01-02"); got != "2026-07-24" {
		t.Errorf("date = %s, want 2026-07-24", got)
	}
}

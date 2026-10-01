package treasureislandmedia

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

// ---- fixtures ----

func listingHTML() string {
	return `<html><body>
<div class="view-content">
  <div class="card"><a href="/scenes/jeff-carvalho-avatar">Jeff Carvalho &amp; Avatar</a></div>
  <div class="card"><a href="/scenes/the-cum-union">The Cum Union</a></div>
  <div class="card"><a href="/scenes/jeff-carvalho-avatar">dup link</a></div>
  <a href="/scenes?channel=All&amp;page=2">next</a>
</div>
</body></html>`
}

func emptyListingHTML() string {
	return `<html><body><div class="view-content"><p>No results</p></div></body></html>`
}

// detailHTML builds a scene detail page in the live Next.js markup: the
// scene id in the player's RSC payload, OpenGraph tags, a VideoObject with the
// release date, and the sub-brand label in the header brand block.
func detailHTML(brand, slug, title, sceneID, uploaded, desc string) string {
	return fmt.Sprintf(`<html><head>
<meta property="og:title" content="%[3]s"/>
<meta property="og:description" content="%[6]s…"/>
<meta property="og:url" content="https://treasureislandmedia.com/scenes/%[2]s"/>
<meta property="og:image" content="https://assets.treasureislandmedia.com/sites/default/files/scenes/images/sliders/1284848.jpg"/>
</head><body>
<a class="to-brandblock" href="/"><span class="to-brandblock__lines"><span class="to-brandblock__label">%[1]s</span><span class="to-brandblock__sub">Treasure Island Media</span></span></a>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"VideoObject","name":"%[3]s","uploadDate":"%[5]s"}</script>
<h1 class="to-scene__title">%[3]s</h1>
<div class="to-caststrip"><span class="to-caststrip__name">Joe Silver</span><span class="to-caststrip__name">Matt Coven</span></div>
<div class="to-meta"><span class="to-chip to-chip--fact">18<!-- --> min</span>
<div class="to-meta__row"><span class="to-meta__label">Directors:</span><span class="to-meta__chips"><a class="to-chip" href="/director/elliott-wilder">Elliott Wilder</a></span></div>
<div class="to-meta__row"><span class="to-meta__label">Tags:</span><span class="to-meta__chips"><a class="to-chip" href="/tag/sucking">Sucking</a><a class="to-chip" href="/tag/flip">Flip</a></span></div></div>
<section id="description"><div class="richtext"><div class="payload-richtext"><p>%[6]s <a href="https://men.treasureislandmedia.com/men/1">JOE</a> more.</p></div></div></section>
<script>self.__next_f.push([1,"{\"sceneId\":%[4]s,\"contentId\":%[4]s}"])</script>
</body></html>`, brand, slug, title, sceneID, uploaded, desc)
}

// ---- TestMatchesURL ----

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://treasureislandmedia.com/scenes?channel=All&page=1", true},
		{"https://timfuck.treasureislandmedia.com/scenes/jeff-carvalho-avatar", true},
		{"http://www.treasureislandmedia.com/scenes/foo", true},
		{"https://example.com/x", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// ---- TestBrandFromURL ----

func TestBrandFromURL(t *testing.T) {
	cases := []struct {
		url       string
		wantID    string
		wantStudp string
	}{
		{"https://timfuck.treasureislandmedia.com/scenes/x", "timfuck", "TIM Fuck"},
		{"https://timsuck.treasureislandmedia.com/scenes/x", "timsuck", "TIM Suck"},
		{"https://ghr.treasureislandmedia.com/scenes/x", "ghr", "Grindhouse Raw"},
		{"https://treasureislandmedia.com/scenes/x", siteID, studioName},
		{"https://www.treasureislandmedia.com/scenes/x", siteID, studioName},
		{"https://newbrand.treasureislandmedia.com/scenes/x", "newbrand", "newbrand"},
		{"https://example.com/x", "", ""},
	}
	for _, c := range cases {
		id, name := brandFromURL(c.url)
		if id != c.wantID || name != c.wantStudp {
			t.Errorf("brandFromURL(%q) = (%q,%q), want (%q,%q)", c.url, id, name, c.wantID, c.wantStudp)
		}
	}
}

// ---- TestFetchListing ----

func TestFetchListing(t *testing.T) {
	orig := baseURL
	defer func() { baseURL = orig }()
	baseURL = "https://treasureislandmedia.com"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, listingHTML())
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client()}
	urls, err := s.fetchListing(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("fetchListing error: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("got %d urls, want 2 (dup dropped): %v", len(urls), urls)
	}
	if urls[0] != "https://treasureislandmedia.com/scenes/jeff-carvalho-avatar" ||
		urls[1] != "https://treasureislandmedia.com/scenes/the-cum-union" {
		t.Errorf("urls = %v", urls)
	}
}

// ---- TestToScene ----

func TestToScene(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, detailHTML("TIMFUCK", "jeff-carvalho-avatar",
			"JEFF CARVALHO &amp; AVATAR", "7970", "2026-06-20T07:00:00.000Z",
			"A full synopsis of the scene."))
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client()}
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	sc, err := s.toScene(context.Background(), "studioURL", ts.URL+"/scenes/jeff-carvalho-avatar", now)
	if err != nil {
		t.Fatalf("toScene: %v", err)
	}

	if sc.ID != "7970" {
		t.Errorf("ID = %q, want 7970", sc.ID)
	}
	if sc.Title != "JEFF CARVALHO & AVATAR" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.SiteID != "timfuck" || sc.Studio != "TIM Fuck" {
		t.Errorf("brand = (%q,%q), want (timfuck,TIM Fuck)", sc.SiteID, sc.Studio)
	}
	if sc.Description != "A full synopsis of the scene. JOE more." {
		t.Errorf("Description = %q, want the untruncated richtext", sc.Description)
	}
	if !strings.HasSuffix(sc.Thumbnail, "/sliders/1284848.jpg") {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != "https://treasureislandmedia.com/scenes/jeff-carvalho-avatar" {
		t.Errorf("URL = %q", sc.URL)
	}
	wantDate := time.Date(2026, 6, 20, 7, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", sc.Date, wantDate)
	}
	if sc.Director != "Elliott Wilder" {
		t.Errorf("Director = %q, want Elliott Wilder", sc.Director)
	}
	if sc.Duration != 18*60 {
		t.Errorf("Duration = %d", sc.Duration)
	}
	if strings.Join(sc.Tags, ",") != "Sucking,Flip" {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if strings.Join(sc.Performers, ",") != "Joe Silver,Matt Coven" {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

// A page without the scene id must surface as a parse failure, not vanish:
// that silent drop is how two image-directory moves emptied the catalogue.
func TestToSceneWithoutIDIsAParseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><head><meta property="og:title" content="X"/></head></html>`)
	}))
	defer ts.Close()

	s := &Scraper{Client: ts.Client()}
	_, err := s.toScene(context.Background(), "studioURL", ts.URL+"/scenes/x", time.Now())
	if err == nil {
		t.Fatal("want an error")
	}
	if k := scraper.Classify(err); k != scraper.FailureParse {
		t.Errorf("kind = %v, want FailureParse", k)
	}
}

func TestBrandFromLabel(t *testing.T) {
	cases := map[string]string{
		"TIMFUCK":      "timfuck",
		"TIM CLASSICS": "classics",
		"LATIN LOADS":  "latinloads",
		"BRUTHALOAD":   "bruthaload",
		"Paul Morris":  "",
	}
	for label, want := range cases {
		if got, _ := brandFromLabel(label); got != want {
			t.Errorf("brandFromLabel(%q) = %q, want %q", label, got, want)
		}
	}
}

// ---- TestListScenes (end-to-end, two sub-brand hosts) ----

func TestListScenes(t *testing.T) {
	orig := baseURL
	defer func() { baseURL = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/scenes":
			if r.URL.Query().Get("page") == "1" {
				_, _ = fmt.Fprint(w, listingHTML())
			} else {
				_, _ = fmt.Fprint(w, emptyListingHTML())
			}
		case "/scenes/jeff-carvalho-avatar":
			_, _ = fmt.Fprint(w, detailHTML("TIMFUCK", "jeff-carvalho-avatar",
				"Jeff Carvalho &amp; Avatar", "1277836", "2026-06-20T07:00:00.000Z", "Synopsis one."))
		case "/scenes/the-cum-union":
			_, _ = fmt.Fprint(w, detailHTML("TIMSUCK", "the-cum-union",
				"The Cum Union", "998877", "2026-05-01T07:00:00.000Z", "Synopsis two."))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	baseURL = ts.URL

	s := &Scraper{Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "studioURL", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}
	got := map[string]string{}
	site := map[string]string{}
	for r := range ch {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
			continue
		}
		if r.Kind == scraper.KindScene {
			got[r.Scene.ID] = r.Scene.Title
			site[r.Scene.ID] = r.Scene.SiteID
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2: %v", len(got), got)
	}
	if got["1277836"] != "Jeff Carvalho & Avatar" || got["998877"] != "The Cum Union" {
		t.Errorf("scenes = %v", got)
	}
	if site["1277836"] != "timfuck" || site["998877"] != "timsuck" {
		t.Errorf("siteIDs = %v", site)
	}
}

// The 2026 rebuild changed several things at once: the grid's scene links
// gained a query string, and the cast, director and runtime moved into new
// markup. Each one alone silently emptied the catalogue or a field.
func TestParsesTheRebuiltMarkup(t *testing.T) {
	const grid = `<a class="to-flip__art-link" href="/scenes/ro-d-luna-john-cortes-albert?from=grid&amp;n=1&amp;sort=released">` +
		`<img src="/_next/image?url=x"/></a>` +
		`<a href="/scenes?channel=All&amp;page=2">2</a>`

	m := sceneLinkRe.FindAllStringSubmatch(grid, -1)
	if len(m) != 1 {
		t.Fatalf("got %d scene links, want 1: %v", len(m), m)
	}
	if m[0][1] != "/scenes/ro-d-luna-john-cortes-albert" {
		t.Errorf("captured %q, want the path without its query", m[0][1])
	}

	const detail = `<div class="to-caststrip"><p class="to-caststrip__label">Starring</p>` +
		`<span class="to-caststrip__name">Ro D. Luna</span>` +
		`<span class="to-caststrip__name">John Cortes</span></div>` +
		`<div class="to-meta"><div class="to-meta__runtime"><span class="to-chip to-chip--fact">18<!-- --> min</span></div>` +
		`<div class="to-meta__row"><span class="to-meta__label">Directors:</span>` +
		`<span class="to-meta__chips"><a class="to-chip" href="/director/mecos">Mecos</a></span></div></div>`

	names := castStripNames.FindAllStringSubmatch(detail, -1)
	if len(names) != 2 || names[0][1] != "Ro D. Luna" || names[1][1] != "John Cortes" {
		t.Errorf("cast = %v", names)
	}
	d := metaDirectorRe.FindStringSubmatch(detail)
	if d == nil {
		t.Fatal("no director row")
	}
	if c := chipTextRe.FindStringSubmatch(d[1]); c == nil || c[1] != "Mecos" {
		t.Errorf("director = %v", c)
	}
	if r := metaRuntimeRe.FindStringSubmatch(detail); r == nil || r[1] != "18" {
		t.Errorf("runtime = %v", r)
	}
}

// A first listing page with no scene links is a broken parser, not an empty
// catalogue, and must say so.
func TestEmptyFirstPageIsAParseError(t *testing.T) {
	orig := baseURL
	defer func() { baseURL = orig }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, emptyListingHTML())
	}))
	defer ts.Close()
	baseURL = ts.URL

	s := &Scraper{Client: ts.Client()}
	ch, err := s.ListScenes(context.Background(), "studioURL", scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []scraper.FailureKind
	for r := range ch {
		if r.Kind == scraper.KindError {
			kinds = append(kinds, scraper.Classify(r.Err))
		}
	}
	if len(kinds) != 1 || kinds[0] != scraper.FailureParse {
		t.Errorf("error kinds = %v, want one FailureParse", kinds)
	}
}

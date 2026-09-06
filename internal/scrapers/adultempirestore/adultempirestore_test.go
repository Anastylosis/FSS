package adultempirestore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteConfigsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range sites {
		if seen[c.SiteID] {
			t.Errorf("duplicate site id %q", c.SiteID)
		}
		seen[c.SiteID] = true
		if c.Host == "" || c.StudioName == "" || len(c.Studios) == 0 {
			t.Errorf("%s: incomplete config %+v", c.SiteID, c)
		}
		for _, f := range c.Studios {
			if f.ID == "" || f.Name == "" {
				t.Errorf("%s: incomplete studio filter %+v", c.SiteID, f)
			}
		}
	}
}

func TestMatchesURL(t *testing.T) {
	fff := New(sites[0])
	rm := New(sites[1])
	cases := []struct {
		s    *Scraper
		url  string
		want bool
	}{
		{fff, "https://www.forbiddenfruitsfilms.com/", true},
		{fff, "https://forbiddenfruitsfilms.com/shop-streaming-video-by-scene.html?studio=93785", true},
		{fff, "https://rodneymoorestore.com/", false},
		{fff, "https://forbiddenfruitsfilms.com.evil.org/", false},
		{rm, "https://rodneymoorestore.com/", true},
		{rm, "https://www.forbiddenfruitsfilms.com/", false},
	}
	for _, c := range cases {
		if got := c.s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s.MatchesURL(%q) = %v, want %v", c.s.ID(), c.url, got, c.want)
		}
	}
}

// A white-label store sells third-party titles too — Forbidden Fruits Films'
// unfiltered scene listing carries Xprime scenes — so a bare site URL must
// still walk the store's own studio filters, never the unfiltered listing.
func TestFiltersForAlwaysFilters(t *testing.T) {
	s := New(sites[1]) // rodneymoore, two filters

	if got := s.filtersFor("https://rodneymoorestore.com/"); len(got) != 2 {
		t.Errorf("bare URL resolved to %d filters, want both", len(got))
	}

	got := s.filtersFor("https://rodneymoorestore.com/shop-streaming-video-by-scene.html?studio=94085")
	if len(got) != 1 || got[0].Name != "Rodney Moore Clips" {
		t.Errorf("explicit studio URL resolved to %+v", got)
	}

	// A studio id the config does not name is still honoured — the store may
	// have added a sub-brand — but it is labelled with the site's studio name
	// rather than invented.
	got = s.filtersFor("https://rodneymoorestore.com/shop-streaming-video-by-scene.html?studio=99999")
	if len(got) != 1 || got[0].ID != "99999" || got[0].Name != "Rodney Moore" {
		t.Errorf("unknown studio id resolved to %+v", got)
	}

	if got := s.filtersFor("https://rodneymoorestore.com/?studio=not-a-number"); len(got) != 2 {
		t.Errorf("a non-numeric studio param must fall back to every filter, got %+v", got)
	}
}

func TestListingURL(t *testing.T) {
	s := New(sites[0])
	s.base = "https://example.com"
	if got := s.listingURL("93785", 1); got != "https://example.com/shop-streaming-video-by-scene.html?studio=93785" {
		t.Errorf("page 1 = %q", got)
	}
	if got := s.listingURL("93785", 3); got != "https://example.com/shop-streaming-video-by-scene.html?page=3&studio=93785" {
		t.Errorf("page 3 = %q", got)
	}
}

func TestLastPage(t *testing.T) {
	body := []byte(`<ul class="pagination">
		<li><a href="?studio=93785" title="Go To Page 1">1</a></li>
		<li><a href="?page=2&amp;studio=93785" title="Go To Page 2">2</a></li>
		<li><a href="?page=14&amp;studio=93785" title="Go To Page 14">14</a></li>
	</ul>`)
	if got := lastPage(body); got != 14 {
		t.Errorf("lastPage = %d, want 14", got)
	}
	if got := lastPage([]byte(`<div>no pager here</div>`)); got != 1 {
		t.Errorf("lastPage = %d, want 1 for an unpaged listing", got)
	}
}

func card(id, master, title, perf, href string, mins int) string {
	return fmt.Sprintf(`<div class="grid-item" id="ascene_%s">
	<article class="scene-widget store-view" data-scene-id="%s" data-master-id="%s">
	<div class="scene-preview-container"><a class="scene-img" href="%s">
	<img class="screenshot img-full-fluid" id="scene_%s"
	  src="https://caps1cdn.adultempire.com/10/%s_150.jpg"
	  data-src="https://caps1cdn.adultempire.com/200/%s_150.jpg" alt="x"/></a></div>
	<div class="scene-info-container"><div class="scene-primary-details">
	<a class="scene-title" href="%s"><h6> %s </h6></a></div>
	<div class="scene-secondary-details">
	<p class="scene-performer-names"> %s </p><p class="scene-length"> %d min </p>
	</div></div></article></div>`, id, id, master, href, id, master, master, href, title, perf, mins)
}

func TestParseCard(t *testing.T) {
	c := card("1758824", "5008408", "Busty Blonde MILF Jodi West Cant Resist Her Stepson&#39;s BBC",
		"Jodi West, Cadence Lux, Jodi West", "/shop/1758824/x-streaming-scene-video.html", 18)

	ref, ok := parseCard(c, "https://www.forbiddenfruitsfilms.com", "Forbidden Fruits Films")
	if !ok {
		t.Fatal("card did not parse")
	}
	if ref.id != "1758824" {
		t.Errorf("id = %q", ref.id)
	}
	if ref.title != "Busty Blonde MILF Jodi West Cant Resist Her Stepson's BBC" {
		t.Errorf("title = %q", ref.title)
	}
	if ref.url != "https://www.forbiddenfruitsfilms.com/shop/1758824/x-streaming-scene-video.html" {
		t.Errorf("url = %q", ref.url)
	}
	if ref.thumbnail != "https://caps1cdn.adultempire.com/200/5008408_150.jpg" {
		t.Errorf("thumbnail = %q", ref.thumbnail)
	}
	if !slices.Equal(ref.performers, []string{"Jodi West", "Cadence Lux"}) {
		t.Errorf("performers = %v — a repeated name must not be stored twice", ref.performers)
	}
	if ref.duration != 18*60 {
		t.Errorf("duration = %d", ref.duration)
	}
	if ref.studioName != "Forbidden Fruits Films" {
		t.Errorf("studio = %q", ref.studioName)
	}
}

// Each card is read from its own block, so a card missing its runtime or cast
// cannot borrow its neighbour's.
func TestParseCardsStayIndependent(t *testing.T) {
	full := card("1", "10", "Full", "Alice", "/1/a-streaming-scene-video.html", 20)
	bare := `<div class="grid-item"><article class="scene-widget store-view" data-scene-id="2" data-master-id="20">
		<a class="scene-img" href="/2/b-streaming-scene-video.html"></a>
		<a class="scene-title" href="/2/b-streaming-scene-video.html"><h6> Bare </h6></a>
		</article></div>`

	blocks := splitCards(bare + full)
	if len(blocks) != 2 {
		t.Fatalf("splitCards returned %d blocks, want 2", len(blocks))
	}
	first, ok := parseCard(blocks[0], "https://example.com", "S")
	if !ok {
		t.Fatal("bare card did not parse")
	}
	if first.duration != 0 || len(first.performers) != 0 {
		t.Errorf("bare card borrowed its neighbour's fields: %+v", first)
	}
	second, _ := parseCard(blocks[1], "https://example.com", "S")
	if second.duration != 1200 || !slices.Equal(second.performers, []string{"Alice"}) {
		t.Errorf("full card lost its own fields: %+v", second)
	}
}

func TestApplyDetail(t *testing.T) {
	body := []byte(`<div class="release-date"><span class="font-weight-bold mr-2">Released:</span>Aug 27, 2020 </div>
	<div class="studio"><span class="font-weight-bold mr-2">Studio:</span><a href="/x?studio=92960">Rodney Moore </a></div>
	<div class="series"><span class="font-weight-bold mr-2">Series:</span><a href="/y">Natural Jumbo Juggs </a></div>
	<div><span class="font-weight-bold mr-2">Director:</span><a href="/z">Rodney Moore </a></div>
	<div class="release-date"><span class="font-weight-bold mr-2">Length:</span>38 min </div>`)

	sc := (sceneRef{id: "1", studioName: "Placeholder"}).scene("https://x/", "rodneymoore")
	applyDetail(&sc, body)

	if sc.Date.Format("2006-01-02") != "2020-08-27" {
		t.Errorf("date = %v", sc.Date)
	}
	if sc.Series != "Natural Jumbo Juggs" {
		t.Errorf("series = %q", sc.Series)
	}
	if sc.Director != "Rodney Moore" {
		t.Errorf("director = %q", sc.Director)
	}
	if sc.Studio != "Rodney Moore" {
		t.Errorf("studio = %q", sc.Studio)
	}
	if sc.Duration != 38*60 {
		t.Errorf("duration = %d", sc.Duration)
	}
}

// The listing card already carries the runtime; the detail page must not
// overwrite it when the two disagree, and must supply it when the card omitted
// it.
func TestApplyDetailKeepsTheCardRuntime(t *testing.T) {
	body := []byte(`<span>Length:</span>99 min`)

	withCard := (sceneRef{id: "1", duration: 1200}).scene("https://x/", "s")
	applyDetail(&withCard, body)
	if withCard.Duration != 1200 {
		t.Errorf("duration = %d, want the card's 1200", withCard.Duration)
	}

	noCard := (sceneRef{id: "2"}).scene("https://x/", "s")
	applyDetail(&noCard, body)
	if noCard.Duration != 99*60 {
		t.Errorf("duration = %d, want the detail page's", noCard.Duration)
	}
}

// ---- end-to-end ----

type fakeStore struct {
	pages map[string]int // studio id -> page count
	per   int

	mu      sync.Mutex
	listing []string
	details int
}

func (f *fakeStore) note(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listing = append(f.listing, path)
}

func (f *fakeStore) noteDetail() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.details++
}

func (f *fakeStore) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != ageCookie {
			// The storefront answers an unconfirmed visitor with its age
			// interstitial instead of the page that was asked for.
			_, _ = fmt.Fprint(w, `<html><body><h1>Age Confirmation</h1></body></html>`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "streaming-scene-video.html") {
			f.noteDetail()
			_, _ = fmt.Fprint(w, `<span>Released:</span>Aug 27, 2020 <span>Series:</span><a href="/s">A Series</a>`)
			return
		}
		if r.URL.Path != scenesPath {
			http.NotFound(w, r)
			return
		}
		f.note(r.URL.String())

		studio := r.URL.Query().Get("studio")
		total, ok := f.pages[studio]
		if !ok {
			t.Errorf("unexpected studio filter %q", studio)
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}

		var b strings.Builder
		if page <= total {
			for i := range f.per {
				// Scene ids on the real store are numeric, and the card regex
				// depends on that.
				id := studio + strconv.Itoa(page) + strconv.Itoa(i)
				b.WriteString(card(id, id, "Scene "+id, "Alice",
					"/"+id+"/x-streaming-scene-video.html", 12))
			}
		}
		fmt.Fprintf(&b, `<ul class="pagination"><li><a href="?studio=%s" title="Go To Page 1">1</a></li>`, studio)
		fmt.Fprintf(&b, `<li><a href="?page=%d&amp;studio=%s" title="Go To Page %d">%d</a></li></ul>`,
			total, studio, total, total)
		_, _ = fmt.Fprint(w, b.String())
	}
}

func newTestScraper(t *testing.T, cfg SiteConfig, f *fakeStore) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	s := New(cfg)
	s.Client = ts.Client()
	s.base = ts.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) ([]string, bool, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 2000)
	go s.run(context.Background(), studioURL, opts, out)
	var ids []string
	var errs []error
	stopped := false
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindStoppedEarly:
			stopped = true
		}
	}
	return ids, stopped, errs
}

func TestRunWalksEveryStudioFilter(t *testing.T) {
	f := &fakeStore{pages: map[string]int{"92960": 2, "94085": 1}, per: 3}
	s := newTestScraper(t, sites[1], f)

	ids, _, errs := collect(t, s, "https://rodneymoorestore.com/", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 3*3 {
		t.Errorf("got %d scenes, want 9 (2 pages + 1 page, 3 each)", len(ids))
	}
	if f.details != len(ids) {
		t.Errorf("fetched %d detail pages for %d scenes", f.details, len(ids))
	}

	// Both filters must be asked; walking only the first would silently lose
	// the sub-brand's whole catalogue.
	var sawSecond bool
	for _, u := range f.listing {
		if strings.Contains(u, "studio=94085") {
			sawSecond = true
		}
	}
	if !sawSecond {
		t.Errorf("never requested the second studio filter: %v", f.listing)
	}
}

// The walk is interleaved: details for page 1 are fetched before page 2 is
// requested, so an early stop cuts the listing walk too rather than only the
// details of an already-scanned catalogue.
func TestRunStopsTheListingWalkAtAKnownID(t *testing.T) {
	f := &fakeStore{pages: map[string]int{"93785": 5}, per: 3}
	s := newTestScraper(t, sites[0], f)

	known := map[string]bool{"9378512": true}
	ids, stopped, errs := collect(t, s, "https://www.forbiddenfruitsfilms.com/",
		scraper.ListOpts{KnownIDs: known})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !stopped {
		t.Error("expected StoppedEarly")
	}
	if len(ids) > 3 {
		t.Errorf("emitted %d scenes, want at most the first page", len(ids))
	}
	if len(f.listing) != 1 {
		t.Errorf("requested %d listing pages, want 1: %v", len(f.listing), f.listing)
	}
}

func TestRunScrapesOneStudioFilterFromItsURL(t *testing.T) {
	f := &fakeStore{pages: map[string]int{"94085": 1}, per: 2}
	s := newTestScraper(t, sites[1], f)

	ids, _, errs := collect(t, s, s.base+scenesPath+"?studio=94085", scraper.ListOpts{})
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 2 {
		t.Errorf("got %d scenes, want 2", len(ids))
	}
}

// The age interstitial answers HTTP 200 with a page carrying no cards, so a
// scraper that forgot the cookie would look like an empty catalogue rather
// than a failure — and an authoritative --full would then delete everything.
func TestRunReportsAnEmptyFirstPageAsAParseError(t *testing.T) {
	f := &fakeStore{pages: map[string]int{"93785": 1}, per: 0}
	s := newTestScraper(t, sites[0], f)

	ids, _, errs := collect(t, s, "https://www.forbiddenfruitsfilms.com/", scraper.ListOpts{})
	if len(ids) != 0 {
		t.Errorf("got %d scenes, want none", len(ids))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if k := scraper.Classify(errs[0]); k != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", k)
	}
}

// A detail page that does not arrive costs the release date, not the scene:
// the card already carries title, cast, runtime and thumbnail.
func TestRunKeepsSceneWhenTheDetailPageFails(t *testing.T) {
	var served int
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "streaming-scene-video.html") {
			mu.Lock()
			served++
			mu.Unlock()
			http.Error(w, "gone", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, card("77", "770", "Kept", "Alice",
			"/77/x-streaming-scene-video.html", 11)+
			`<ul class="pagination"><li><a href="?studio=93785" title="Go To Page 1">1</a></li></ul>`)
	}))
	defer ts.Close()

	s := New(sites[0])
	s.Client = ts.Client()
	s.base = ts.URL

	ids, _, errs := collect(t, s, "https://www.forbiddenfruitsfilms.com/", scraper.ListOpts{})
	if !slices.Equal(ids, []string{"77"}) {
		t.Errorf("ids = %v, want the scene kept", ids)
	}
	if len(errs) != 1 {
		t.Errorf("got %d errors, want the detail failure reported", len(errs))
	}
}

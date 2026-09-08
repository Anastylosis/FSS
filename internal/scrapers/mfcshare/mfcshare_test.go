package mfcshare

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
		if c.Username == "" || c.Studio == "" {
			t.Errorf("%s: incomplete config %+v", c.SiteID, c)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	kerri := New(sites[0])
	goddess := New(sites[1])
	cases := []struct {
		s    *Scraper
		url  string
		want bool
	}{
		{kerri, "https://share.myfreecams.com/KerriKing", true},
		{kerri, "https://share.myfreecams.com/kerriking/albums", true},
		{kerri, "https://share.myfreecams.com/GoddessOfPleasure", false},
		{goddess, "https://share.myfreecams.com/GoddessOfPleasure", true},
		// StashDB records the former username, which the site still redirects.
		{goddess, "https://share.myfreecams.com/ErectionRX/", true},
		{goddess, "https://share.myfreecams.com/SomeoneElse", false},
		{kerri, "https://share.myfreecams.com.evil.org/KerriKing", false},
	}
	for _, c := range cases {
		if got := c.s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s.MatchesURL(%q) = %v, want %v", c.s.ID(), c.url, got, c.want)
		}
	}
}

func albumCardHTML(slug, kind, title, dur string) string {
	return fmt.Sprintf(`<div data-box-infinite-scroll-target='box'>
	<div id="album-%s" data-id="2329667" data-slug="%s" data-controller="album-box" class="box_v2 album">
	<a href="/a/%s" class="media-container">
	<album-box-piece data-slug="%s" data-index="0" data-type="%s" data-src="https://share-thumbs.myfreecams.com/x/%s/thumb/0.jpg"></album-box-piece>
	<img class="mini-thumbnail" src="data:image/gif;base64,AAAA" />
	<img class="default-thumbnail" data-type="%s" src="https://share-thumbs.myfreecams.com/x/%s/thumb/0.jpg" />
	<div class="top-left"><span class="secured-info"><span>300 Tokens</span></span></div>
	<div class="top-right"><span><i class="icon-play"></i> %s </span></div>
	<div class="title-stripe-bottom"><span class="title">%s</span></div>
	</a></div></div>`, slug, slug, slug, slug, kind, slug, kind, slug, dur, title)
}

// MFC Share hosts photo sets in the same grid, under the same URL shape;
// storing one as a scene would file a gallery as a video.
func TestParseAlbumsKeepsOnlyVideos(t *testing.T) {
	page := albumCardHTML("erv18qux", "Video", "Overtime", "9:35") +
		albumCardHTML("photoset1", "Photo", "Some Pictures", "") +
		albumCardHTML("dzjctpob", "Video", "Second &amp; Third", "1:02:30") +
		// The same album twice must not become two scenes.
		albumCardHTML("erv18qux", "Video", "Overtime", "9:35")

	got := parseAlbums([]byte(page))
	if len(got) != 2 {
		t.Fatalf("got %d albums, want 2: %+v", len(got), got)
	}
	if got[0].slug != "erv18qux" || got[0].title != "Overtime" {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].duration != 9*60+35 {
		t.Errorf("duration = %d, want 575", got[0].duration)
	}
	if got[0].thumbnail != "https://share-thumbs.myfreecams.com/x/erv18qux/thumb/0.jpg" {
		t.Errorf("thumbnail = %q", got[0].thumbnail)
	}
	if got[1].title != "Second & Third" {
		t.Errorf("second title = %q", got[1].title)
	}
	if got[1].duration != 3600+2*60+30 {
		t.Errorf("second duration = %d", got[1].duration)
	}
}

func TestBaseScene(t *testing.T) {
	s := New(sites[0])
	sc := s.baseScene(albumCard{slug: "erv18qux", title: "Overtime", duration: 575,
		thumbnail: "https://t/0.jpg"}, "https://share.myfreecams.com/KerriKing")

	if sc.ID != "erv18qux" || sc.SiteID != "kerriking" {
		t.Errorf("id/site = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.URL != "https://share.myfreecams.com/a/erv18qux" {
		t.Errorf("url = %q", sc.URL)
	}
	if !slices.Equal(sc.Performers, []string{"Kerri King"}) {
		t.Errorf("performers = %v", sc.Performers)
	}
	// Albums are priced in tokens, which PriceSnapshot cannot express — it
	// carries a bare amount with no currency — so no price is recorded.
	if len(sc.PriceHistory) != 0 {
		t.Errorf("price history = %v, want none", sc.PriceHistory)
	}
	// The date lives only on the per-album page, which the site's request
	// budget makes impractical to walk.
	if !sc.Date.IsZero() {
		t.Errorf("date = %v, want zero", sc.Date)
	}
}

func TestBaseSceneFallsBackToTheSlugForATitle(t *testing.T) {
	s := New(sites[0])
	sc := s.baseScene(albumCard{slug: "erv18qux"}, "https://share.myfreecams.com/KerriKing")
	if sc.Title != "erv18qux" {
		t.Errorf("title = %q, want the slug", sc.Title)
	}
}

// ---- end-to-end ----

type fakeSite struct {
	total int
	// photoEvery marks every Nth album a photo set, to prove a page of them
	// does not end the walk.
	photoEvery int

	mu      sync.Mutex
	offsets []int
}

func (f *fakeSite) note(o int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offsets = append(f.offsets, o)
}

func (f *fakeSite) noted() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.offsets)
}

func (f *fakeSite) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/albums") {
			t.Errorf("unexpected path %q — the walk must not fetch album pages", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		f.note(offset)
		// Past the last album the site answers 404.
		if offset >= f.total {
			http.NotFound(w, r)
			return
		}
		var b strings.Builder
		for i := offset; i < min(offset+pageSize, f.total); i++ {
			kind := "Video"
			if f.photoEvery > 0 && i%f.photoEvery == 0 {
				kind = "Photo"
			}
			b.WriteString(albumCardHTML(fmt.Sprintf("a%d", i), kind, fmt.Sprintf("Album %d", i), "9:35"))
		}
		_, _ = fmt.Fprint(w, b.String())
	}
}

func newTestScraper(t *testing.T, cfg SiteConfig, f *fakeSite) *Scraper {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	s := New(cfg)
	s.Client = ts.Client()
	s.base = ts.URL
	return s
}

func collect(t *testing.T, s *Scraper, studioURL string) ([]string, []error) {
	t.Helper()
	out := make(chan scraper.SceneResult, 2000)
	go s.run(context.Background(), studioURL, scraper.ListOpts{}, out)
	var ids []string
	var errs []error
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			ids = append(ids, r.Scene.ID)
		case scraper.KindError:
			errs = append(errs, r.Err)
		}
	}
	return ids, errs
}

// An offset past the last album answers 404. That is the end of the catalogue,
// not a failure — reporting it would mark every full run incomplete and block
// the authoritative Save.
func TestRunTreatsA404PastTheEndAsDone(t *testing.T) {
	f := &fakeSite{total: 35}
	s := newTestScraper(t, sites[0], f)

	ids, errs := collect(t, s, "https://share.myfreecams.com/KerriKing")
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 35 {
		t.Errorf("got %d scenes, want 35", len(ids))
	}
	if !slices.Contains(f.noted(), 48) {
		t.Errorf("offsets = %v, want the walk to probe past the end", f.noted())
	}
}

// A page of nothing but photo albums yields no scenes, but the catalogue
// continues past it.
func TestRunContinuesPastAPageOfPhotoAlbums(t *testing.T) {
	f := &fakeSite{total: 32, photoEvery: 1}
	s := newTestScraper(t, sites[0], f)

	ids, errs := collect(t, s, "https://share.myfreecams.com/KerriKing")
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want the empty first page reported: %v", len(errs), errs)
	}
	if len(ids) != 0 {
		t.Errorf("got %d scenes from a photo-only catalogue", len(ids))
	}

	// With videos after the first page, the walk must reach them.
	f2 := &fakeSite{total: 32, photoEvery: 0}
	s2 := newTestScraper(t, sites[0], f2)
	ids2, errs2 := collect(t, s2, "https://share.myfreecams.com/KerriKing")
	if len(errs2) > 0 {
		t.Fatalf("errors: %v", errs2)
	}
	if len(ids2) != 32 {
		t.Errorf("got %d scenes, want 32", len(ids2))
	}
}

func TestRunReportsAnEmptyFirstPage(t *testing.T) {
	f := &fakeSite{total: 0}
	s := newTestScraper(t, sites[0], f)

	ids, errs := collect(t, s, "https://share.myfreecams.com/KerriKing")
	if len(ids) != 0 {
		t.Errorf("got %d scenes, want none", len(ids))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
}

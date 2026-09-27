package realitylovers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func siteByID(t *testing.T, id string) siteConfig {
	t.Helper()
	for _, cfg := range sites {
		if cfg.SiteID == id {
			return cfg
		}
	}
	t.Fatalf("unknown site %q", id)
	return siteConfig{}
}

func TestUniqueSiteIDsAndDomains(t *testing.T) {
	ids, domains := map[string]bool{}, map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.SiteID] {
			t.Errorf("duplicate SiteID %q", cfg.SiteID)
		}
		if domains[cfg.Domain] {
			t.Errorf("duplicate domain %q", cfg.Domain)
		}
		ids[cfg.SiteID], domains[cfg.Domain] = true, true
	}
}

// KinkVR, the network's fifth StashDB brand, now redirects to kink.com and is
// covered by the `kink` scraper, so it must not be registered here.
func TestKinkVRIsNotASite(t *testing.T) {
	for _, cfg := range sites {
		if strings.Contains(cfg.Domain, "kinkvr") {
			t.Errorf("kinkvr.com redirects to kink.com and belongs to the kink scraper")
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := newScraper(siteByID(t, "realitylovers"))
	cases := map[string]bool{
		"https://realitylovers.com":                   true,
		"https://www.realitylovers.com/videos/page3/": true,
		"https://realitylovers.com/vd/184241303/X/":   true,
		"https://wearecrazy.com/videos/":              false,
		"https://realitylovers.com.evil.test/":        false,
		"":                                            false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

// Page 1 has no page segment; the rest are /videos/pageN/.
func TestListingURL(t *testing.T) {
	s := newScraper(siteByID(t, "realitylovers"))
	if got, want := s.listingURL(1), "https://realitylovers.com/videos/"; got != want {
		t.Errorf("page 1 = %q, want %q", got, want)
	}
	if got, want := s.listingURL(4), "https://realitylovers.com/videos/page4/"; got != want {
		t.Errorf("page 4 = %q, want %q", got, want)
	}
}

// The template renders a grid view and a list view of the same set, so every
// scene appears more than once per page.
func TestParseListingDedupesTheDuplicateViews(t *testing.T) {
	items := parseListing(readFixture(t, "listing.html"))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 — the repeated views must collapse", len(items))
	}
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.id] {
			t.Errorf("duplicate id %q survived", it.id)
		}
		seen[it.id] = true
		if it.slug == "" {
			t.Errorf("item %q has no slug", it.id)
		}
	}
	if !seen["184241303"] {
		t.Errorf("expected id 184241303, got %v", items)
	}
}

// The page-2 link is not a scene link and must not become an item.
func TestParseListingIgnoresNonSceneLinks(t *testing.T) {
	items := parseListing([]byte(`<a href="/videos/page2/">2</a><a href="/girl/183213324/Acabella/">A</a>`))
	if len(items) != 0 {
		t.Errorf("got %v, want no items", items)
	}
}

func TestParseVideoDetails(t *testing.T) {
	vd := parseVideoDetails(string(readFixture(t, "detail.html")))
	if vd == nil {
		t.Fatal("no videoDetails found")
	}
	if vd.ContentID != 184241303 {
		t.Errorf("ContentID = %d", vd.ContentID)
	}
	if vd.Title != "Best of Acabella - Vol.1" {
		t.Errorf("Title = %q", vd.Title)
	}
	if vd.ReleaseDate != "2026-07-17" {
		t.Errorf("ReleaseDate = %q — the site publishes clean ISO dates", vd.ReleaseDate)
	}
	if len(vd.Starring) == 0 || vd.Starring[0].Name != "Acabella" {
		t.Errorf("Starring = %v", vd.Starring)
	}
	if len(vd.Categories) == 0 {
		t.Error("Categories is empty")
	}
}

func TestParseVideoDetailsRejectsGarbage(t *testing.T) {
	if vd := parseVideoDetails("<html>nothing</html>"); vd != nil {
		t.Errorf("expected nil, got %+v", vd)
	}
	if vd := parseVideoDetails("const videoDetails = {not json};\n"); vd != nil {
		t.Errorf("expected nil for malformed JSON, got %+v", vd)
	}
}

// The srcset is a comma-separated "url width" list; taking it whole would store
// a candidate list rather than a URL.
func TestFirstSrcSetURL(t *testing.T) {
	vd := &videoDetails{}
	if got := firstSrcSetURL(vd); got != "" {
		t.Errorf("no images should give %q, got %q", "", got)
	}
	vd.MainImages = []struct {
		ImgSrcSet string `json:"imgSrcSet"`
	}{{ImgSrcSet: "https://x/small.jpg 405w,https://x/large.jpg 810w"}}
	if got, want := firstSrcSetURL(vd), "https://x/small.jpg"; got != want {
		t.Errorf("firstSrcSetURL = %q, want %q", got, want)
	}
}

func TestNamesDedupesAndCleans(t *testing.T) {
	got := names([]namedRef{{Name: " Acabella "}, {Name: "Acabella"}, {Name: ""}, {Name: "Kyla  Noctra"}})
	if !slices.Equal(got, []string{"Acabella", "Kyla Noctra"}) {
		t.Errorf("names = %v", got)
	}
}

// ---- end-to-end ----

func newSiteServer(t *testing.T) (*httptest.Server, func() int, func() int) {
	t.Helper()
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")
	var listPages, withCookie atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Without the disclaimer cookie the real site serves only an age-gate
		// splash with no scene links.
		if c, err := r.Cookie("agreedToDisclaimer"); err != nil || c.Value != "true" {
			_, _ = w.Write([]byte("<html><body>age gate</body></html>"))
			return
		}
		withCookie.Add(1)

		if strings.HasPrefix(r.URL.Path, "/vd/") {
			_, _ = w.Write(detail)
			return
		}
		if listPages.Add(1) == 1 {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>no scenes</body></html>"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { return int(listPages.Load()) }, func() int { return int(withCookie.Load()) }
}

func newTestScraper(t *testing.T, srv *httptest.Server) *Scraper {
	t.Helper()
	s := newScraper(siteByID(t, "realitylovers"))
	s.Client = srv.Client()
	s.base = srv.URL
	return s
}

func TestListScenes(t *testing.T) {
	srv, listPages, cookied := newSiteServer(t)
	s := newTestScraper(t, srv)

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, sc := range scenes {
		if sc.SiteID != "realitylovers" || sc.Studio != "Reality Lovers" {
			t.Errorf("scene %s: SiteID=%q Studio=%q", sc.ID, sc.SiteID, sc.Studio)
		}
		if sc.Title == "" || sc.Date.IsZero() || len(sc.Categories) == 0 {
			t.Errorf("scene %s incomplete: %+v", sc.ID, sc)
		}
		// The network publishes no runtime anywhere.
		if sc.Duration != 0 {
			t.Errorf("scene %s: Duration = %d, want 0", sc.ID, sc.Duration)
		}
	}
	if got := listPages(); got != 2 {
		t.Errorf("fetched %d listing pages, want 2", got)
	}
	// Every request — listings and details alike — must carry the cookie.
	if cookied() == 0 {
		t.Error("no request carried the disclaimer cookie")
	}
}

// Without the cookie the site answers with a splash page that has no scene
// links, which would silently produce an empty scrape.
func TestDisclaimerCookieIsSentOnEveryRequest(t *testing.T) {
	var missing atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("agreedToDisclaimer"); err != nil || c.Value != "true" {
			missing.Add(1)
		}
		_, _ = w.Write([]byte("<html><body>no scenes</body></html>"))
	}))
	defer srv.Close()

	s := newTestScraper(t, srv)
	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	testutil.CollectScenes(t, ch)

	if missing.Load() != 0 {
		t.Errorf("%d requests went out without the disclaimer cookie", missing.Load())
	}
}

func TestContextCancellation(t *testing.T) {
	listing := readFixture(t, "listing.html")
	detail := readFixture(t, "detail.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/vd/") {
			_, _ = w.Write(detail)
			return
		}
		_, _ = w.Write(listing)
	}))
	defer srv.Close()

	s := newTestScraper(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("channel did not close after context cancellation")
	}
}

func TestListingErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newTestScraper(t, srv)
	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sawErr := false
	for res := range ch {
		if res.Kind == scraper.KindError {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("a listing failure produced no error result")
	}
}

// A detail fetch that fails used to discard the scene silently: `--full` then
// deleted a scene the site still lists. The card alone is a usable scene, so it
// is kept and the failure is reported.
func TestDetailFailureKeepsTheListingScene(t *testing.T) {
	listing := readFixture(t, "listing.html")
	var listPages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("agreedToDisclaimer"); err != nil || c.Value != "true" {
			_, _ = w.Write([]byte("<html><body>age gate</body></html>"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/vd/") {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if listPages.Add(1) == 1 {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>no scenes</body></html>"))
	}))
	defer srv.Close()

	s := newTestScraper(t, srv)
	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}

	var scenes int
	var errs int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.ID == "" || r.Scene.Title == "" || r.Scene.URL == "" {
				t.Errorf("listing-derived scene is incomplete: %+v", r.Scene)
			}
		case scraper.KindError:
			errs++
		}
	}
	if scenes == 0 {
		t.Error("every scene was discarded; the listing card is still a scene")
	}
	if errs == 0 {
		t.Error("the failed enrichment must be reported")
	}
}

func TestTitleFromSlug(t *testing.T) {
	if got := titleFromSlug("a-hot-summer-night"); got != "a hot summer night" {
		t.Errorf("titleFromSlug = %q", got)
	}
}

// A page whose detail fetches all fail must not read as the end of the
// catalogue: `Paginate` treats an empty Scenes slice as end-of-listing, and
// `--full` then hard-deletes everything the walk never reached. This scraper
// takes the "keep the listing-derived scene and report the failure" answer —
// the card alone is a usable scene — so the page is never empty.
func TestAllDetailsFailingKeepsTheWalkGoing(t *testing.T) {
	listing := readFixture(t, "listing.html")
	var listPages, detailHits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("agreedToDisclaimer"); err != nil || c.Value != "true" {
			_, _ = w.Write([]byte("<html><body>age gate</body></html>"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/vd/") {
			detailHits.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if listPages.Add(1) == 1 {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>no scenes</body></html>"))
	}))
	defer srv.Close()

	s := newTestScraper(t, srv)
	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	var scenes, errs int
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
		case scraper.KindError:
			errs++
		}
	}
	if detailHits.Load() == 0 {
		t.Fatal("no detail pages were fetched; the fixture did not exercise the path")
	}
	if scenes == 0 {
		t.Error("every scene was dropped — an authoritative Save would delete them all")
	}
	if errs == 0 {
		t.Error("the failed enrichments were silent; the run would look complete")
	}
	// The walk asked for the next page rather than stopping at the first.
	if listPages.Load() < 2 {
		t.Errorf("fetched %d listing pages — an all-failed page ended the walk", listPages.Load())
	}
}

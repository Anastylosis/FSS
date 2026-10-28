package updateitem

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
	"github.com/Anastylosis/FSS/models"
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
		if cfg.StudioName == "" {
			t.Errorf("%s: StudioName is empty", cfg.SiteID)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	ssm := newScraper(siteByID(t, "sheseducedme"))
	lsx := newScraper(siteByID(t, "lesbiansexuality"))

	for _, u := range []string{
		"https://www.sheseducedme.com",
		"https://sheseducedme.com/categories/movies_2_d.html",
	} {
		if !ssm.MatchesURL(u) {
			t.Errorf("sheseducedme should match %q", u)
		}
		if lsx.MatchesURL(u) {
			t.Errorf("lesbiansexuality wrongly matches %q", u)
		}
	}
	for _, u := range []string{"https://sheseducedme.com.evil.test/", ""} {
		if ssm.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true, want false", u)
		}
	}
}

// Lesbian Sexuality serves its whole tour under /tour, the other two at the
// root, so listing and scene URLs must carry the configured prefix.
func TestPrefixIsAppliedToURLs(t *testing.T) {
	lsx := newScraper(siteByID(t, "lesbiansexuality"))
	items := lsx.parseListing(readFixture(t, "listing_lsx.html"))
	if len(items) == 0 {
		t.Fatal("no items parsed")
	}
	if !strings.Contains(items[0].url, "/tour/updates/") {
		t.Errorf("url = %q, want it under /tour/updates/", items[0].url)
	}

	ssm := newScraper(siteByID(t, "sheseducedme"))
	items = ssm.parseListing(readFixture(t, "listing_ssm.html"))
	if len(items) == 0 {
		t.Fatal("no items parsed")
	}
	if strings.Contains(items[0].url, "/tour/") {
		t.Errorf("url = %q, want no /tour/ prefix", items[0].url)
	}
}

func TestParseListingSheSeducedMe(t *testing.T) {
	s := newScraper(siteByID(t, "sheseducedme"))
	items := s.parseListing(readFixture(t, "listing_ssm.html"))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	it := items[0]
	if it.title != "Five Star Ride" {
		t.Errorf("title = %q", it.title)
	}
	if it.id != "4896" {
		t.Errorf("id = %q, want the numeric contentthumbs stem", it.id)
	}
	if want := "https://www.sheseducedme.com/updates/Five-Star-Ride.html"; it.url != want {
		t.Errorf("url = %q, want %q", it.url, want)
	}
	if !slices.Equal(it.performers, []string{"Gigi Sweets", "Lindsay Lee"}) {
		t.Errorf("performers = %v", it.performers)
	}
	// She Seduced Me publishes no date anywhere.
	if !it.date.IsZero() {
		t.Errorf("date = %v, want zero", it.date)
	}
}

// Lesbian Sexuality is the only one of the three printing the date on the card,
// in a bare <span> after the cast.
func TestParseListingLesbianSexualityCardDate(t *testing.T) {
	s := newScraper(siteByID(t, "lesbiansexuality"))
	items := s.parseListing(readFixture(t, "listing_lsx.html"))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	it := items[0]
	if want := time.Date(2022, time.June, 15, 0, 0, 0, 0, time.UTC); !it.date.Equal(want) {
		t.Errorf("date = %v, want %v", it.date, want)
	}
	if it.id != "459" {
		t.Errorf("id = %q", it.id)
	}
	if !slices.Equal(it.performers, []string{"Kylie Rocket", "Tiffany Watson"}) {
		t.Errorf("performers = %v", it.performers)
	}
}

func TestParseListingMySweetApple(t *testing.T) {
	s := newScraper(siteConfig{"mysweetapple", "mysweetapple.com", "MySweetApple", "", nil})
	items := s.parseListing(readFixture(t, "listing_msa.html"))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	it := items[0]
	// MySweetApple has no contentthumbs path, so the id is the content dir.
	if it.id != "999_042_SHOW_Blowjob_and_cum_on_my_lenses" {
		t.Errorf("id = %q", it.id)
	}
	if !strings.HasPrefix(it.thumb, "https://mysweetapple.com/content/") {
		t.Errorf("thumb = %q — relative card thumbs must be absolutised", it.thumb)
	}
}

// The skin exposes no id of its own, so it is derived from the thumbnail path.
func TestSceneID(t *testing.T) {
	cases := map[string]string{
		"/content//contentthumbs/48/96/4896-1x.jpg":               "4896",
		"/tour/content//contentthumbs/04/59/459-2x.jpg":           "459",
		"content/999_042_SHOW_Blowjob_and_cum_on_my_lenses/1.jpg": "999_042_SHOW_Blowjob_and_cum_on_my_lenses",
		"content/250-Sunny-Terrace-Creampie/1-4x.jpg":             "250-Sunny-Terrace-Creampie",
		"https://cdn.example.com/img.jpg":                         "",
	}
	for in, want := range cases {
		if got := sceneID(in); got != want {
			t.Errorf("sceneID(%q) = %q, want %q", in, got, want)
		}
	}
}

// A date is matched by shape, never by trusting a container to hold one — the
// thumbnail paths in a card are full of slash-separated digits.
func TestCardDateIsScopedToTheDetailsBlock(t *testing.T) {
	s := newScraper(siteByID(t, "lesbiansexuality"))
	card := `<div class="updateItem">
	<a href="https://x/tour/updates/y.html">
	<img class="stdimage " src0_1x="/tour/content//contentthumbs/04/59/459-1x.jpg" /></a>
	<div class="updateDetails">
	<h4><a href="https://x/tour/updates/y.html">Y</a></h4>
	<p><span class="tour_update_models"></span><span>06/15/2022</span></p>
	</div></div>`
	items := s.parseListing([]byte(card))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if want := time.Date(2022, time.June, 15, 0, 0, 0, 0, time.UTC); !items[0].date.Equal(want) {
		t.Errorf("date = %v, want %v", items[0].date, want)
	}
}

// ---- detail ----

func TestApplyDetailMySweetApple(t *testing.T) {
	sc := models.Scene{Title: "LIVE SHOW Blowjob And Facial with Glasses"}
	applyDetail(&sc, string(readFixture(t, "detail_msa.html")))

	if sc.Title != "LIVE SHOW: Quickie Blowjob And Facial with Glasses" {
		t.Errorf("Title = %q — the detail title is the full one", sc.Title)
	}
	if want := time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if !slices.Contains(sc.Tags, "Balls Licking") {
		t.Errorf("Tags = %v", sc.Tags)
	}
	if sc.Description == "" || strings.Contains(sc.Description, "<") {
		t.Errorf("Description = %q", sc.Description)
	}
}

// The availdate span exists on She Seduced Me but its contents are commented
// out, so trusting the span to hold a date yields markup fragments.
func TestEmptyAvailDateLeavesTheDateZero(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, string(readFixture(t, "detail_ssm.html")))
	if !sc.Date.IsZero() {
		t.Errorf("Date = %v, want zero — She Seduced Me publishes none", sc.Date)
	}
	if sc.Description == "" {
		t.Error("the description should still be read")
	}
	if strings.Contains(sc.Description, "-->") || strings.Contains(sc.Description, "<") {
		t.Errorf("Description holds markup: %q", sc.Description)
	}
}

// A date already read from the card must not be overwritten by the detail page.
func TestCardDateWinsOverDetail(t *testing.T) {
	sc := models.Scene{Date: time.Date(2022, time.June, 15, 0, 0, 0, 0, time.UTC)}
	applyDetail(&sc, `<span class="availdate">01/01/1999</span>`)
	if sc.Date.Year() != 2022 {
		t.Errorf("Date = %v, want the card's 2022 value", sc.Date)
	}
}

func TestApplyDetailTagsAreDeduped(t *testing.T) {
	var sc models.Scene
	applyDetail(&sc, `<span class="update_tags">Tags:
	<a href="https://x/categories/Blondes.html">Blondes</a>
	<a href="https://x/categories/blondes.html">Blondes</a>
	<a href="https://x/categories/Teen.html">Teen</a>
	</span>`)
	if !slices.Equal(sc.Tags, []string{"Blondes", "Teen"}) {
		t.Errorf("Tags = %v, want [Blondes Teen]", sc.Tags)
	}
}

// ---- end-to-end ----

func newSiteServer(t *testing.T, listing, detail []byte) (*httptest.Server, func() int) {
	t.Helper()
	var listPages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/updates/") {
			_, _ = w.Write(detail)
			return
		}
		if listPages.Add(1) == 1 {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>no cards</body></html>"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { return int(listPages.Load()) }
}

func newTestScraper(t *testing.T, id string, srv *httptest.Server) *Scraper {
	t.Helper()
	s := newScraper(siteByID(t, id))
	s.Client = srv.Client()
	s.base = srv.URL
	return s
}

func TestListScenes(t *testing.T) {
	srv, listPages := newSiteServer(t, readFixture(t, "listing_lsx.html"), readFixture(t, "detail_lsx.html"))
	s := newTestScraper(t, "lesbiansexuality", srv)

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	sc := scenes[0]
	if sc.SiteID != "lesbiansexuality" || sc.Studio != "Lesbian Sexuality" {
		t.Errorf("SiteID=%q Studio=%q", sc.SiteID, sc.Studio)
	}
	if sc.Date.IsZero() || len(sc.Tags) == 0 || len(sc.Performers) == 0 {
		t.Errorf("scene incomplete: %+v", sc)
	}
	if got := listPages(); got != 2 {
		t.Errorf("fetched %d listing pages, want 2", got)
	}
}

func TestDetailFailureKeepsTheCard(t *testing.T) {
	listing := readFixture(t, "listing_lsx.html")
	var listPages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/updates/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if listPages.Add(1) == 1 {
			_, _ = w.Write(listing)
			return
		}
		_, _ = w.Write([]byte("<html><body>no cards</body></html>"))
	}))
	defer srv.Close()

	s := newTestScraper(t, "lesbiansexuality", srv)
	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	scenes := testutil.CollectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	// The card carries title, cast and date on this site, so they survive.
	if scenes[0].Title == "" || len(scenes[0].Performers) == 0 || scenes[0].Date.IsZero() {
		t.Errorf("card fields lost on detail failure: %+v", scenes[0])
	}
}

func TestContextCancellation(t *testing.T) {
	listing := readFixture(t, "listing_lsx.html")
	detail := readFixture(t, "detail_lsx.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/updates/") {
			_, _ = w.Write(detail)
			return
		}
		_, _ = w.Write(listing)
	}))
	defer srv.Close()

	s := newTestScraper(t, "lesbiansexuality", srv)
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

	s := newTestScraper(t, "sheseducedme", srv)
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

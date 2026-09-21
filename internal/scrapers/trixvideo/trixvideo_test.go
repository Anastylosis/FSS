package trixvideo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func siteByID(t *testing.T, id string) SiteConfig {
	t.Helper()
	for _, cfg := range sites {
		if cfg.SiteID == id {
			return cfg
		}
	}
	t.Fatalf("site %q not in table", id)
	return SiteConfig{}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = New(sites[0])
}

func TestRegisteredSiteIDsAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, cfg := range sites {
		if seen[cfg.SiteID] {
			t.Errorf("duplicate site id %q", cfg.SiteID)
		}
		seen[cfg.SiteID] = true
		if cfg.StudioName == "" || cfg.Domain == "" {
			t.Errorf("site %q has an empty domain or studio name", cfg.SiteID)
		}
	}
	if len(sites) != 10 {
		t.Errorf("expected the 10 Trix Video sites, got %d", len(sites))
	}
}

func TestTourPathDefaultsToTour(t *testing.T) {
	if got := (SiteConfig{}).tourPath(); got != "/tour" {
		t.Errorf("tourPath() = %q, want /tour", got)
	}
	if got := (SiteConfig{TourPath: "/tour3"}).tourPath(); got != "/tour3" {
		t.Errorf("tourPath() = %q, want the configured path", got)
	}
	s := New(siteByID(t, "desperateamateurs"))
	if got := s.updatePages()(2); got != "/tour3/updates/page_2.html" {
		t.Errorf("updatePages(2) = %q", got)
	}
	if got := s.categoryPages("BBW")(1); got != "/tour3/categories/BBW_1_p.html" {
		t.Errorf("categoryPages(1) = %q", got)
	}
	for _, u := range []string{
		"https://desperateamateurs.com/tour3/models/Bexx.html",
		"https://desperateamateurs.com/tour3/categories/BBW.html",
	} {
		if !modelURLRe.MatchString(u) && !categoryURLRe.MatchString(u) {
			t.Errorf("%q matched no URL mode", u)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(siteByID(t, "dallasdiamondz"))
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.dallasdiamondz.com/tour/", true},
		{"https://dallasdiamondz.com/tour/", true},
		{"http://dallasdiamondz.com/", true},
		{"https://www.dallasdiamondz.com/tour/models/DallasDiamondz.html", true},
		{"https://www.dallasdiamondz.com", true},
		{"https://www.example.com/", false},
		{"https://notdallasdiamondz.com/tour/", false},
		{"https://www.dallasdiamondz.com.evil.test/", false},
		{"https://www.suburbantaboo.com/tour/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestEachSiteMatchesOnlyItsOwnDomain(t *testing.T) {
	for _, cfg := range sites {
		s := New(cfg)
		own := "https://www." + cfg.Domain + "/tour/"
		if !s.MatchesURL(own) {
			t.Errorf("%s does not match its own URL %q", cfg.SiteID, own)
		}
		for _, other := range sites {
			if other.SiteID == cfg.SiteID {
				continue
			}
			if s.MatchesURL("https://www." + other.Domain + "/tour/") {
				t.Errorf("%s wrongly matches %s", cfg.SiteID, other.Domain)
			}
		}
	}
}

// cardHTML is the compact `update_details` card, in the older build's markup
// (no space between the alt and class attributes).
const cardHTML = `
<div class="category_listing_wrapper_updates">
<div class="update_details" data-setid="423">
	<a  href="https://www.dallasdiamondz.com/tour/updates/AFUN3WAY.html" >
		<img alt="D_3way"class="update_thumb thumbs" src="content/D_3way/1.jpg" src0_2x="content/D_3way/1-2x.jpg" />	</a>
	<a  href="https://www.dallasdiamondz.com/tour/updates/AFUN3WAY.html">A FUN 3 WAY</a>
	<br />
	<span class="update_models">
	<a href="https://www.dallasdiamondz.com/tour/models/DallasDiamondz.html">Dallas Diamondz</a>
	</span>
	<div class="update_counts">36&nbsp;Photos</div>
	<div class="table"><div class="row">
		<div class="cell update_date">
		<!-- Date -->
		12/17/2012		</div>
	</div></div>
</div>
</div>`

// newBuildCardHTML is the same card from a site running the newer build: an
// extra data-vr attribute, a space before class, and a stdimage class prefix.
const newBuildCardHTML = `
<div class="update_details" data-setid="1575">
	<a data-vr="0" data-vrformat=""  href="https://www.dixiestrailerpark.com/tour/updates/Second-Set.html" >
		<img alt="wbh112406" class="stdimage update_thumb thumbs" src="content/wbh112406/1.jpg" />	</a>
	<a data-vr="0"  href="https://www.dixiestrailerpark.com/tour/updates/Second-Set.html">Second &amp; Set</a>
	<span class="update_models">
	<a href="https://www.dixiestrailerpark.com/tour/models/Dixie.html">Dixie</a>,
	<a href="https://www.dixiestrailerpark.com/tour/models/paris-rose.html">Ms Paris Rose</a>
	</span>
	<div class="table"><div class="row">
		<div class="cell update_date">
		01/02/2013		</div>
	</div></div>
</div>`

// tileHTML is a category tile: same wrapper class, but join-gated — no set id
// and no detail link. Parsing must skip it.
const tileHTML = `
<div class="update_details">
	Anal
	<a class="model_title" href="join.php" >
	<img class="update_thumb thumbs stdimage" src0_1x="/tour/content//contentthumbs/00/06/6-cat-1x.jpg" />	</a>
</div>`

func TestParseCards(t *testing.T) {
	cards := parseCards(cardHTML + newBuildCardHTML + tileHTML)
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2 (the join-gated tile must be skipped)", len(cards))
	}

	a := cards[0]
	if a.id != "423" {
		t.Errorf("id = %q, want 423", a.id)
	}
	if a.path != "/tour/updates/AFUN3WAY.html" {
		t.Errorf("path = %q", a.path)
	}
	if a.title != "A FUN 3 WAY" {
		t.Errorf("title = %q", a.title)
	}
	if a.date != "12/17/2012" {
		t.Errorf("date = %q", a.date)
	}
	if a.thumbnail != "content/D_3way/1.jpg" {
		t.Errorf("thumbnail = %q", a.thumbnail)
	}
	if len(a.performers) != 1 || a.performers[0] != "Dallas Diamondz" {
		t.Errorf("performers = %v", a.performers)
	}
	if !a.creditsModel("dallasdiamondz") || !a.creditsModel("DallasDiamondz") {
		t.Errorf("creditsModel should match case-insensitively, slugs = %v", a.modelSlugs)
	}
	if a.creditsModel("paris-rose") {
		t.Error("creditsModel matched a model the card does not credit")
	}

	b := cards[1]
	if b.id != "1575" || b.path != "/tour/updates/Second-Set.html" {
		t.Errorf("new-build card = %+v", b)
	}
	if b.title != "Second & Set" {
		t.Errorf("title = %q, want entities unescaped", b.title)
	}
	if len(b.modelSlugs) != 2 || !b.creditsModel("paris-rose") {
		t.Errorf("modelSlugs = %v", b.modelSlugs)
	}
}

const detailHTML = `<html><body>
<div class="update_block">
<div class="update_block_info">
	<span class="update_title">THREE Creamy HOLES to FILL</span>
	<span class="tour_update_models">
	<a href="https://www.dallasdiamondz.com/tour/models/DallasDiamondz.html">Dallas Diamondz</a>
	</span>
	<span class="update_date">01/20/2018</span>
	<hr class="update_hr" />
	<span class="latest_update_description">Dallas has a few guys over &amp; fills her holes</span>
	<span class="tour_update_tags">
Tags:
	  <a href="https://www.dallasdiamondz.com/tour/categories/Anal.html">Anal</a>,
	  <a href="https://www.dallasdiamondz.com/tour/categories/CreamPies.html">Cream Pies</a>
	</span>
</div>
<div class="update_image">
	<img alt="Dallas_02272013_GB2"class="large_update_thumb left thumbs" src="content/Dallas_02272013_GB2/0.jpg" src0_4x="content/Dallas_02272013_GB2/0-4x.jpg" />
</div>
</div>
</body></html>`

func TestParseDetail(t *testing.T) {
	d := parseDetail(detailHTML)
	if d.title != "THREE Creamy HOLES to FILL" {
		t.Errorf("title = %q", d.title)
	}
	if d.date != "01/20/2018" {
		t.Errorf("date = %q", d.date)
	}
	if d.description != "Dallas has a few guys over & fills her holes" {
		t.Errorf("description = %q", d.description)
	}
	if len(d.performers) != 1 || d.performers[0] != "Dallas Diamondz" {
		t.Errorf("performers = %v", d.performers)
	}
	want := []string{"Anal", "Cream Pies"}
	if len(d.tags) != len(want) {
		t.Fatalf("tags = %v, want %v", d.tags, want)
	}
	for i := range want {
		if d.tags[i] != want[i] {
			t.Errorf("tags[%d] = %q, want %q", i, d.tags[i], want[i])
		}
	}
	// The advertised -4x variants 404 on every site; only the 1x src is real.
	if d.thumbnail != "content/Dallas_02272013_GB2/0.jpg" {
		t.Errorf("thumbnail = %q, want the plain src", d.thumbnail)
	}
}

// The newer build writes `alt="x" class="stdimage large_update_thumb ..."`,
// and a set with nobody credited omits the models span entirely.
func TestParseDetailNewBuildAndNoModels(t *testing.T) {
	body := `<div class="update_block">
	<span class="update_title">10 inches and a HOT Blonde TEEN</span>
	<!-- List Of Models -->
	<span class="update_date">12/28/2012</span>
	<span class="latest_update_description">By the hot tub.</span>
	<span class="tour_update_tags">Tags: <a href="/tour/categories/BlowJobs.html">BlowJobs</a></span>
	<img alt="th1" class="stdimage large_update_thumb left thumbs" src="content/th1/0.jpg" />
	</div>`
	d := parseDetail(body)
	if d.title != "10 inches and a HOT Blonde TEEN" {
		t.Errorf("title = %q", d.title)
	}
	if len(d.performers) != 0 {
		t.Errorf("performers = %v, want none", d.performers)
	}
	if d.thumbnail != "content/th1/0.jpg" {
		t.Errorf("thumbnail = %q", d.thumbnail)
	}
}

// The template prints a bare dd/mm-shaped string with no clue which way round
// it is, and the sites disagree: every Trix Video tour writes MM/DD/YYYY while
// justdanica writes DD/MM/YYYY. Guessing wrong silently mis-dates every scene
// whose day is 12 or lower, so the layout is stated per site.
func TestParseDate(t *testing.T) {
	got, ok := parseDate("01/20/2018", defaultDateLayout)
	if !ok {
		t.Fatal("parseDate failed on a well-formed date")
	}
	if got.Year() != 2018 || got.Month() != time.January || got.Day() != 20 {
		t.Errorf("parseDate = %v, want 2018-01-20 (MM/DD/YYYY)", got)
	}
	if got.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", got.Location())
	}
	if _, ok := parseDate("", defaultDateLayout); ok {
		t.Error("parseDate accepted an empty string")
	}
	if _, ok := parseDate("20/01/2018", defaultDateLayout); ok {
		t.Error("parseDate accepted DD/MM/YYYY under the MM/DD layout")
	}

	// The same string under the other layout is a different, valid date — this
	// is exactly the silent mis-dating the per-site layout exists to prevent.
	euro, ok := parseDate("03/01/2012", "02/01/2006")
	if !ok {
		t.Fatal("parseDate failed under the DD/MM layout")
	}
	if euro.Month() != time.January || euro.Day() != 3 {
		t.Errorf("DD/MM parse = %v, want 3 January 2012", euro)
	}
	us, _ := parseDate("03/01/2012", defaultDateLayout)
	if us.Month() != time.March || us.Day() != 1 {
		t.Errorf("MM/DD parse = %v, want 1 March 2012", us)
	}
}

func TestDateLayoutDefaultsToUS(t *testing.T) {
	if got := (SiteConfig{}).dateLayout(); got != defaultDateLayout {
		t.Errorf("dateLayout() = %q, want %q", got, defaultDateLayout)
	}
	if got := (SiteConfig{DateLayout: "02/01/2006"}).dateLayout(); got != "02/01/2006" {
		t.Errorf("dateLayout() = %q, want the configured layout", got)
	}
	for _, cfg := range sites {
		if cfg.DateLayout != "" {
			t.Errorf("%s sets a DateLayout; every Trix Video tour is MM/DD", cfg.SiteID)
		}
	}
}

func TestResolveURL(t *testing.T) {
	const base = "https://www.dallasdiamondz.com"
	tests := []struct{ tourPath, ref, want string }{
		{"/tour", "content/X/0.jpg", base + "/tour/content/X/0.jpg"},
		{"/tour", "/tour/content/X/0.jpg", base + "/tour/content/X/0.jpg"},
		{"/tour", "https://cdn.example.com/a.jpg", "https://cdn.example.com/a.jpg"},
		// A site on a numbered tour writes its relative paths against that one.
		{"/tour3", "content/X/0.jpg", base + "/tour3/content/X/0.jpg"},
		{"/tour3", "/tour3/content/X/0.jpg", base + "/tour3/content/X/0.jpg"},
	}
	for _, tt := range tests {
		if got := resolveURL(base, tt.tourPath, tt.ref); got != tt.want {
			t.Errorf("resolveURL(%q, %q) = %q, want %q", tt.tourPath, tt.ref, got, tt.want)
		}
	}
}

// ---- end-to-end against a fake tour ----

// tourServer serves a listing whose pages hold `pages` batches of cards, plus a
// detail page per set. Out-of-range pages repeat the last one, the way the real
// CMS clamps them.
type tourServer struct {
	*httptest.Server
	listingHits atomic.Int32
	detailHits  atomic.Int32
}

func newTourServer(t *testing.T, pages [][]string, models map[string][]string) *tourServer {
	t.Helper()
	ts := &tourServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/tour/updates/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/tour/updates/")
		if strings.HasPrefix(name, "page_") {
			ts.listingHits.Add(1)
			var n int
			_, _ = fmt.Sscanf(name, "page_%d.html", &n)
			if n < 1 || n > len(pages) {
				// Clamp, like the real tour: never an empty page or a 404.
				n = len(pages)
			}
			var sb strings.Builder
			for _, id := range pages[n-1] {
				fmt.Fprintf(&sb, `<div class="update_details" data-setid="%s">
					<a href="/tour/updates/set%s.html">Set %s</a>
					<span class="update_models">%s</span>
					<div class="cell update_date">03/0%s/2020</div>
					<img src="content/dir%s/1.jpg" />
					</div>`, id, id, id, modelSpan(models[id]), id, id)
			}
			_, _ = fmt.Fprint(w, sb.String())
			return
		}
		ts.detailHits.Add(1)
		id := strings.TrimSuffix(strings.TrimPrefix(name, "set"), ".html")
		_, _ = fmt.Fprintf(w, `<div class="update_block">
			<span class="update_title">Set %s Title</span>
			<span class="tour_update_models">%s</span>
			<span class="update_date">03/0%s/2020</span>
			<span class="latest_update_description">Description %s</span>
			<span class="tour_update_tags">Tags: <a href="/tour/categories/Anal.html">Anal</a></span>
			<img alt="d" class="large_update_thumb thumbs" src="content/dir%s/0.jpg" />
			</div>`, id, modelSpan(models[id]), id, id, id)
	})
	mux.HandleFunc("/tour/categories/", func(w http.ResponseWriter, r *http.Request) {
		ts.listingHits.Add(1)
		if !strings.Contains(r.URL.Path, "_1_p.html") {
			return // only page 1 of a category has anything
		}
		_, _ = fmt.Fprint(w, `<div class="update_details" data-setid="9">
			<a href="/tour/updates/set9.html">Set 9</a>
			<div class="cell update_date">03/09/2020</div>
			</div>`)
	})
	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func modelSpan(names []string) string {
	if len(names) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, n := range names {
		fmt.Fprintf(&sb, `<a href="/tour/models/%s.html">%s</a>`, n, n)
	}
	return sb.String()
}

func newTestScraper(t *testing.T, ts *tourServer, id string) *Scraper {
	t.Helper()
	s := New(siteByID(t, id))
	s.Client = ts.Client()
	s.baseOverride = ts.URL
	return s
}

func collectScenes(t *testing.T, ch <-chan scraper.SceneResult) ([]models.Scene, []error, int) {
	t.Helper()
	var scenes []models.Scene
	var errs []error
	total := 0
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		case scraper.KindError:
			errs = append(errs, r.Err)
		case scraper.KindTotal:
			total = r.Total
		}
	}
	return scenes, errs, total
}

func TestListScenes(t *testing.T) {
	ts := newTourServer(t, [][]string{{"1", "2"}, {"2", "3"}, {"3"}}, map[string][]string{
		"1": {"Dallas"},
		"2": {"Dallas", "Paris"},
		"3": {"Paris"},
	})
	s := newTestScraper(t, ts, "dallasdiamondz")

	ch, err := s.ListScenes(context.Background(), "https://www.dallasdiamondz.com/tour/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, total := collectScenes(t, ch)

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3 deduped across overlapping pages", len(scenes))
	}
	if total != 3 {
		t.Errorf("progress total = %d, want 3", total)
	}
	if got := ts.detailHits.Load(); got != 3 {
		t.Errorf("detail fetches = %d, want 3 (one per distinct set)", got)
	}

	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}
	one, ok := byID["1"]
	if !ok {
		t.Fatalf("set 1 missing, got ids %v", byID)
	}
	if one.Title != "Set 1 Title" {
		t.Errorf("title = %q", one.Title)
	}
	if one.SiteID != "dallasdiamondz" {
		t.Errorf("siteID = %q", one.SiteID)
	}
	if one.Studio != "Dallas Diamondz" {
		t.Errorf("studio = %q", one.Studio)
	}
	if one.StudioURL != "https://www.dallasdiamondz.com/tour/" {
		t.Errorf("studioURL = %q, want the operator's URL verbatim", one.StudioURL)
	}
	if one.Description != "Description 1" {
		t.Errorf("description = %q", one.Description)
	}
	if len(one.Tags) != 1 || one.Tags[0] != "Anal" {
		t.Errorf("tags = %v", one.Tags)
	}
	if one.Date.Format("2006-01-02") != "2020-03-01" {
		t.Errorf("date = %v", one.Date)
	}
	if one.ScrapedAt.IsZero() {
		t.Error("ScrapedAt not stamped")
	}
	// Every fetched URL must live on the test server; a scraper that followed
	// the absolute URLs a real page embeds would silently hit production here.
	for _, sc := range scenes {
		if !strings.HasPrefix(sc.URL, ts.URL) {
			t.Errorf("scene %s URL %q left the test server", sc.ID, sc.URL)
		}
		if !strings.HasPrefix(sc.Thumbnail, ts.URL+"/tour/content/") {
			t.Errorf("scene %s thumbnail %q not resolved against the tour base", sc.ID, sc.Thumbnail)
		}
	}
}

// The CMS clamps out-of-range pages instead of 404ing, so the walk has to stop
// on repeats. It must tolerate one repeated page without giving up, because the
// widgets these cards come from rotate.
func TestListScenesStopsOnRepeatedPagesButToleratesOne(t *testing.T) {
	ts := newTourServer(t, [][]string{{"1"}, {"1"}, {"2"}}, nil)
	s := newTestScraper(t, ts, "suburbantaboo")

	ch, err := s.ListScenes(context.Background(), "https://www.suburbantaboo.com/tour/", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collectScenes(t, ch)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 — page 2 repeats page 1 but page 3 is new", len(scenes))
	}
	if got := ts.listingHits.Load(); got > 6 {
		t.Errorf("%d listing fetches — the walk did not stop on the clamped pages", got)
	}
}

func TestListScenesModelFilter(t *testing.T) {
	ts := newTourServer(t, [][]string{{"1", "2", "3"}}, map[string][]string{
		"1": {"Dallas"},
		"2": {"Dallas", "Paris"},
		"3": {"Paris"},
	})
	s := newTestScraper(t, ts, "dallasdiamondz")

	ch, err := s.ListScenes(context.Background(),
		"https://www.dallasdiamondz.com/tour/models/Paris.html", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collectScenes(t, ch)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want the 2 crediting Paris", len(scenes))
	}
	for _, sc := range scenes {
		if sc.ID == "1" {
			t.Error("set 1 does not credit Paris and should have been filtered out")
		}
	}
	// Filtering happens on the listing card, so the excluded set costs no fetch.
	if got := ts.detailHits.Load(); got != 2 {
		t.Errorf("detail fetches = %d, want 2", got)
	}
}

// /tour/models/models.html is the model index, not a model.
func TestModelIndexIsNotTreatedAsAModel(t *testing.T) {
	ts := newTourServer(t, [][]string{{"1", "2"}}, map[string][]string{"1": {"Dallas"}})
	s := newTestScraper(t, ts, "dallasdiamondz")

	ch, err := s.ListScenes(context.Background(),
		"https://www.dallasdiamondz.com/tour/models/models.html", scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, _ := collectScenes(t, ch)
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want the full listing", len(scenes))
	}
}

func TestListScenesCategory(t *testing.T) {
	ts := newTourServer(t, [][]string{{"1"}}, nil)
	s := newTestScraper(t, ts, "dallasdiamondz")

	ch, err := s.ListScenes(context.Background(),
		"https://www.dallasdiamondz.com/tour/categories/MILF.html", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collectScenes(t, ch)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(scenes) != 1 || scenes[0].ID != "9" {
		t.Fatalf("got %+v, want the single category set 9", scenes)
	}
}

// A page that fetches cleanly but parses to nothing is a template change, not
// an empty site, and must not be reported as a clean empty success.
func TestEmptyListingReportsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>nothing here</body></html>`)
	}))
	defer srv.Close()

	s := New(siteByID(t, "dallasdiamondz"))
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://www.dallasdiamondz.com/tour/", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collectScenes(t, ch)
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes from an empty tour", len(scenes))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if got := scraper.Classify(errs[0]); got != scraper.FailureParse {
		t.Errorf("classified as %v, want FailureParse", got)
	}
}

// An unknown model must not be reported as a broken template — the listing
// parsed fine, the filter is what emptied it, and the two need different fixes.
func TestUnknownModelReportsAFilterMissNotAParseFailure(t *testing.T) {
	ts := newTourServer(t, [][]string{{"1", "2"}}, map[string][]string{
		"1": {"Dallas"},
		"2": {"Dallas"},
	})
	s := newTestScraper(t, ts, "dallasdiamondz")

	ch, err := s.ListScenes(context.Background(),
		"https://www.dallasdiamondz.com/tour/models/Nobody.html", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	scenes, errs, _ := collectScenes(t, ch)
	if len(scenes) != 0 {
		t.Fatalf("got %d scenes for a model nothing credits", len(scenes))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "credits model") {
		t.Errorf("error = %q, want it to name the filter as the cause", errs[0])
	}
	if got := scraper.Classify(errs[0]); got == scraper.FailureParse {
		t.Error("a filter miss must not classify as a parse failure")
	}
	if ts.detailHits.Load() != 0 {
		t.Error("no detail page should be fetched when the filter matches nothing")
	}
}

// A card carrying a set id but no link of its own would otherwise take the next
// link in its chunk and land the same page under two ids.
func TestDuplicateDetailPathIsNotIngestedTwice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "page_") {
			_, _ = fmt.Fprint(w, `
			<div class="update_details" data-setid="10">
				<a href="/tour/updates/same.html">Same</a>
				<div class="cell update_date">03/01/2020</div>
			</div>
			<div class="update_details" data-setid="11">
				<a href="/tour/updates/same.html">Same Again</a>
				<div class="cell update_date">03/02/2020</div>
			</div>`)
			return
		}
		_, _ = fmt.Fprint(w, `<div class="update_block">
			<span class="update_title">Same</span>
			<span class="update_date">03/01/2020</span>
			</div>`)
	}))
	defer srv.Close()

	s := New(siteByID(t, "dallasdiamondz"))
	s.Client = srv.Client()
	s.baseOverride = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://www.dallasdiamondz.com/tour/", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	scenes, _, _ := collectScenes(t, ch)
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 — the repeated detail path must not ingest twice", len(scenes))
	}
	if scenes[0].ID != "10" {
		t.Errorf("kept id %q, want the first card's id", scenes[0].ID)
	}
}

func TestContextCancellationStopsTheScrape(t *testing.T) {
	ts := newTourServer(t, [][]string{{"1", "2", "3"}}, nil)
	s := newTestScraper(t, ts, "dallasdiamondz")

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := s.ListScenes(ctx, "https://www.dallasdiamondz.com/tour/", scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	// The channel must close rather than block; a leaked worker would hang here.
	for range ch { //nolint:revive // draining is the point
	}
}

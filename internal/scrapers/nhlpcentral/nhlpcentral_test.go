package nhlpcentral

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := map[string]bool{
		"https://nhlpcentral.com":                          true,
		"https://www.nhlpcentral.com/":                     true,
		"https://nhlpcentral.com/index.php":                true,
		"https://nhlpcentral.com/bio.php?name=Chloe%20Toy": true,
		"http://nhlpcentral.com/bio.php?name=Chloe Toy":    true,
		"https://nhlpcentral.com.evil.test/":               false,
		"https://vintageflash.com/":                        false,
		"https://nylonscash.com/":                          false,
		"":                                                 false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestModelName(t *testing.T) {
	cases := []struct {
		url  string
		name string
		ok   bool
	}{
		{"https://nhlpcentral.com/bio.php?name=Chloe%20Toy", "Chloe Toy", true},
		{"https://nhlpcentral.com/bio.php?name=Chloe+Toy&sta=4&num=45", "Chloe Toy", true},
		// The site's own links carry the space unencoded and doubled.
		{"https://nhlpcentral.com/bio.php?name=Taylor  T", "Taylor T", true},
		{"https://nhlpcentral.com/bio.php", "", false},
		{"https://nhlpcentral.com/", "", false},
		{"https://nhlpcentral.com/update.php?id=5", "", false},
	}
	for _, c := range cases {
		name, ok := modelName(c.url)
		if name != c.name || ok != c.ok {
			t.Errorf("modelName(%q) = %q, %v; want %q, %v", c.url, name, ok, c.name, c.ok)
		}
	}
}

func parse(t *testing.T, name string, id int) (models.Scene, probeState) {
	t.Helper()
	sc, state, err := parseScene(fixture(t, name), id, sceneURL(id), "https://nhlpcentral.com", time.Now())
	if err != nil {
		t.Fatalf("parseScene(%s): %v", name, err)
	}
	return sc, state
}

func TestParseScene(t *testing.T) {
	sc, state := parse(t, "update_1123.html", 1123)
	if state != stateScene {
		t.Fatalf("state = %v, want a scene", state)
	}
	if sc.ID != "1123" || sc.SiteID != siteID {
		t.Errorf("ID/SiteID = %q/%q", sc.ID, sc.SiteID)
	}
	if sc.Title != "Hidden assets." {
		t.Errorf("Title = %q", sc.Title)
	}
	if !slices.Equal(sc.Performers, []string{"Stella Cox"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if want := time.Date(2015, time.November, 13, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if sc.Duration != 17*60+30 {
		t.Errorf("Duration = %d, want 1050", sc.Duration)
	}
	if !strings.HasPrefix(sc.Description, "Who would guess that under her pants") {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Thumbnail != "https://nhlpcentral.com/awizicon/1123_6.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	for _, tag := range []string{"retro glamour", "full fashioned", "STOCKING_QUEENS"} {
		if !slices.Contains(sc.Tags, tag) {
			t.Errorf("Tags %v missing %q", sc.Tags, tag)
		}
	}
	if sc.Studio != studioName {
		t.Errorf("Studio = %q, want %q", sc.Studio, studioName)
	}
}

// PH4U is the tag NHLP gives sets from Pantyhosed4U, whose domain now
// redirects here.
func TestParseScenePantyhosed4U(t *testing.T) {
	sc, _ := parse(t, "update_1208.html", 1208)
	if sc.Studio != "Pantyhosed4U" {
		t.Errorf("Studio = %q, want Pantyhosed4U", sc.Studio)
	}
	if sc.Title != "Need a distraction..." || sc.Duration != 16*60+25 {
		t.Errorf("Title/Duration = %q/%d", sc.Title, sc.Duration)
	}
}

// The credit names two models, but the site records only one; co-stars are
// not guessed from the credit.
func TestParseSceneTwoModelCredit(t *testing.T) {
	sc, _ := parse(t, "update_1936.html", 1936)
	if sc.Title != "Sneaky lezbo liaison!" {
		t.Errorf("Title = %q", sc.Title)
	}
	if !slices.Equal(sc.Performers, []string{"Chloe K"}) {
		t.Errorf("Performers = %v", sc.Performers)
	}
}

// Some video sets list their files but no "Video: mm:ss" line.
func TestParseSceneVideoWithoutDuration(t *testing.T) {
	sc, state := parse(t, "update_5.html", 5)
	if state != stateScene {
		t.Fatalf("state = %v, want a scene", state)
	}
	if sc.Duration != 0 || sc.Title != "Just Peachy!" {
		t.Errorf("Duration/Title = %d/%q", sc.Duration, sc.Title)
	}
}

func TestParseScenePhotoOnly(t *testing.T) {
	if _, state := parse(t, "update_103.html", 103); state != statePhotoOnly {
		t.Errorf("state = %v, want photo-only", state)
	}
}

func TestParseSceneUnusedID(t *testing.T) {
	if _, state := parse(t, "update_missing.html", 1262); state != stateMissing {
		t.Errorf("state = %v, want missing", state)
	}
}

func TestParseSceneWithoutTitleIsAnError(t *testing.T) {
	body := strings.Replace(fixture(t, "update_1123.html"), "Stella Cox - Hidden assets.", "", 1)
	if _, _, err := parseScene(body, 1, "u", "s", time.Now()); err == nil {
		t.Error("a set page with no title parsed without error")
	}
}

func TestStripCredit(t *testing.T) {
	cases := []struct{ title, model, want string }{
		{"Stella Cox - Hidden assets.", "Stella Cox", "Hidden assets."},
		{"Kiana - It's all about my panties!", "Kiana Kraze", "It's all about my panties!"},
		{"Sapphire Blue- Sucking toes!", "Sapphire Blue", "Sucking toes!"},
		{"Jessica Pressley -Open bottom girdle gal!", "Jessica Pressley", "Open bottom girdle gal!"},
		{"Victoria C. Bank on me wank on me!", "Victoria C", "Victoria C. Bank on me wank on me!"},
		{"Kloe Kane 01", "Kloe Kane", "Kloe Kane 01"},
		// A dash inside a title is not a credit unless the prefix names the model.
		{"Pre-Christmas treat", "Red", "Pre-Christmas treat"},
		{"Lesbian coffee morning - part two", "Red", "Lesbian coffee morning - part two"},
		{"Red - Quick one, then to the pub?", "", "Red - Quick one, then to the pub?"},
	}
	for _, c := range cases {
		if got := stripCredit(c.title, c.model); got != c.want {
			t.Errorf("stripCredit(%q, %q) = %q, want %q", c.title, c.model, got, c.want)
		}
	}
}

func TestStudioFor(t *testing.T) {
	cases := []struct {
		tags []string
		want string
	}{
		{[]string{"PH4U", "pantyhose"}, "Pantyhosed4U"},
		{[]string{"PH4U", "PHFLASH"}, studioName},
		{[]string{"STOCKING_QUEENS", "retro glamour"}, studioName},
		{nil, studioName},
	}
	for _, c := range cases {
		if got := studioFor(c.tags); got != c.want {
			t.Errorf("studioFor(%v) = %q, want %q", c.tags, got, c.want)
		}
	}
}

func TestSplitTagsDedupesCaseInsensitively(t *testing.T) {
	got := splitTags(" ass, Ass ,, garter  belt, reinforced heel and toe")
	want := []string{"ass", "garter belt", "reinforced heel and toe"}
	if !slices.Equal(got, want) {
		t.Errorf("splitTags = %v, want %v", got, want)
	}
}

func TestParseBioPage(t *testing.T) {
	ids, more, err := parseBioPage(fixture(t, "bio_first.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []int{2740, 2663, 2589, 2549}) || !more {
		t.Errorf("first page = %v more=%v", ids, more)
	}
	ids, more, err = parseBioPage(fixture(t, "bio_last.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []int{1792}) || more {
		t.Errorf("last page = %v more=%v", ids, more)
	}
	if _, _, err := parseBioPage("<html>redesigned</html>"); err == nil {
		t.Error("a page without the set block parsed without error")
	}
}

func TestLatestIDs(t *testing.T) {
	ids := uniqueIDs(latestRe, fixture(t, "home.html"))
	want := []int{2745, 2744, 939, 2740, 2735, 2732, 2733, 2729, 2728, 2725, 2724}
	if !slices.Equal(ids, want) {
		t.Errorf("latest ids = %v, want %v", ids, want)
	}
}

// ---- end-to-end ----

type site struct {
	t      *testing.T
	home   string
	sets   map[int]string // id -> page body
	bio    map[int]string // sta -> page body
	status int            // non-zero answers every request with this status

	mu       sync.Mutex
	probed   []int
	bioSta   []int
	homeHits int
}

func (st *site) handler(w http.ResponseWriter, r *http.Request) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.status != 0 {
		w.WriteHeader(st.status)
		return
	}
	switch r.URL.Path {
	case "/":
		st.homeHits++
		_, _ = w.Write([]byte(st.home))
	case "/update.php":
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		st.probed = append(st.probed, id)
		if body, ok := st.sets[id]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(fixture(st.t, "update_missing.html")))
	case "/bio.php":
		sta, _ := strconv.Atoi(r.URL.Query().Get("sta"))
		st.bioSta = append(st.bioSta, sta)
		if r.URL.Query().Get("name") != "Chloe Toy" {
			_, _ = w.Write([]byte(`<table><tr><!--u --> </tr></table>`))
			return
		}
		_, _ = w.Write([]byte(st.bio[sta]))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (st *site) start() *Scraper {
	st.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(st.handler))
	st.t.Cleanup(srv.Close)
	orig := siteBase
	siteBase = srv.URL
	st.t.Cleanup(func() { siteBase = orig })
	s := New()
	s.Client = srv.Client()
	return s
}

func (st *site) probedIDs() []int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return slices.Clone(st.probed)
}

type result struct {
	ids     []string
	errs    []error
	stopped bool
	total   int
}

func scrape(t *testing.T, s *Scraper, studioURL string, opts scraper.ListOpts) result {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), studioURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	var r result
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			r.ids = append(r.ids, res.Scene.ID)
			testutil.ValidateScene(t, res.Scene)
		case scraper.KindError:
			r.errs = append(r.errs, res.Err)
		case scraper.KindStoppedEarly:
			r.stopped = true
		case scraper.KindTotal:
			r.total = res.Total
		}
	}
	return r
}

func TestWalkCollectsVideoSetsAndStopsAfterTheGap(t *testing.T) {
	// Sets 1 and 560 are further apart than maxGap; only the photo-only set
	// at 280 between them keeps the walk going, so it must count as used.
	st := &site{t: t, sets: map[int]string{
		1:   fixture(t, "update_5.html"),
		280: fixture(t, "update_103.html"),
		560: fixture(t, "update_1208.html"),
		570: fixture(t, "update_1936.html"),
	}}
	s := st.start()

	r := scrape(t, s, "https://nhlpcentral.com", scraper.ListOpts{Workers: 8})
	if len(r.errs) != 0 {
		t.Fatalf("errors: %v", r.errs)
	}
	if want := []string{"1", "560", "570"}; !slices.Equal(r.ids, want) {
		t.Errorf("scenes = %v, want %v", r.ids, want)
	}
	probed := st.probedIDs()
	if last := slices.Max(probed); last > 570+maxGap+batchSize {
		t.Errorf("walked to id %d, want it to stop within %d of the last set", last, maxGap+batchSize)
	}
}

// Incremental: the homepage lists the latest releases, so a known id there
// ends the scrape without walking the id range.
func TestIncrementalStopsAtKnownIDOnHomepage(t *testing.T) {
	body := fixture(t, "update_1123.html")
	st := &site{t: t, home: fixture(t, "home.html"), sets: map[int]string{2745: body, 2744: body}}
	s := st.start()

	r := scrape(t, s, "https://nhlpcentral.com", scraper.ListOpts{KnownIDs: map[string]bool{"939": true}})
	if len(r.errs) != 0 {
		t.Fatalf("errors: %v", r.errs)
	}
	if !slices.Equal(r.ids, []string{"2745", "2744"}) || !r.stopped {
		t.Errorf("scenes = %v stopped=%v, want [2745 2744] and an early stop", r.ids, r.stopped)
	}
	if probed := st.probedIDs(); len(probed) != 2 {
		t.Errorf("probed %v, want only the two new homepage ids", probed)
	}
}

// With no known id among the latest releases the walk runs, and a set already
// emitted from the homepage is not emitted again.
func TestIncrementalFallsBackToTheWalk(t *testing.T) {
	body := fixture(t, "update_1123.html")
	st := &site{t: t, home: `<a rel="//x/update.php?id=3"></a><a rel="//x/update.php?id=2"></a>`,
		sets: map[int]string{1: body, 2: body, 3: body}}
	s := st.start()

	r := scrape(t, s, "https://nhlpcentral.com", scraper.ListOpts{KnownIDs: map[string]bool{"77": true}})
	if len(r.errs) != 0 || r.stopped {
		t.Fatalf("errors=%v stopped=%v", r.errs, r.stopped)
	}
	if want := []string{"3", "2", "1"}; !slices.Equal(r.ids, want) {
		t.Errorf("scenes = %v, want %v", r.ids, want)
	}
}

// A full scrape never reads the homepage: the walk alone is authoritative.
func TestFullScrapeSkipsTheHomepage(t *testing.T) {
	st := &site{t: t, home: fixture(t, "home.html"), sets: map[int]string{1: fixture(t, "update_1123.html")}}
	s := st.start()
	if r := scrape(t, s, "https://nhlpcentral.com", scraper.ListOpts{}); len(r.ids) != 1 {
		t.Errorf("scenes = %v, want 1", r.ids)
	}
	if st.homeHits != 0 {
		t.Errorf("homepage fetched %d times on a full scrape", st.homeHits)
	}
}

func TestWalkAbortsWhenTheSiteStopsAnswering(t *testing.T) {
	st := &site{t: t, status: http.StatusForbidden}
	s := st.start()

	r := scrape(t, s, "https://nhlpcentral.com", scraper.ListOpts{})
	if len(r.ids) != 0 {
		t.Errorf("scenes = %v, want none", r.ids)
	}
	if n := len(r.errs); n == 0 || n > maxConsecutiveErrors+1 {
		t.Errorf("%d errors, want the walk to abort after %d", n, maxConsecutiveErrors)
	}
}

func modelSite(t *testing.T) *site {
	body := fixture(t, "update_1123.html")
	return &site{t: t,
		bio: map[int]string{0: fixture(t, "bio_first.html"), 4: fixture(t, "bio_last.html")},
		sets: map[int]string{
			2740: body, 2663: body, 2589: body, 2549: body, 1792: body,
		}}
}

const modelURL = "https://nhlpcentral.com/bio.php?name=Chloe%20Toy"

func TestModelPage(t *testing.T) {
	st := modelSite(t)
	s := st.start()

	r := scrape(t, s, modelURL, scraper.ListOpts{})
	if len(r.errs) != 0 {
		t.Fatalf("errors: %v", r.errs)
	}
	if want := []string{"2740", "2663", "2589", "2549", "1792"}; !slices.Equal(r.ids, want) {
		t.Errorf("scenes = %v, want %v", r.ids, want)
	}
	if r.total != 5 {
		t.Errorf("progress total = %d, want 5", r.total)
	}
	if !slices.Equal(st.bioSta, []int{0, 4}) {
		t.Errorf("model pages requested at sta=%v, want [0 4]", st.bioSta)
	}
}

func TestModelPageStopsAtKnownID(t *testing.T) {
	st := modelSite(t)
	s := st.start()

	r := scrape(t, s, modelURL, scraper.ListOpts{KnownIDs: map[string]bool{"2589": true}})
	if !slices.Equal(r.ids, []string{"2740", "2663"}) || !r.stopped {
		t.Errorf("scenes = %v stopped=%v", r.ids, r.stopped)
	}
	if !slices.Equal(st.bioSta, []int{0}) {
		t.Errorf("model pages requested at sta=%v, want only the first", st.bioSta)
	}
}

// A set the model page lists but whose page is unused was not collected, so it
// is an error rather than a silent skip.
func TestModelPageListedSetMissingIsAnError(t *testing.T) {
	st := modelSite(t)
	delete(st.sets, 2663)
	s := st.start()

	r := scrape(t, s, modelURL, scraper.ListOpts{})
	if len(r.ids) != 4 || len(r.errs) != 1 {
		t.Errorf("scenes=%v errs=%v, want 4 scenes and 1 error", r.ids, r.errs)
	}
}

func TestUnknownModelIsAnError(t *testing.T) {
	st := modelSite(t)
	s := st.start()

	r := scrape(t, s, "https://nhlpcentral.com/bio.php?name=Nobody", scraper.ListOpts{})
	if len(r.ids) != 0 || len(r.errs) != 1 {
		t.Errorf("scenes=%v errs=%v, want one error", r.ids, r.errs)
	}
}

func TestCancellation(t *testing.T) {
	body := fixture(t, "update_1123.html")
	sets := map[int]string{}
	for i := 1; i <= 2000; i++ {
		sets[i] = body
	}
	st := &site{t: t, sets: sets}
	s := st.start()
	testutil.AssertCancellable(t, s, "https://nhlpcentral.com", scraper.ListOpts{})
}

package testutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// goodScene is the shape sceneProblems must accept without complaint. Each
// test below breaks exactly one field of it.
func goodScene() models.Scene {
	return models.Scene{
		ID:         "123",
		SiteID:     "example",
		Title:      "A Scene",
		URL:        "https://example.com/scene/123",
		Date:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Duration:   600,
		Performers: []string{"Someone"},
		ScrapedAt:  time.Now().UTC(),
	}
}

func TestSceneProblemsAcceptsValidScene(t *testing.T) {
	if got := sceneProblems(goodScene()); len(got) != 0 {
		t.Errorf("sceneProblems(good) = %v, want none", got)
	}
}

// Every rule ValidateScene claims to enforce must actually reject its bad
// shape — otherwise 344 integration tests are asserting nothing.
func TestSceneProblemsRejectsEachBadShape(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*models.Scene)
		want string
	}{
		{"empty ID", func(s *models.Scene) { s.ID = "" }, "empty ID"},
		{"empty SiteID", func(s *models.Scene) { s.SiteID = "" }, "empty SiteID"},
		{"empty Title", func(s *models.Scene) { s.Title = "" }, "empty Title"},
		{"empty URL", func(s *models.Scene) { s.URL = "" }, "empty URL"},
		{"relative URL", func(s *models.Scene) { s.URL = "/scene/123" }, "malformed URL"},
		{"scheme-only URL", func(s *models.Scene) { s.URL = "https://" }, "malformed URL"},
		{"negative Duration", func(s *models.Scene) { s.Duration = -1 }, "implausible Duration"},
		{"Duration over 7 days", func(s *models.Scene) { s.Duration = 7*24*60*60 + 1 }, "implausible Duration"},
		{"no credits", func(s *models.Scene) { s.Performers = nil; s.Studio = "" }, "neither Performers nor Studio"},
		{"zero ScrapedAt", func(s *models.Scene) { s.ScrapedAt = time.Time{} }, "zero ScrapedAt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := goodScene()
			c.mut(&s)
			got := sceneProblems(s)
			if len(got) == 0 {
				t.Fatalf("sceneProblems accepted a scene with %s", c.name)
			}
			if !strings.Contains(strings.Join(got, "; "), c.want) {
				t.Errorf("sceneProblems = %v, want a message containing %q", got, c.want)
			}
		})
	}
}

// Studio alone satisfies the credits rule — many sites have no per-scene
// performer list.
func TestSceneProblemsAcceptsStudioWithoutPerformers(t *testing.T) {
	s := goodScene()
	s.Performers = nil
	s.Studio = "Example Studio"
	if got := sceneProblems(s); len(got) != 0 {
		t.Errorf("sceneProblems = %v, want none (Studio should satisfy the credits rule)", got)
	}
}

// Duration 0 and a zero Date are advisory, not failures: list endpoints often
// omit both. If these ever become hard errors, ~40 scrapers start failing.
func TestSceneProblemsToleratesMissingDurationAndDate(t *testing.T) {
	s := goodScene()
	s.Duration = 0
	s.Date = time.Time{}
	if got := sceneProblems(s); len(got) != 0 {
		t.Errorf("sceneProblems = %v, want none (Duration/Date are advisory)", got)
	}
	notes := strings.Join(sceneNotes(s), "; ")
	if !strings.Contains(notes, "zero Date") {
		t.Errorf("sceneNotes = %q, want a zero-Date note", notes)
	}
}

func TestSceneNotesSilentOnGoodScene(t *testing.T) {
	s := goodScene()
	s.Thumbnail = "https://example.com/t.jpg"
	if got := sceneNotes(s); len(got) != 0 {
		t.Errorf("sceneNotes = %v, want none", got)
	}
}

// A missing thumbnail is advisory, not a failure: promoting it to an error
// before the blast radius across ~1600 sites is measured would break smoke runs
// for sites that legitimately publish none.
func TestMissingThumbnailIsAdvisoryOnly(t *testing.T) {
	s := goodScene() // no Thumbnail set
	if got := sceneProblems(s); len(got) != 0 {
		t.Errorf("sceneProblems = %v, want none (Thumbnail must not fail a test yet)", got)
	}
	notes := strings.Join(sceneNotes(s), "; ")
	if !strings.Contains(notes, "empty Thumbnail") {
		t.Errorf("sceneNotes = %q, want an empty-Thumbnail note", notes)
	}
}

func TestDrainSeparatesKinds(t *testing.T) {
	ch := make(chan scraper.SceneResult, 8)
	ch <- scraper.Progress(42)
	ch <- scraper.Scene(models.Scene{ID: "1"})
	ch <- scraper.Error(io.ErrUnexpectedEOF)
	ch <- scraper.Scene(models.Scene{ID: "2"})
	ch <- scraper.StoppedEarly()
	close(ch)

	scenes, stoppedEarly, errs := drain(ch)
	if len(scenes) != 2 || scenes[0].ID != "1" || scenes[1].ID != "2" {
		t.Errorf("scenes = %v, want IDs 1,2 in order", scenes)
	}
	if !stoppedEarly {
		t.Error("stoppedEarly = false, want true")
	}
	if len(errs) != 1 || !errors.Is(errs[0], io.ErrUnexpectedEOF) {
		t.Errorf("errs = %v, want the one sent error", errs)
	}
}

func TestDrainEmptyChannel(t *testing.T) {
	ch := make(chan scraper.SceneResult)
	close(ch)
	scenes, stoppedEarly, errs := drain(ch)
	if len(scenes) != 0 || stoppedEarly || len(errs) != 0 {
		t.Errorf("drain(empty) = (%v, %v, %v), want all zero", scenes, stoppedEarly, errs)
	}
}

// A Progress signal alone must not be mistaken for a scene.
func TestDrainIgnoresProgress(t *testing.T) {
	ch := make(chan scraper.SceneResult, 1)
	ch <- scraper.Progress(99)
	close(ch)
	if scenes, _, _ := drain(ch); len(scenes) != 0 {
		t.Errorf("scenes = %v, want none", scenes)
	}
}

func TestIsPlaceholder(t *testing.T) {
	if !isPlaceholder("https://REPLACE-ME.com/") {
		t.Error("isPlaceholder did not detect REPLACE-ME")
	}
	if isPlaceholder("https://example.com/studio") {
		t.Error("isPlaceholder flagged a real URL")
	}
}

const sitemapFixture = `<?xml version="1.0"?>
<urlset><url><loc>https://honeytrans.com/scenes/1/a</loc></url>
<url><loc>https://honeytrans.com/scenes/2/b</loc></url></urlset>`

func TestRewriteSitemapHost(t *testing.T) {
	out, err := rewriteSitemapHost([]byte(sitemapFixture), "https://honeytrans.com", "http://127.0.0.1:1234")
	if err != nil {
		t.Fatalf("rewriteSitemapHost: %v", err)
	}
	if strings.Contains(string(out), "honeytrans.com") {
		t.Errorf("live host survived the rewrite: %s", out)
	}
	if n := strings.Count(string(out), "http://127.0.0.1:1234"); n != 2 {
		t.Errorf("rewrote %d occurrences, want 2", n)
	}
}

// The tripwire is the whole point of the helper: a refreshed fixture on a
// different host would make the rewrite a silent no-op and send detail fetches
// to the live site.
func TestRewriteSitemapHostFailsWhenHostAbsent(t *testing.T) {
	_, err := rewriteSitemapHost([]byte(sitemapFixture), "https://someothersite.com", "http://127.0.0.1:1234")
	if err == nil {
		t.Fatal("rewriteSitemapHost accepted a fixture not containing the live host")
	}
	if !strings.Contains(err.Error(), "no-op") {
		t.Errorf("error = %v, want it to explain the no-op risk", err)
	}
}

func TestSitemapServerServesRewrittenSitemapAndDetail(t *testing.T) {
	srv := SitemapServer(t, "https://honeytrans.com", []byte(sitemapFixture), []byte("DETAIL-BODY"))

	body := httpGet(t, srv.URL+"/sitemap.xml")
	if strings.Contains(body, "honeytrans.com") {
		t.Errorf("served sitemap still points at the live host: %s", body)
	}
	if !strings.Contains(body, srv.URL) {
		t.Errorf("served sitemap does not point at the test server: %s", body)
	}

	// Any non-sitemap path is a detail page.
	if got := httpGet(t, srv.URL+"/scenes/1/a"); got != "DETAIL-BODY" {
		t.Errorf("detail body = %q, want DETAIL-BODY", got)
	}
	// Per-language and video sitemap variants must also serve the sitemap.
	if got := httpGet(t, srv.URL+"/sitemap_video.xml"); !strings.Contains(got, "urlset") {
		t.Errorf("sitemap_video.xml did not serve the sitemap: %q", got)
	}
}

func httpGet(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", u, err)
	}
	return string(b)
}

func TestDuplicateIDs(t *testing.T) {
	sc := func(id string) models.Scene { return models.Scene{ID: id} }

	if got := duplicateIDs([]models.Scene{sc("a"), sc("b"), sc("c")}); len(got) != 0 {
		t.Errorf("duplicateIDs(unique) = %v, want none", got)
	}
	if got := duplicateIDs(nil); len(got) != 0 {
		t.Errorf("duplicateIDs(nil) = %v, want none", got)
	}
	got := duplicateIDs([]models.Scene{sc("a"), sc("b"), sc("a"), sc("b"), sc("a")})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("duplicateIDs = %v, want [a b] (each reported once, in first-repeat order)", got)
	}
}

// leakyScraper ignores ctx entirely: it keeps sending until its buffer drains,
// modelling a scraper whose channel sends are not guarded by a select on
// ctx.Done(). AssertCancellable must catch it.
type leakyScraper struct{ block chan struct{} }

func (l *leakyScraper) ID() string         { return "leaky" }
func (l *leakyScraper) Patterns() []string { return []string{"leaky.test"} }
func (l *leakyScraper) MatchesURL(string) bool {
	return true
}
func (l *leakyScraper) ListScenes(_ context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		out <- scraper.Scene(models.Scene{ID: "1"})
		<-l.block // never returns until the test releases it
	}()
	return out, nil
}

// goodScraper exits promptly once its context is cancelled.
type goodScraper struct{}

func (goodScraper) ID() string             { return "good" }
func (goodScraper) Patterns() []string     { return []string{"good.test"} }
func (goodScraper) MatchesURL(string) bool { return true }
func (goodScraper) ListScenes(ctx context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		for i := 0; ; i++ {
			select {
			case out <- scraper.Scene(models.Scene{ID: strconv.Itoa(i)}):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func TestAssertCancellablePassesForACancellableScraper(t *testing.T) {
	AssertCancellable(t, goodScraper{}, "https://good.test/", scraper.ListOpts{})
}

// The helper is only worth having if it fails on a scraper that ignores
// cancellation — verified here by running it against one and asserting it
// reports a failure, rather than trusting that it would.
func TestAssertCancellableCatchesALeak(t *testing.T) {
	l := &leakyScraper{block: make(chan struct{})}
	defer close(l.block)

	// The point here is that the timeout fires at all, not how long it is.
	orig := cancelDrainTimeout
	cancelDrainTimeout = 50 * time.Millisecond
	defer func() { cancelDrainTimeout = orig }()

	fake := &testing.T{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		AssertCancellable(fake, l, "https://leaky.test/", scraper.ListOpts{})
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("AssertCancellable never returned")
	}
	if !fake.Failed() {
		t.Error("AssertCancellable passed a scraper that ignores context cancellation")
	}
}

// --- site table helpers -------------------------------------------------------
//
// CheckSiteTable and CheckSiteDomainTable guard 24 config tables, but they are
// only ever *called* from those packages — per-package coverage therefore showed
// them as untested here. These exercise them directly, and more importantly pin
// that each rule fires, since a table check that cannot fail is worse than none.

// tableProblems runs a checker against rows and reports the messages it emitted,
// using a throwaway *testing.T so a deliberate failure does not fail this test.
func tableProblems(t *testing.T, check func(*testing.T, []SiteRow), rows []SiteRow) bool {
	t.Helper()
	fake := &testing.T{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		check(fake, rows)
	}()
	<-done
	return fake.Failed()
}

func goodRows() []SiteRow {
	return []SiteRow{
		{ID: "a", Base: "https://a.example", Studio: "A", Patterns: []string{"a.example"},
			MatchRe: regexp.MustCompile(`^https?://(?:www\.)?a\.example`)},
		{ID: "b", Base: "https://b.example", Studio: "B", Patterns: []string{"b.example"},
			MatchRe: regexp.MustCompile(`^https?://(?:www\.)?b\.example`)},
	}
}

func TestCheckSiteTableAcceptsAGoodTable(t *testing.T) {
	CheckSiteTable(t, goodRows())
	CheckSiteTableDomains(t, goodRows())
}

func TestCheckSiteTableRejectsEachFault(t *testing.T) {
	cases := []struct {
		name string
		mut  func([]SiteRow) []SiteRow
	}{
		{"empty ID", func(r []SiteRow) []SiteRow { r[0].ID = ""; return r }},
		{"empty Base", func(r []SiteRow) []SiteRow { r[0].Base = ""; return r }},
		{"duplicate ID", func(r []SiteRow) []SiteRow { r[1].ID = r[0].ID; return r }},
		{"no Patterns", func(r []SiteRow) []SiteRow { r[0].Patterns = nil; return r }},
		{"nil MatchRe", func(r []SiteRow) []SiteRow { r[0].MatchRe = nil; return r }},
		{"relative Base", func(r []SiteRow) []SiteRow { r[0].Base = "a.example"; return r }},
		{"trailing slash", func(r []SiteRow) []SiteRow { r[0].Base = "https://a.example/"; return r }},
		// Studio is only required when other rows set one.
		{"one row missing Studio", func(r []SiteRow) []SiteRow { r[0].Studio = ""; return r }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !tableProblems(t, CheckSiteTable, c.mut(goodRows())) {
				t.Errorf("CheckSiteTable accepted a table with %s", c.name)
			}
		})
	}
}

// A table where *no* row carries a Studio is a design choice (paysite), not a
// fault — only a partly filled column is.
func TestCheckSiteTableAllowsAnEntirelyAbsentStudio(t *testing.T) {
	rows := goodRows()
	rows[0].Studio, rows[1].Studio = "", ""
	if tableProblems(t, CheckSiteTable, rows) {
		t.Error("CheckSiteTable rejected a table with no Studio column at all")
	}
}

func TestCheckSiteTableDomainsRejectsACopyPastedRegex(t *testing.T) {
	rows := goodRows()
	// The classic slip: row a's regex left naming row b's domain.
	rows[0].MatchRe = rows[1].MatchRe
	if !tableProblems(t, CheckSiteTableDomains, rows) {
		t.Error("CheckSiteTableDomains accepted a regex matching another row's base")
	}
}

func TestCheckSiteTableDomainsRejectsAnUnclaimedPattern(t *testing.T) {
	rows := goodRows()
	rows[0].Patterns = []string{"somewhere-else.example"}
	if !tableProblems(t, CheckSiteTableDomains, rows) {
		t.Error("CheckSiteTableDomains accepted a pattern its own MatchRe rejects")
	}
}

func TestCheckSiteDomainTable(t *testing.T) {
	good := []DomainRow{
		{ID: "a", Domain: "a.example", Studio: "A"},
		{ID: "b", Domain: "b.example", Studio: "B"},
	}
	run := func(rows []DomainRow) bool {
		fake := &testing.T{}
		done := make(chan struct{})
		go func() { defer close(done); CheckSiteDomainTable(fake, rows) }()
		<-done
		return fake.Failed()
	}
	if run(good) {
		t.Error("CheckSiteDomainTable rejected a good table")
	}
	for _, c := range []struct {
		name string
		rows []DomainRow
	}{
		{"duplicate domain", []DomainRow{{ID: "a", Domain: "x.example", Studio: "A"}, {ID: "b", Domain: "x.example", Studio: "B"}}},
		{"scheme in domain", []DomainRow{{ID: "a", Domain: "https://a.example", Studio: "A"}}},
		{"path in domain", []DomainRow{{ID: "a", Domain: "a.example/x", Studio: "A"}}},
		{"no dot", []DomainRow{{ID: "a", Domain: "localhost", Studio: "A"}}},
		{"uppercase", []DomainRow{{ID: "a", Domain: "A.example", Studio: "A"}}},
		{"empty studio", []DomainRow{{ID: "a", Domain: "a.example"}}},
		{"empty id", []DomainRow{{Domain: "a.example", Studio: "A"}}},
	} {
		if !run(c.rows) {
			t.Errorf("CheckSiteDomainTable accepted %s", c.name)
		}
	}
}

// Surrounding whitespace on a performer or tag name is a site artefact that
// matters because these strings are compared exactly: `fss stash import` looks
// entities up in Stash by name, so an untrimmed one creates a duplicate.
//
// Advisory rather than a failure — see sceneNotes for why — so this pins both
// halves: the note is emitted, and sceneProblems stays quiet.
func TestSceneNotesFlagsUntrimmedNames(t *testing.T) {
	s := goodScene()
	s.Thumbnail = "https://example.com/t.jpg"
	s.Performers = []string{"Nikki Nuttz ", "Clean Name"}
	s.Tags = []string{" Blowjob", "Clean Tag"}

	notes := strings.Join(sceneNotes(s), "; ")
	if !strings.Contains(notes, `performer "Nikki Nuttz "`) {
		t.Errorf("sceneNotes = %q, want a note naming the untrimmed performer", notes)
	}
	if !strings.Contains(notes, `tag " Blowjob"`) {
		t.Errorf("sceneNotes = %q, want a note naming the untrimmed tag", notes)
	}
	if strings.Contains(notes, "Clean Name") || strings.Contains(notes, "Clean Tag") {
		t.Errorf("sceneNotes = %q, must not flag already-trimmed entries", notes)
	}
	if got := sceneProblems(s); len(got) != 0 {
		t.Errorf("sceneProblems = %v, want none — untrimmed names must not fail a scraper", got)
	}
}

// ---- site outage vs scraper regression ----
//
// A scrape that returns nothing is either a scraper that broke or a site that
// stopped serving. These guard the line between the two: only the second may
// downgrade a failure to a skip, or a real regression would go unreported.

func TestSiteSideErrorClassifies(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"500 is the site failing", &httpx.StatusError{StatusCode: 500}, true},
		{"503 is the site failing", &httpx.StatusError{StatusCode: 503}, true},
		{"404 is usually our URL", &httpx.StatusError{StatusCode: 404}, false},
		{"403 is not an outage", &httpx.StatusError{StatusCode: 403}, false},
		{"DNS failure", &net.DNSError{Err: "no such host", Name: "gone.example"}, true},
		{"connection refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		{"a parse failure is ours", errors.New("parsing listing: no cards found"), false},
		{"wrapped 500 still counts", fmt.Errorf("page 3: %w", &httpx.StatusError{StatusCode: 500}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := siteSideError(c.err); got != c.want {
				t.Errorf("siteSideError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// A timeout is as likely to be a client deadline set below the origin's own
// ceiling — our bug — as it is the site being down, so it must not skip.
func TestTimeoutIsNotTreatedAsAnOutage(t *testing.T) {
	err := fmt.Errorf("API page 3: %w", context.DeadlineExceeded)
	if _, down := siteSideError(err); down {
		t.Error("a timeout must stay a failure, not become a skip")
	}
}

// One error we cannot blame on the site keeps the whole run a failure.
func TestSiteIsDownRequiresEveryErrorToBeSiteSide(t *testing.T) {
	mixed := []error{
		&httpx.StatusError{StatusCode: 500},
		errors.New("parsing detail page: title not found"),
	}
	if _, down := siteIsDown("https://example.com", mixed); down {
		t.Error("a parse error alongside a 500 must still fail")
	}

	allSite := []error{
		&httpx.StatusError{StatusCode: 500},
		&httpx.StatusError{StatusCode: 502},
	}
	if _, down := siteIsDown("https://example.com", allSite); !down {
		t.Error("errors that are all site-side should report an outage")
	}
}

func TestProbeSiteDetectsOutages(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer down.Close()
	if reason, isDown := probeSite(down.URL); !isDown {
		t.Errorf("a 5xx site should read as down, got %q", reason)
	}

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html>fine</html>")
	}))
	defer up.Close()
	if reason, isDown := probeSite(up.URL); isDown {
		t.Errorf("a healthy site must not read as down: %q", reason)
	}
}

// A site that redirects onto somebody else's domain has been parked or folded
// into another brand; the catalogue the scraper targets is not there.
func TestProbeSiteDetectsOffDomainRedirect(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "<html>a different site</html>")
	}))
	defer elsewhere.Close()

	// 127.0.0.1 and localhost are different hostnames, which is enough to
	// exercise the comparison without leaving the machine.
	target := strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1)
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}))
	defer moved.Close()

	if _, isDown := probeSite(moved.URL); !isDown {
		t.Error("a redirect onto another domain should read as down")
	}
}

func TestSameSiteIgnoresWWW(t *testing.T) {
	cases := []struct {
		requested, final string
		want             bool
	}{
		{"https://www.example.com/videos", "https://example.com/videos", true},
		{"https://example.com/a", "https://www.example.com/b", true},
		{"https://example.com", "https://elsewhere.com", false},
		{"https://vintageflash.com", "https://nhlpcentral.com/", false},
	}
	for _, c := range cases {
		u, err := url.Parse(c.final)
		if err != nil {
			t.Fatalf("parse %q: %v", c.final, err)
		}
		if got := sameSite(c.requested, u); got != c.want {
			t.Errorf("sameSite(%q, %q) = %v, want %v", c.requested, c.final, got, c.want)
		}
	}
}

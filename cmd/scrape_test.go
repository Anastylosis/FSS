package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMergeSiteDelays_emptyInputs(t *testing.T) {
	got, err := mergeSiteDelays(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestMergeSiteDelays_configOnly(t *testing.T) {
	got, err := mergeSiteDelays(map[string]int{"manyvids": 100, "pornhub": 2000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["manyvids"] != 100 || got["pornhub"] != 2000 {
		t.Errorf("got %v", got)
	}
}

func TestMergeSiteDelays_cliOverlaysConfig(t *testing.T) {
	cfg := map[string]int{"manyvids": 100, "pornhub": 2000}
	got, err := mergeSiteDelays(cfg, []string{"manyvids=500", "brazzers=300"})
	if err != nil {
		t.Fatal(err)
	}
	if got["manyvids"] != 500 {
		t.Errorf("CLI did not override config: %v", got)
	}
	if got["pornhub"] != 2000 {
		t.Errorf("config value preserved when not overridden: %v", got)
	}
	if got["brazzers"] != 300 {
		t.Errorf("CLI-only entry missing: %v", got)
	}
}

func TestMergeSiteDelays_zeroIsValid(t *testing.T) {
	// User explicitly disables delay for a site that the config set non-zero.
	got, err := mergeSiteDelays(map[string]int{"pornhub": 2000}, []string{"pornhub=0"})
	if err != nil {
		t.Fatal(err)
	}
	if got["pornhub"] != 0 {
		t.Errorf("explicit 0 should override, got %d", got["pornhub"])
	}
}

func TestMergeSiteDelays_skipsBlankPairs(t *testing.T) {
	got, err := mergeSiteDelays(nil, []string{"", "  ", "manyvids=50"})
	if err != nil {
		t.Fatalf("blank pairs should be skipped, got error: %v", err)
	}
	if len(got) != 1 || got["manyvids"] != 50 {
		t.Errorf("got %v", got)
	}
}

func TestMergeSiteDelays_rejectsMalformed(t *testing.T) {
	cases := []string{
		"no-equals-here",
		"=missing-name",
		"missing-value=",
		"manyvids=not-a-number",
		"manyvids=-100",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			_, err := mergeSiteDelays(nil, []string{p})
			if err == nil {
				t.Errorf("expected error for %q", p)
			}
		})
	}
}

func TestResolveSiteDelay(t *testing.T) {
	defaultDelay := 500 * time.Millisecond
	perSite := map[string]int{
		"manyvids": 0,    // explicit 0 = no delay even when default is non-zero
		"pornhub":  2000, // explicit override
	}

	cases := []struct {
		siteID string
		want   time.Duration
	}{
		{"manyvids", 0},
		{"pornhub", 2 * time.Second},
		{"brazzers", 500 * time.Millisecond}, // no override → default
		{"unknown", 500 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.siteID, func(t *testing.T) {
			got := resolveSiteDelay(c.siteID, defaultDelay, perSite)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestResolveSiteDelay_nilMap(t *testing.T) {
	got := resolveSiteDelay("any", 500*time.Millisecond, nil)
	if got != 500*time.Millisecond {
		t.Errorf("nil map should fall back to default, got %v", got)
	}
}

// Sanity check that the error from mergeSiteDelays mentions the malformed pair.
func TestMergeSiteDelays_errorIncludesOffendingPair(t *testing.T) {
	_, err := mergeSiteDelays(nil, []string{"manyvids=oops"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "manyvids=oops") {
		t.Errorf("error should mention the bad pair, got: %v", err)
	}
}

func snap(date time.Time, price float64) models.PriceSnapshot {
	return models.PriceSnapshot{Date: date, Regular: price}
}

func freeSnap(date time.Time) models.PriceSnapshot {
	return models.PriceSnapshot{Date: date, IsFree: true}
}

func TestCarryOverPriceHistory_mergesExistingAndFresh(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	existing := models.Scene{ID: "1"}
	existing.AddPrice(snap(t0, 9.99))
	existing.AddPrice(snap(t1, 4.99))

	fresh := models.Scene{ID: "1"}
	fresh.AddPrice(snap(t2, 7.99))

	got := carryOverPriceHistory(fresh, existing)
	if len(got.PriceHistory) != 3 {
		t.Fatalf("got %d snapshots, want 3", len(got.PriceHistory))
	}
	if got.PriceHistory[0].Regular != 9.99 {
		t.Errorf("first snapshot = %.2f, want 9.99", got.PriceHistory[0].Regular)
	}
	if got.PriceHistory[2].Regular != 7.99 {
		t.Errorf("last snapshot = %.2f, want 7.99 (fresh)", got.PriceHistory[2].Regular)
	}
	if got.LowestPrice != 4.99 {
		t.Errorf("lowestPrice = %.2f, want 4.99", got.LowestPrice)
	}
}

func TestCarryOverPriceHistory_noExistingHistory(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := models.Scene{ID: "1"}
	fresh := models.Scene{ID: "1"}
	fresh.AddPrice(snap(t0, 9.99))

	got := carryOverPriceHistory(fresh, existing)
	if len(got.PriceHistory) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(got.PriceHistory))
	}
	if got.LowestPrice != 9.99 {
		t.Errorf("lowestPrice = %.2f", got.LowestPrice)
	}
}

func TestCarryOverPriceHistory_noFreshPrice(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := models.Scene{ID: "1"}
	existing.AddPrice(snap(t0, 5.00))

	fresh := models.Scene{ID: "1"}

	got := carryOverPriceHistory(fresh, existing)
	if len(got.PriceHistory) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(got.PriceHistory))
	}
	if got.LowestPrice != 5.00 {
		t.Errorf("lowestPrice = %.2f", got.LowestPrice)
	}
}

func TestCarryOverPriceHistory_bothEmpty(t *testing.T) {
	got := carryOverPriceHistory(models.Scene{ID: "1"}, models.Scene{ID: "1"})
	if len(got.PriceHistory) != 0 {
		t.Errorf("got %d snapshots, want 0", len(got.PriceHistory))
	}
	if got.LowestPriceDate != nil {
		t.Error("lowestPriceDate should be nil")
	}
}

func TestCarryOverPriceHistory_freshLowerPrice(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	existing := models.Scene{ID: "1"}
	existing.AddPrice(snap(t0, 9.99))

	fresh := models.Scene{ID: "1"}
	fresh.AddPrice(snap(t1, 2.99))

	got := carryOverPriceHistory(fresh, existing)
	if got.LowestPrice != 2.99 {
		t.Errorf("lowestPrice = %.2f, want 2.99", got.LowestPrice)
	}
	if !got.LowestPriceDate.Equal(t1) {
		t.Errorf("lowestPriceDate = %v, want %v", got.LowestPriceDate, t1)
	}
}

func TestCarryOverPriceHistory_freeSnap(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	existing := models.Scene{ID: "1"}
	existing.AddPrice(snap(t0, 9.99))

	fresh := models.Scene{ID: "1"}
	fresh.AddPrice(freeSnap(t1))

	got := carryOverPriceHistory(fresh, existing)
	if got.LowestPrice != 0 {
		t.Errorf("lowestPrice = %.2f, want 0 (free)", got.LowestPrice)
	}
}

// TestCarryOverPriceHistory_multipleFreshSnapshots locks in the contract
// that EVERY snapshot a scraper emits on a fresh scene survives the
// carry-over — not just the last one. A previous implementation only
// kept the trailing snapshot, which silently dropped data on any
// scraper that ever emitted >1 snapshot per scrape (backfill, multi-
// tier pricing, batched updates).
func TestCarryOverPriceHistory_multipleFreshSnapshots(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	existing := models.Scene{ID: "1"}
	existing.AddPrice(snap(t0, 19.99))

	fresh := models.Scene{ID: "1"}
	fresh.AddPrice(snap(t1, 14.99))
	fresh.AddPrice(snap(t2, 9.99))
	fresh.AddPrice(snap(t3, 12.99))

	got := carryOverPriceHistory(fresh, existing)
	if len(got.PriceHistory) != 4 {
		t.Fatalf("got %d snapshots, want 4 (1 existing + 3 fresh)", len(got.PriceHistory))
	}
	// Order must be existing-first, fresh-in-emit-order.
	wantRegular := []float64{19.99, 14.99, 9.99, 12.99}
	for i, w := range wantRegular {
		if got.PriceHistory[i].Regular != w {
			t.Errorf("snapshot[%d].Regular = %.2f, want %.2f", i, got.PriceHistory[i].Regular, w)
		}
	}
	if got.LowestPrice != 9.99 {
		t.Errorf("LowestPrice = %.2f, want 9.99 (must consider every fresh snapshot)", got.LowestPrice)
	}
	if got.LowestPriceDate == nil || !got.LowestPriceDate.Equal(t2) {
		t.Errorf("LowestPriceDate = %v, want %v", got.LowestPriceDate, t2)
	}
}

// fakeScraper streams a fixed slice of scenes on each ListScenes call.
// Each invocation reads from the next entry in batches, mimicking
// successive scrape runs over time.
type fakeScraper struct {
	id      string
	batches [][]models.Scene
	call    int
}

func (f *fakeScraper) ID() string             { return f.id }
func (f *fakeScraper) Patterns() []string     { return nil }
func (f *fakeScraper) MatchesURL(string) bool { return true }

func (f *fakeScraper) ListScenes(_ context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	ch := make(chan scraper.SceneResult, len(f.batches[f.call])+1)
	for _, s := range f.batches[f.call] {
		ch <- scraper.Scene(s)
	}
	close(ch)
	f.call++
	return ch, nil
}

// TestScrapeAll_preservesPriceHistory pins finding 1.1 from AUDIT.md: --full
// must carry forward existing price history across re-scrapes for both stores.
// Before the fix, scrapeAll discarded all prior snapshots — Flat by overwrite,
// SQLite by diff-based syncPriceHistory.
func TestScrapeAll_preservesPriceHistory(t *testing.T) {
	const studioURL = "https://example.com/studio/test"
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	build := func(date time.Time, price float64) models.Scene {
		s := models.Scene{
			ID:        "scene-1",
			SiteID:    "fakesite",
			StudioURL: studioURL,
			Title:     "Test Scene",
			URL:       "https://example.com/scene/1",
			ScrapedAt: date,
		}
		s.AddPrice(snap(date, price))
		return s
	}

	stores := map[string]func(t *testing.T) store.Store{
		"flat": func(t *testing.T) store.Store {
			return store.NewFlat(t.TempDir(), []string{"json"})
		},
		"sqlite": func(t *testing.T) store.Store {
			path := filepath.Join(t.TempDir(), "test.db")
			db, err := store.NewSQLite(path)
			if err != nil {
				t.Fatalf("NewSQLite: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		},
	}

	for name, factory := range stores {
		t.Run(name, func(t *testing.T) {
			st := factory(t)

			// Initial scrape: store one snapshot at $9.99.
			if err := st.Save(studioURL, []models.Scene{build(t0, 9.99)}); err != nil {
				t.Fatalf("seed save: %v", err)
			}

			// --full re-scrape with a different price.
			sc := &fakeScraper{
				id:      "fakesite",
				batches: [][]models.Scene{{build(t1, 4.99)}},
			}
			scenes, _, err := scrapeAll(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
			if err != nil {
				t.Fatalf("scrapeAll: %v", err)
			}
			if err := st.Save(studioURL, scenes); err != nil {
				t.Fatalf("save: %v", err)
			}

			got, err := st.Load(studioURL)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d scenes, want 1", len(got))
			}
			ph := got[0].PriceHistory
			if len(ph) != 2 {
				t.Fatalf("price history has %d snapshots, want 2: %+v", len(ph), ph)
			}
			if ph[0].Regular != 9.99 {
				t.Errorf("snapshot[0] = %.2f, want 9.99 (original)", ph[0].Regular)
			}
			if ph[1].Regular != 4.99 {
				t.Errorf("snapshot[1] = %.2f, want 4.99 (fresh)", ph[1].Regular)
			}
			if got[0].LowestPrice != 4.99 {
				t.Errorf("lowestPrice = %.2f, want 4.99", got[0].LowestPrice)
			}
		})
	}
}

// TestScrapeAll_dropsMissingScenes verifies the documented difference between
// --full and --refresh: scenes that no longer appear on the site are dropped,
// not soft-deleted.
func TestScrapeAll_dropsMissingScenes(t *testing.T) {
	const studioURL = "https://example.com/studio/test"
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	build := func(id string) models.Scene {
		return models.Scene{
			ID:        id,
			SiteID:    "fakesite",
			StudioURL: studioURL,
			Title:     "Scene " + id,
			URL:       "https://example.com/scene/" + id,
			ScrapedAt: t0,
		}
	}

	st := store.NewFlat(t.TempDir(), []string{"json"})
	if err := st.Save(studioURL, []models.Scene{build("1"), build("2")}); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	// --full sees only scene 2; scene 1 should be dropped (no soft-delete).
	sc := &fakeScraper{
		id:      "fakesite",
		batches: [][]models.Scene{{build("2")}},
	}
	scenes, _, err := scrapeAll(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
	if err != nil {
		t.Fatalf("scrapeAll: %v", err)
	}
	if len(scenes) != 1 || scenes[0].ID != "2" {
		t.Fatalf("got %d scenes (ids=%v), want [2]", len(scenes), sceneIDs(scenes))
	}
	if scenes[0].DeletedAt != nil {
		t.Error("scene 2 should not be soft-deleted")
	}
}

func sceneIDs(s []models.Scene) []string {
	ids := make([]string, len(s))
	for i, sc := range s {
		ids[i] = sc.ID
	}
	return ids
}

// collectScenes partial-error tests

type mixedScraper struct {
	id     string
	scenes []models.Scene
	errors int
}

func (f *mixedScraper) ID() string             { return f.id }
func (f *mixedScraper) Patterns() []string     { return nil }
func (f *mixedScraper) MatchesURL(string) bool { return true }

func (f *mixedScraper) ListScenes(_ context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	ch := make(chan scraper.SceneResult, len(f.scenes)+f.errors)
	for i := range f.errors {
		ch <- scraper.Error(fmt.Errorf("error %d", i+1))
	}
	for _, s := range f.scenes {
		ch <- scraper.Scene(s)
	}
	close(ch)
	return ch, nil
}

// kindedScraper emits pre-built errors verbatim, so a test can control how each
// failure classifies rather than relying on plain fmt.Errorf values.
type kindedScraper struct {
	id     string
	scenes []models.Scene
	errs   []error
}

func (f *kindedScraper) ID() string             { return f.id }
func (f *kindedScraper) Patterns() []string     { return nil }
func (f *kindedScraper) MatchesURL(string) bool { return true }

func (f *kindedScraper) ListScenes(_ context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	ch := make(chan scraper.SceneResult, len(f.scenes)+len(f.errs))
	for _, e := range f.errs {
		ch <- scraper.Error(e)
	}
	for _, s := range f.scenes {
		ch <- scraper.Scene(s)
	}
	close(ch)
	return ch, nil
}

// An optional sub-listing a site does not have costs no scenes, so a run whose
// only failures were absences saw everything there was to see. Counting those as
// tr.incomplete demoted the run to non-destructive for no reason — the bug this
// classification exists to fix.
func TestCollectScenes_absentErrorsKeepTraversalComplete(t *testing.T) {
	sc := &kindedScraper{
		id:     "kinded",
		scenes: []models.Scene{{ID: "1", SiteID: "kinded", Title: "Scene 1"}},
		errs: []error{
			scraper.AbsentError("https://example.com/tags", errors.New("HTTP 404")),
			scraper.AbsentError("https://example.com/dvds", errors.New("HTTP 410")),
		},
	}
	scenes, tr, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, sceneOverrides{})
	if err != nil {
		t.Fatalf("collectScenes: %v", err)
	}
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
	if tr.incomplete {
		t.Error("incomplete=true, want false — absent resources cost no scenes")
	}
}

// The other half of the same decision: a page that arrived but could not be read
// did cost us scenes, whatever the status code said.
func TestCollectScenes_lossyErrorsMakeTraversalIncomplete(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"parse", scraper.ParseError("https://example.com/p/2", errors.New("no video block"))},
		{"transport", scraper.TransportError("https://example.com/p/2", errors.New("connection reset"))},
		{"unclassified", errors.New("boom")},
	} {
		t.Run(c.name, func(t *testing.T) {
			sc := &kindedScraper{
				id:     "kinded",
				scenes: []models.Scene{{ID: "1", SiteID: "kinded", Title: "Scene 1"}},
				errs:   []error{c.err},
			}
			_, tr, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, sceneOverrides{})
			if err != nil {
				t.Fatalf("collectScenes: %v", err)
			}
			if !tr.incomplete {
				t.Errorf("incomplete=false for a %s failure, want true", c.name)
			}
		})
	}
}

// A mix must not let the absences mask the losses.
func TestCollectScenes_mixedKindsCountLossesOnly(t *testing.T) {
	sc := &kindedScraper{
		id:     "kinded",
		scenes: []models.Scene{{ID: "1", SiteID: "kinded", Title: "Scene 1"}},
		errs: []error{
			scraper.AbsentError("https://example.com/tags", errors.New("HTTP 404")),
			scraper.ParseError("https://example.com/p/2", errors.New("no video block")),
		},
	}
	_, tr, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, sceneOverrides{})
	if err != nil {
		t.Fatalf("collectScenes: %v", err)
	}
	if !tr.incomplete {
		t.Error("incomplete=false, want true — one parse failure lost scenes")
	}
}

func TestSummarizeFailures(t *testing.T) {
	got := summarizeFailures(map[scraper.FailureKind]int{
		scraper.FailureAbsent:    1,
		scraper.FailureTransport: 2,
		scraper.FailureParse:     3,
	})
	// Fixed order regardless of map iteration, most-actionable first.
	if want := "2 transport, 3 parse, 1 absent"; got != want {
		t.Errorf("summarizeFailures() = %q, want %q", got, want)
	}
	if got := summarizeFailures(map[scraper.FailureKind]int{}); got != "" {
		t.Errorf("summarizeFailures(empty) = %q, want empty", got)
	}
}

func TestCollectScenes_errorsWithSomeScenes(t *testing.T) {
	sc := &mixedScraper{
		id: "mix",
		scenes: []models.Scene{
			{ID: "1", SiteID: "mix", Title: "Scene 1"},
		},
		errors: 2,
	}
	scenes, tr, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, sceneOverrides{})
	if err != nil {
		t.Fatalf("expected no error when some scenes succeed, got: %v", err)
	}
	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
	if !tr.incomplete {
		t.Error("expected incomplete=true when fetch errors occurred")
	}
}

func TestCollectScenes_allErrorsNoScenes(t *testing.T) {
	sc := &mixedScraper{
		id:     "mix",
		errors: 3,
	}
	_, _, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, sceneOverrides{})
	if err == nil {
		t.Fatal("expected error when all attempts fail with 0 scenes")
	}
	if !strings.Contains(err.Error(), "0 scenes") {
		t.Errorf("error should mention 0 scenes, got: %v", err)
	}
}

func TestCollectScenes_noErrorsNoScenes(t *testing.T) {
	sc := &mixedScraper{id: "mix"}
	scenes, tr, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, sceneOverrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scenes) != 0 {
		t.Errorf("got %d scenes, want 0", len(scenes))
	}
	if tr.incomplete {
		t.Error("expected incomplete=false for a clean empty traversal")
	}
}

// scrapeRefresh soft-delete state machine tests

func TestScrapeRefresh_softDeleteAndRevive(t *testing.T) {
	const studioURL = "https://example.com/studio/test"
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	build := func(id string) models.Scene {
		return models.Scene{
			ID: id, SiteID: "fakesite", StudioURL: studioURL,
			Title: "Scene " + id, URL: "https://example.com/" + id,
			ScrapedAt: t0,
		}
	}

	st := store.NewFlat(t.TempDir(), []string{"json"})
	// Seed: scenes 1, 2, 3 all present.
	if err := st.Save(studioURL, []models.Scene{build("1"), build("2"), build("3")}); err != nil {
		t.Fatal(err)
	}

	// Phase 1: scene 2 disappears → should be soft-deleted.
	sc := &fakeScraper{
		id:      "fakesite",
		batches: [][]models.Scene{{build("1"), build("3")}},
	}
	result, _, err := scrapeRefresh(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(studioURL, result); err != nil {
		t.Fatal(err)
	}

	loaded, _ := st.Load(studioURL)
	byID := map[string]models.Scene{}
	for _, s := range loaded {
		byID[s.ID] = s
	}
	if byID["2"].DeletedAt == nil {
		t.Fatal("scene 2 should be soft-deleted after disappearing")
	}
	if byID["1"].DeletedAt != nil {
		t.Error("scene 1 should not be soft-deleted")
	}

	// Phase 2: scene 2 comes back → should be revived (DeletedAt == nil).
	sc2 := &fakeScraper{
		id:      "fakesite",
		batches: [][]models.Scene{{build("1"), build("2"), build("3")}},
	}
	result2, _, err := scrapeRefresh(context.Background(), sc2, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(studioURL, result2); err != nil {
		t.Fatal(err)
	}

	loaded2, _ := st.Load(studioURL)
	for _, s := range loaded2 {
		if s.ID == "2" && s.DeletedAt != nil {
			t.Error("scene 2 should be revived after reappearing")
		}
	}
}

func TestParseFormats_json(t *testing.T) {
	got, err := parseFormats("json")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"json"}) {
		t.Errorf("got %v, want [json]", got)
	}
}

func TestParseFormats_csv(t *testing.T) {
	got, err := parseFormats("csv")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"csv"}) {
		t.Errorf("got %v, want [csv]", got)
	}
}

func TestParseFormats_both(t *testing.T) {
	got, err := parseFormats("json,csv")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"json", "csv"}) {
		t.Errorf("got %v, want [json csv]", got)
	}
}

func TestParseFormats_dedup(t *testing.T) {
	got, err := parseFormats("json,json")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"json"}) {
		t.Errorf("got %v, want [json]", got)
	}
}

func TestParseFormats_unknown(t *testing.T) {
	_, err := parseFormats("xml")
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "xml") {
		t.Errorf("error should mention the unknown format, got: %v", err)
	}
}

func TestParseFormats_empty(t *testing.T) {
	got, err := parseFormats("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestParseFormats_whitespace(t *testing.T) {
	got, err := parseFormats(" json , csv ")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"json", "csv"}) {
		t.Errorf("got %v, want [json csv]", got)
	}
}

// runScrape fills in a missing scheme before ForURL sees the URL. urlStampScraper
// matches by exact string, so it only runs if the normalisation happened.
func TestRunScrape_normalizesSchemelessURL(t *testing.T) {
	const canonical = "https://example.com/normscrape"
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FSS_NO_NOTICES", "1")

	scraper.Register(&urlStampScraper{
		id: "normscrape", matchURL: canonical,
		stampURL: canonical, sceneID: "1", siteID: "normsite",
	})

	outDir := t.TempDir()
	t.Cleanup(func() {
		_ = scrapeCmd.Flags().Set("out-dir", "")
		_ = scrapeCmd.Flags().Set("delay", "0")
	})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"scrape", "example.com/normscrape", "--out-dir", outDir, "--delay", "0"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("scrape: %v", err)
	}

	st := store.NewFlat(outDir, []string{"json"})
	got, err := st.Load(canonical)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scenes under %s, want 1", len(got), canonical)
	}
}

// urlStampScraper emits one scene that stamps a StudioURL different from the
// requested one, and matches only its configured URL.
type urlStampScraper struct {
	id       string
	matchURL string
	stampURL string
	sceneID  string
	siteID   string
}

func (u *urlStampScraper) ID() string               { return u.id }
func (u *urlStampScraper) Patterns() []string       { return nil }
func (u *urlStampScraper) MatchesURL(s string) bool { return s == u.matchURL }

func (u *urlStampScraper) ListScenes(_ context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	ch := make(chan scraper.SceneResult, 2)
	ch <- scraper.Scene(models.Scene{
		ID: u.sceneID, SiteID: u.siteID, StudioURL: u.stampURL,
		Title: "stamped", URL: "https://example.com/v/1", ScrapedAt: time.Now().UTC(),
	})
	close(ch)
	return ch, nil
}

// TestScrapeOne_normalizesStudioURL is the A3 regression: scrapeOne must store
// scenes under the requested studioURL even when the scraper stamps a different
// (canonical/derived) URL on each scene, so the (id, site_id, studio_url) rows
// stay reachable by Load.
func TestScrapeOne_normalizesStudioURL(t *testing.T) {
	const requested = "https://example.com/a3-requested"
	scraper.Register(&urlStampScraper{
		id: "a3stamp", matchURL: requested,
		stampURL: "https://cdn.example.com/canonical-different",
		sceneID:  "1", siteID: "a3site",
	})

	st := store.NewFlat(t.TempDir(), []string{"json"})
	if err := scrapeOne(context.Background(), st, scrapeTarget{url: requested}, "", "", "", []string{"json"},
		false, false, false, true, 1, 0, nil, nil, sceneOverrides{}); err != nil {
		t.Fatalf("scrapeOne: %v", err)
	}

	got, err := st.Load(requested)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scenes, want 1", len(got))
	}
	if got[0].StudioURL != requested {
		t.Errorf("StudioURL = %q, want %q (not the scraper's stamped URL)", got[0].StudioURL, requested)
	}
}

// TestScrapeAll_multiSiteKeyAndDedup is the A6 regression: scenes that share an
// id but differ in site_id must NOT collide in the merge maps, and duplicate
// (id, site_id) entries from pagination overlap must be deduped.
func TestScrapeAll_multiSiteKeyAndDedup(t *testing.T) {
	const studioURL = "https://example.com/a6"
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id, site, title string) models.Scene {
		return models.Scene{ID: id, SiteID: site, StudioURL: studioURL,
			Title: title, URL: "https://example.com/v/" + id + "-" + site, ScrapedAt: now}
	}

	sc := &fakeScraper{
		id: "a6",
		batches: [][]models.Scene{{
			mk("1", "siteA", "A-one"),
			mk("1", "siteB", "B-one"),     // same id, different site → must be kept
			mk("1", "siteA", "A-one-dup"), // duplicate (id, site) → must be dropped
		}},
	}

	st := store.NewFlat(t.TempDir(), []string{"json"})
	scenes, _, err := scrapeAll(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
	if err != nil {
		t.Fatalf("scrapeAll: %v", err)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2 (one per site_id, dup dropped): %+v", len(scenes), scenes)
	}
	got := map[sceneKey]string{}
	for _, s := range scenes {
		got[keyOf(s)] = s.Title
	}
	if got[sceneKey{"1", "siteA"}] != "A-one" {
		t.Errorf("siteA title = %q, want first occurrence A-one", got[sceneKey{"1", "siteA"}])
	}
	if got[sceneKey{"1", "siteB"}] != "B-one" {
		t.Errorf("siteB scene missing/wrong: %q", got[sceneKey{"1", "siteB"}])
	}
}

// TestScrapeAll_incompletePreservesExisting is the A1 regression: when --full's
// traversal is tr.incomplete (a fetch error), scenes not re-collected must be
// merged forward rather than hard-deleted by the authoritative Save.
func TestScrapeAll_incompletePreservesExisting(t *testing.T) {
	const studioURL = "https://example.com/a1"
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id string) models.Scene {
		return models.Scene{ID: id, SiteID: "a1", StudioURL: studioURL,
			Title: "Scene " + id, URL: "https://example.com/v/" + id, ScrapedAt: now}
	}

	st := store.NewFlat(t.TempDir(), []string{"json"})
	// Seed three existing scenes.
	if err := st.Save(studioURL, []models.Scene{mk("1"), mk("2"), mk("3")}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A run that re-collects only scene 1 but reports a fetch error (tr.incomplete).
	sc := &mixedScraper{id: "a1", scenes: []models.Scene{mk("1")}, errors: 1}
	scenes, _, err := scrapeAll(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
	if err != nil {
		t.Fatalf("scrapeAll: %v", err)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3 (existing preserved on tr.incomplete run)", len(scenes))
	}
	if err := st.Save(studioURL, scenes); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.Load(studioURL)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("after tr.incomplete --full, %d scenes survive, want 3", len(got))
	}
}

// TestScrapeAll_completeDropsMissing confirms a *complete* --full still hard-
// deletes scenes that genuinely disappeared (no merge fallback).
func TestScrapeAll_completeDropsMissing(t *testing.T) {
	const studioURL = "https://example.com/a1-complete"
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id string) models.Scene {
		return models.Scene{ID: id, SiteID: "a1c", StudioURL: studioURL,
			Title: "Scene " + id, URL: "https://example.com/v/" + id, ScrapedAt: now}
	}
	st := store.NewFlat(t.TempDir(), []string{"json"})
	if err := st.Save(studioURL, []models.Scene{mk("1"), mk("2"), mk("3")}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sc := &fakeScraper{id: "a1c", batches: [][]models.Scene{{mk("1")}}} // clean, only scene 1
	scenes, _, err := scrapeAll(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
	if err != nil {
		t.Fatalf("scrapeAll: %v", err)
	}
	if len(scenes) != 1 {
		t.Errorf("complete --full should keep only the 1 re-collected scene, got %d", len(scenes))
	}
}

// TestScrapeRefresh_incompleteSkipsSoftDelete is the A1 regression for
// --refresh: an tr.incomplete traversal must NOT soft-delete scenes that simply
// weren't reached this run.
func TestScrapeRefresh_incompleteSkipsSoftDelete(t *testing.T) {
	const studioURL = "https://example.com/a1-refresh"
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id string) models.Scene {
		return models.Scene{ID: id, SiteID: "a1r", StudioURL: studioURL,
			Title: "Scene " + id, URL: "https://example.com/v/" + id, ScrapedAt: now}
	}
	st := store.NewFlat(t.TempDir(), []string{"json"})
	if err := st.Save(studioURL, []models.Scene{mk("1"), mk("2")}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Re-collect only scene 1, with an error (tr.incomplete).
	sc := &mixedScraper{id: "a1r", scenes: []models.Scene{mk("1")}, errors: 1}
	scenes, _, err := scrapeRefresh(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, true, sceneOverrides{})
	if err != nil {
		t.Fatalf("scrapeRefresh: %v", err)
	}
	for _, s := range scenes {
		if s.DeletedAt != nil {
			t.Errorf("scene %s soft-deleted on tr.incomplete refresh", s.ID)
		}
	}
}

// TestScrapeOne_emptyWipeGuard is the A2 regression: a clean 0-scene --full for
// a populated studio must refuse the destructive save; --force overrides it.
func TestScrapeOne_emptyWipeGuard(t *testing.T) {
	const url = "https://example.com/a2-guard"
	now := time.Now().UTC().Truncate(time.Second)
	seed := models.Scene{ID: "1", SiteID: "a2", StudioURL: url,
		Title: "Seed", URL: "https://example.com/v/1", ScrapedAt: now}

	scraper.Register(&fakeRegistered{id: "a2empty", url: url}) // emits 0 scenes

	st := store.NewFlat(t.TempDir(), []string{"json"})
	if err := st.Save(url, []models.Scene{seed}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// --full, no --force: must refuse and keep the existing scene.
	if err := scrapeOne(context.Background(), st, scrapeTarget{url: url}, "", "", "", []string{"json"},
		true, false, false, true, 1, 0, nil, nil, sceneOverrides{}); err != nil {
		t.Fatalf("scrapeOne (guard): %v", err)
	}
	got, _ := st.Load(url)
	if len(got) != 1 {
		t.Fatalf("guard failed: %d scenes after 0-scene --full, want 1 preserved", len(got))
	}

	// --force: the wipe is allowed.
	if err := scrapeOne(context.Background(), st, scrapeTarget{url: url}, "", "", "", []string{"json"},
		true, false, true, true, 1, 0, nil, nil, sceneOverrides{}); err != nil {
		t.Fatalf("scrapeOne (force): %v", err)
	}
	got, _ = st.Load(url)
	if len(got) != 0 {
		t.Errorf("--force should wipe, got %d scenes", len(got))
	}
}

// fakeRegistered emits zero scenes and matches a single URL.
type fakeRegistered struct {
	id  string
	url string
}

func (f *fakeRegistered) ID() string               { return f.id }
func (f *fakeRegistered) Patterns() []string       { return nil }
func (f *fakeRegistered) MatchesURL(s string) bool { return s == f.url }
func (f *fakeRegistered) ListScenes(_ context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	ch := make(chan scraper.SceneResult)
	close(ch)
	return ch, nil
}

// TestScrapeRefresh_preservesEnrichment covers the parser-regression case: a
// site redesign makes the scraper return scenes with the right IDs but no tags,
// performers or description. Save is authoritative, so without preservation the
// refresh would replace good stored metadata with nothing.
func TestScrapeRefresh_preservesEnrichment(t *testing.T) {
	const studioURL = "https://example.com/studio/preserve"
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rich := models.Scene{
		ID: "1", SiteID: "fakesite", StudioURL: studioURL,
		Title: "Scene 1", URL: "https://example.com/1",
		Description: "a long description",
		Performers:  []string{"Alice", "Bob"},
		Tags:        []string{"tag-a"},
		Categories:  []string{"cat-a"},
		Thumbnail:   "https://example.com/1.jpg",
		Duration:    600,
		Date:        t0.Add(-24 * time.Hour),
		ExternalIDs: map[string]string{models.ExternalStashDB: "uuid-1"},
		ScrapedAt:   t0,
	}
	// What a broken parser emits: identity intact, everything else blank.
	stripped := models.Scene{
		ID: "1", SiteID: "fakesite", StudioURL: studioURL,
		Title: "Scene 1", URL: "https://example.com/1",
		ScrapedAt: t0.Add(24 * time.Hour),
	}

	run := func(t *testing.T, preserve bool) models.Scene {
		t.Helper()
		st := store.NewFlat(t.TempDir(), []string{"json"})
		if err := st.Save(studioURL, []models.Scene{rich}); err != nil {
			t.Fatal(err)
		}
		sc := &fakeScraper{id: "fakesite", batches: [][]models.Scene{{stripped}}}
		result, _, err := scrapeRefresh(context.Background(), sc, st, studioURL, scraper.ListOpts{Workers: 1}, preserve, sceneOverrides{})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Save(studioURL, result); err != nil {
			t.Fatal(err)
		}
		loaded, _ := st.Load(studioURL)
		if len(loaded) != 1 {
			t.Fatalf("got %d scenes, want 1", len(loaded))
		}
		return loaded[0]
	}

	t.Run("preserve", func(t *testing.T) {
		got := run(t, true)
		if got.Description != rich.Description {
			t.Errorf("description = %q, want preserved", got.Description)
		}
		if len(got.Performers) != 2 || len(got.Tags) != 1 || len(got.Categories) != 1 {
			t.Errorf("relations lost: performers=%v tags=%v categories=%v", got.Performers, got.Tags, got.Categories)
		}
		if got.Thumbnail == "" || got.Duration != 600 || got.Date.IsZero() {
			t.Errorf("scalars lost: thumb=%q duration=%d date=%v", got.Thumbnail, got.Duration, got.Date)
		}
		if got.ExternalIDs[models.ExternalStashDB] != "uuid-1" {
			t.Errorf("externalIDs lost: %v", got.ExternalIDs)
		}
		// The fresh scrape still wins where it has data.
		if !got.ScrapedAt.Equal(stripped.ScrapedAt) {
			t.Errorf("scrapedAt = %v, want the fresh value", got.ScrapedAt)
		}
	})

	t.Run("no-preserve", func(t *testing.T) {
		got := run(t, false)
		if got.Description != "" || len(got.Performers) != 0 || len(got.Tags) != 0 {
			t.Errorf("--no-preserve should let the blank scrape win, got %+v", got)
		}
	})
}

// ---- coverage-collapse guard ----

func TestCoverageCollapsed(t *testing.T) {
	cases := []struct {
		name string
		cov  coverage
		want bool
	}{
		{"aunt judy's: 9 of 412", coverage{stored: 412, seen: 9}, true},
		{"exactly at the threshold is fine", coverage{stored: 100, seen: 50}, false},
		{"one below the threshold trips", coverage{stored: 100, seen: 49}, true},
		{"full coverage", coverage{stored: 100, seen: 100}, false},
		{"small studio is exempt", coverage{stored: 9, seen: 0}, false},
		{"empty store", coverage{stored: 0, seen: 0}, false},
		{
			"incomplete traversal has its own handling",
			coverage{stored: 400, seen: 4, traversal: traversal{incomplete: true}},
			false,
		},
		{
			"early stop explains a small fresh set",
			coverage{stored: 400, seen: 4, traversal: traversal{stoppedEarly: true}},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cov.collapsed(); got != c.want {
				t.Errorf("collapsed() = %v, want %v (%d/%d)", got, c.want, c.cov.seen, c.cov.stored)
			}
		})
	}
}

func withPrompt(t *testing.T, terminal bool, answer string) {
	t.Helper()
	origTerm, origIn := stdinIsTerminal, promptIn
	stdinIsTerminal = func() bool { return terminal }
	promptIn = strings.NewReader(answer)
	t.Cleanup(func() { stdinIsTerminal, promptIn = origTerm, origIn })
}

func TestProceedAfterCollapse(t *testing.T) {
	collapsed := coverage{stored: 400, seen: 9}

	cases := []struct {
		name        string
		destructive bool
		force       bool
		terminal    bool
		answer      string
		want        bool
	}{
		{"incremental only reports", false, false, false, "", true},
		{"--force proceeds unattended", true, true, false, "", true},
		{"non-interactive refuses", true, false, false, "", false},
		{"declined at the prompt", true, false, true, "n\n", false},
		{"empty answer defaults to no", true, false, true, "\n", false},
		{"eof defaults to no", true, false, true, "", false},
		{"accepted at the prompt", true, false, true, "y\n", true},
		{"accepted, spelled out", true, false, true, "YES\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withPrompt(t, c.terminal, c.answer)
			got := proceedAfterCollapse(collapsed, "https://example.com", "example", c.destructive, c.force)
			if got != c.want {
				t.Errorf("proceedAfterCollapse = %v, want %v", got, c.want)
			}
		})
	}
}

// fakePartial emits a fixed slice — a scraper that still works but now reaches
// only part of the catalogue.
type fakePartial struct {
	id     string
	url    string
	scenes []models.Scene
}

func (f *fakePartial) ID() string               { return f.id }
func (f *fakePartial) Patterns() []string       { return nil }
func (f *fakePartial) MatchesURL(s string) bool { return s == f.url }
func (f *fakePartial) ListScenes(_ context.Context, _ string, _ scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	ch := make(chan scraper.SceneResult, len(f.scenes))
	for _, sc := range f.scenes {
		ch <- scraper.Scene(sc)
	}
	close(ch)
	return ch, nil
}

// TestScrapeOneCollapseSkipsDestructiveSave is the end-to-end case: a studio
// with a populated store, re-scraped by a scraper that now returns a fraction
// of it, must not have the remainder deleted without an answer.
func TestScrapeOneCollapseSkipsDestructiveSave(t *testing.T) {
	const studioURL = "https://example.com/collapse-guard"
	dir := t.TempDir()
	st := store.NewFlat(dir, []string{"json"})

	stored := make([]models.Scene, 0, 40)
	for i := 0; i < 40; i++ {
		stored = append(stored, models.Scene{
			ID: fmt.Sprintf("%d", i), SiteID: "collapse", StudioURL: studioURL,
			Title: fmt.Sprintf("Scene %d", i), ScrapedAt: time.Now().UTC().Truncate(time.Second),
		})
	}
	if err := st.Save(studioURL, stored); err != nil {
		t.Fatal(err)
	}

	scraper.Register(&fakePartial{id: "collapseguard", url: studioURL, scenes: stored[:2]})

	// Declined at the prompt: the store must be untouched.
	withPrompt(t, true, "n\n")
	if err := scrapeOne(context.Background(), st, scrapeTarget{url: studioURL}, "", "", dir, []string{"json"},
		true, false, false, true, 1, 0, nil, nil, sceneOverrides{}); err != nil {
		t.Fatalf("scrapeOne: %v", err)
	}
	after, err := st.Load(studioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 40 {
		t.Errorf("store holds %d scenes, want all 40 — a declined save must not delete", len(after))
	}

	// Confirmed: the authoritative save goes through as it always did.
	withPrompt(t, true, "y\n")
	if err := scrapeOne(context.Background(), st, scrapeTarget{url: studioURL}, "", "", dir, []string{"json"},
		true, false, false, true, 1, 0, nil, nil, sceneOverrides{}); err != nil {
		t.Fatalf("scrapeOne (confirmed): %v", err)
	}
	after, err = st.Load(studioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Errorf("store holds %d scenes, want 2 after a confirmed authoritative save", len(after))
	}
}

func TestMergeSiteCookies_cliOverlaysConfig(t *testing.T) {
	got, err := mergeSiteCookies(
		map[string]string{"mydirtyhobby": "KEY=old", "visitx": "sid=1"},
		[]string{"mydirtyhobby=KEY=new"})
	if err != nil {
		t.Fatalf("mergeSiteCookies: %v", err)
	}
	// Only the first `=` separates the name: a cookie value contains one of
	// its own, and splitting on every `=` would truncate it to "KEY".
	if got["mydirtyhobby"] != "KEY=new" {
		t.Errorf("mydirtyhobby = %q, want %q", got["mydirtyhobby"], "KEY=new")
	}
	if got["visitx"] != "sid=1" {
		t.Errorf("config entry not carried through: %q", got["visitx"])
	}
}

func TestMergeSiteCookies_keepsMultipleCookies(t *testing.T) {
	got, err := mergeSiteCookies(nil, []string{"mydirtyhobby=KEY=12*34:56:78:1; other=2"})
	if err != nil {
		t.Fatalf("mergeSiteCookies: %v", err)
	}
	if got["mydirtyhobby"] != "KEY=12*34:56:78:1; other=2" {
		t.Errorf("value = %q", got["mydirtyhobby"])
	}
}

func TestMergeSiteCookies_rejectsMalformed(t *testing.T) {
	for _, bad := range []string{"mydirtyhobby", "=KEY=1", "mydirtyhobby="} {
		if _, err := mergeSiteCookies(nil, []string{bad}); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	// A newline would let a pasted value inject a second header.
	if _, err := mergeSiteCookies(nil, []string{"mydirtyhobby=KEY=1\r\nX-Evil: 1"}); err == nil {
		t.Error("expected an error for a value containing a newline")
	}
}

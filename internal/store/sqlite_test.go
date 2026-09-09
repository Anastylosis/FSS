package store

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
	_ "modernc.org/sqlite"
)

// ---- studios ----

func TestSQLiteUpsertListStudios(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	last := now.Add(time.Hour)

	st := models.Studio{
		URL:           testStudioURL,
		SiteID:        "manyvids",
		Name:          "Bettie Bondage",
		AddedAt:       now,
		LastScrapedAt: &last,
	}
	if err := s.UpsertStudio(st); err != nil {
		t.Fatalf("UpsertStudio: %v", err)
	}

	studios, err := s.ListStudios()
	if err != nil {
		t.Fatalf("ListStudios: %v", err)
	}
	if len(studios) != 1 {
		t.Fatalf("got %d studios, want 1", len(studios))
	}
	got := studios[0]
	if got.URL != testStudioURL {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Name != "Bettie Bondage" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.SiteID != "manyvids" {
		t.Errorf("SiteID = %q", got.SiteID)
	}
	if got.LastScrapedAt == nil || !got.LastScrapedAt.Equal(last) {
		t.Errorf("LastScrapedAt = %v, want %v", got.LastScrapedAt, last)
	}
}

func TestSQLiteUpsertStudioPreservesName(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	// First insert: with a name.
	if err := s.UpsertStudio(models.Studio{
		URL: testStudioURL, SiteID: "manyvids", Name: "Bettie Bondage", AddedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// Second upsert: no name (e.g. scrape without --name flag) — should not clear the existing name.
	later := now.Add(time.Hour)
	if err := s.UpsertStudio(models.Studio{
		URL: testStudioURL, SiteID: "manyvids", Name: "", AddedAt: now, LastScrapedAt: &later,
	}); err != nil {
		t.Fatal(err)
	}

	studios, _ := s.ListStudios()
	if studios[0].Name != "Bettie Bondage" {
		t.Errorf("Name cleared by upsert without name, got %q", studios[0].Name)
	}
	if studios[0].LastScrapedAt == nil || !studios[0].LastScrapedAt.Equal(later) {
		t.Error("LastScrapedAt not updated")
	}
}

func TestSQLiteUpsertStudioUpdatesName(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := s.UpsertStudio(models.Studio{
		URL: testStudioURL, SiteID: "manyvids", Name: "Old Name", AddedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Passing a new non-empty name should overwrite.
	if err := s.UpsertStudio(models.Studio{
		URL: testStudioURL, SiteID: "manyvids", Name: "New Name", AddedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	studios, _ := s.ListStudios()
	if studios[0].Name != "New Name" {
		t.Errorf("Name not updated, got %q", studios[0].Name)
	}
}

const testStudioURL = "https://www.manyvids.com/Profile/123/test-creator/Store/Videos"

func newTestDB(t *testing.T) *SQLite {
	t.Helper()
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSQLiteLock(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSQLite(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	unlock, err := s.Lock(testStudioURL)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := unlock.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-acquire after release.
	unlock2, err := s.Lock(testStudioURL)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	_ = unlock2.Close()

	// Verify lock file was created in the DB directory.
	slug := Slugify(testStudioURL)
	lockPath := filepath.Join(dir, slug+".lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file not created at %s: %v", lockPath, err)
	}
}

func TestSQLiteSaveLoad(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{
		{
			ID:          "1",
			SiteID:      "manyvids",
			StudioURL:   testStudioURL,
			Title:       "Scene One",
			URL:         "https://www.manyvids.com/Video/1/scene-one",
			Date:        now.Add(-48 * time.Hour),
			Description: "A description",
			Thumbnail:   "https://cdn.example.com/thumb1.jpg",
			Preview:     "https://cdn.example.com/preview1.mp4",
			Performers:  []string{"Alice"},
			Studio:      "Alice Studio",
			Tags:        []string{"Tag1", "Tag2"},
			Categories:  []string{"Cat1"},
			Duration:    1200,
			Resolution:  "4K",
			Width:       3840,
			Height:      2160,
			Format:      "MP4",
			Views:       500,
			Likes:       42,
			Comments:    7,
			ScrapedAt:   now,
		},
		{
			ID:         "2",
			SiteID:     "manyvids",
			StudioURL:  testStudioURL,
			Title:      "Scene Two",
			URL:        "https://www.manyvids.com/Video/2/scene-two",
			Date:       now.Add(-24 * time.Hour),
			Performers: []string{"Alice"},
			ScrapedAt:  now,
		},
	}
	scenes[0].AddPrice(models.PriceSnapshot{
		Date: now, Regular: 29.99, Discounted: 14.99, IsOnSale: true, DiscountPercent: 50,
	})

	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("Load returned %d scenes, want 2", len(loaded))
	}

	byID := map[string]models.Scene{}
	for _, sc := range loaded {
		byID[sc.ID] = sc
	}

	sc1 := byID["1"]
	check := func(field, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
	check("Title", sc1.Title, "Scene One")
	check("Description", sc1.Description, "A description")
	check("Thumbnail", sc1.Thumbnail, "https://cdn.example.com/thumb1.jpg")
	check("Resolution", sc1.Resolution, "4K")
	check("Format", sc1.Format, "MP4")
	if sc1.Duration != 1200 {
		t.Errorf("Duration = %d, want 1200", sc1.Duration)
	}
	if sc1.Width != 3840 || sc1.Height != 2160 {
		t.Errorf("Width/Height = %d/%d", sc1.Width, sc1.Height)
	}
	if len(sc1.Tags) != 2 || sc1.Tags[0] != "Tag1" || sc1.Tags[1] != "Tag2" {
		t.Errorf("Tags = %v", sc1.Tags)
	}
	if len(sc1.Performers) != 1 || sc1.Performers[0] != "Alice" {
		t.Errorf("Performers = %v", sc1.Performers)
	}
	if len(sc1.Categories) != 1 || sc1.Categories[0] != "Cat1" {
		t.Errorf("Categories = %v", sc1.Categories)
	}
	if sc1.DeletedAt != nil {
		t.Error("DeletedAt should be nil")
	}

	// Price history
	if len(sc1.PriceHistory) != 1 {
		t.Fatalf("PriceHistory len = %d, want 1", len(sc1.PriceHistory))
	}
	p := sc1.PriceHistory[0]
	if p.Regular != 29.99 {
		t.Errorf("Regular = %v, want 29.99", p.Regular)
	}
	if p.Discounted != 14.99 {
		t.Errorf("Discounted = %v, want 14.99", p.Discounted)
	}
	if !p.IsOnSale {
		t.Error("IsOnSale should be true")
	}
	if p.DiscountPercent != 50 {
		t.Errorf("DiscountPercent = %d, want 50", p.DiscountPercent)
	}
	if sc1.LowestPrice != 14.99 {
		t.Errorf("LowestPrice = %v, want 14.99", sc1.LowestPrice)
	}

	// Scene 2 should have no price history
	if len(byID["2"].PriceHistory) != 0 {
		t.Errorf("scene 2 PriceHistory should be empty")
	}
}

func TestSQLiteSaveIdempotent(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL,
		Title: "Original", ScrapedAt: now,
	}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	// Save again with updated title — should replace, not duplicate.
	scene.Title = "Updated"
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 scene after idempotent save, got %d", len(loaded))
	}
	if loaded[0].Title != "Updated" {
		t.Errorf("Title = %q, want Updated", loaded[0].Title)
	}
}

// TestSQLiteSaveDropsMissing verifies the new --full contract: a Save
// with a slice that omits a previously-stored scene must hard-delete
// that scene (and its relations + price_history) from the store. This
// is what makes SQLite match Flat's behaviour on `--full` re-scrapes.
func TestSQLiteSaveDropsMissing(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	original := []models.Scene{
		{
			ID: "1", SiteID: "manyvids", StudioURL: testStudioURL,
			Title: "Keep me", ScrapedAt: now,
			Performers: []string{"Alice"},
			Tags:       []string{"solo"},
			Categories: []string{"foo"},
		},
		{
			ID: "2", SiteID: "manyvids", StudioURL: testStudioURL,
			Title: "Drop me", ScrapedAt: now,
			Performers: []string{"Bob"},
			Tags:       []string{"bdsm"},
		},
		{
			ID: "3", SiteID: "manyvids", StudioURL: testStudioURL,
			Title: "Drop me too", ScrapedAt: now,
		},
	}
	original[1].AddPrice(models.PriceSnapshot{Date: now, Regular: 9.99})
	original[2].AddPrice(models.PriceSnapshot{Date: now, Regular: 4.99})

	if err := s.Save(testStudioURL, original); err != nil {
		t.Fatal(err)
	}

	// Second save omits ID 2 and 3. The contract requires both to vanish.
	if err := s.Save(testStudioURL, []models.Scene{original[0]}); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != "1" {
		t.Fatalf("after Save omitting 2&3, got %d scenes: %+v", len(loaded), loaded)
	}

	// price_history rows for dropped scenes must be gone too — no FK
	// CASCADE on that table so the Save must delete them explicitly.
	row := s.db.QueryRow(
		`SELECT COUNT(*) FROM price_history WHERE site_id = 'manyvids' AND scene_id IN ('2','3')`,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("price_history for dropped scenes survived: %d rows remain", n)
	}

	// Scene 1's relations must still be present (Save shouldn't have
	// touched them as collateral).
	loaded1 := loaded[0]
	if len(loaded1.Performers) != 1 || loaded1.Performers[0] != "Alice" {
		t.Errorf("kept scene lost performers: %v", loaded1.Performers)
	}
	if len(loaded1.Tags) != 1 || loaded1.Tags[0] != "solo" {
		t.Errorf("kept scene lost tags: %v", loaded1.Tags)
	}
}

// TestSQLiteSaveOnlyAffectsOwnStudio guards against a delete-missing
// implementation that nukes scenes belonging to a different studio.
func TestSQLiteSaveOnlyAffectsOwnStudio(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	const otherURL = "https://www.manyvids.com/Profile/999/other/Store/Videos"

	// Two unrelated studios each with one scene.
	if err := s.Save(testStudioURL, []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "A", ScrapedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(otherURL, []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: otherURL, Title: "B", ScrapedAt: now},
	}); err != nil {
		t.Fatal(err)
	}

	// Empty save for testStudioURL must NOT touch otherURL's scene even
	// though both share the same (id, site_id).
	if err := s.Save(testStudioURL, nil); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Load(testStudioURL); len(got) != 0 {
		t.Errorf("testStudioURL should be empty after Save(nil), got %d scenes", len(got))
	}
	got, err := s.Load(otherURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "B" {
		t.Errorf("otherURL scene was clobbered: got %d scenes %+v", len(got), got)
	}
}

func TestSQLitePriceHistoryAccumulates(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "X", ScrapedAt: now,
	}
	scene.AddPrice(models.PriceSnapshot{Date: now, Regular: 29.99})

	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	// Second scrape: load, add new price, save.
	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	loaded[0].AddPrice(models.PriceSnapshot{Date: now.Add(24 * time.Hour), Regular: 24.99, IsOnSale: true, Discounted: 24.99})
	if err := s.Save(testStudioURL, loaded); err != nil {
		t.Fatal(err)
	}

	final, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(final[0].PriceHistory) != 2 {
		t.Fatalf("PriceHistory len = %d, want 2", len(final[0].PriceHistory))
	}
	if final[0].LowestPrice != 24.99 {
		t.Errorf("LowestPrice = %v, want 24.99", final[0].LowestPrice)
	}
}

func TestSQLiteMarkDeleted(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "A", ScrapedAt: now},
		{ID: "2", SiteID: "manyvids", StudioURL: testStudioURL, Title: "B", ScrapedAt: now},
	}
	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeleted(testStudioURL, "manyvids", []string{"1"}); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]models.Scene{}
	for _, sc := range loaded {
		byID[sc.ID] = sc
	}
	if byID["1"].DeletedAt == nil {
		t.Error("scene 1 DeletedAt should be set")
	}
	if byID["2"].DeletedAt != nil {
		t.Error("scene 2 DeletedAt should be nil")
	}

	// Mark deleted is idempotent — calling again should not change DeletedAt.
	firstDeletedAt := *byID["1"].DeletedAt
	if err := s.MarkDeleted(testStudioURL, "manyvids", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	loaded2, _ := s.Load(testStudioURL)
	for _, sc := range loaded2 {
		if sc.ID == "1" && !sc.DeletedAt.Equal(firstDeletedAt) {
			t.Error("MarkDeleted should not update DeletedAt if already set")
		}
	}
}

func TestSQLiteMarkDeletedNonexistentID(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "A", ScrapedAt: now},
	}
	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}

	// MarkDeleted with a non-existent ID should not error.
	if err := s.MarkDeleted(testStudioURL, "manyvids", []string{"nonexistent"}); err != nil {
		t.Fatalf("MarkDeleted for non-existent ID should not error: %v", err)
	}

	// Existing scene should be unaffected.
	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].DeletedAt != nil {
		t.Error("existing scene should be unaffected by non-existent MarkDeleted")
	}
}

// TestSQLiteSaveAutoRevives locks in the documented Save contract: a
// re-emitted scene with DeletedAt == nil clears any prior soft-delete.
// This is the "site brought the scene back" path that the cmd layer's
// incremental scrape relies on — when a scraper re-emits an ID after
// a previous MarkDeleted, the store should reflect that the scene is
// alive again.
// TestSQLiteRelationLookupNoColonCollision guards against the previous
// `siteID + ":" + sceneID` string-concat keying in loadRelation /
// loadPriceHistory. Two scenes whose (siteID, ID) pairs produce the
// same flattened string — e.g. ("a", "b:c") and ("a:b", "c") — used to
// land on the same map slot, so the second scene's performers/tags/
// price history would overwrite (or no-op against) the first.
func TestSQLiteRelationLookupNoColonCollision(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{
		{
			ID: "b:c", SiteID: "a", StudioURL: testStudioURL,
			Title: "alpha", ScrapedAt: now,
			Performers: []string{"Alpha"},
			Tags:       []string{"x"},
		},
		{
			ID: "c", SiteID: "a:b", StudioURL: testStudioURL,
			Title: "beta", ScrapedAt: now,
			Performers: []string{"Beta"},
			Tags:       []string{"y"},
		},
	}
	scenes[0].AddPrice(models.PriceSnapshot{Date: now, Regular: 1.00})
	scenes[1].AddPrice(models.PriceSnapshot{Date: now, Regular: 2.00})

	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("got %d scenes, want 2", len(loaded))
	}

	// Build by composite key so we don't depend on Load's row order.
	byKey := map[sceneKey]models.Scene{}
	for _, sc := range loaded {
		byKey[sceneKey{id: sc.ID, siteID: sc.SiteID}] = sc
	}

	alpha, ok := byKey[sceneKey{id: "b:c", siteID: "a"}]
	if !ok {
		t.Fatal("alpha key missing from loaded scenes")
	}
	if len(alpha.Performers) != 1 || alpha.Performers[0] != "Alpha" {
		t.Errorf("alpha Performers = %v, want [Alpha]", alpha.Performers)
	}
	if len(alpha.Tags) != 1 || alpha.Tags[0] != "x" {
		t.Errorf("alpha Tags = %v, want [x]", alpha.Tags)
	}
	if len(alpha.PriceHistory) != 1 || alpha.PriceHistory[0].Regular != 1.00 {
		t.Errorf("alpha PriceHistory = %+v, want [{Regular:1.00}]", alpha.PriceHistory)
	}

	beta, ok := byKey[sceneKey{id: "c", siteID: "a:b"}]
	if !ok {
		t.Fatal("beta key missing from loaded scenes")
	}
	if len(beta.Performers) != 1 || beta.Performers[0] != "Beta" {
		t.Errorf("beta Performers = %v, want [Beta]", beta.Performers)
	}
	if len(beta.Tags) != 1 || beta.Tags[0] != "y" {
		t.Errorf("beta Tags = %v, want [y]", beta.Tags)
	}
	if len(beta.PriceHistory) != 1 || beta.PriceHistory[0].Regular != 2.00 {
		t.Errorf("beta PriceHistory = %+v, want [{Regular:2.00}]", beta.PriceHistory)
	}
}

// TestSQLiteLoadOrderDeterministic locks in the documented Load order
// (scraped_at DESC, then id ASC for tie-break). Two scenes with the
// same scraped_at must come back in stable id order; newer scenes come
// before older ones. Without ORDER BY, SQLite is free to return rows
// in any order on subsequent calls — making diffs of JSON/CSV exports
// noisy and tests flaky.
func TestSQLiteLoadOrderDeterministic(t *testing.T) {
	s := newTestDB(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	scenes := []models.Scene{
		{ID: "b", SiteID: "manyvids", StudioURL: testStudioURL, Title: "B", ScrapedAt: t0},
		{ID: "a", SiteID: "manyvids", StudioURL: testStudioURL, Title: "A", ScrapedAt: t2},
		{ID: "c", SiteID: "manyvids", StudioURL: testStudioURL, Title: "C", ScrapedAt: t1},
		// Tie on scraped_at — id breaks the tie ascending.
		{ID: "y", SiteID: "manyvids", StudioURL: testStudioURL, Title: "Y", ScrapedAt: t1},
		{ID: "x", SiteID: "manyvids", StudioURL: testStudioURL, Title: "X", ScrapedAt: t1},
	}
	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "c", "x", "y", "b"} // t2; then t1 ASC by id; then t0
	got := make([]string, len(loaded))
	for i, sc := range loaded {
		got[i] = sc.ID
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Load order = %v, want %v", got, want)
	}
}

// TestSQLiteSaveRejectsEmptyKeyFields locks in the store-boundary
// guard added with the validateScenes helper. A scene with an empty
// ID or SiteID would either fail at insert time, collide with other
// empty-keyed scenes, or silently lose its relations on Load — catch
// it loudly at the boundary instead.
func TestSQLiteSaveRejectsEmptyKeyFields(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC()

	emptyID := models.Scene{ID: "", SiteID: "manyvids", StudioURL: testStudioURL, Title: "x", ScrapedAt: now}
	if err := s.Save(testStudioURL, []models.Scene{emptyID}); err == nil {
		t.Errorf("Save with empty ID should error")
	} else if !strings.Contains(err.Error(), "ID is required") {
		t.Errorf("error should mention ID: %v", err)
	}

	emptySite := models.Scene{ID: "1", SiteID: "", StudioURL: testStudioURL, Title: "y", ScrapedAt: now}
	if err := s.Save(testStudioURL, []models.Scene{emptySite}); err == nil {
		t.Errorf("Save with empty SiteID should error")
	} else if !strings.Contains(err.Error(), "SiteID is required") {
		t.Errorf("error should mention SiteID: %v", err)
	}

	// Nothing should have been written.
	loaded, _ := s.Load(testStudioURL)
	if len(loaded) != 0 {
		t.Errorf("rejected Save still wrote: got %d scenes", len(loaded))
	}
}

func TestSQLiteSaveAutoRevives(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL,
		Title: "A", ScrapedAt: now,
	}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeleted(testStudioURL, "manyvids", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := s.Load(testStudioURL)
	if loaded[0].DeletedAt == nil {
		t.Fatal("setup: scene should be soft-deleted after MarkDeleted")
	}

	// Re-emit the same scene with DeletedAt == nil — auto-revive.
	revived := scene
	revived.Title = "A (back)"
	if err := s.Save(testStudioURL, []models.Scene{revived}); err != nil {
		t.Fatal(err)
	}
	loaded, _ = s.Load(testStudioURL)
	if len(loaded) != 1 {
		t.Fatalf("got %d scenes, want 1", len(loaded))
	}
	if loaded[0].DeletedAt != nil {
		t.Errorf("Save with DeletedAt=nil should auto-revive, got DeletedAt=%v", loaded[0].DeletedAt)
	}
	if loaded[0].Title != "A (back)" {
		t.Errorf("Title not updated: %q", loaded[0].Title)
	}
}

// TestSQLiteSavePreservesExplicitDeletedAt verifies the symmetric path:
// when a Save includes a scene with DeletedAt explicitly set, the
// stored value matches. This is how scrapeRefresh propagates soft-
// deletes for scenes the scraper no longer sees.
func TestSQLiteSavePreservesExplicitDeletedAt(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	earlier := now.Add(-24 * time.Hour)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL,
		Title: "A", ScrapedAt: now, DeletedAt: &earlier,
	}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := s.Load(testStudioURL)
	if loaded[0].DeletedAt == nil || !loaded[0].DeletedAt.Equal(earlier) {
		t.Errorf("DeletedAt = %v, want %v", loaded[0].DeletedAt, earlier)
	}
}

// TestSQLiteRelationDiffAddRemove covers the syncRelation diff path: re-saving
// a scene with a different relation set should add new entries and drop removed
// ones, without re-touching unchanged rows.
func TestSQLiteRelationDiffAddRemove(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "X", ScrapedAt: now,
		Performers: []string{"Alice", "Bob"},
		Tags:       []string{"red", "green"},
	}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	// Drop Bob, add Carol; drop "green", add "blue".
	scene.Performers = []string{"Alice", "Carol"}
	scene.Tags = []string{"red", "blue"}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("got %d scenes, want 1", len(loaded))
	}
	got := loaded[0]
	wantPerformers := []string{"Alice", "Carol"}
	if len(got.Performers) != 2 || got.Performers[0] != wantPerformers[0] || got.Performers[1] != wantPerformers[1] {
		t.Errorf("Performers = %v, want %v", got.Performers, wantPerformers)
	}
	// Tags have no deterministic order in the schema; check as a set.
	gotTags := map[string]bool{}
	for _, t := range got.Tags {
		gotTags[t] = true
	}
	if !gotTags["red"] || !gotTags["blue"] || gotTags["green"] || len(gotTags) != 2 {
		t.Errorf("Tags = %v, want {red, blue}", got.Tags)
	}
}

// TestSQLiteRelationDiffPositionUpdate covers the positioned-relation case:
// reordering performers should update positions in place, not duplicate rows.
func TestSQLiteRelationDiffPositionUpdate(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "X", ScrapedAt: now,
		Performers: []string{"Alice", "Bob"},
	}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	scene.Performers = []string{"Bob", "Alice"}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	loaded, _ := s.Load(testStudioURL)
	got := loaded[0].Performers
	if len(got) != 2 || got[0] != "Bob" || got[1] != "Alice" {
		t.Errorf("Performers after reorder = %v, want [Bob, Alice]", got)
	}

	var rowCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scene_performers WHERE scene_id = '1'`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 2 {
		t.Errorf("scene_performers row count = %d, want 2", rowCount)
	}
}

// TestSQLitePriceHistoryDiff verifies that re-saving with the same history is
// a no-op (no duplicate inserts) and that adding one snapshot inserts only
// the new row.
func TestSQLitePriceHistoryDiff(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "X", ScrapedAt: now,
	}
	scene.AddPrice(models.PriceSnapshot{Date: now, Regular: 29.99})
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}

	var firstID int64
	if err := s.db.QueryRow(`SELECT id FROM price_history WHERE scene_id = '1'`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}

	// Re-save with no change: row id must be preserved (no DELETE+reinsert churn).
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}
	var afterResaveID int64
	if err := s.db.QueryRow(`SELECT id FROM price_history WHERE scene_id = '1'`).Scan(&afterResaveID); err != nil {
		t.Fatal(err)
	}
	if afterResaveID != firstID {
		t.Errorf("re-save churned row id: %d -> %d (should be unchanged)", firstID, afterResaveID)
	}

	// Add a new snapshot. Original row id must still be preserved.
	scene.AddPrice(models.PriceSnapshot{Date: now.Add(24 * time.Hour), Regular: 24.99, IsOnSale: true, Discounted: 24.99})
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}
	var rowCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM price_history WHERE scene_id = '1'`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 2 {
		t.Fatalf("price_history row count after add = %d, want 2", rowCount)
	}
	var stillFirst int64
	if err := s.db.QueryRow(`SELECT id FROM price_history WHERE scene_id = '1' AND regular = 29.99`).Scan(&stillFirst); err != nil {
		t.Fatal(err)
	}
	if stillFirst != firstID {
		t.Errorf("original snapshot row id changed: %d -> %d (diff path should not delete unchanged rows)", firstID, stillFirst)
	}
}

// TestSQLiteRelationFastPathPreservesEntities verifies the no-op case: re-saving
// with identical relations should not churn the entity table or junction rows.
func TestSQLiteRelationFastPathPreservesEntities(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "X", ScrapedAt: now,
		Performers: []string{"Alice"},
	}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}
	var firstAliceID int64
	if err := s.db.QueryRow(`SELECT id FROM performers WHERE name = 'Alice'`).Scan(&firstAliceID); err != nil {
		t.Fatal(err)
	}

	// Re-save unchanged: Alice's id and her junction row must be untouched.
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatal(err)
	}
	var afterAliceID int64
	if err := s.db.QueryRow(`SELECT id FROM performers WHERE name = 'Alice'`).Scan(&afterAliceID); err != nil {
		t.Fatal(err)
	}
	if afterAliceID != firstAliceID {
		t.Errorf("Alice id churned: %d -> %d", firstAliceID, afterAliceID)
	}
	var perfCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scene_performers WHERE scene_id = '1'`).Scan(&perfCount); err != nil {
		t.Fatal(err)
	}
	if perfCount != 1 {
		t.Errorf("scene_performers row count after no-op resave = %d, want 1", perfCount)
	}
}

// ---- Export ----

func TestSQLiteExportJSON(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{{
		ID: "1", SiteID: "test", StudioURL: testStudioURL,
		Title: "Export Me", URL: "https://example.com/1",
		Performers: []string{"Alice"}, Tags: []string{"tag1"},
		Duration: 600, ScrapedAt: now,
	}}
	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "export.json")
	if err := s.Export("json", path, testStudioURL); err != nil {
		t.Fatalf("Export JSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sf models.StudioFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if sf.StudioURL != testStudioURL {
		t.Errorf("studioUrl = %q", sf.StudioURL)
	}
	if len(sf.Scenes) != 1 || sf.Scenes[0].Title != "Export Me" {
		t.Errorf("scenes = %v", sf.Scenes)
	}
	if len(sf.Scenes[0].Performers) != 1 || sf.Scenes[0].Performers[0] != "Alice" {
		t.Errorf("performers = %v", sf.Scenes[0].Performers)
	}
}

func TestSQLiteExportCSV(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{{
		ID: "1", SiteID: "test", StudioURL: testStudioURL,
		Title: "CSV Scene", Performers: []string{"Bob", "Carol"},
		Tags: []string{"t1", "t2"}, Duration: 1200, ScrapedAt: now,
	}}
	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "export.csv")
	if err := s.Export("csv", path, testStudioURL); err != nil {
		t.Fatalf("Export CSV: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d rows, want 2 (header + 1)", len(records))
	}
	if records[1][3] != "CSV Scene" {
		t.Errorf("title = %q", records[1][3])
	}
}

func TestSQLiteExportUnknownFormat(t *testing.T) {
	s := newTestDB(t)
	err := s.Export("xml", "/tmp/nope.xml", testStudioURL)
	if err == nil {
		t.Error("expected error for unknown format")
	}
}

// ---- Migration ----

// newV0DB creates a SQLite database at schema v0 (no junction tables).
// Scenes have JSON arrays in the performers/tags/categories TEXT columns,
// just like the original schema before migration 1.
func newV0DB(t *testing.T) *SQLite {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(baseSchema); err != nil {
		t.Fatalf("baseSchema: %v", err)
	}
	// Explicitly set version to 0 so migration 1 will run.
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	return &SQLite{db: db}
}

func TestSQLiteMigration1(t *testing.T) {
	s := newV0DB(t)
	now := timeStr(time.Now().UTC().Truncate(time.Second))

	// Insert v0-style scenes with JSON arrays in text columns.
	_, err := s.db.Exec(`
		INSERT INTO scenes (id, site_id, studio_url, title, url, date,
			performers, tags, categories, scraped_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"s1", "test", testStudioURL, "Scene One", "https://example.com/1", now,
		`["Alice","Bob"]`, `["blowjob","anal"]`, `["premium"]`, now,
	)
	if err != nil {
		t.Fatalf("insert scene 1: %v", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO scenes (id, site_id, studio_url, title, url, date,
			performers, tags, categories, scraped_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"s2", "test", testStudioURL, "Scene Two", "https://example.com/2", now,
		`[]`, `["solo"]`, `[]`, now,
	)
	if err != nil {
		t.Fatalf("insert scene 2: %v", err)
	}

	// Run migration 1.
	if err := s.applyMigration1(); err != nil {
		t.Fatalf("applyMigration1: %v", err)
	}

	// Verify schema version updated.
	var version int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("schema version = %d, want 1", version)
	}

	// Load reads the v2 (studio_url-qualified) schema, so apply migration 2;
	// it carries the migration-1 junction data forward unchanged.
	if err := s.applyMigration2(); err != nil {
		t.Fatalf("applyMigration2: %v", err)
	}
	if err := s.applyMigration3(); err != nil {
		t.Fatalf("applyMigration3: %v", err)
	}
	if err := s.applyMigration4(); err != nil {
		t.Fatalf("applyMigration4: %v", err)
	}
	if err := s.applyMigration5(); err != nil {
		t.Fatalf("applyMigration5: %v", err)
	}
	if err := s.applyMigration6(); err != nil {
		t.Fatalf("applyMigration6: %v", err)
	}
	if err := s.applyMigration7(); err != nil {
		t.Fatalf("applyMigration7: %v", err)
	}
	if err := s.applyMigration8(); err != nil {
		t.Fatalf("applyMigration8: %v", err)
	}

	// Verify junction table data via Load.
	scenes, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}

	byID := map[string]models.Scene{}
	for _, sc := range scenes {
		byID[sc.ID] = sc
	}

	sc1 := byID["s1"]
	if len(sc1.Performers) != 2 || sc1.Performers[0] != "Alice" || sc1.Performers[1] != "Bob" {
		t.Errorf("s1 performers = %v, want [Alice Bob]", sc1.Performers)
	}
	if len(sc1.Tags) != 2 {
		t.Errorf("s1 tags = %v, want 2 tags", sc1.Tags)
	}
	if len(sc1.Categories) != 1 || sc1.Categories[0] != "premium" {
		t.Errorf("s1 categories = %v, want [premium]", sc1.Categories)
	}

	sc2 := byID["s2"]
	if len(sc2.Performers) != 0 {
		t.Errorf("s2 performers = %v, want empty", sc2.Performers)
	}
	if len(sc2.Tags) != 1 || sc2.Tags[0] != "solo" {
		t.Errorf("s2 tags = %v, want [solo]", sc2.Tags)
	}
	if len(sc2.Categories) != 0 {
		t.Errorf("s2 categories = %v, want empty", sc2.Categories)
	}

	// Verify entity tables were populated.
	var perfCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM performers`).Scan(&perfCount); err != nil {
		t.Fatal(err)
	}
	if perfCount != 2 {
		t.Errorf("performers table has %d rows, want 2", perfCount)
	}
}

func TestSQLiteMigration1EmptyDB(t *testing.T) {
	s := newV0DB(t)

	if err := s.applyMigration1(); err != nil {
		t.Fatalf("applyMigration1 on empty DB: %v", err)
	}

	var version int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("schema version = %d, want 1", version)
	}
}

func TestSQLiteMigration1NullJSON(t *testing.T) {
	s := newV0DB(t)
	now := timeStr(time.Now().UTC().Truncate(time.Second))

	// Insert a scene where JSON columns are empty strings or null.
	_, err := s.db.Exec(`
		INSERT INTO scenes (id, site_id, studio_url, title, url, date,
			performers, tags, categories, scraped_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"s1", "test", testStudioURL, "Null Scene", "https://example.com/1", now,
		`null`, ``, `[]`, now,
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.applyMigration1(); err != nil {
		t.Fatalf("applyMigration1 with null JSON: %v", err)
	}
	if err := s.applyMigration2(); err != nil {
		t.Fatalf("applyMigration2: %v", err)
	}
	if err := s.applyMigration3(); err != nil {
		t.Fatalf("applyMigration3: %v", err)
	}
	if err := s.applyMigration4(); err != nil {
		t.Fatalf("applyMigration4: %v", err)
	}
	if err := s.applyMigration5(); err != nil {
		t.Fatalf("applyMigration5: %v", err)
	}
	if err := s.applyMigration6(); err != nil {
		t.Fatalf("applyMigration6: %v", err)
	}
	if err := s.applyMigration7(); err != nil {
		t.Fatalf("applyMigration7: %v", err)
	}
	if err := s.applyMigration8(); err != nil {
		t.Fatalf("applyMigration8: %v", err)
	}

	scenes, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	if len(scenes[0].Performers) != 0 {
		t.Errorf("performers = %v, want empty", scenes[0].Performers)
	}
}

// ---- unmarshalStrings ----

func TestUnmarshalStrings(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty string", "", nil},
		{"empty array", "[]", nil},
		{"null", "null", nil},
		{"single", `["alice"]`, []string{"alice"}},
		{"multiple", `["alice","bob","carol"]`, []string{"alice", "bob", "carol"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := unmarshalStrings(c.input)
			if err != nil {
				t.Fatalf("unmarshalStrings(%q): %v", c.input, err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestUnmarshalStringsInvalid(t *testing.T) {
	_, err := unmarshalStrings(`{not json}`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// TestSQLitePragmas pins AUDIT.md §Store #6, #7: foreign_keys was never set,
// silently disabling the ON DELETE CASCADE clauses on the three junction
// tables. journal_mode/synchronous were issued as a multi-statement Exec,
// which is unreliable across drivers — we now issue each PRAGMA on its own.
//
// PRAGMA return-value reference: foreign_keys → 0/1, journal_mode → string
// ("wal"), synchronous → 0/1/2/3 (NORMAL = 1).
func TestSQLitePragmas(t *testing.T) {
	s := newTestDB(t)

	var fk int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	var journalMode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	// In-memory databases don't support WAL and fall back to "memory" — accept
	// either as long as the PRAGMA was applied (i.e. didn't error).
	if journalMode != "wal" && journalMode != "memory" {
		t.Errorf("journal_mode = %q, want wal or memory (for :memory: DB)", journalMode)
	}

	var sync int
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatalf("PRAGMA synchronous: %v", err)
	}
	if sync != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", sync)
	}
}

// TestSQLiteForeignKeysCascade proves that ON DELETE CASCADE actually fires
// for the junction tables. With foreign_keys=OFF (the pre-fix state) a hard
// DELETE on scenes would orphan scene_performers rows; this test would then
// see lingering rows and fail.
func TestSQLiteForeignKeysCascade(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	scene := models.Scene{
		ID: "cascade-1", SiteID: "manyvids", StudioURL: testStudioURL,
		Title: "X", ScrapedAt: now,
		Performers: []string{"Alice", "Bob"},
		Tags:       []string{"red", "blue"},
		Categories: []string{"solo"},
	}
	if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Sanity: junction rows are present.
	for _, table := range []string{"scene_performers", "scene_tags", "scene_categories"} {
		var n int
		if err := s.db.QueryRow(
			"SELECT count(*) FROM "+table+" WHERE scene_id = ? AND site_id = ?",
			scene.ID, scene.SiteID,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n == 0 {
			t.Fatalf("%s has 0 rows after Save (test setup wrong)", table)
		}
	}

	// Hard-delete the scene. With foreign_keys=ON the cascade clauses fire
	// and the junction rows go away.
	if _, err := s.db.Exec(
		"DELETE FROM scenes WHERE id = ? AND site_id = ?",
		scene.ID, scene.SiteID,
	); err != nil {
		t.Fatalf("DELETE scenes: %v", err)
	}

	for _, table := range []string{"scene_performers", "scene_tags", "scene_categories"} {
		var n int
		if err := s.db.QueryRow(
			"SELECT count(*) FROM "+table+" WHERE scene_id = ? AND site_id = ?",
			scene.ID, scene.SiteID,
		).Scan(&n); err != nil {
			t.Fatalf("count %s after delete: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d orphan rows after DELETE scenes — cascade did not fire", table, n)
		}
	}
}

// TestSQLiteCompositePKNoCrossStudioSteal is the A7 regression: two studio URLs
// on the same site sharing a scene (id, site_id) must keep independent rows —
// including performers/tags/categories and price history — so neither studio's
// authoritative Save deletes or overwrites the other's data.
func TestSQLiteCompositePKNoCrossStudioSteal(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	const urlA = "https://example.com/studio-a"
	const urlB = "https://example.com/studio-b"

	mk := func(studioURL, title, performer, tag string, price float64) models.Scene {
		sc := models.Scene{
			ID: "1", SiteID: "shared", StudioURL: studioURL,
			Title: title, URL: "https://example.com/v/1",
			Performers: []string{performer}, Tags: []string{tag}, ScrapedAt: now,
		}
		sc.AddPrice(models.PriceSnapshot{Date: now, Regular: price})
		return sc
	}

	if err := s.Save(urlA, []models.Scene{mk(urlA, "A title", "Alice", "tagA", 9.99)}); err != nil {
		t.Fatalf("Save A: %v", err)
	}
	if err := s.Save(urlB, []models.Scene{mk(urlB, "B title", "Bob", "tagB", 4.99)}); err != nil {
		t.Fatalf("Save B: %v", err)
	}

	check := func(url, wantTitle, wantPerformer, wantTag string, wantPrice float64) {
		t.Helper()
		got, err := s.Load(url)
		if err != nil {
			t.Fatalf("Load %s: %v", url, err)
		}
		if len(got) != 1 {
			t.Fatalf("Load %s: got %d scenes, want 1", url, len(got))
		}
		sc := got[0]
		if sc.Title != wantTitle {
			t.Errorf("%s title = %q, want %q", url, sc.Title, wantTitle)
		}
		if len(sc.Performers) != 1 || sc.Performers[0] != wantPerformer {
			t.Errorf("%s performers = %v, want [%s]", url, sc.Performers, wantPerformer)
		}
		if len(sc.Tags) != 1 || sc.Tags[0] != wantTag {
			t.Errorf("%s tags = %v, want [%s]", url, sc.Tags, wantTag)
		}
		if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != wantPrice {
			t.Errorf("%s price = %v, want %v", url, sc.PriceHistory, wantPrice)
		}
	}

	// Both studios keep their own row/relations/price despite sharing (id, site_id).
	check(urlA, "A title", "Alice", "tagA", 9.99)
	check(urlB, "B title", "Bob", "tagB", 4.99)

	// An authoritative Save that empties studio A must not touch studio B.
	if err := s.Save(urlA, nil); err != nil {
		t.Fatalf("Save A empty: %v", err)
	}
	if got, _ := s.Load(urlA); len(got) != 0 {
		t.Errorf("studio A should be empty, got %d", len(got))
	}
	check(urlB, "B title", "Bob", "tagB", 4.99)
}

// A DEFAULT on a TEXT column does not make it NOT NULL — it only applies when
// the column is omitted on insert. A NULL written by a hand edit or an external
// tool used to fail the row scan and take the entire studio's Load with it.
func TestLoadToleratesNullColumns(t *testing.T) {
	dir := t.TempDir()
	st, err := NewSQLite(filepath.Join(dir, "null.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	const studioURL = "https://example.com/studio"
	if err := st.Save(studioURL, []models.Scene{{
		ID:        "1",
		SiteID:    "example",
		StudioURL: studioURL,
		Title:     "Intact",
		ScrapedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	// Null out every column that the schema leaves nullable.
	if _, err := st.db.Exec(`
		UPDATE scenes SET
			date = NULL, description = NULL, thumbnail = NULL, preview = NULL,
			director = NULL, studio = NULL, series = NULL, series_part = NULL,
			duration = NULL, resolution = NULL, width = NULL, height = NULL,
			format = NULL, views = NULL, likes = NULL, comments = NULL,
			lowest_price = NULL
		WHERE id = '1'`); err != nil {
		t.Fatal(err)
	}

	scenes, err := st.Load(studioURL)
	if err != nil {
		t.Fatalf("Load failed on a row with NULL columns: %v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "1" || sc.Title != "Intact" {
		t.Errorf("scene = %+v", sc)
	}
	if !sc.Date.IsZero() {
		t.Errorf("Date = %v, want the zero time for a NULL date", sc.Date)
	}
	if sc.Description != "" || sc.Duration != 0 || sc.LowestPrice != 0 {
		t.Errorf("NULLs should read as zero values, got %+v", sc)
	}
}

// D2: tag and category order must survive a Save→Load round-trip.
//
// Before migration 3 the junction tables had no position column and loadRelation
// issued no ORDER BY at all, so the order was whatever the join happened to produce.
// That is unspecified in SQLite and moves with the query planner — an ANALYZE, a new
// index, or a library upgrade is enough. Scrapers emit tags in the site's own order and
// it flows through to Stash and NFO output, so a reshuffle is a silent metadata
// regression nobody would trace back to a SQLite version bump.
//
// The order chosen here is deliberately not alphabetical, so a fallback to name ordering
// fails this too.
func TestSQLiteTagAndCategoryOrderPreserved(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	// Scene 1 mints the tag/category rows, so their AUTOINCREMENT ids follow this
	// order. Scene 2 then uses the same names in a *different* order — that is what
	// makes the test discriminating: joining on tag_id yields scene 1's order, so a
	// missing ORDER BY returns the wrong list for scene 2. A single-scene test passes
	// either way, because the join happens to come back in insertion order.
	tags := []string{"zebra", "alpha", "Mango", "beta", "9lives"}
	cats := []string{"Solo", "anal", "BDSM", "amateur"}
	reTags := []string{"beta", "9lives", "zebra", "Mango", "alpha"}
	reCats := []string{"amateur", "BDSM", "Solo", "anal"}

	if err := s.Save(testStudioURL, []models.Scene{
		{
			ID: "1", SiteID: "manyvids", StudioURL: testStudioURL,
			Title: "T1", URL: "https://example.com/1", ScrapedAt: now,
			Tags: tags, Categories: cats,
		},
		{
			ID: "2", SiteID: "manyvids", StudioURL: testStudioURL,
			Title: "T2", URL: "https://example.com/2", ScrapedAt: now,
			Tags: reTags, Categories: reCats,
		},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scenes, want 2", len(got))
	}
	byID := map[string]models.Scene{}
	for _, sc := range got {
		byID[sc.ID] = sc
	}
	if !reflect.DeepEqual(byID["1"].Tags, tags) {
		t.Errorf("scene 1 Tags = %v, want %v", byID["1"].Tags, tags)
	}
	if !reflect.DeepEqual(byID["1"].Categories, cats) {
		t.Errorf("scene 1 Categories = %v, want %v", byID["1"].Categories, cats)
	}
	if !reflect.DeepEqual(byID["2"].Tags, reTags) {
		t.Errorf("scene 2 Tags = %v, want %v — this is the order that differs from "+
			"tag_id order, so a missing ORDER BY shows up here", byID["2"].Tags, reTags)
	}
	if !reflect.DeepEqual(byID["2"].Categories, reCats) {
		t.Errorf("scene 2 Categories = %v, want %v", byID["2"].Categories, reCats)
	}
}

// Re-saving with a reordered list must persist the new order, not keep the first one —
// the position column is rewritten, not just populated on insert.
func TestSQLiteTagOrderUpdatedOnResave(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	base := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL,
		Title: "T", URL: "https://example.com/1", ScrapedAt: now,
	}

	first := []string{"one", "two", "three"}
	base.Tags = first
	if err := s.Save(testStudioURL, []models.Scene{base}); err != nil {
		t.Fatal(err)
	}

	reordered := []string{"three", "one", "two"}
	base.Tags = reordered
	if err := s.Save(testStudioURL, []models.Scene{base}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got[0].Tags, reordered) {
		t.Errorf("Tags = %v, want %v — a re-save must rewrite positions", got[0].Tags, reordered)
	}
}

// Every upgrade path must end at a schema Load can read: fresh, v0, and a database
// already at v2. Migration 3 is an ALTER TABLE, so a path that creates the column twice
// would fail with "duplicate column name" — which is why migrations 1 and 2 were left in
// their historical form rather than edited to include it.
func TestSQLiteMigration3AllUpgradePaths(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	scene := models.Scene{
		ID: "1", SiteID: "manyvids", StudioURL: testStudioURL,
		Title: "T", URL: "https://example.com/1", ScrapedAt: now,
		Tags: []string{"z", "a"}, Categories: []string{"y", "b"},
	}

	t.Run("fresh database", func(t *testing.T) {
		s := newTestDB(t)
		if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Load(testStudioURL)
		if err != nil {
			t.Fatalf("Load on a fresh database: %v", err)
		}
		if !reflect.DeepEqual(got[0].Tags, scene.Tags) {
			t.Errorf("Tags = %v, want %v", got[0].Tags, scene.Tags)
		}
	})

	t.Run("v0 upgraded through every migration", func(t *testing.T) {
		s := newV0DB(t)
		if err := s.migrate(); err != nil {
			t.Fatalf("full migrate from v0: %v", err)
		}
		if err := s.Save(testStudioURL, []models.Scene{scene}); err != nil {
			t.Fatal(err)
		}
		got, err := s.Load(testStudioURL)
		if err != nil {
			t.Fatalf("Load after upgrading from v0: %v", err)
		}
		if !reflect.DeepEqual(got[0].Tags, scene.Tags) {
			t.Errorf("Tags = %v, want %v", got[0].Tags, scene.Tags)
		}
	})

	t.Run("migrate is idempotent", func(t *testing.T) {
		s := newTestDB(t)
		if err := s.migrate(); err != nil {
			t.Fatalf("second migrate: %v", err)
		}
		if err := s.migrate(); err != nil {
			t.Fatalf("third migrate: %v — ALTER TABLE ran twice", err)
		}
	})
}

// countWrites reports how many rows the scenes table reports as modified since
// a marker, using SQLite's own change counter via a trigger-free proxy: we
// stamp a sentinel column and count rows whose content_hash was rewritten.
func storedHashes(t *testing.T, s *SQLite, studioURL string) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, content_hash FROM scenes WHERE studio_url = ?`, studioURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, h string
		if err := rows.Scan(&id, &h); err != nil {
			t.Fatal(err)
		}
		out[id] = h
	}
	return out
}

// TestSQLiteSaveSkipsUnchangedScenes pins the diff-aware Save: re-saving a scene
// whose content did not change must not rewrite its relations or price history.
// Before this, recording one new scene in a large studio issued ~5 statements
// per stored scene.
func TestSQLiteSaveSkipsUnchangedScenes(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	build := func(id, title string, tags []string) models.Scene {
		return models.Scene{
			ID: id, SiteID: "t", StudioURL: testStudioURL,
			Title: title, URL: "https://example.com/" + id,
			Tags: tags, Performers: []string{"P"}, ScrapedAt: now,
		}
	}
	scenes := []models.Scene{
		build("1", "One", []string{"a", "b"}),
		build("2", "Two", []string{"c"}),
	}
	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}
	before := storedHashes(t, s, testStudioURL)
	if before["1"] == "" || before["2"] == "" {
		t.Fatalf("content_hash not stamped: %v", before)
	}

	// Re-save with scene 2 changed; scene 1 must keep its hash and its data.
	scenes[1].Title = "Two (updated)"
	if err := s.Save(testStudioURL, scenes); err != nil {
		t.Fatal(err)
	}
	after := storedHashes(t, s, testStudioURL)
	if after["1"] != before["1"] {
		t.Error("unchanged scene 1 had its content_hash rewritten")
	}
	if after["2"] == before["2"] {
		t.Error("changed scene 2 kept its old content_hash")
	}

	got, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]models.Scene{}
	for _, sc := range got {
		byID[sc.ID] = sc
	}
	if len(byID["1"].Tags) != 2 || byID["1"].Tags[0] != "a" || byID["1"].Tags[1] != "b" {
		t.Errorf("skipped scene lost its tags: %v", byID["1"].Tags)
	}
	if byID["2"].Title != "Two (updated)" {
		t.Errorf("changed scene not updated: %q", byID["2"].Title)
	}
}

// A scrape that only moves ScrapedAt must still record the new timestamp, even
// though the content hash is unchanged and the expensive path is skipped.
func TestSQLiteSaveUpdatesScrapedAtOnUnchangedScene(t *testing.T) {
	s := newTestDB(t)
	first := time.Now().UTC().Truncate(time.Second).Add(-48 * time.Hour)

	sc := models.Scene{
		ID: "1", SiteID: "t", StudioURL: testStudioURL,
		Title: "One", URL: "https://example.com/1",
		Tags: []string{"a"}, ScrapedAt: first,
	}
	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}

	later := first.Add(24 * time.Hour)
	sc.ScrapedAt = later
	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scenes", len(got))
	}
	if !got[0].ScrapedAt.Equal(later) {
		t.Errorf("ScrapedAt = %v, want %v", got[0].ScrapedAt, later)
	}
	if !got[0].FirstSeenAt.Equal(first) {
		t.Errorf("FirstSeenAt = %v, want the original %v", got[0].FirstSeenAt, first)
	}
}

// MarkDeleted writes deleted_at outside upsertScene, so it must invalidate the
// content hash — otherwise re-saving the scene undeleted would be skipped and
// the soft-delete would never lift.
func TestSQLiteMarkDeletedInvalidatesContentHash(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	sc := models.Scene{
		ID: "1", SiteID: "t", StudioURL: testStudioURL,
		Title: "One", URL: "https://example.com/1", ScrapedAt: now,
	}
	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeleted(testStudioURL, "t", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if h := storedHashes(t, s, testStudioURL)["1"]; h != "" {
		t.Errorf("content_hash = %q after MarkDeleted, want cleared", h)
	}

	// Re-saving the same scene with DeletedAt == nil must revive it.
	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Load(testStudioURL)
	if len(got) != 1 || got[0].DeletedAt != nil {
		t.Errorf("scene not revived: %+v", got)
	}
}

// The hash must be computed over the stored representation, so a scene that
// round-trips through the database hashes identically to the one that went in.
// Otherwise every scene looks changed and nothing is ever skipped.
func TestSceneContentHashSurvivesRoundTrip(t *testing.T) {
	s := newTestDB(t)
	price := time.Date(2026, 3, 1, 12, 30, 45, 0, time.UTC)
	sc := models.Scene{
		ID: "1", SiteID: "t", StudioURL: testStudioURL,
		Title: "One", URL: "https://example.com/1",
		Date:        time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC), // sub-second
		Performers:  []string{"Alice", "Bob"},
		Tags:        []string{"x", "y"},
		Categories:  []string{"c"},
		ExternalIDs: map[string]string{"stashdb": "uuid", "tpdb": "t1"},
		Duration:    600,
		ScrapedAt:   time.Now().UTC(),
	}
	sc.AddPrice(models.PriceSnapshot{Date: price, Regular: 9.99})

	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scenes", len(got))
	}
	if a, b := sceneContentHash(sc), sceneContentHash(got[0]); a != b {
		t.Errorf("hash changed across a round trip:\n  in  %s\n  out %s", a, b)
	}
}

// Distinct content must produce distinct hashes, including in the collection
// fields where a naive concatenation could collide.
func TestSceneContentHashDistinguishesFields(t *testing.T) {
	base := models.Scene{ID: "1", SiteID: "t", Title: "One", Tags: []string{"ab", "c"}}
	variants := map[string]func(models.Scene) models.Scene{
		"title":            func(s models.Scene) models.Scene { s.Title = "Two"; return s },
		"tag split":        func(s models.Scene) models.Scene { s.Tags = []string{"a", "bc"}; return s },
		"tag order":        func(s models.Scene) models.Scene { s.Tags = []string{"c", "ab"}; return s },
		"extra tag":        func(s models.Scene) models.Scene { s.Tags = append(s.Tags, "d"); return s },
		"performers":       func(s models.Scene) models.Scene { s.Performers = []string{"A"}; return s },
		"duration":         func(s models.Scene) models.Scene { s.Duration = 1; return s },
		"external id":      func(s models.Scene) models.Scene { s.ExternalIDs = map[string]string{"a": "b"}; return s },
		"deleted":          func(s models.Scene) models.Scene { now := time.Now().UTC(); s.DeletedAt = &now; return s },
		"price":            func(s models.Scene) models.Scene { s.AddPrice(models.PriceSnapshot{Regular: 1}); return s },
		"lowest price val": func(s models.Scene) models.Scene { s.LowestPrice = 5; return s },
	}
	h0 := sceneContentHash(base)
	for name, mutate := range variants {
		if h := sceneContentHash(mutate(base)); h == h0 {
			t.Errorf("%s: hash unchanged after mutation", name)
		}
	}

	// ScrapedAt is excluded by design — including it would mean nothing ever
	// matches and the skip path would be dead code.
	bumped := base
	bumped.ScrapedAt = time.Now().UTC().Add(time.Hour)
	if sceneContentHash(bumped) != h0 {
		t.Error("ScrapedAt must not affect the content hash")
	}
	bumped.FirstSeenAt = time.Now().UTC()
	if sceneContentHash(bumped) != h0 {
		t.Error("FirstSeenAt must not affect the content hash")
	}
}

// TestSQLiteUpgradeFromPreHashDatabase covers the path every existing database
// takes: migration 6 adds content_hash with a ” default, so pre-existing rows
// carry no fingerprint. The first Save must rewrite them rather than mistake
// the empty hash for a match, and the rewrite must reproduce relations and
// price history intact.
func TestSQLiteUpgradeFromPreHashDatabase(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	sc := models.Scene{
		ID: "1", SiteID: "t", StudioURL: testStudioURL,
		Title: "One", URL: "https://example.com/1",
		Performers: []string{"Alice", "Bob"},
		Tags:       []string{"z", "a"}, // deliberately not alphabetical
		Categories: []string{"c1"},
		ScrapedAt:  now,
	}
	sc.AddPrice(models.PriceSnapshot{Date: now, Regular: 4.99})
	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}

	// Simulate the post-migration-6 state of a database populated by an older
	// build: rows present, fingerprint absent.
	if _, err := s.db.Exec(`UPDATE scenes SET content_hash = ''`); err != nil {
		t.Fatal(err)
	}

	// A Save of the same content must take the full write path and re-stamp.
	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	if h := storedHashes(t, s, testStudioURL)["1"]; h == "" {
		t.Fatal("content_hash still empty after a save — pre-hash rows were skipped")
	}

	got, err := s.Load(testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scenes, want 1", len(got))
	}
	if len(got[0].Performers) != 2 || got[0].Performers[0] != "Alice" {
		t.Errorf("performers = %v", got[0].Performers)
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != "z" || got[0].Tags[1] != "a" {
		t.Errorf("tags = %v, want [z a] (source order preserved)", got[0].Tags)
	}
	if len(got[0].PriceHistory) != 1 {
		t.Errorf("price history = %v", got[0].PriceHistory)
	}

	// And the now-stamped row is skipped on the next save.
	before := storedHashes(t, s, testStudioURL)
	if err := s.Save(testStudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	if storedHashes(t, s, testStudioURL)["1"] != before["1"] {
		t.Error("hash changed on a no-op save")
	}
}

// TestSQLiteRelationsAreStudioScoped guards the loadRelation rewrite. It used to
// reach the studio through `JOIN scenes ... WHERE s.studio_url = ?`; it now
// filters `j.studio_url = ?` directly. Two studios sharing scene IDs must still
// get their own relations.
func TestSQLiteRelationsAreStudioScoped(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	urlA := "https://a.example.com/studio"
	urlB := "https://b.example.com/studio"

	// Same scene ID and site ID in both studios, different relations.
	mk := func(studioURL string, tags, performers []string) models.Scene {
		return models.Scene{
			ID: "shared-1", SiteID: "t", StudioURL: studioURL,
			Title: "Shared", URL: studioURL + "/1",
			Tags: tags, Performers: performers, ScrapedAt: now,
		}
	}
	if err := s.Save(urlA, []models.Scene{mk(urlA, []string{"a-tag"}, []string{"Alice"})}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(urlB, []models.Scene{mk(urlB, []string{"b-tag"}, []string{"Bob"})}); err != nil {
		t.Fatal(err)
	}

	gotA, err := s.Load(urlA)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := s.Load(urlB)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA) != 1 || len(gotB) != 1 {
		t.Fatalf("got %d / %d scenes, want 1 each", len(gotA), len(gotB))
	}
	if len(gotA[0].Tags) != 1 || gotA[0].Tags[0] != "a-tag" {
		t.Errorf("studio A tags = %v, want [a-tag]", gotA[0].Tags)
	}
	if len(gotB[0].Tags) != 1 || gotB[0].Tags[0] != "b-tag" {
		t.Errorf("studio B tags = %v, want [b-tag]", gotB[0].Tags)
	}
	if len(gotA[0].Performers) != 1 || gotA[0].Performers[0] != "Alice" {
		t.Errorf("studio A performers = %v", gotA[0].Performers)
	}
	if len(gotB[0].Performers) != 1 || gotB[0].Performers[0] != "Bob" {
		t.Errorf("studio B performers = %v", gotB[0].Performers)
	}
}

// TestSQLiteStudioIndexesExist pins migration 7. Without these, Load scans every
// junction row in the database to read one studio's — correct, but O(all
// studios). A dropped index would be a silent performance regression.
func TestSQLiteStudioIndexesExist(t *testing.T) {
	s := newTestDB(t)
	want := []string{
		"idx_scene_performers_studio",
		"idx_scene_tags_studio",
		"idx_scene_categories_studio",
		"idx_scene_external_ids_studio",
	}
	for _, name := range want {
		var got string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&got)
		if err != nil {
			t.Errorf("index %s missing: %v", name, err)
		}
	}

	// And the planner actually uses one, rather than scanning.
	rows, err := s.db.Query(
		`EXPLAIN QUERY PLAN SELECT scene_id FROM scene_tags WHERE studio_url = ?`, testStudioURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + " "
	}
	if !strings.Contains(plan, "idx_scene_tags_studio") {
		t.Errorf("planner did not use the studio index; plan was: %s", plan)
	}
}

// TestSQLiteSchemaIsDocumented guards docs/usage.md against schema drift. The
// documented column lists were wrong in six places before this existed —
// missing `studio_url` on every child table, missing `first_seen_at`,
// `content_hash` and the whole `scene_external_ids` table — and one example
// query joined without `studio_url`, which silently merges studios.
func TestSQLiteSchemaIsDocumented(t *testing.T) {
	s := newTestDB(t)

	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "usage.md"))
	if err != nil {
		t.Skipf("docs not readable: %v", err)
	}
	text := string(docs)

	tables := []string{
		"scenes", "price_history", "studios",
		"performers", "tags", "categories",
		"scene_performers", "scene_tags", "scene_categories",
		"scene_external_ids", "schema_version",
	}
	for _, table := range tables {
		if !strings.Contains(text, "`"+table+"`") {
			t.Errorf("table %s is not mentioned in docs/usage.md", table)
		}
	}

	// Every column of the tables whose layout the docs spell out must appear.
	documented := []string{"scenes", "price_history", "studios", "scene_external_ids"}
	for _, table := range documented {
		rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatalf("pragma_table_info(%s): %v", table, err)
		}
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, c)
		}
		_ = rows.Close()
		if len(cols) == 0 {
			t.Fatalf("%s reported no columns", table)
		}
		for _, c := range cols {
			if !strings.Contains(text, "`"+c+"`") {
				t.Errorf("%s.%s is not documented in docs/usage.md", table, c)
			}
		}
	}
}

// The schema version the code migrates to must match what the docs claim, so a
// new migration cannot land without the docs being updated.
func TestSQLiteDocumentedSchemaVersion(t *testing.T) {
	s := newTestDB(t)
	var version int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}

	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "usage.md"))
	if err != nil {
		t.Skipf("docs not readable: %v", err)
	}
	want := fmt.Sprintf("schema version %d", version)
	if !strings.Contains(string(docs), want) {
		t.Errorf("docs/usage.md does not state %q — bump it when adding a migration", want)
	}
}

// TestSQLiteMigration9MergesURLVariants covers the case migration 9 exists for:
// one catalogue stored under several spellings of its URL. Canonicalising the
// key makes them collide on the primary key, so the migration must merge rather
// than fail — keeping the freshest row and carrying child rows across.
func TestSQLiteMigration9MergesURLVariants(t *testing.T) {
	s := newTestDB(t)
	older := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Two spellings of one site. Scene "shared" exists in both; each has one
	// scene the other lacks.
	httpURL := "http://www.example.com"
	httpsURL := "https://www.example.com/"

	mk := func(id, title, studioURL string, when time.Time, tags []string) models.Scene {
		return models.Scene{
			ID: id, SiteID: "x", StudioURL: studioURL,
			Title: title, URL: "https://www.example.com/" + id,
			Tags: tags, ScrapedAt: when,
		}
	}
	if err := s.Save(httpURL, []models.Scene{
		mk("shared", "old title", httpURL, older, []string{"old-tag"}),
		mk("only-http", "Only HTTP", httpURL, older, nil),
	}); err != nil {
		t.Fatal(err)
	}
	// Write the https variant directly, bypassing the canonicalising Save so the
	// database ends up in the pre-migration shape.
	if _, err := s.db.Exec(`INSERT INTO scenes (id, site_id, studio_url, title, url, scraped_at, first_seen_at, content_hash)
		VALUES ('shared','x',?,'new title','u',?,?,''), ('only-https','x',?,'Only HTTPS','u',?,?,'')`,
		httpsURL, timeStr(newer), timeStr(newer), httpsURL, timeStr(newer), timeStr(newer)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO studios (url, site_id, name, added_at, last_scraped_at)
		VALUES (?,?,?,?,?), (?,?,?,?,?)`,
		httpURL, "x", "Example", timeStr(older), timeStr(older),
		httpsURL, "x", "Example", timeStr(newer), timeStr(newer)); err != nil {
		t.Fatal(err)
	}
	// Rewind so migration 9 runs again over this hand-made state.
	if _, err := s.db.Exec(`DELETE FROM schema_version`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_version (version) VALUES (8)`); err != nil {
		t.Fatal(err)
	}

	if err := s.applyMigration9(); err != nil {
		t.Fatalf("applyMigration9: %v", err)
	}

	canonical := "https://www.example.com"
	got, err := s.Load(canonical)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]models.Scene{}
	for _, sc := range got {
		byID[sc.ID] = sc
	}
	if len(got) != 3 {
		t.Fatalf("got %d scenes, want 3 (both variants merged): %v", len(got), byID)
	}
	// The colliding scene keeps the newer row.
	if byID["shared"].Title != "new title" {
		t.Errorf("shared title = %q, want the newer row's value", byID["shared"].Title)
	}
	// Scenes unique to each variant survive.
	if byID["only-http"].Title == "" || byID["only-https"].Title == "" {
		t.Errorf("a variant-only scene was dropped: %v", byID)
	}
	// Child rows followed their parent.
	if len(byID["only-http"].Tags) != 0 && len(byID["shared"].Tags) == 0 {
		t.Error("relations were not carried across")
	}
	// Every remaining row is under the canonical URL only.
	var stray int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM scenes WHERE studio_url <> ?`, canonical).Scan(&stray); err != nil {
		t.Fatal(err)
	}
	if stray != 0 {
		t.Errorf("%d scene rows left under a non-canonical URL", stray)
	}
	// The studios table merged to one row.
	studios, err := s.ListStudios()
	if err != nil {
		t.Fatal(err)
	}
	if len(studios) != 1 || studios[0].URL != canonical {
		t.Errorf("studios = %+v, want a single canonical row", studios)
	}
	// And no orphaned child rows remain.
	for _, table := range []string{"scene_performers", "scene_tags", "scene_categories", "price_history"} {
		var orphans int
		q := `SELECT COUNT(*) FROM ` + table + ` j WHERE NOT EXISTS (
			SELECT 1 FROM scenes s WHERE s.id = j.scene_id AND s.site_id = j.site_id
			  AND s.studio_url = j.studio_url)`
		if err := s.db.QueryRow(q).Scan(&orphans); err != nil {
			t.Fatal(err)
		}
		if orphans != 0 {
			t.Errorf("%s has %d orphaned rows", table, orphans)
		}
	}
}

// A database whose URLs are all already canonical must pass through untouched.
func TestSQLiteMigration9NoopWhenAlreadyCanonical(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	url := "https://www.example.com/studio"
	if err := s.Save(url, []models.Scene{
		{ID: "1", SiteID: "x", StudioURL: url, Title: "One", Tags: []string{"t"}, ScrapedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Load(url)
	if err := s.applyMigration9(); err != nil {
		t.Fatalf("applyMigration9: %v", err)
	}
	after, err := s.Load(url)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || after[0].Title != before[0].Title || len(after[0].Tags) != 1 {
		t.Errorf("no-op migration changed data: %+v -> %+v", before, after)
	}
}

// orderedNames restores the stored order of a scene's relations. SQLite usually
// hands rows back already sorted, so the reordering branch only runs when it
// does not — the case that would otherwise silently scramble performer order.
func TestOrderedNames(t *testing.T) {
	tests := []struct {
		name      string
		names     string
		positions string
		want      []string
		wantErr   bool
	}{
		{name: "already sorted", names: `["a","b","c"]`, positions: `[0,1,2]`, want: []string{"a", "b", "c"}},
		{name: "reversed", names: `["c","b","a"]`, positions: `[2,1,0]`, want: []string{"a", "b", "c"}},
		{name: "shuffled", names: `["b","c","a"]`, positions: `[1,2,0]`, want: []string{"a", "b", "c"}},
		{name: "sparse positions", names: `["b","a"]`, positions: `[70,7]`, want: []string{"a", "b"}},
		{name: "duplicate positions keep input order", names: `["b","a"]`, positions: `[0,0]`, want: []string{"b", "a"}},
		{name: "single", names: `["only"]`, positions: `[3]`, want: []string{"only"}},
		{name: "empty", names: `[]`, positions: `[]`, want: nil},
		{name: "length mismatch", names: `["a","b"]`, positions: `[0]`, wantErr: true},
		{name: "malformed names", names: `not json`, positions: `[0]`, wantErr: true},
		{name: "malformed positions", names: `["a"]`, positions: `not json`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := orderedNames(tt.names, tt.positions)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("orderedNames: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Save removes a scene's junction rows but leaves the shared entity rows they
// pointed at, so vocabulary accumulates across re-scrapes. These cover the
// maintenance path that clears it.
func TestUnreferencedVocabularyAndPrune(t *testing.T) {
	db := newTestDB(t)
	const studio = "https://vocab.example.com"

	sc := models.Scene{
		ID: "1", SiteID: "s", StudioURL: studio, Title: "One",
		ScrapedAt:  time.Now().UTC(),
		Performers: []string{"Ada Stone"},
		Tags:       []string{"Tockings", "Silk"},
		Categories: []string{"Solo"},
	}
	if err := db.Save(studio, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	if counts, err := db.UnreferencedVocabulary(); err != nil {
		t.Fatal(err)
	} else if counts["tags"] != 0 || counts["performers"] != 0 {
		t.Fatalf("fresh save already has unreferenced rows: %v", counts)
	}

	// The scrape that corrects a truncated tag: the junction rows move, the old
	// vocabulary row is left behind.
	sc.Tags = []string{"Stockings", "Silk"}
	if err := db.Save(studio, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	counts, err := db.UnreferencedVocabulary()
	if err != nil {
		t.Fatal(err)
	}
	if counts["tags"] != 1 {
		t.Fatalf("unreferenced tags = %d, want 1 (the corrected spelling orphans the old row)", counts["tags"])
	}

	removed, err := db.PruneVocabulary()
	if err != nil {
		t.Fatal(err)
	}
	if removed["tags"] != 1 {
		t.Errorf("pruned %d tags, want 1", removed["tags"])
	}
	after, err := db.UnreferencedVocabulary()
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range after {
		if n != 0 {
			t.Errorf("%s still has %d unreferenced rows after prune", table, n)
		}
	}

	// Pruning must not touch anything a scene still references.
	got, err := db.Load(studio)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Tags) != 2 || len(got[0].Performers) != 1 {
		t.Fatalf("prune damaged live data: %+v", got)
	}
}

// An entity shared by two studios must survive one of them dropping it.
func TestPruneVocabularyKeepsSharedEntities(t *testing.T) {
	db := newTestDB(t)
	const a, b = "https://a.example.com", "https://b.example.com"
	now := time.Now().UTC()

	mk := func(studio, id string, tags ...string) models.Scene {
		return models.Scene{ID: id, SiteID: "s", StudioURL: studio, Title: id, ScrapedAt: now, Tags: tags}
	}
	if err := db.Save(a, []models.Scene{mk(a, "1", "Shared")}); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(b, []models.Scene{mk(b, "2", "Shared")}); err != nil {
		t.Fatal(err)
	}
	// Studio A stops using the tag; studio B still does.
	if err := db.Save(a, []models.Scene{mk(a, "1")}); err != nil {
		t.Fatal(err)
	}
	if removed, err := db.PruneVocabulary(); err != nil {
		t.Fatal(err)
	} else if removed["tags"] != 0 {
		t.Errorf("pruned %d tags, want 0 — the tag is still on studio B", removed["tags"])
	}
	got, err := db.Load(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Tags) != 1 || got[0].Tags[0] != "Shared" {
		t.Fatalf("shared tag lost from studio B: %+v", got)
	}
}

// SceneCounts backs `fss doctor`'s store comparison, which is the only thing
// that tells an operator a studio exists as JSON but not in the database.

func TestSQLiteSceneCountsPerStudio(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	const otherStudio = "https://www.manyvids.com/Profile/999/other/Store/Videos"
	if err := s.Save(testStudioURL, []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "One", ScrapedAt: now},
		{ID: "2", SiteID: "manyvids", StudioURL: testStudioURL, Title: "Two", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(otherStudio, []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: otherStudio, Title: "Solo", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save other: %v", err)
	}

	counts, err := s.SceneCounts()
	if err != nil {
		t.Fatalf("SceneCounts: %v", err)
	}
	if got := counts[testStudioURL]; got != 2 {
		t.Errorf("count for %s = %d, want 2", testStudioURL, got)
	}
	if got := counts[otherStudio]; got != 1 {
		t.Errorf("count for %s = %d, want 1", otherStudio, got)
	}
	if len(counts) != 2 {
		t.Errorf("got %d studios, want 2: %v", len(counts), counts)
	}
}

// A soft-deleted scene is still a row, but it is one the site stopped
// publishing. Counting it would make the database look ahead of a JSON file
// that never recorded it, i.e. report a spurious mismatch.
func TestSQLiteSceneCountsExcludesSoftDeleted(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := s.Save(testStudioURL, []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: testStudioURL, Title: "One", ScrapedAt: now},
		{ID: "2", SiteID: "manyvids", StudioURL: testStudioURL, Title: "Two", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.MarkDeleted(testStudioURL, "manyvids", []string{"2"}); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}

	counts, err := s.SceneCounts()
	if err != nil {
		t.Fatalf("SceneCounts: %v", err)
	}
	if got := counts[testStudioURL]; got != 1 {
		t.Errorf("count = %d, want 1 (the soft-deleted scene must not count)", got)
	}
}

// The counts are keyed the way Load and Save key, so a studio saved under a
// non-canonical spelling is not reported as a second studio.
func TestSQLiteSceneCountsKeysCanonically(t *testing.T) {
	s := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	const variant = "http://WWW.ManyVids.com/Profile/123/test-creator/Store/Videos/"
	if err := s.Save(variant, []models.Scene{
		{ID: "1", SiteID: "manyvids", StudioURL: variant, Title: "One", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	counts, err := s.SceneCounts()
	if err != nil {
		t.Fatalf("SceneCounts: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("got %d keys, want 1: %v", len(counts), counts)
	}
	// The canonical spelling of variant is testStudioURL: https scheme,
	// lowercased host, no trailing slash, path case preserved.
	if got := counts[testStudioURL]; got != 1 {
		t.Errorf("count under the canonical key = %d, want 1 (keys: %v)", got, counts)
	}
}

func TestSQLiteSceneCountsEmptyDatabase(t *testing.T) {
	counts, err := newTestDB(t).SceneCounts()
	if err != nil {
		t.Fatalf("SceneCounts: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("got %v, want no studios", counts)
	}
}

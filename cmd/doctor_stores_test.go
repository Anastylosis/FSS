package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
)

func writeInventoryFile(t *testing.T, dir, name, studioURL string, count int) {
	t.Helper()
	f := models.StudioFile{
		SchemaVersion: models.StoreSchemaVersion,
		StudioURL:     studioURL,
		SceneCount:    count,
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestFlatInventoryReadsStudioFiles(t *testing.T) {
	dir := t.TempDir()
	writeInventoryFile(t, dir, "a.json", "https://a.example/studio/1", 12)
	writeInventoryFile(t, dir, "b.json", "https://b.example/studio/2", 3)

	inv, broken, err := flatInventory(dir)
	if err != nil {
		t.Fatalf("flatInventory: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("broken = %v, want none", broken)
	}
	if inv["https://a.example/studio/1"] != 12 || inv["https://b.example/studio/2"] != 3 {
		t.Errorf("inventory = %v", inv)
	}
}

// out_dir holds more than studio files — the changelog and any CSV the
// operator left there. Only files carrying a studioUrl are studios.
func TestFlatInventoryIgnoresNonStudioJSON(t *testing.T) {
	dir := t.TempDir()
	writeInventoryFile(t, dir, "a.json", "https://a.example/studio/1", 5)
	if err := os.WriteFile(filepath.Join(dir, "fss-stashbox-changelog.json"), []byte(`{"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv, broken, err := flatInventory(dir)
	if err != nil {
		t.Fatalf("flatInventory: %v", err)
	}
	if len(inv) != 1 {
		t.Errorf("inventory = %v, want just the studio", inv)
	}
	if len(broken) != 0 {
		t.Errorf("broken = %v; a changelog is not a broken studio file", broken)
	}
}

// A corrupt studio file must not read as "absent from the database" — that
// would send the operator to `fss import`, which cannot fix it.
func TestFlatInventoryReportsUnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"studioUrl": `), 0o644); err != nil {
		t.Fatal(err)
	}
	inv, broken, err := flatInventory(dir)
	if err != nil {
		t.Fatalf("flatInventory: %v", err)
	}
	if len(inv) != 0 {
		t.Errorf("inventory = %v, want empty", inv)
	}
	if len(broken) != 1 || broken[0] != "broken.json" {
		t.Errorf("broken = %v, want [broken.json]", broken)
	}
}

// A studio saved under an http:// or www. spelling must not read as missing
// from a database that stores the canonical form.
func TestFlatInventoryCanonicalisesStudioURLs(t *testing.T) {
	dir := t.TempDir()
	writeInventoryFile(t, dir, "a.json", "http://WWW.Example.com/studio/1/", 4)

	inv, _, err := flatInventory(dir)
	if err != nil {
		t.Fatalf("flatInventory: %v", err)
	}
	db := studioInventory{"https://www.example.com/studio/1": 4}
	if d := diffStores(inv, db); !d.inSync() {
		t.Errorf("variant spelling read as out of sync: onlyInFlat=%v differing=%v", d.OnlyInFlat, d.Differing)
	}
}

func TestDiffStoresClassifies(t *testing.T) {
	flat := studioInventory{
		"https://a.example/s": 10, // both, same count
		"https://b.example/s": 7,  // both, different count
		"https://c.example/s": 3,  // JSON only
	}
	db := studioInventory{
		"https://a.example/s": 10,
		"https://b.example/s": 9,
		"https://d.example/s": 1, // database only
	}
	d := diffStores(flat, db)

	if len(d.OnlyInFlat) != 1 || d.OnlyInFlat[0] != "https://c.example/s" {
		t.Errorf("OnlyInFlat = %v", d.OnlyInFlat)
	}
	if len(d.Differing) != 1 || d.Differing[0] != "https://b.example/s" {
		t.Errorf("Differing = %v", d.Differing)
	}
	if len(d.OnlyInDB) != 1 || d.OnlyInDB[0] != "https://d.example/s" {
		t.Errorf("OnlyInDB = %v", d.OnlyInDB)
	}
	if d.inSync() {
		t.Error("inSync() = true with an unimported studio")
	}
}

// Database-only studios are normal after exporting or tidying JSON away, so
// they must not make the check fail on their own.
func TestDiffStoresDatabaseOnlyIsNotOutOfSync(t *testing.T) {
	d := diffStores(studioInventory{}, studioInventory{"https://a.example/s": 5})
	if !d.inSync() {
		t.Error("a database-only studio reported as out of sync")
	}
	if len(d.OnlyInDB) != 1 {
		t.Errorf("OnlyInDB = %v", d.OnlyInDB)
	}
}

func TestSummarizeNamesStudiosAndTruncates(t *testing.T) {
	flat := studioInventory{}
	for _, u := range []string{"https://a/s", "https://b/s", "https://c/s"} {
		flat[u] = 1
	}
	d := diffStores(flat, studioInventory{})

	var b strings.Builder
	d.summarize(&b, 2)
	out := b.String()
	if !strings.Contains(out, "only in JSON (3)") {
		t.Errorf("missing count header:\n%s", out)
	}
	if !strings.Contains(out, "… and 1 more") {
		t.Errorf("long list not truncated:\n%s", out)
	}
	if !strings.Contains(out, "fss import") {
		t.Errorf("no remedy named:\n%s", out)
	}
}

// dbInventory is the database half of doctor's store comparison. Without it
// the comparison has nothing to compare the JSON files against.

func newInventoryDB(t *testing.T) *store.SQLite {
	t.Helper()
	db, err := store.NewSQLite(filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDBInventoryCountsLiveScenesPerStudio(t *testing.T) {
	db := newInventoryDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	const a = "https://example.com/studio-a"
	const b = "https://example.com/studio-b"
	if err := db.Save(a, []models.Scene{
		{ID: "1", SiteID: "t", StudioURL: a, Title: "One", ScrapedAt: now},
		{ID: "2", SiteID: "t", StudioURL: a, Title: "Two", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := db.Save(b, []models.Scene{
		{ID: "1", SiteID: "t", StudioURL: b, Title: "Solo", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save b: %v", err)
	}

	inv, err := dbInventory(db)
	if err != nil {
		t.Fatalf("dbInventory: %v", err)
	}
	if inv[a] != 2 || inv[b] != 1 {
		t.Errorf("inventory = %v, want %s:2 %s:1", inv, a, b)
	}
}

// A studio saved under a non-canonical spelling must land on the same key the
// flat inventory uses, or doctor reports a studio as missing from both stores.
func TestDBInventoryCanonicalisesStudioURLs(t *testing.T) {
	db := newInventoryDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	const variant = "http://WWW.Example.com/studio-a/"
	if err := db.Save(variant, []models.Scene{
		{ID: "1", SiteID: "t", StudioURL: variant, Title: "One", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	inv, err := dbInventory(db)
	if err != nil {
		t.Fatalf("dbInventory: %v", err)
	}
	if len(inv) != 1 {
		t.Fatalf("got %d studios, want 1: %v", len(inv), inv)
	}
	if inv["https://www.example.com/studio-a"] != 1 {
		t.Errorf("inventory = %v, want the canonical key to hold the count", inv)
	}
}

func TestDBInventoryEmptyDatabase(t *testing.T) {
	inv, err := dbInventory(newInventoryDB(t))
	if err != nil {
		t.Fatalf("dbInventory: %v", err)
	}
	if len(inv) != 0 {
		t.Errorf("got %v, want empty", inv)
	}
}

// A soft-deleted scene is not live, so it must not widen the gap between the
// two stores.
func TestDBInventoryExcludesSoftDeleted(t *testing.T) {
	db := newInventoryDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	const a = "https://example.com/studio-a"
	if err := db.Save(a, []models.Scene{
		{ID: "1", SiteID: "t", StudioURL: a, Title: "One", ScrapedAt: now},
		{ID: "2", SiteID: "t", StudioURL: a, Title: "Two", ScrapedAt: now},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := db.MarkDeleted(a, "t", []string{"2"}); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}

	inv, err := dbInventory(db)
	if err != nil {
		t.Fatalf("dbInventory: %v", err)
	}
	if inv[a] != 1 {
		t.Errorf("inventory = %v, want %s:1", inv, a)
	}
}

// ---- describeCounts (doctor's vocabulary summary) ----

func TestDescribeCounts(t *testing.T) {
	cases := []struct {
		name   string
		counts map[string]int
		want   string
	}{
		{"all three, in fixed order", map[string]int{"tags": 2, "categories": 3, "performers": 1},
			"1 performers, 2 tags, 3 categories"},
		{"subset", map[string]int{"tags": 5}, "5 tags"},
		{"zeroes are omitted", map[string]int{"performers": 0, "tags": 4, "categories": 0}, "4 tags"},
		{"all zero", map[string]int{"performers": 0, "tags": 0, "categories": 0}, "nothing"},
		{"empty", map[string]int{}, "nothing"},
		{"unknown keys are ignored", map[string]int{"studios": 9}, "nothing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := describeCounts(c.counts); got != c.want {
				t.Errorf("describeCounts(%v) = %q, want %q", c.counts, got, c.want)
			}
		})
	}
}

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/output"
)

// exportCmdFor builds a standalone command carrying the same flags as the real
// one, so a test can set them without mutating the shared rootCmd.
func exportCmdFor(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{RunE: runExport}
	c.Flags().String("db", "", "")
	c.Flags().Lookup("db").NoOptDefVal = "default"
	c.Flags().StringP("output", "o", "", "")
	c.Flags().String("out-dir", "", "")
	c.Flags().Bool("force", false, "")
	c.SetOut(&strings.Builder{})
	if err := c.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	return c
}

func seedDB(t *testing.T, studioURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fss.db")
	db, err := store.NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer func() { _ = db.Close() }()

	now := time.Now().UTC().Truncate(time.Second)
	scenes := []models.Scene{{
		ID: "1", SiteID: "ex", StudioURL: studioURL, Title: "Exported Scene",
		URL: studioURL + "/v/1", Performers: []string{"Alice"},
		Tags: []string{"tag"}, Studio: "Example", ScrapedAt: now,
	}}
	if err := db.Save(studioURL, scenes); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := db.UpsertStudio(models.Studio{
		URL: studioURL, SiteID: "ex", Name: "Example", AddedAt: now,
	}); err != nil {
		t.Fatalf("UpsertStudio: %v", err)
	}
	return path
}

func TestExportWritesRequestedFormats(t *testing.T) {
	const studioURL = "https://export.example.com/studio"
	dbPath := seedDB(t, studioURL)
	outDir := t.TempDir()

	c := exportCmdFor(t, "--db="+dbPath, "--out-dir", outDir, "--output", "json,csv")
	if err := runExport(c, nil); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	slug := output.Slugify(studioURL)
	for _, ext := range []string{"json", "csv"} {
		path := filepath.Join(outDir, slug+"."+ext)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", path)
		}
	}

	raw, err := os.ReadFile(filepath.Join(outDir, slug+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var file models.StudioFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("exported JSON does not parse: %v", err)
	}
	if len(file.Scenes) != 1 || file.Scenes[0].Title != "Exported Scene" {
		t.Errorf("exported scenes = %+v", file.Scenes)
	}
}

// With no URL arguments the tracked studios are exported.
func TestExportDefaultsToEveryTrackedStudio(t *testing.T) {
	const studioURL = "https://export-all.example.com/studio"
	dbPath := seedDB(t, studioURL)
	outDir := t.TempDir()

	c := exportCmdFor(t, "--db="+dbPath, "--out-dir", outDir, "--output", "json")
	if err := runExport(c, nil); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, output.Slugify(studioURL)+".json")); err != nil {
		t.Errorf("no file for the tracked studio: %v", err)
	}
}

func TestExportRequiresDB(t *testing.T) {
	c := exportCmdFor(t, "--out-dir", t.TempDir())
	err := runExport(c, nil)
	if err == nil {
		t.Fatal("expected an error without --db")
	}
	if !strings.Contains(err.Error(), "--db is required") {
		t.Errorf("err = %v", err)
	}
}

func TestExportEmptyDatabaseIsAnError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	db, err := store.NewSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	c := exportCmdFor(t, "--db="+dbPath, "--out-dir", t.TempDir())
	err = runExport(c, nil)
	if err == nil || !strings.Contains(err.Error(), "no studios tracked") {
		t.Errorf("err = %v, want the no-studios message", err)
	}
}

func TestExportAbsentStudioRefusesAndWritesNothing(t *testing.T) {
	dbPath := seedDB(t, "https://example.com/a")
	out := t.TempDir()
	err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), []string{"https://example.com/missing"})
	if err == nil || !strings.Contains(err.Error(), "example.com/missing") {
		t.Fatalf("err = %v", err)
	}
	if ents, _ := filepath.Glob(filepath.Join(out, "*.json")); len(ents) != 0 {
		t.Fatalf("files written: %v", ents)
	}
}

func TestExportDoesNotOverwriteLargerFlatFile(t *testing.T) {
	u := "https://example.com/a"
	dbPath := seedDB(t, u)
	out := t.TempDir()
	fl := store.NewFlat(out, nil)
	now := time.Now().UTC()
	var sc []models.Scene
	for _, id := range []string{"1", "2", "3"} {
		sc = append(sc, models.Scene{ID: id, SiteID: "ex", StudioURL: u, Title: "t", URL: u + "/" + id, ScrapedAt: now})
	}
	if err := fl.Save(u, sc); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), []string{u}); err == nil {
		t.Fatal("expected refusal")
	}
	if got, _ := fl.Load(u); len(got) != 3 {
		t.Fatalf("flat file clobbered: %d scenes", len(got))
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out, "--force"), []string{u}); err != nil {
		t.Fatalf("force: %v", err)
	}
}

func TestExportURLSpellingsWriteOneFile(t *testing.T) {
	dbPath := seedDB(t, "https://example.com/a")
	out := t.TempDir()
	args := []string{"https://example.com/a", "HTTPS://EXAMPLE.com/a/", "example.com/a"}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), args); err != nil {
		t.Fatal(err)
	}
	if ents, _ := filepath.Glob(filepath.Join(out, "*.json")); len(ents) != 1 {
		t.Fatalf("files: %v", ents)
	}
}

func flatScenes(u string, ids ...string) []models.Scene {
	var sc []models.Scene
	for _, id := range ids {
		sc = append(sc, models.Scene{ID: id, SiteID: "ex", StudioURL: u, Title: "t", URL: u + "/" + id, ScrapedAt: time.Now().UTC()})
	}
	return sc
}

func TestExportRefusesLegacyNamedFlatFile(t *testing.T) {
	u := "https://example.com/a"
	dbPath := seedDB(t, u)
	out := t.TempDir()
	sf := models.StudioFile{SchemaVersion: models.StoreSchemaVersion, StudioURL: u, Scenes: flatScenes(u, "1", "2", "3")}
	if err := output.WriteJSON(sf, filepath.Join(out, output.LegacySlugify(u)+".json")); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), []string{u}); err == nil {
		t.Fatal("expected refusal")
	}
	if got, _ := store.NewFlat(out, nil).Load(u); len(got) != 3 {
		t.Fatalf("legacy file lost: %d", len(got))
	}
}

func TestExportRefusesVariantNamedFlatFile(t *testing.T) {
	dbPath := seedDB(t, "https://example.com/a")
	out := t.TempDir()
	v := "HTTPS://Example.com/a/"
	sf := models.StudioFile{SchemaVersion: models.StoreSchemaVersion, StudioURL: v, Scenes: flatScenes(v, "1", "2", "3")}
	if err := output.WriteJSON(sf, filepath.Join(out, output.Slugify(v)+".json")); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), []string{"https://example.com/a"}); err == nil {
		t.Fatal("expected refusal")
	}
	if got, _ := store.NewFlat(out, nil).Load("https://example.com/a"); len(got) != 3 {
		t.Fatalf("variant file lost: %d", len(got))
	}
}

func TestExportRefusesCorruptAndUnreadableFlatFile(t *testing.T) {
	u := "https://example.com/a"
	dbPath := seedDB(t, u)
	out := t.TempDir()
	path := filepath.Join(out, output.Slugify(u)+".json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), []string{u}); err == nil {
		t.Fatal("corrupt: expected refusal")
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Fatal("corrupt file overwritten")
	}
	if os.Geteuid() == 0 {
		return
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), []string{u}); err == nil {
		t.Fatal("unreadable: expected refusal")
	}
	_ = os.Chmod(path, 0o644)
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Fatal("unreadable file overwritten")
	}
}

func TestExportEqualCountDifferentIDsRefused(t *testing.T) {
	u := "https://example.com/a"
	dbPath := seedDB(t, u)
	out := t.TempDir()
	if err := store.NewFlat(out, nil).Save(u, flatScenes(u, "other")); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), []string{u}); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestExportRefusedJSONLeavesCSVAlone(t *testing.T) {
	u := "https://example.com/a"
	dbPath := seedDB(t, u)
	out := t.TempDir()
	if err := store.NewFlat(out, nil).Save(u, flatScenes(u, "1", "2", "3")); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(out, output.Slugify(u)+".csv")
	if err := os.WriteFile(csvPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out, "-o", "json,csv"), []string{u}); err == nil {
		t.Fatal("expected refusal")
	}
	if b, _ := os.ReadFile(csvPath); string(b) != "keep" {
		t.Fatalf("csv touched: %q", b)
	}
}

func TestExportAllStudiosOneRefusedOthersWritten(t *testing.T) {
	a, b := "https://example.com/a", "https://example.com/b"
	dbPath := seedDB(t, a)
	db, err := store.NewSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Save(b, flatScenes(b, "1")); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertStudio(models.Studio{URL: b, SiteID: "ex", Name: "B", AddedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	out := t.TempDir()
	if err := store.NewFlat(out, nil).Save(a, flatScenes(a, "x", "y")); err != nil {
		t.Fatal(err)
	}
	if err := runExport(exportCmdFor(t, "--db="+dbPath, "--out-dir", out), nil); err == nil {
		t.Fatal("expected error")
	}
	if got, _ := store.NewFlat(out, nil).Load(b); len(got) != 1 {
		t.Fatalf("b not written: %d", len(got))
	}
	if got, _ := store.NewFlat(out, nil).Load(a); len(got) != 2 {
		t.Fatalf("a clobbered: %d", len(got))
	}
}

func TestScrapeDBExportGuardRefusesWithoutFailing(t *testing.T) {
	u := "https://example.com/a"
	db, err := store.NewSQLite(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Save(u, flatScenes(u, "1")); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	fl := store.NewFlat(out, nil)
	if err := fl.Save(u, flatScenes(u, "1", "2", "3")); err != nil {
		t.Fatal(err)
	}
	_, err = exportStudio(db, fl, u, out, []string{"json"}, nil, false, true)
	if !errors.Is(err, errExportRefused) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := fl.Load(u); len(got) != 3 {
		t.Fatalf("clobbered: %d", len(got))
	}
}

func TestScrapeDBExportInOutDirDoesNotDeadlock(t *testing.T) {
	u := "https://example.com/a"
	out := t.TempDir()
	dbPath := filepath.Join(out, "fss.db")
	db, err := store.NewSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Save(u, flatScenes(u, "1")); err != nil {
		t.Fatal(err)
	}
	held, err := db.Lock(u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()

	done := make(chan error, 1)
	go func() {
		_, err := exportStudio(db, store.NewFlat(out, nil), u, out, []string{"json"}, nil, false, !sameDir(out, filepath.Dir(dbPath)))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("export blocked on the lock scrape already holds")
	}
}

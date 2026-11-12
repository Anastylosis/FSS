package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

var importCmd = &cobra.Command{
	Use:   "import <file-or-dir> [file-or-dir ...]",
	Short: "Load studio JSON files into the SQLite database",
	Long: `Load one or more studio JSON files (or every *.json in a directory) into the
SQLite store.

The database schema is a superset of the JSON layout, so nothing is lost. Each
file's own "studioUrl" decides which studio it belongs to — filenames are not
parsed.

Existing database scenes are merged with the file by default: a scene present in
both takes the file's values, price history is carried forward, and scenes only
in the database are kept. Pass --replace to make the file authoritative instead,
which deletes database scenes absent from it.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runImport,
}

func init() {
	rootCmd.AddCommand(importCmd)
	importCmd.Flags().String("db", "", "path to SQLite database (no value = the configured db, or the default location)")
	importCmd.Flags().Lookup("db").NoOptDefVal = "default"
	importCmd.Flags().Bool("replace", false, "make each file authoritative: delete stored scenes it does not contain")
	importCmd.Flags().Bool("dry-run", false, "report what would be imported without writing")
}

func runImport(cmd *cobra.Command, args []string) error {
	dbPath := resolveDBPath(cmd)
	if dbPath == "" {
		return fmt.Errorf("--db is required (pass --db for the default location, or --db /path/to/file.db)")
	}
	replace, _ := cmd.Flags().GetBool("replace")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	files, err := collectJSONFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .json files found in %s", strings.Join(args, ", "))
	}

	// A dry run must not create anything. Opening a database that does not
	// exist yet would create the file (and its directory), which made
	// --dry-run fail outright on a first import — the case it is most useful
	// for. With no database there is simply nothing stored to merge against.
	var db *store.SQLite
	if _, statErr := os.Stat(dbPath); !dryRun || statErr == nil {
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
				return fmt.Errorf("creating database directory: %w", err)
			}
		}
		opened, err := store.NewSQLite(dbPath)
		if err != nil {
			return fmt.Errorf("opening database: %w", err)
		}
		defer func() { _ = opened.Close() }()
		db = opened
	} else {
		fmt.Printf("No database at %s yet — it would be created.\n", dbPath)
	}

	// Two files describing the same studio is common (a re-scrape saved
	// alongside the original) and silently destructive: the merge is
	// last-write-wins per field, so whichever lands second decides. Name both
	// rather than let the outcome depend on filenames.
	seenStudios := map[string]string{}

	var imported, skipped int
	for _, path := range files {
		n, studioURL, err := importFile(db, path, replace, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", path, err)
			skipped++
			continue
		}
		if prev, dup := seenStudios[studioURL]; dup {
			fmt.Fprintf(os.Stderr,
				"warning: %s and %s both contain %s — the later file wins field by field; "+
					"files are processed oldest-first by modification time\n",
				filepath.Base(prev), filepath.Base(path), studioURL)
		}
		seenStudios[studioURL] = path
		imported += n
	}

	verb := "imported"
	if dryRun {
		verb = "would import"
	}
	fmt.Printf("Done: %s %d scene(s) from %d file(s)", verb, imported, len(files)-skipped)
	if skipped > 0 {
		fmt.Printf(", %d file(s) skipped", skipped)
	}
	fmt.Println()
	if skipped > 0 {
		return &exitError{code: ExitFailure, err: fmt.Errorf("%d file(s) could not be imported", skipped), reported: true}
	}
	return nil
}

// collectJSONFiles expands each argument: a directory contributes its *.json
// entries (not recursive), a file contributes itself.
//
// The result is ordered oldest-first by modification time. Merging is
// last-write-wins per field, so processing order decides which version of a
// re-scraped studio survives — and sorting by name made that depend on the
// filenames. A browser saving a second download as `studio (1).json` sorts it
// *before* `studio.json`, so the newer file would be overwritten by the older
// one. Modification time is the only ordering here that reflects data recency.
//
// Ties break on path so the order is deterministic.
func collectJSONFiles(args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", arg, err)
		}
		if !info.IsDir() {
			if !seen[arg] {
				seen[arg] = true
				out = append(out, arg)
			}
			continue
		}
		matches, err := filepath.Glob(filepath.Join(arg, "*.json"))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	mtimes := make(map[string]time.Time, len(out))
	for _, p := range out {
		if info, err := os.Stat(p); err == nil {
			mtimes[p] = info.ModTime()
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := mtimes[out[i]], mtimes[out[j]]
		if a.Equal(b) {
			return out[i] < out[j]
		}
		return a.Before(b)
	})
	return out, nil
}

// importFile loads one studio file into db and returns how many scenes the
// studio ends up with and which studio URL it belongs to.
//
// db may be nil on a dry run when the database does not exist yet; the file is
// then reported as-is, since there is nothing stored to merge against.
func importFile(db *store.SQLite, path string, replace, dryRun bool) (int, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", err
	}
	var sf models.StudioFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return 0, "", fmt.Errorf("parsing: %w", err)
	}
	if sf.SchemaVersion > models.StoreSchemaVersion {
		return 0, "", fmt.Errorf("store schema v%d is newer than this build's v%d — upgrade fss",
			sf.SchemaVersion, models.StoreSchemaVersion)
	}
	// The studio URL comes from the file, never the filename: Slugify is lossy
	// and its hash suffix is not reversible.
	if sf.StudioURL == "" {
		return 0, "", fmt.Errorf("no studioUrl in file — cannot tell which studio these scenes belong to")
	}
	if len(sf.Scenes) == 0 {
		return 0, "", nil
	}

	// db is nil only on a dry run against a database that does not exist yet:
	// there is nothing to lock and nothing stored to merge against.
	var existing []models.Scene
	if db != nil {
		unlock, err := db.Lock(sf.StudioURL)
		if err != nil {
			return 0, "", fmt.Errorf("locking studio: %w", err)
		}
		defer func() { _ = unlock.Close() }()

		existing, err = db.Load(sf.StudioURL)
		if err != nil {
			return 0, "", fmt.Errorf("loading stored scenes: %w", err)
		}
	}
	existingByKey := make(map[sceneKey]models.Scene, len(existing))
	for _, s := range existing {
		existingByKey[keyOf(s)] = s
	}

	incoming := make([]models.Scene, 0, len(sf.Scenes))
	seen := make(map[sceneKey]bool, len(sf.Scenes))
	for _, s := range sf.Scenes {
		s.StudioURL = sf.StudioURL
		k := keyOf(s)
		if seen[k] {
			continue
		}
		seen[k] = true
		if prev, ok := existingByKey[k]; ok {
			s = importCarryOver(s, prev)
		}
		incoming = append(incoming, s)
	}
	if !replace {
		for _, s := range existing {
			if !seen[keyOf(s)] {
				incoming = append(incoming, s)
			}
		}
	}

	scraper.Debugf(1, "import: %s → %s (%d in file, %d stored, %d after merge)",
		path, sf.StudioURL, len(sf.Scenes), len(existing), len(incoming))

	if dryRun {
		fmt.Printf("  %-50s  →  %s (%d scene(s))\n", truncate(filepath.Base(path), 50), sf.StudioURL, len(incoming))
		return len(incoming), sf.StudioURL, nil
	}

	if err := db.Save(sf.StudioURL, incoming); err != nil {
		return 0, "", fmt.Errorf("saving: %w", err)
	}
	if err := db.UpsertStudio(studioFromFile(sf, incoming)); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s: could not update studio record: %v\n", path, err)
	}
	fmt.Printf("  %-50s  →  %s (%d scene(s))\n", truncate(filepath.Base(path), 50), sf.StudioURL, len(incoming))
	return len(incoming), sf.StudioURL, nil
}

// studioFromFile derives the studios-table row the JSON does not carry: site ID
// and display name come from the scenes, AddedAt from the earliest first-seen
// timestamp available.
func studioFromFile(sf models.StudioFile, scenes []models.Scene) models.Studio {
	st := models.Studio{URL: sf.StudioURL}
	for _, s := range scenes {
		if st.SiteID == "" {
			st.SiteID = s.SiteID
		}
		if st.Name == "" {
			st.Name = s.Studio
		}
		if t := s.FirstSeenAt; !t.IsZero() && (st.AddedAt.IsZero() || t.Before(st.AddedAt)) {
			st.AddedAt = t
		}
	}
	if st.AddedAt.IsZero() {
		st.AddedAt = sf.ScrapedAt
	}
	if !sf.ScrapedAt.IsZero() {
		last := sf.ScrapedAt
		st.LastScrapedAt = &last
	}
	return st
}

// importCarryOver is carryOver for a file's scene against its stored twin. The
// file holds a whole history that usually overlaps the stored one, so the two
// are merged as sets rather than replayed one after the other, which would
// duplicate every snapshot on each re-import.
func importCarryOver(fresh, existing models.Scene) models.Scene {
	fresh = preserveEnrichment(fresh, existing)
	return mergePriceHistories(fresh, existing)
}

func mergePriceHistories(fresh, existing models.Scene) models.Scene {
	type key struct {
		date time.Time
		snap models.PriceSnapshot
	}
	keyOf := func(p models.PriceSnapshot) key {
		d := p.Date.UTC().Truncate(time.Second)
		p.Date = time.Time{}
		return key{d, p}
	}
	seen := map[key]bool{}
	var all []models.PriceSnapshot
	for _, list := range [][]models.PriceSnapshot{existing.PriceHistory, fresh.PriceHistory} {
		for _, p := range list {
			if k := keyOf(p); !seen[k] {
				seen[k] = true
				all = append(all, p)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Date.Before(all[j].Date) })

	fresh.PriceHistory = nil
	fresh.LowestPrice = 0
	fresh.LowestPriceDate = nil
	for _, p := range all {
		fresh.AddPrice(p)
	}
	return fresh
}

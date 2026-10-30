package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/output"
)

var exportCmd = &cobra.Command{
	Use:   "export [studio-url ...]",
	Short: "Write studio JSON/CSV files out of the SQLite database",
	Long: `Export studios from the SQLite store as JSON and/or CSV files, one per studio,
named the same way the flat store names them.

With no arguments every tracked studio is exported.`,
	RunE: runExport,
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.Flags().String("db", "", "path to SQLite database (no value = the configured db, or the default location)")
	exportCmd.Flags().Lookup("db").NoOptDefVal = "default"
	exportCmd.Flags().StringP("output", "o", "", "export formats: json, csv, or json,csv (default from config)")
	exportCmd.Flags().Bool("force", false, "overwrite an existing studio file that holds more scenes than the export")
	exportCmd.Flags().String("out-dir", "", "output directory (default from config)")
}

func runExport(cmd *cobra.Command, args []string) error {
	dbPath := resolveDBPath(cmd)
	if dbPath == "" {
		return fmt.Errorf("--db is required (pass --db for the default location, or --db /path/to/file.db)")
	}

	outputFlag, _ := cmd.Flags().GetString("output")
	outputStr := outputFlag
	if outputStr == "" && cfg != nil {
		outputStr = cfg.Output
	}
	formats, err := parseFormats(outputStr)
	if err != nil {
		return err
	}
	if len(formats) == 0 {
		formats = []string{"json"}
	}

	force, _ := cmd.Flags().GetBool("force")
	outDir, _ := cmd.Flags().GetString("out-dir")
	if outDir == "" && cfg != nil {
		outDir = cfg.OutDir
	}
	if outDir == "" {
		outDir = "."
	}

	db, err := store.NewSQLite(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = db.Close() }()

	urls := args
	if len(urls) == 0 {
		studios, err := db.ListStudios()
		if err != nil {
			return err
		}
		for _, s := range studios {
			urls = append(urls, s.URL)
		}
		if len(urls) == 0 {
			return fmt.Errorf("no studios tracked in %s — pass a studio URL explicitly, or scrape one with --db first", dbPath)
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	known := map[string]bool{}
	if studios, err := db.ListStudios(); err == nil {
		for _, s := range studios {
			known[output.CanonicalStudioURL(s.URL)] = true
		}
	}
	flat := store.NewFlat(outDir, nil)

	written := 0
	var firstErr error
	for _, studioURL := range urls {
		if len(args) > 0 {
			studioURL = normalizeInputURL(studioURL)
		}
		n, err := exportStudio(db, flat, studioURL, outDir, formats, known, force, true)
		written += n
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	fmt.Printf("Done: %d file(s) written to %s\n", written, outDir)
	return firstErr
}

var errExportRefused = errors.New("export refused")

type exportSceneKey struct{ id, site string }

func exportStudio(src store.Store, flat *store.Flat, studioURL, outDir string, formats []string, known map[string]bool, force, lockFlat bool) (int, error) {
	canon := output.CanonicalStudioURL(studioURL)
	scenes, err := src.Load(canon)
	if err != nil {
		return 0, fmt.Errorf("loading %s: %w", studioURL, err)
	}
	if known != nil && len(scenes) == 0 && !known[canon] {
		return 0, fmt.Errorf("%s is not in the database; nothing exported (scrape it with --db first, or `fss import` its JSON)", studioURL)
	}

	if lockFlat {
		lock, err := flat.Lock(canon)
		if err != nil {
			return 0, err
		}
		defer func() { _ = lock.Close() }()
	}

	slug := output.Slugify(canon)
	if !force && slices.Contains(formats, "json") {
		if err := checkFlatFile(flat, canon, scenes, filepath.Join(outDir, slug+".json")); err != nil {
			return 0, fmt.Errorf("%w: %w (use --force to overwrite)", errExportRefused, err)
		}
	}

	written := 0
	var errs []error
	for _, format := range formats {
		path := filepath.Join(outDir, slug+"."+format)
		if err := src.Export(format, path, canon); err != nil {
			errs = append(errs, fmt.Errorf("exporting %s as %s: %w", studioURL, format, err))
			continue
		}
		fmt.Printf("  %s\n", path)
		written++
	}
	return written, errors.Join(errs...)
}

func checkFlatFile(flat *store.Flat, canon string, scenes []models.Scene, path string) error {
	existing, err := flat.Load(canon)
	if err != nil {
		return fmt.Errorf("cannot read the existing studio file for %s: %w", canon, err)
	}
	have := make(map[exportSceneKey]bool, len(scenes))
	for _, sc := range scenes {
		have[exportSceneKey{sc.ID, sc.SiteID}] = true
	}
	missing := 0
	for _, sc := range existing {
		if !have[exportSceneKey{sc.ID, sc.SiteID}] {
			missing++
		}
	}
	if missing > 0 {
		return fmt.Errorf("%s holds %d scene(s) the database lacks", path, missing)
	}
	return nil
}

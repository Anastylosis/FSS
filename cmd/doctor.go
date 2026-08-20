package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/scraper"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check environment health: config, database, Stash, tools",
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().Bool("prune", false, "delete performer/tag/category rows no scene references any more")
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	w := cmd.OutOrStdout()
	allOK := true

	check := func(name string, fn func() (string, bool)) {
		detail, ok := fn()
		status := "ok"
		if !ok {
			status = "FAIL"
			allOK = false
		}
		if detail != "" {
			_, _ = fmt.Fprintf(w, "  %-22s %s  (%s)\n", name, status, detail)
		} else {
			_, _ = fmt.Fprintf(w, "  %-22s %s\n", name, status)
		}
	}

	_, _ = fmt.Fprintln(w, "Config:")
	check("config file", func() (string, bool) {
		path := config.DefaultPath()
		if _, err := os.Stat(path); err != nil {
			return path + " — not found", false
		}
		return path, true
	})
	check("scrapers registered", func() (string, bool) {
		all := scraper.All()
		return fmt.Sprintf("%d", len(all)), len(all) > 0
	})

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Database:")
	check("SQLite", func() (string, bool) {
		dbPath := ""
		if cfg != nil {
			dbPath, _ = cfg.DBSetting()
		}
		if dbPath == "" {
			return "disabled (flat JSON mode)", true
		}
		if dbPath == "default" {
			dbPath = config.DefaultDBPath()
		}
		s, err := store.NewSQLite(dbPath)
		if err != nil {
			return dbPath + " — " + err.Error(), false
		}
		_ = s.Close()
		return dbPath, true
	})
	check("active store", func() (string, bool) {
		// What `fss scrape` writes to. Distinct from the "SQLite" check above,
		// which only says whether a configured database is reachable — a
		// database can be present and healthy while the flat store is still
		// the one being written.
		path, set := "", false
		if cfg != nil {
			path, set = cfg.DBSetting()
		}
		switch {
		case !set:
			return "flat JSON (no `db:` configured)", true
		case path == "":
			return "flat JSON (`db: \"\"` — set deliberately)", true
		default:
			return "SQLite", true
		}
	})
	check("store contents", func() (string, bool) {
		db, _, err := openConfiguredDB()
		if err != nil || db == nil {
			// Nothing to compare against. Not a failure: the flat store on its
			// own is still the supported default.
			return "no database to compare against", true
		}
		defer func() { _ = db.Close() }()

		dir := "."
		if cfg != nil && cfg.OutDir != "" {
			dir = cfg.OutDir
		}
		flat, broken, err := flatInventory(dir)
		if err != nil {
			return err.Error(), false
		}
		inDB, err := dbInventory(db)
		if err != nil {
			return err.Error(), false
		}

		d := diffStores(flat, inDB)
		var b strings.Builder
		fmt.Fprintf(&b, "%d studio(s) in JSON, %d in the database", len(flat), len(inDB))
		if len(broken) > 0 {
			fmt.Fprintf(&b, "\n    unreadable JSON (%d): %s", len(broken), strings.Join(broken, ", "))
		}
		if d.inSync() && len(d.OnlyInDB) == 0 {
			b.WriteString(" — in sync")
			return b.String(), len(broken) == 0
		}
		d.summarize(&b, 10)
		// Only-in-JSON is the one that costs data visibility after the switch,
		// so it is the only state that fails the check.
		return b.String(), len(d.OnlyInFlat) == 0 && len(broken) == 0
	})
	check("vocabulary", func() (string, bool) {
		db, path, err := openConfiguredDB()
		if err != nil || db == nil {
			return "no database", true
		}
		defer func() { _ = db.Close() }()
		_ = path

		prune, _ := cmd.Flags().GetBool("prune")
		counts, err := db.UnreferencedVocabulary()
		if err != nil {
			return err.Error(), false
		}
		total := 0
		for _, n := range counts {
			total += n
		}
		if total == 0 {
			return "no unreferenced rows", true
		}
		if !prune {
			// Reported, not fixed: deleting rows is the operator's call, and
			// a count on its own is often the first sign of a parser that
			// briefly wrote mangled names.
			return fmt.Sprintf("%s unreferenced (run `fss doctor --prune` to remove)", describeCounts(counts)), true
		}
		removed, err := db.PruneVocabulary()
		if err != nil {
			return err.Error(), false
		}
		return "pruned " + describeCounts(removed), true
	})
	check("output directory", func() (string, bool) {
		dir := "."
		if cfg != nil && cfg.OutDir != "" {
			dir = cfg.OutDir
		}
		info, err := os.Stat(dir)
		if err != nil {
			return dir + " — not found", false
		}
		if !info.IsDir() {
			return dir + " — not a directory", false
		}
		f, err := os.CreateTemp(dir, ".fss-doctor-*")
		if err != nil {
			return dir + " — not writable", false
		}
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return dir, true
	})

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Stash:")
	check("stash connection", func() (string, bool) {
		u := stashURL(cmd)
		if u == "" {
			return "not configured", true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c := newStashClient(cmd)
		if err := c.Ping(ctx); err != nil {
			return u + " — not reachable", false
		}
		return u, true
	})

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Tools:")
	check("ffprobe", func() (string, bool) {
		path, err := exec.LookPath("ffprobe")
		if err != nil {
			return "not found (optional, for fss identify)", true
		}
		return path, true
	})

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Network:")
	check("HTTP egress", func() (string, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := httpx.Do(ctx, client, httpx.Request{
			URL:         "https://api.github.com/zen",
			MaxAttempts: 1,
		})
		if err != nil {
			return err.Error(), false
		}
		_ = resp.Body.Close()
		return "github.com reachable", true
	})

	_, _ = fmt.Fprintln(w)
	if allOK {
		_, _ = fmt.Fprintln(w, "All checks passed.")
	} else {
		_, _ = fmt.Fprintln(w, "Some checks failed — see above.")
	}
	return nil
}

// openConfiguredDB opens the database named in the config, or returns nil when
// none is configured. The caller must Close a non-nil result.
func openConfiguredDB() (*store.SQLite, string, error) {
	path := config.ResolveDBPath(configuredDB())
	if path == "" {
		return nil, "", nil
	}
	db, err := store.NewSQLite(path)
	if err != nil {
		return nil, path, err
	}
	return db, path, nil
}

// describeCounts renders a per-table breakdown in a stable order.
func describeCounts(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for _, name := range []string{"performers", "tags", "categories"} {
		if n := counts[name]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, name))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

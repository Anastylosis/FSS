package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/store"
)

var listStudiosCmd = &cobra.Command{
	Use:   "list-studios",
	Short: "List all studios tracked in the SQLite database",
	RunE:  runListStudios,
}

func init() {
	rootCmd.AddCommand(listStudiosCmd)
	listStudiosCmd.Flags().String("db", "", "path to SQLite database (no value = default location)")
	listStudiosCmd.Flags().Lookup("db").NoOptDefVal = "default"
}

func runListStudios(cmd *cobra.Command, _ []string) error {
	path := resolveDBPath(cmd)
	if path == "" {
		return fmt.Errorf("--db is required (pass --db for the default location, or --db /path/to/file.db)")
	}
	db, err := store.NewSQLite(path)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer func() { _ = db.Close() }()

	studios, err := db.ListStudios()
	if err != nil {
		return err
	}
	if len(studios) == 0 {
		fmt.Println("No studios tracked yet. Scrape a studio with --db to add it.")
		return nil
	}
	for _, s := range studios {
		name := s.Name
		if name == "" {
			name = "(no name)"
		}
		last := "never"
		if s.LastScrapedAt != nil {
			last = s.LastScrapedAt.Format("2006-01-02")
		}
		fmt.Printf("%-30s  %-12s  last scraped: %s\n  %s\n", name, s.SiteID, last, s.URL)
	}
	return nil
}

package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
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
	addFormatFlag(listStudiosCmd)
}

type studioInfo struct {
	URL           string     `json:"url"`
	SiteID        string     `json:"siteId"`
	Name          string     `json:"name,omitempty"`
	LastScrapedAt *time.Time `json:"lastScrapedAt"`
}

func runListStudios(cmd *cobra.Command, _ []string) error {
	studios, err := loadStudioList(cmd)
	if jsonMode(cmd) {
		out := make([]studioInfo, 0, len(studios))
		for _, s := range studios {
			out = append(out, studioInfo{URL: s.URL, SiteID: s.SiteID, Name: s.Name, LastScrapedAt: s.LastScrapedAt})
		}
		if err != nil {
			return emitJSON(cmd, nil, err)
		}
		return emitJSON(cmd, map[string]any{"count": len(out), "studios": out}, nil)
	}
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

func loadStudioList(cmd *cobra.Command) ([]models.Studio, error) {
	path := resolveDBPath(cmd)
	if path == "" {
		return nil, withCode(codeInvalidArgs, fmt.Errorf("--db is required (pass --db for the default location, or --db /path/to/file.db)"))
	}
	db, err := store.NewSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	defer func() { _ = db.Close() }()
	return db.ListStudios()
}

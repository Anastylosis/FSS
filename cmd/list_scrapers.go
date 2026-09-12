package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/scraper"
)

var listScrapersMarkdown bool

var listScrapersCmd = &cobra.Command{
	Use:   "list-scrapers",
	Short: "List all registered scrapers and their URL patterns",
	RunE:  runListScrapers,
}

func init() {
	listScrapersCmd.Flags().BoolVar(&listScrapersMarkdown, "markdown", false, "output a markdown table")
	rootCmd.AddCommand(listScrapersCmd)
}

func runListScrapers(_ *cobra.Command, _ []string) error {
	all := scraper.All()
	if len(all) == 0 {
		fmt.Println("No scrapers registered.")
		return nil
	}

	if listScrapersMarkdown {
		fmt.Printf("# Supported Sites (%d)\n\n", len(all))
		fmt.Println("| # | ID | URL Patterns |")
		fmt.Println("|--:|----|-------------|")
		for i, s := range all {
			patterns := strings.Join(s.Patterns(), ", ")
			fmt.Printf("| %d | %s | %s |\n", i+1, s.ID(), patterns)
		}
		return nil
	}

	for _, s := range all {
		fmt.Printf("%s:\n", s.ID())
		for _, p := range s.Patterns() {
			fmt.Printf("  %s\n", p)
		}
		if langs := scraper.LanguagesFor(s); len(langs) > 0 {
			fmt.Printf("  languages: %s (default %s)\n", strings.Join(langs, ", "), langs[0])
		}
	}
	return nil
}

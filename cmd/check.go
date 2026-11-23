// Package cmd implements the fss command-line interface: the cobra command
// tree, flag wiring, and config resolution. It is the CLI entry point, not a
// library surface — external consumers should import the public packages
// listed in docs/library.md instead.
package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/scraper"
)

var checkCmd = &cobra.Command{
	Use:   "check <url>",
	Short: "Check whether a URL is supported and show the matching scraper",
	Args:  cobra.ExactArgs(1),
	RunE:  runCheck,
}

func init() {
	addFormatFlag(checkCmd)
	rootCmd.AddCommand(checkCmd)
}

type checkResult struct {
	URL               string   `json:"url"`
	Supported         bool     `json:"supported"`
	Scraper           string   `json:"scraper,omitempty"`
	Patterns          []string `json:"patterns,omitempty"`
	RequestSupportURL string   `json:"requestSupportUrl,omitempty"`
}

func newScraperIssueURL(siteURL string) string {
	q := url.Values{}
	q.Set("template", "new_scraper.yml")
	q.Set("url", siteURL)
	return "https://github.com/Anastylosis/FSS/issues/new?" + q.Encode()
}

func runCheck(cmd *cobra.Command, args []string) error {
	rawURL := normalizeInputURL(args[0])
	w := cmd.OutOrStdout()

	s, err := scraper.ForURL(rawURL)
	if jsonMode(cmd) {
		res := checkResult{URL: rawURL, Supported: err == nil}
		if err == nil {
			res.Scraper, res.Patterns = s.ID(), s.Patterns()
		} else {
			res.RequestSupportURL = newScraperIssueURL(rawURL)
		}
		return emitJSON(cmd, res, nil)
	}
	if err != nil {
		_, _ = fmt.Fprintf(w, "Not supported: %s\n", rawURL)
		_, _ = fmt.Fprintf(w, "\nRequest support: %s\n", newScraperIssueURL(rawURL))
		return nil
	}

	_, _ = fmt.Fprintf(w, "Scraper:  %s\n", s.ID())
	_, _ = fmt.Fprintf(w, "Patterns: %s\n", strings.Join(s.Patterns(), ", "))
	return nil
}

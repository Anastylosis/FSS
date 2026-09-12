package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/i18n"
	"github.com/Anastylosis/FSS/internal/i18n/cobrai18n"
	"github.com/Anastylosis/FSS/scraper"
)

var cfg *config.Config

var rootCmd = &cobra.Command{
	Use:   "fss",
	Short: "FullStudioScraper — scrape all scenes and metadata from a studio URL",
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		verbose, _ := cmd.Flags().GetCount("debug")
		scraper.SetVerbose(verbose)

		switch cmd.Name() {
		case "version", "list-scrapers", "completion", "init", "path":
			return nil
		}
		var err error
		cfg, err = config.Load()
		if err != nil {
			return err
		}
		httpx.SetDefaultUA(cfg.UserAgent)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().CountP("debug", "d", "increase debug verbosity (repeat for more: -d, -dd, -ddd)")
	// Value is never read via GetString — see the langFromArgs pre-scan in i18n.go.
	rootCmd.PersistentFlags().String("lang", "", "help language code, e.g. ko (see docs/translations.md)")
}

var buildVersion, buildCommit, buildDate, buildChannel string

// SetVersion is called from main with values injected by -ldflags at build time.
// channel names how the binary was shipped (see versionLine); it is empty for a
// build that did not say.
func SetVersion(version, commit, date, channel string) {
	buildVersion = version
	buildCommit = commit
	buildDate = date
	buildChannel = channel
	rootCmd.Version = versionLine(version, commit, date, channel)
}

// Execute runs the root command and exits non-zero on failure.
func Execute() {
	tag, explicit := resolveLanguage()
	resolved := i18n.SetLanguage(tag)
	if explicit && resolved == i18n.SourceLanguage && i18n.Normalize(tag) != i18n.SourceLanguage {
		fmt.Fprintf(os.Stderr, "unknown language %q; using English (available: %s)\n",
			tag, strings.Join(i18n.Available(), ", "))
	}
	cobrai18n.Localize(rootCmd) // restore func deliberately dropped: one Execute per process
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

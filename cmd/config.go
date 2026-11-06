package cmd

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage FSS configuration",
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default config file",
	RunE:  runConfigInit,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	Run: func(cmd *cobra.Command, _ []string) {
		active, _ := xdg.SearchConfigFile("fss/config.yaml")
		for _, p := range config.SearchPaths() {
			if p == active {
				p += "  (in use)"
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), p)
		}
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configPathCmd)
}

//go:embed config_default.yaml
var defaultConfig string

func runConfigInit(cmd *cobra.Command, _ []string) error {
	path := config.DefaultPath()
	w := cmd.OutOrStdout()

	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists at %s", path)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(defaultConfig), 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	_, _ = fmt.Fprintf(w, "Created %s\n", path)
	return nil
}

package cmd

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
)

func isolateXDG(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_CONFIG_DIRS", dir)
	old, oldDirs := xdg.ConfigHome, xdg.ConfigDirs
	xdg.Reload()
	t.Cleanup(func() {
		xdg.ConfigHome, xdg.ConfigDirs = old, oldDirs
	})
}

func initConfig(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&buf)
	if err := runConfigInit(c, nil); err != nil {
		t.Fatalf("config init: %v", err)
	}
	return buf.String()
}

// A second `config init` must not overwrite the config the operator has since
// edited — the default template would silently replace their API keys.
func TestConfigInitRefusesToOverwrite(t *testing.T) {
	isolateXDG(t)
	initConfig(t)

	path := config.DefaultPath()
	edited := "workers: 9\ndb: \"/tmp/mine.db\"\n"
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runConfigInit(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("second config init succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want it to say the config already exists", err)
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != edited {
		t.Errorf("config was overwritten:\n%s", after)
	}
}

// The file `fss config init` writes is what every new user starts from, so it
// has to survive the loader that reads it back.
func TestConfigInitProducesALoadableConfig(t *testing.T) {
	isolateXDG(t)

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(nil) })

	initConfig(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading the config we just shipped: %v", err)
	}

	// A key the shipped file still sets but Config no longer knows about is
	// only reported through this warning — Load itself ignores it.
	if strings.Contains(logs.String(), "unknown settings") {
		t.Errorf("shipped config has keys FSS no longer understands: %s", logs.String())
	}

	if cfg.Workers != 0 {
		t.Errorf("Workers = %d, want 0", cfg.Workers)
	}
	if cfg.Output != "json" {
		t.Errorf("Output = %q, want json", cfg.Output)
	}
	if cfg.Delay != 500 {
		t.Errorf("Delay = %d, want 500", cfg.Delay)
	}
	if cfg.OutDir != "." {
		t.Errorf("OutDir = %q, want .", cfg.OutDir)
	}
	if cfg.Stash.URL != "http://localhost:9999" {
		t.Errorf("Stash.URL = %q", cfg.Stash.URL)
	}
	if cfg.Stash.Tag != "fss_import" {
		t.Errorf("Stash.Tag = %q, want fss_import", cfg.Stash.Tag)
	}

	// `db: ""` in the shipped file must read back as set-but-empty, or the
	// documented opt-out stops working the moment the default flips.
	value, set := cfg.DBSetting()
	if !set || value != "" {
		t.Errorf("DBSetting() = (%q, %v), want (\"\", true)", value, set)
	}
}

func TestSkipsConfigByPath(t *testing.T) {
	for path, want := range map[string]bool{
		"fss version": true, "fss list-scrapers": true, "fss config init": true,
		"fss config path": true, "fss completion bash": true,
		"fss creators init": false, "fss scrape": false, "fss stash path": false,
	} {
		root := &cobra.Command{Use: "fss"}
		cur := root
		for _, name := range strings.Fields(path)[1:] {
			next := &cobra.Command{Use: name}
			cur.AddCommand(next)
			cur = next
		}
		if got := skipsConfig(cur); got != want {
			t.Errorf("skipsConfig(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestCheckSiteIDs(t *testing.T) {
	mk := func(args ...string) *cobra.Command {
		c := &cobra.Command{Use: "scrape"}
		c.Flags().StringSlice("site-delay", nil, "")
		c.Flags().StringSlice("site-cookie", nil, "")
		c.Flags().StringSlice("site-language", nil, "")
		if err := c.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		return c
	}
	empty := &config.Config{}
	if err := checkSiteIDs(empty, mk("--site-delay=pornhub=100")); err != nil {
		t.Errorf("known id rejected: %v", err)
	}
	if err := checkSiteIDs(empty, mk("--site-delay=nopesuchsite=100")); err == nil {
		t.Error("unknown --site-delay id accepted")
	}
	if err := checkSiteIDs(empty, mk("--site-language=nope=en")); err == nil {
		t.Error("unknown --site-language id accepted")
	}
}

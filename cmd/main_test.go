package cmd

import (
	"fmt"
	"os"
	"testing"

	"github.com/adrg/xdg"
)

// TestMain points every XDG directory at a scratch dir. Tests that drive
// rootCmd load config the way a real run does, and with the operator's own
// config that meant writing into their real database.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fss-cmd-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		_ = os.Setenv(k, dir+"/"+k)
	}
	_ = os.Unsetenv("XDG_CONFIG_DIRS")
	_ = os.Unsetenv("FSS_LANG")
	xdg.Reload()
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

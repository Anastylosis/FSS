package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shared fuzz job passes green when a named target no longer exists, so
// the workflow's list is checked against the source here.
func TestCIFuzzTargetsExist(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Skipf("workflow not available: %v", err)
	}
	_, block, ok := strings.Cut(string(data), "fuzz-targets: |")
	if !ok {
		t.Fatal("no fuzz-targets block in ci.yml")
	}
	n := 0
	for _, line := range strings.Split(block, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || !strings.HasPrefix(f[0], "./") {
			break
		}
		n++
		dir := filepath.Join("..", strings.TrimSuffix(f[0], "/..."))
		re := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(f[1]) + `\(`)
		found := false
		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err == nil && re.Match(b) {
				found = true
			}
		}
		if !found {
			t.Errorf("fuzz target %s not found in %s", f[1], f[0])
		}
	}
	if n == 0 {
		t.Error("parsed no fuzz targets from ci.yml")
	}
}

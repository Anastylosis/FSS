//go:build !windows

package selfupdate

import "os"

// replace swaps the new binary in. POSIX rename over a running executable is
// atomic and the running process keeps its open inode, so nothing breaks
// mid-run.
func replace(newPath, targetPath string) error {
	return os.Rename(newPath, targetPath)
}

// sweepLeftovers is a no-op outside Windows: nothing is left behind.
func sweepLeftovers(string) {}

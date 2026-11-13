//go:build !windows

package selfupdate

import (
	"os"
	"syscall"
)

// replace swaps the new binary in. POSIX rename over a running executable is
// atomic and the running process keeps its open inode, so nothing breaks
// mid-run.
func replace(newPath, targetPath string) error {
	return os.Rename(newPath, targetPath)
}

// sweepLeftovers removes temp files an interrupted update orphaned.
func sweepLeftovers(targetPath string) { sweepStaleTemps(targetPath) }

// preserveOwner gives the new file the original's owner where permitted; as an
// unprivileged user chown to another owner fails, which is ignored.
func preserveOwner(path string, info os.FileInfo) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(path, int(st.Uid), int(st.Gid))
	}
}

package selfupdate

import (
	"fmt"
	"os"
)

// replace swaps the new binary in on Windows, where a running .exe cannot be
// overwritten: the current one is moved aside first, which is allowed, and the
// leftover is deleted by the next run (see sweepLeftovers).
func replace(newPath, targetPath string) error {
	old := targetPath + ".old"
	_ = os.Remove(old)
	if err := os.Rename(targetPath, old); err != nil {
		return fmt.Errorf("moving the running binary aside: %w", err)
	}
	if err := os.Rename(newPath, targetPath); err != nil {
		// Put the old one back rather than leaving no fss at all.
		if restoreErr := os.Rename(old, targetPath); restoreErr != nil {
			return fmt.Errorf("installing %s failed (%w) and the previous binary could not be restored from %s: %w",
				targetPath, err, old, restoreErr)
		}
		return fmt.Errorf("installing the new binary: %w", err)
	}
	return nil
}

// sweepLeftovers removes the previous binary a past update moved aside. It can
// only be deleted once it is no longer the running process, so the next run
// does it.
func sweepLeftovers(targetPath string) {
	_ = os.Remove(targetPath + ".old")
}

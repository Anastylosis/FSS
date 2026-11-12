//go:build !windows

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func lockFile(path string) (*os.File, error) {
	return lockFileCtx(context.Background(), path, "")
}

// lockFileCtx takes the exclusive flock without ever blocking in the kernel:
// a blocking flock(2) ignores ctx, and Go restarts it after SIGINT, so Ctrl+C
// would do nothing while another process held the lock. It polls LOCK_NB and
// says so once the wait passes lockNoticeAfter.
func lockFileCtx(ctx context.Context, path, label string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	start := time.Now()
	noticed := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			return nil, fmt.Errorf("acquiring lock %s: %w", path, err)
		}
		if !noticed && time.Since(start) >= lockNoticeAfter {
			noticed = true
			announceLockWait(label, path)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for lock %s: %w", path, ctx.Err())
		case <-time.After(lockPollInterval):
		}
	}
}

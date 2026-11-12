package store

import (
	"fmt"
	"io"
	"os"
	"time"
)

var (
	lockNoticeAfter            = time.Second
	lockPollInterval           = 100 * time.Millisecond
	lockNoticeOut    io.Writer = os.Stderr
)

func announceLockWait(label, path string) {
	if label == "" {
		label = path
	}
	_, _ = fmt.Fprintf(lockNoticeOut, "waiting for lock on %s (held by another fss)\n", label)
}

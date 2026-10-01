package cmd

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// noticesSuppressed reports whether the operator has opted out of advisory
// notices — the `[notice]` lines that explain a situation rather than report a
// result. Cron jobs and scripts want quiet output.
//
// Advisory means advisory: warnings and errors ignore this.
func noticesSuppressed() bool {
	if os.Getenv("FSS_NO_NOTICES") != "" {
		return true
	}
	return cfg != nil && cfg.Notices != nil && !*cfg.Notices
}

var pacingNoticeOnce sync.Once

// noticePacingIsRunWide tells an operator, once per process, that --delay now
// spaces the whole run rather than each worker.
//
// It used to be per worker, so `--delay 500 --workers 8` issued bursts of eight
// requests every 500ms — about sixteen a second, not two. Fixing that makes an
// unchanged invocation slower, and a scrape that quietly takes eight times
// longer is exactly the kind of surprise a release note exists to prevent.
// Only the combination that changed behaviour says anything.
func noticePacingIsRunWide(delay time.Duration, workers int) {
	if delay <= 0 || workers <= 1 || noticesSuppressed() {
		return
	}
	pacingNoticeOnce.Do(func() {
		fmt.Printf("[notice] --delay %v now spaces the whole run, not each of the %d workers: "+
			"about %.1f requests a second in total. It used to be per worker. "+
			"Lower --delay to go faster; --workers no longer changes the request rate.\n",
			delay, workers, float64(time.Second)/float64(delay))
	})
}

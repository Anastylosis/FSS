package cmd

import "os"

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

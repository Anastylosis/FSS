package cmd

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// --delay used to be honoured per worker, so an unchanged invocation is now
// slower; the notice is what keeps that from looking like a regression.
func TestNoticePacingIsRunWide(t *testing.T) {
	cases := []struct {
		name    string
		delay   time.Duration
		workers int
		want    bool
	}{
		{"delay and a pool", 500 * time.Millisecond, 8, true},
		{"one worker is unchanged", 500 * time.Millisecond, 1, false},
		{"no delay is unchanged", 0, 8, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pacingNoticeOnce = sync.Once{}
			out := captureStdout(t, func() { noticePacingIsRunWide(c.delay, c.workers) })
			if got := strings.Contains(out, "[notice]"); got != c.want {
				t.Errorf("notice printed = %v, want %v (output %q)", got, c.want, out)
			}
		})
	}
}

// Once per process: a run over fifty studios must not print it fifty times.
func TestNoticePacingIsRunWideSaysItOnce(t *testing.T) {
	pacingNoticeOnce = sync.Once{}
	out := captureStdout(t, func() {
		for i := 0; i < 3; i++ {
			noticePacingIsRunWide(500*time.Millisecond, 8)
		}
	})
	if n := strings.Count(out, "[notice]"); n != 1 {
		t.Errorf("printed %d notices, want 1", n)
	}
}

func TestNoticePacingIsRunWideRespectsSuppression(t *testing.T) {
	t.Setenv("FSS_NO_NOTICES", "1")
	pacingNoticeOnce = sync.Once{}
	out := captureStdout(t, func() { noticePacingIsRunWide(500*time.Millisecond, 8) })
	if out != "" {
		t.Errorf("a suppressed notice still printed %q", out)
	}
}

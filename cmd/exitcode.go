package cmd

import (
	"errors"
	"fmt"
)

// Process exit codes. Zero is success; a traversal that merely came back
// incomplete still exits 0 (the run saved what it found).
const (
	ExitFailure     = 1 // an error, or at least one scrape target failed
	ExitRefused     = 2 // a save was refused or declined; nothing was written
	ExitInterrupted = 3 // SIGINT/SIGTERM stopped the run
)

// exitError carries a specific exit code. reported means the cause was already
// printed, so Execute must not print it again.
type exitError struct {
	code     int
	err      error
	reported bool
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func newExitError(code int, format string, a ...any) *exitError {
	return &exitError{code: code, err: fmt.Errorf(format, a...)}
}

// exitCodeOf maps an error to a process exit code.
func exitCodeOf(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return ExitFailure
}

// exitPriority orders codes when several targets end differently: an
// interrupt outranks a failure, which outranks a refusal.
func exitPriority(code int) int {
	switch code {
	case ExitInterrupted:
		return 3
	case ExitFailure:
		return 2
	case ExitRefused:
		return 1
	}
	return 0
}

func isReported(err error) bool {
	var ee *exitError
	return errors.As(err, &ee) && ee.reported
}

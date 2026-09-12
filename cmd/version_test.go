package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// The v-prefix handling is the reason this is worth pinning: the tags carry one
// and buildVersion may or may not, so a comparison on the raw strings reports an
// update on every run for a user who is already current.
func TestUpdateLines(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    string // substring the first line must contain
		wantURL bool
	}{
		{"same version, both prefixed", "v1.2.3", "v1.2.3", "latest version", false},
		{"same version, current unprefixed", "1.2.3", "v1.2.3", "latest version", false},
		{"same version, tag unprefixed", "v1.2.3", "1.2.3", "latest version", false},
		{"newer release available", "v1.2.3", "v1.3.0", "Update available: v1.2.3 → v1.3.0", true},
		{"dev build", "dev", "v1.3.0", "running dev build", false},
		{"dev build, prefixed", "vdev", "v1.3.0", "running dev build", false},

		// Not "up to date" and not a bogus update — see updateLines.
		{"empty tag", "v1.2.3", "", "Could not determine", false},
		{"whitespace tag", "v1.2.3", "   ", "Could not determine", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := updateLines(c.current, c.latest, "")
			if len(got) == 0 {
				t.Fatal("no output lines")
			}
			if !strings.Contains(got[0], c.want) {
				t.Errorf("first line = %q, want it to contain %q", got[0], c.want)
			}
			hasURL := false
			for _, l := range got {
				if strings.Contains(l, "releases/latest") {
					hasURL = true
				}
			}
			if hasURL != c.wantURL {
				t.Errorf("release URL present = %v, want %v (lines: %q)", hasURL, c.wantURL, got)
			}
		})
	}
}

// A user who is up to date must not be told to go somewhere, and a user who is
// behind must be given the link. Stated separately from the table because it is
// the property that matters rather than the wording.
func TestUpdateLinesOnlyLinksWhenBehind(t *testing.T) {
	if lines := updateLines("v2.0.0", "v2.0.0", ""); len(lines) != 1 {
		t.Errorf("an up-to-date build printed %d lines, want 1: %q", len(lines), lines)
	}
	if lines := updateLines("v1.0.0", "v2.0.0", ""); len(lines) != 2 {
		t.Errorf("a stale build printed %d lines, want 2 (notice + link): %q", len(lines), lines)
	}
}

// The annotation is what the maintainer wanted said about the release that the
// commit list cannot say on its own, so it has to survive into the output — and
// the link has to stay last, after it rather than buried above it.
func TestUpdateLinesShowsAnnotation(t *testing.T) {
	lines := updateLines("v1.0.0", "v2.0.0", "maintenance only\n\nno scraper changes")
	joined := strings.Join(lines, "\n")

	for _, want := range []string{"maintenance only", "no scraper changes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q:\n%s", want, joined)
		}
	}
	if !strings.Contains(lines[0], "Update available") {
		t.Errorf("first line = %q, want the update notice", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "releases/latest") {
		t.Errorf("last line = %q, want the release link", lines[len(lines)-1])
	}
}

// A release with no annotation must look exactly as it did before the feature
// existed — no stray blank lines padding the notice.
func TestUpdateLinesWithoutAnnotationIsUnpadded(t *testing.T) {
	if lines := updateLines("v1.0.0", "v2.0.0", ""); len(lines) != 2 {
		t.Errorf("an un-annotated release printed %d lines, want 2: %q", len(lines), lines)
	}
	if lines := updateLines("dev", "v2.0.0", ""); len(lines) != 1 {
		t.Errorf("an un-annotated release printed %d lines on a dev build, want 1: %q", len(lines), lines)
	}
}

// Re-printing the annotation on every `fss version` for someone already running
// that release is noise, not news.
func TestUpdateLinesHidesAnnotationWhenCurrent(t *testing.T) {
	lines := updateLines("v2.0.0", "v2.0.0", "maintenance only")
	if joined := strings.Join(lines, "\n"); strings.Contains(joined, "maintenance only") {
		t.Errorf("annotation shown to a user already on that release:\n%s", joined)
	}
}

// The annotation is only reachable because the release payload is decoded with
// a `body` field — a rename or a dropped tag there would silently stop the
// callout from ever appearing, with every releaseNote unit test still green.
func TestLatestReleaseDecodesAnnotatedBody(t *testing.T) {
	payload := `{
		"tag_name": "v1.29.0",
		"body": "> **maintenance only, no new scrapers**\n>\n> Existing scrapers are unaffected.\n\n## Changes\n\n- fix a thing\n"
	}`

	var rel latestRelease
	if err := json.Unmarshal([]byte(payload), &rel); err != nil {
		t.Fatalf("decoding release payload: %v", err)
	}
	if rel.TagName != "v1.29.0" {
		t.Errorf("TagName = %q, want v1.29.0", rel.TagName)
	}

	lines := updateLines("v1.28.1", rel.TagName, releaseNote(rel.Body))
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"Update available: v1.28.1 → v1.29.0",
		"  maintenance only, no new scrapers",
		"  Existing scrapers are unaffected.",
		"https://github.com/Anastylosis/FSS/releases/latest",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "## Changes") || strings.Contains(joined, "fix a thing") {
		t.Errorf("commit list leaked into the annotation block:\n%s", joined)
	}
}

func TestReleaseNote(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "subject and body",
			body: "> **maintenance only**\n>\n> Existing scrapers are unaffected.\n\n## Changes\n\n- fix a thing\n",
			want: "maintenance only\n\nExisting scrapers are unaffected.",
		},
		{
			name: "subject alone",
			body: "> **maintenance only**\n\n## Changes\n",
			want: "maintenance only",
		},
		// A lightweight tag: release.yml emits an empty note, so the body
		// opens straight onto the generated notes.
		{"lightweight tag", "\n\n## Changes\n\n- fix a thing\n", ""},
		{"empty body", "", ""},

		// GitHub's generated notes quote commit bodies. Only the callout at
		// the very top is the annotation.
		{
			name: "quote further down is not the annotation",
			body: "## Changes\n\n- fix a thing\n\n> quoted from a commit message\n",
			want: "",
		},
		// CRLF is what the API returns in practice.
		{"crlf", "> **maintenance only**\r\n\r\n## Changes\r\n", "maintenance only"},

		// Trailing `>` lines would otherwise render as blank padding.
		{"trailing empty quote lines", "> **note**\n>\n>\n\n## Changes\n", "note"},

		// Printed verbatim to a terminal, so escape sequences must not survive.
		{"control characters stripped", "> **red\x1b[31m alert**\n", "red[31m alert"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := releaseNote(c.body); got != c.want {
				t.Errorf("releaseNote() = %q, want %q", got, c.want)
			}
		})
	}
}

// A build that stamps no channel must print exactly what it printed before the
// field existed — every release up to v1.31.0 is such a build.
func TestVersionLine(t *testing.T) {
	cases := []struct {
		name                           string
		version, commit, date, channel string
		want                           string
	}{
		{"unstamped", "v1.31.0", "abc1234", "2026-09-11", "", "v1.31.0 (abc1234, 2026-09-11)"},
		{"blank channel", "v1.31.0", "abc1234", "2026-09-11", "  ", "v1.31.0 (abc1234, 2026-09-11)"},
		{"stamped", "v1.31.0", "abc1234", "2026-09-11", "deb", "v1.31.0 (abc1234, 2026-09-11, deb)"},
		{"dev", "dev", "none", "unknown", "", "dev (none, unknown)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionLine(c.version, c.commit, c.date, c.channel); got != c.want {
				t.Errorf("versionLine = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSetVersionStampsTheChannel(t *testing.T) {
	prev := []string{buildVersion, buildCommit, buildDate, buildChannel, rootCmd.Version}
	t.Cleanup(func() {
		buildVersion, buildCommit, buildDate, buildChannel = prev[0], prev[1], prev[2], prev[3]
		rootCmd.Version = prev[4]
	})

	SetVersion("v1.31.0", "abc1234", "2026-09-11", "aur")
	if buildChannel != "aur" {
		t.Errorf("buildChannel = %q, want aur", buildChannel)
	}
	if rootCmd.Version != "v1.31.0 (abc1234, 2026-09-11, aur)" {
		t.Errorf("--version prints %q", rootCmd.Version)
	}
}

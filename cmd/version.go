package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/selfupdate"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version and check for updates",
	Args:  cobra.NoArgs,
	RunE:  runVersion,
	// A refusal here is an explanation, not a misuse of the command; the usage
	// block would bury it.
	SilenceUsage: true,
}

func init() {
	versionCmd.Flags().Bool("update", false, "download the latest release and replace this binary")
	rootCmd.AddCommand(versionCmd)
}

// versionLine renders the build identity. The channel is appended only when a
// build stamped one, so a build that did not prints exactly what it always did.
func versionLine(version, commit, date, channel string) string {
	if strings.TrimSpace(channel) == "" {
		return fmt.Sprintf("%s (%s, %s)", version, commit, date)
	}
	return fmt.Sprintf("%s (%s, %s, %s)", version, commit, date, channel)
}

func runVersion(cmd *cobra.Command, _ []string) error {
	fmt.Printf("fss %s\n", versionLine(buildVersion, buildCommit, buildDate, buildChannel))

	update, _ := cmd.Flags().GetBool("update")

	latest, err := fetchLatestRelease()
	if err != nil {
		if update {
			return fmt.Errorf("checking for updates: %w", err)
		}
		fmt.Printf("Could not check for updates: %v\n", err)
		return nil
	}

	for _, line := range updateLines(buildVersion, latest.TagName, releaseNote(latest.Body)) {
		fmt.Println(line)
	}
	if !update {
		return nil
	}
	return runSelfUpdate(cmd.Context(), latest)
}

// updateLines renders the update notice comparing the built-in version against
// the latest release tag.
//
// Split out from runVersion so the comparison is testable without reaching
// GitHub — the network call is best-effort and its own concern.
//
// An empty tag is treated as "could not determine": GitHub returning a body
// without tag_name (or any 200 that is not the expected shape) otherwise fell
// into the default branch and printed "Update available: v1.2.3 → " with nothing
// after the arrow, pointing the user at a release that was never identified.
//
// `note` is the release's tag annotation (see releaseNote), shown only when this
// build is not the release being described — a user already running it has read
// it once and does not need it on every `fss version`.
func updateLines(current, latest, note string) []string {
	if strings.TrimSpace(latest) == "" {
		return []string{"Could not determine the latest release."}
	}

	cur := strings.TrimPrefix(current, "v")
	remote := strings.TrimPrefix(latest, "v")

	switch cur {
	case "dev":
		lines := []string{fmt.Sprintf("Latest release: %s (running dev build)", latest)}
		return append(lines, noteLines(note)...)
	case remote:
		return []string{"You are running the latest version."}
	default:
		lines := []string{fmt.Sprintf("Update available: %s → %s", current, latest)}
		lines = append(lines, noteLines(note)...)
		return append(lines, "https://github.com/Anastylosis/FSS/releases/latest")
	}
}

// noteLines renders a tag annotation as an indented block set off by blank
// lines, or nothing at all when the release carried none.
func noteLines(note string) []string {
	if note == "" {
		return nil
	}
	out := []string{""}
	for _, l := range strings.Split(note, "\n") {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+l)
	}
	return append(out, "")
}

// releaseNote extracts the tag annotation from a GitHub release body.
//
// The release workflow renders an annotated tag's message as a Markdown
// blockquote at the very top of the body, ahead of `## Changes` (see the "Read
// tag annotation" step in .github/workflows/release.yml). Reading it back out of
// the body the update check already fetched keeps this to one request — going to
// the tag object instead would cost two more (refs/tags → git/tags) on a check
// that budgets 5s in total and is allowed to fail silently.
//
// Only a *leading* blockquote counts. GitHub's generated notes quote commit
// bodies further down the page, and those are not release notes.
//
// A lightweight tag produces no callout, so this returns "" and the caller
// prints nothing extra. That is also the graceful failure mode if the workflow
// ever stops emitting the blockquote: no annotation shown, no wrong output.
func releaseNote(body string) string {
	var quoted []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" && len(quoted) == 0 {
			continue // blank lines ahead of the callout
		}
		if !strings.HasPrefix(trimmed, ">") {
			break
		}
		quoted = append(quoted, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
	}
	if len(quoted) == 0 {
		return ""
	}

	// The workflow bolds the subject line; a terminal renders ** as literal
	// asterisks rather than emphasis.
	quoted[0] = strings.TrimSuffix(strings.TrimPrefix(quoted[0], "**"), "**")

	for len(quoted) > 0 && quoted[len(quoted)-1] == "" {
		quoted = quoted[:len(quoted)-1]
	}
	return sanitizeTerminal(strings.Join(quoted, "\n"))
}

// sanitizeTerminal drops control characters from text that arrived over the
// network before it is printed verbatim. The annotation is maintainer-written
// rather than hostile, but an escape sequence reaching the terminal would be
// interpreted instead of displayed, and stripping them costs nothing.
func sanitizeTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

// latestRelease is the subset of GitHub's release payload the update check
// reads. Body carries the rendered release notes, whose leading blockquote is
// the tag annotation — see releaseNote.
type latestRelease struct {
	TagName string             `json:"tag_name"`
	Body    string             `json:"body"`
	Assets  []selfupdate.Asset `json:"assets"`
}

func fetchLatestRelease() (latestRelease, error) {
	// Align ctx + client timeouts at 5s — the previous 1s ctx vs 5s
	// client mismatch meant ctx always tripped first, making the
	// transport-level deadline dead code.
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := httpx.NewClient(timeout)
	// Single attempt — `version` is a best-effort check and shouldn't
	// stall the user with 0s/2s/4s retry backoff if GitHub is having
	// a bad day.
	resp, err := httpx.Do(ctx, client, httpx.Request{
		URL:         "https://api.github.com/repos/Anastylosis/FSS/releases/latest",
		Headers:     map[string]string{"Accept": "application/vnd.github+json"},
		MaxAttempts: 1,
	})
	if err != nil {
		return latestRelease{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var release latestRelease
	if err := httpx.DecodeJSON(resp.Body, &release); err != nil {
		return latestRelease{}, err
	}
	return release, nil
}

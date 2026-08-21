package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// targetCmd builds a command carrying just the flags resolveScrapeTargets reads,
// pointed at a temporary creators directory so no test can reach the operator's
// real one.
func targetCmd(t *testing.T, dir string, args ...string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "scrape", RunE: func(*cobra.Command, []string) error { return nil }}
	c.Flags().StringArray("creator", nil, "")
	c.Flags().Bool("all-creators", false, "")
	c.Flags().String("creators-dir", dir, "")
	if err := c.Flags().Parse(args); err != nil {
		t.Fatal(err)
	}
	return c
}

func writeCreator(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func targetURLs(targets []scrapeTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.url)
	}
	return out
}

func TestResolveScrapeTargetsRequiresSomething(t *testing.T) {
	cmd := targetCmd(t, t.TempDir())
	_, err := resolveScrapeTargets(cmd, nil)
	if err == nil {
		t.Fatal("expected an error when nothing selects a studio")
	}
	if !strings.Contains(err.Error(), "--creator") {
		t.Errorf("error should point at the alternatives, got: %v", err)
	}
}

// A bare host must still work: every MatchesURL regex is anchored on a scheme.
func TestResolveScrapeTargetsNormalisesBareArguments(t *testing.T) {
	cmd := targetCmd(t, t.TempDir())
	targets, err := resolveScrapeTargets(cmd, []string{"example.com/studio"})
	if err != nil {
		t.Fatal(err)
	}
	if got := targetURLs(targets); len(got) != 1 || got[0] != "https://example.com/studio" {
		t.Errorf("targets = %v", got)
	}
	if targets[0].creator != "" {
		t.Errorf("a bare argument should carry no creator, got %q", targets[0].creator)
	}
}

func TestResolveScrapeTargetsExpandsACreator(t *testing.T) {
	dir := t.TempDir()
	writeCreator(t, dir, "mara-vance.yaml", `name: Mara Vance
stores:
  - url: https://a.example.com
  - url: https://b.example.com
    delay: 2500
`)
	cmd := targetCmd(t, dir, "--creator", "mara")
	targets, err := resolveScrapeTargets(cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := targetURLs(targets); strings.Join(got, ",") != "https://a.example.com,https://b.example.com" {
		t.Fatalf("targets = %v", got)
	}
	for _, tg := range targets {
		if tg.creator != "Mara Vance" {
			t.Errorf("%s carries creator %q", tg.url, tg.creator)
		}
	}
	if targets[0].delay != nil {
		t.Errorf("store with no delay carried one: %v", targets[0].delay)
	}
	if targets[1].delay == nil || *targets[1].delay != 2500*time.Millisecond {
		t.Errorf("per-store delay = %v, want 2.5s", targets[1].delay)
	}
}

// A store marked enabled:false is exactly the login-walled one an unattended
// cron run must not attempt.
func TestResolveScrapeTargetsSkipsDisabledStores(t *testing.T) {
	dir := t.TempDir()
	writeCreator(t, dir, "c.yaml", `name: Someone
stores:
  - url: https://on.example.com
  - url: https://off.example.com
    enabled: false
`)
	cmd := targetCmd(t, dir, "--all-creators")
	targets, err := resolveScrapeTargets(cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := targetURLs(targets); len(got) != 1 || got[0] != "https://on.example.com" {
		t.Errorf("targets = %v", got)
	}
}

func TestResolveScrapeTargetsAllCreators(t *testing.T) {
	dir := t.TempDir()
	writeCreator(t, dir, "b.yaml", "name: Bea\nstores:\n  - url: https://b.example.com\n")
	writeCreator(t, dir, "a.yaml", "name: Ann\nstores:\n  - url: https://a.example.com\n")
	cmd := targetCmd(t, dir, "--all-creators")
	targets, err := resolveScrapeTargets(cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Creators load sorted by name, so the run order is stable across runs —
	// a cron log that reorders itself is hard to diff.
	if got := targetURLs(targets); strings.Join(got, ",") != "https://a.example.com,https://b.example.com" {
		t.Errorf("targets = %v", got)
	}
}

// Naming a URL explicitly alongside --all-creators must not scrape it twice.
func TestResolveScrapeTargetsDeduplicates(t *testing.T) {
	dir := t.TempDir()
	writeCreator(t, dir, "c.yaml", "name: Someone\nstores:\n  - url: https://dup.example.com/\n")
	cmd := targetCmd(t, dir, "--all-creators")
	targets, err := resolveScrapeTargets(cmd, []string{"https://dup.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %v, want the URL once", targetURLs(targets))
	}
	// The bare argument came first, so it wins and carries no creator label.
	if targets[0].creator != "" {
		t.Errorf("creator = %q, want the first occurrence to win", targets[0].creator)
	}
}

// A site serving one studio at two URLs must not produce two studios: the
// scraper's preferred spelling is applied before the de-duplication key.
func TestResolveScrapeTargetsFoldsScraperAliases(t *testing.T) {
	cmd := targetCmd(t, t.TempDir())
	targets, err := resolveScrapeTargets(cmd, []string{
		"yourvids.com/leina-sex",
		"https://yourvids.com/creators/leina-sex",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://yourvids.com/creators/leina-sex"}
	got := targetURLs(targets)
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("targets = %v, want %v", got, want)
	}
}

func TestResolveScrapeTargetsUnknownCreator(t *testing.T) {
	dir := t.TempDir()
	writeCreator(t, dir, "c.yaml", "name: Someone\nstores:\n  - url: https://a.example.com\n")
	cmd := targetCmd(t, dir, "--creator", "nobody")
	if _, err := resolveScrapeTargets(cmd, nil); err == nil {
		t.Fatal("expected an error for an unknown creator")
	}
}

func TestParseStaleDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"7d":   7 * 24 * time.Hour,
		"1d":   24 * time.Hour,
		"2w":   14 * 24 * time.Hour,
		"12h":  12 * time.Hour,
		"90m":  90 * time.Minute,
		"1.5d": 36 * time.Hour,
		"0h":   0,
	}
	for in, want := range cases {
		got, err := parseStaleDuration(in)
		if err != nil {
			t.Errorf("parseStaleDuration(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseStaleDuration(%q) = %v, want %v", in, got, want)
		}
	}

	for _, bad := range []string{"", "7", "d", "-7d", "-1h", "next tuesday", "7x"} {
		if _, err := parseStaleDuration(bad); err == nil {
			t.Errorf("parseStaleDuration(%q) accepted a bad value", bad)
		}
	}
}

func TestHumanAge(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:              "<1m",
		45 * time.Minute:              "45m",
		5 * time.Hour:                 "5h",
		9 * 24 * time.Hour:            "9d",
		36 * time.Hour:                "1d",
		23*time.Hour + 59*time.Minute: "23h",
	}
	for in, want := range cases {
		if got := humanAge(in); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", in, got, want)
		}
	}
}

// A per-store delay is the most specific statement available and must beat the
// per-site and global values.
func TestResolveTargetDelayPrecedence(t *testing.T) {
	perStore := 3 * time.Second
	siteDelays := map[string]int{"acme": 1000}
	global := 500 * time.Millisecond

	if got := resolveTargetDelay(scrapeTarget{delay: &perStore}, "acme", global, siteDelays); got != perStore {
		t.Errorf("per-store delay lost: %v", got)
	}
	if got := resolveTargetDelay(scrapeTarget{}, "acme", global, siteDelays); got != time.Second {
		t.Errorf("site delay = %v, want 1s", got)
	}
	if got := resolveTargetDelay(scrapeTarget{}, "other", global, siteDelays); got != global {
		t.Errorf("global delay = %v, want 500ms", got)
	}
	// A store explicitly set to 0 disables the delay rather than inheriting.
	zero := time.Duration(0)
	if got := resolveTargetDelay(scrapeTarget{delay: &zero}, "acme", global, siteDelays); got != 0 {
		t.Errorf("explicit zero delay = %v, want 0", got)
	}
}

func TestScrapeTargetLabel(t *testing.T) {
	if got := (scrapeTarget{url: "https://a.example.com"}).label(); got != "https://a.example.com" {
		t.Errorf("label = %q", got)
	}
	if got := (scrapeTarget{url: "https://a.example.com", creator: "Ann"}).label(); got != "https://a.example.com [Ann]" {
		t.Errorf("label = %q", got)
	}
}

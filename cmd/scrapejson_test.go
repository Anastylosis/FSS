package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// runScrapeCLI drives `fss scrape` through the root command. Flags persist on
// the shared command between runs, so the ones used here are put back after.
// --db= keeps a configured database out of it: the user's real config is loaded.
func runScrapeCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("FSS_NO_NOTICES", "1")
	t.Cleanup(func() {
		for _, name := range []string{"format", "out-dir", "output", "full", "refresh", "force", "delay", "db"} {
			if f := scrapeCmd.Flags().Lookup(name); f != nil {
				_ = f.Value.Set(f.DefValue)
				f.Changed = false
			}
		}
	})
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"scrape", "--delay", "0", "--output", "json", "--db=", "--creators-dir", t.TempDir()}, args...))
	err = rootCmd.Execute()
	return out.String(), errb.String(), err
}

func jsonLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q: %v", l, err)
		}
		lines = append(lines, m)
	}
	return lines
}

func TestScrapeJSONSuccess(t *testing.T) {
	const url = "https://example.com/sj-success"
	scraper.Register(&urlStampScraper{id: "sjsuccess", matchURL: url, stampURL: url, sceneID: "1", siteID: "sjsite"})
	dir := t.TempDir()

	stdout, stderr, err := runScrapeCLI(t, "--format", "json", "--out-dir", dir, url)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	lines := jsonLines(t, stdout)
	if len(lines) != 2 {
		t.Fatalf("want a target line and a summary, got %d lines:\n%s", len(lines), stdout)
	}
	tl, sl := lines[0], lines[1]
	if tl["type"] != "target" || tl["command"] != "scrape" || tl["ok"] != true || tl["formatVersion"] != float64(1) {
		t.Errorf("target line: %v", tl)
	}
	res := tl["result"].(map[string]any)
	if res["status"] != "saved" || res["mode"] != "incremental" || res["siteId"] != "sjsuccess" || res["scenesSaved"] != float64(1) {
		t.Errorf("target result: %v", res)
	}
	if cov := res["coverage"].(map[string]any); cov["fresh"] != float64(1) || cov["stored"] != float64(0) {
		t.Errorf("coverage: %v", cov)
	}
	if res["complete"] != true || len(res["written"].([]any)) != 1 {
		t.Errorf("complete/written: %v", res)
	}
	if sl["type"] != "summary" || sl["ok"] != true {
		t.Errorf("summary line: %v", sl)
	}
	sum := sl["result"].(map[string]any)
	if sum["targets"] != float64(1) || sum["saved"] != float64(1) || sum["interrupted"] != false {
		t.Errorf("summary result: %v", sum)
	}
	if !strings.Contains(stderr, "Incremental scrape: "+url) {
		t.Errorf("progress prose should be on stderr, got %q", stderr)
	}
	if strings.Contains(stderr, "\r") {
		t.Errorf("carriage-return progress leaked in json mode: %q", stderr)
	}
}

func TestScrapeJSONZeroSceneFullIsRefused(t *testing.T) {
	const url = "https://example.com/sj-refused"
	scraper.Register(&fakeRegistered{id: "sjrefused", url: url})
	dir := t.TempDir()
	now := time.Now().UTC()
	st := store.NewFlat(dir, []string{"json"})
	if err := st.Save(url, []models.Scene{{ID: "1", SiteID: "sj", StudioURL: url, Title: "T", URL: "u", ScrapedAt: now}}); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runScrapeCLI(t, "--format", "json", "--full", "--out-dir", dir, url)
	if err == nil || exitCodeOf(err) != ExitRefused {
		t.Fatalf("want exit %d, got %v", ExitRefused, err)
	}
	lines := jsonLines(t, stdout)
	tl, sl := lines[0], lines[len(lines)-1]
	res := tl["result"].(map[string]any)
	if res["status"] != "refused" || res["refusalReason"] != "zero_scenes" || tl["ok"] != false {
		t.Errorf("target line: %v", tl)
	}
	if e := tl["error"].(map[string]any); e["code"] != "refused" || e["reason"] != "zero_scenes" {
		t.Errorf("target error: %v", e)
	}
	if sl["ok"] != false || sl["result"].(map[string]any)["refused"] != float64(1) {
		t.Errorf("summary: %v", sl)
	}
}

func TestScrapeJSONUnsupportedURL(t *testing.T) {
	stdout, _, err := runScrapeCLI(t, "--format", "json", "--out-dir", t.TempDir(), "https://example.com/no-scraper-here")
	if err == nil || exitCodeOf(err) != ExitFailure {
		t.Fatalf("want exit %d, got %v", ExitFailure, err)
	}
	lines := jsonLines(t, stdout)
	e := lines[0]["error"].(map[string]any)
	if e["code"] != "unsupported_url" || lines[0]["result"].(map[string]any)["status"] != "failed" {
		t.Errorf("target line: %v", lines[0])
	}
}

func TestScrapeJSONNothingToScrape(t *testing.T) {
	stdout, _, err := runScrapeCLI(t, "--format", "json")
	if err == nil {
		t.Fatal("want an error")
	}
	lines := jsonLines(t, stdout)
	if len(lines) != 1 || lines[0]["type"] != "summary" || lines[0]["ok"] != false {
		t.Fatalf("want one failed summary, got %s", stdout)
	}
	if e := lines[0]["error"].(map[string]any); e["code"] != "invalid_args" {
		t.Errorf("error: %v", e)
	}
}

func TestScrapeTextOutputUnchanged(t *testing.T) {
	const url = "https://example.com/sj-text"
	scraper.Register(&urlStampScraper{id: "sjtext", matchURL: url, stampURL: url, sceneID: "1", siteID: "sjsite"})
	dir := t.TempDir()

	var stdout string
	var err error
	stdout = captureStdout(t, func() {
		_, _, err = runScrapeCLI(t, "--format", "text", "--out-dir", dir, url)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Incremental scrape: " + url + "\n" +
		"\r  fetching: 1 scenes (0s elapsed)\n" +
		"  1 new, 0 existing → 1 total\n" +
		"Done: 1 scenes saved (0s elapsed).\n"
	if stdout != want {
		t.Errorf("text output changed:\n got %q\nwant %q", stdout, want)
	}
}

func TestScrapeJSONSummaryReportsTheErrorBehindTheExitCode(t *testing.T) {
	const url = "https://example.com/sj-mixed"
	scraper.Register(&fakeRegistered{id: "sjmixed", url: url})
	dir := t.TempDir()
	st := store.NewFlat(dir, []string{"json"})
	if err := st.Save(url, []models.Scene{{ID: "1", SiteID: "sj", StudioURL: url, Title: "T", URL: "u", ScrapedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}

	// Refused first, failed second: the failure decides the exit code, so it
	// must also be what the summary reports.
	stdout, _, err := runScrapeCLI(t, "--format", "json", "--full", "--out-dir", dir, url, "https://example.com/sj-mixed-unsupported")
	if exitCodeOf(err) != ExitFailure {
		t.Fatalf("want exit %d, got %v", ExitFailure, err)
	}
	lines := jsonLines(t, stdout)
	sl := lines[len(lines)-1]
	if e := sl["error"].(map[string]any); e["code"] != "unsupported_url" {
		t.Errorf("summary error = %v, want unsupported_url", e)
	}
	sum := sl["result"].(map[string]any)
	if sum["refused"] != float64(1) || sum["failed"] != float64(1) {
		t.Errorf("summary counts: %v", sum)
	}
}

func TestScrapeJSONRefusedSummaryCarriesReason(t *testing.T) {
	const url = "https://example.com/sj-reason"
	scraper.Register(&fakeRegistered{id: "sjreason", url: url})
	dir := t.TempDir()
	st := store.NewFlat(dir, []string{"json"})
	if err := st.Save(url, []models.Scene{{ID: "1", SiteID: "sj", StudioURL: url, Title: "T", URL: "u", ScrapedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runScrapeCLI(t, "--format", "json", "--full", "--out-dir", dir, url)
	lines := jsonLines(t, stdout)
	if e := lines[len(lines)-1]["error"].(map[string]any); e["code"] != "refused" || e["reason"] != "zero_scenes" {
		t.Errorf("summary error = %v", e)
	}
}

func TestPreRunEnvelopeForScrapeIsASummary(t *testing.T) {
	var buf bytes.Buffer
	writePreRunEnvelope(&buf, "scrape", withCode(codeInvalidArgs, errors.New("unknown flag: --nope")))
	lines := jsonLines(t, buf.String())
	if len(lines) != 1 || lines[0]["type"] != "summary" || lines[0]["result"].(map[string]any)["targets"] != float64(0) {
		t.Errorf("got %s", buf.String())
	}
	buf.Reset()
	writePreRunEnvelope(&buf, "check", errors.New("x"))
	if l := jsonLines(t, buf.String())[0]; l["type"] != nil || l["result"] != nil {
		t.Errorf("non-scrape envelope gained scrape fields: %s", buf.String())
	}
}

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/httpx"
	_ "github.com/Anastylosis/FSS/internal/scrapers/all"
	"github.com/Anastylosis/FSS/scraper"
)

// runFormatJSON executes rootCmd with args and decodes stdout as exactly one
// envelope. Cobra keeps parsed flag values between Executes, so --format is
// put back to text afterwards.
func runFormatJSON(t *testing.T, args ...string) (jsonEnvelope, map[string]any, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		resetFormatFlags(rootCmd)
	})
	err := rootCmd.Execute()

	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var raw map[string]any
	if dErr := dec.Decode(&raw); dErr != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s\nstderr:\n%s", dErr, stdout.String(), stderr.String())
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one JSON document:\n%s", stdout.String())
	}
	var env jsonEnvelope
	if dErr := json.Unmarshal(stdout.Bytes(), &env); dErr != nil {
		t.Fatal(dErr)
	}
	if env.FormatVersion != jsonFormatVersion {
		t.Errorf("formatVersion = %d, want %d", env.FormatVersion, jsonFormatVersion)
	}
	if env.OK != (err == nil) {
		t.Errorf("ok = %v but Execute returned %v", env.OK, err)
	}
	return env, raw, err
}

func resetFormatFlags(c *cobra.Command) {
	if f := c.Flags().Lookup("format"); f != nil {
		_ = f.Value.Set("text")
		f.Changed = false
	}
	for _, sub := range c.Commands() {
		resetFormatFlags(sub)
	}
}

func TestCheckFormatJSON(t *testing.T) {
	env, raw, err := runFormatJSON(t, "check", "brazzers.com", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	if env.Command != "check" {
		t.Errorf("command = %q", env.Command)
	}
	res := raw["result"].(map[string]any)
	if res["supported"] != true || res["scraper"] != "brazzers" || res["url"] != "https://brazzers.com" {
		t.Errorf("result = %v", res)
	}
}

func TestCheckFormatJSONUnsupportedIsNotAnError(t *testing.T) {
	_, raw, err := runFormatJSON(t, "check", "https://example.com/x", "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	res := raw["result"].(map[string]any)
	if res["supported"] != false || res["requestSupportUrl"] == "" {
		t.Errorf("result = %v", res)
	}
}

func TestListScrapersFormatJSON(t *testing.T) {
	_, raw, err := runFormatJSON(t, "list-scrapers", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	res := raw["result"].(map[string]any)
	list := res["scrapers"].([]any)
	if int(res["count"].(float64)) != len(scraper.All()) || len(list) != len(scraper.All()) {
		t.Errorf("count = %v, %d entries, registry has %d", res["count"], len(list), len(scraper.All()))
	}
	first := list[0].(map[string]any)
	if first["id"] == "" || first["patterns"] == nil {
		t.Errorf("first entry = %v", first)
	}
}

func TestFormatFlagRejectsUnknownValue(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"check", "brazzers.com", "--format", "yaml"})
	t.Cleanup(func() { resetFormatFlags(rootCmd) })
	err := rootCmd.Execute()
	var ce *codedError
	if !errors.As(err, &ce) || ce.code != codeInvalidArgs {
		t.Fatalf("err = %v, want invalid_args", err)
	}
}

func TestJSONFromArgs(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"check", "x", "--format", "json"}, true},
		{[]string{"check", "--format=json", "x"}, true},
		{[]string{"check", "--format", "text"}, false},
		{[]string{"stash", "import", "--json", "a.json"}, false},
		{[]string{"check", "--", "--format", "json"}, false},
		{[]string{"check", "--format"}, false},
	}
	for _, c := range cases {
		if got := jsonFromArgs(c.args); got != c.want {
			t.Errorf("jsonFromArgs(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestToJSONErrorCodes(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{"coded", withCode(codeStashUnreachable, errors.New("x")), codeStashUnreachable, 0},
		{"interrupted", &exitError{code: ExitInterrupted, err: errors.New("x")}, codeInterrupted, 0},
		{"refused", &exitError{code: ExitRefused, err: errors.New("x")}, codeRefused, 0},
		{"parse", scraper.ParseError("u", errors.New("x")), codeParse, 0},
		{"http status", fmt.Errorf("fetching: %w", &httpx.StatusError{StatusCode: 429}), codeTransport, 429},
		{"plain", errors.New("x"), codeError, 0},
		{"refused with reason", withReason(codeRefused, "zero_scenes", errors.New("x")), codeRefused, 0},
	}
	for _, c := range cases {
		je := toJSONError(c.err)
		if je.Code != c.code || je.HTTPStatus != c.status {
			t.Errorf("%s: got code %q status %d, want %q %d", c.name, je.Code, je.HTTPStatus, c.code, c.status)
		}
	}
	if toJSONError(nil) != nil {
		t.Error("nil error must give a nil error object")
	}
}

func TestClassifyPreRun(t *testing.T) {
	if je := toJSONError(classifyPreRun(errors.New("unknown flag: --bogus"))); je.Code != codeInvalidArgs {
		t.Errorf("unknown flag: code %q", je.Code)
	}
	if je := toJSONError(classifyPreRun(withCode(codeConfig, errors.New("bad yaml")))); je.Code != codeConfig {
		t.Errorf("config: code %q", je.Code)
	}
}

func TestCommandName(t *testing.T) {
	if got := errorCommandName(rootCmd, []string{"stash", "import", "--apply"}); got != "stash import" {
		t.Errorf("got %q", got)
	}
	if got := errorCommandName(rootCmd, []string{"--bogus"}); got != "" {
		t.Errorf("got %q", got)
	}
}

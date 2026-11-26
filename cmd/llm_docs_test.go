package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	codeSpanRe = regexp.MustCompile("`([^`\n]+)`")
	fenceRe    = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
	fssCallRe  = regexp.MustCompile(`(?:^|[\s"(])fss((?: +[^\s` + "`" + `"]+)+)`)
	flagRe     = regexp.MustCompile(`(?:^|[\s"\[(])(--[a-z][a-z0-9-]*)`)
)

// The llm/ skill is read by models that act on it, so a renamed command or
// flag there would send an agent off with a command that no longer works.
func TestLLMDocsNameRealCommands(t *testing.T) {
	var files []string
	err := filepath.WalkDir("../llm", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".json")) {
			files = append(files, p)
		}
		return err
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("reading llm/: %v (%d files)", err, len(files))
	}
	known := allFlagNames(rootCmd)
	known["help"] = true // cobra adds it lazily, when a command runs
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		isJSON := strings.HasSuffix(f, ".json")
		for _, snippet := range codeSnippets(string(b), isJSON) {
			// tools.json mentions fss in prose; only its flags are checked.
			for _, m := range fssCallRe.FindAllStringSubmatch(snippet, -1) {
				if !isJSON {
					checkFSSCall(t, f, strings.Fields(m[1]))
				}
			}
			for _, m := range flagRe.FindAllStringSubmatch(snippet, -1) {
				if !known[strings.TrimPrefix(m[1], "--")] {
					t.Errorf("%s: %s is not a flag of any fss command", f, m[1])
				}
			}
		}
	}
}

// codeSnippets returns code spans and fenced blocks; prose is free to say
// "fss" without naming a command. A .json file is all code.
func codeSnippets(s string, isJSON bool) []string {
	if isJSON {
		return []string{s}
	}
	var out []string
	for _, m := range fenceRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	for _, m := range codeSpanRe.FindAllStringSubmatch(fenceRe.ReplaceAllString(s, ""), -1) {
		out = append(out, m[1])
	}
	return out
}

func checkFSSCall(t *testing.T, file string, args []string) {
	t.Helper()
	var words []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "<") {
			break
		}
		words = append(words, a)
	}
	c, rest, err := rootCmd.Find(words)
	if err != nil || (c == rootCmd && len(words) > 0) {
		t.Errorf("%s: `fss %s` is not an fss command", file, strings.Join(args, " "))
		return
	}
	if len(rest) > 0 && len(c.Commands()) > 0 {
		t.Errorf("%s: `fss %s`: %q is not a subcommand of %q", file, strings.Join(args, " "), rest[0], c.CommandPath())
	}
	for _, a := range args {
		name, _, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if !strings.HasPrefix(a, "--") || name == "" || name == "help" {
			continue
		}
		if c.Flags().Lookup(name) == nil && c.InheritedFlags().Lookup(name) == nil {
			t.Errorf("%s: `fss %s`: %s has no --%s flag", file, strings.Join(args, " "), c.CommandPath(), name)
		}
	}
}

func allFlagNames(c *cobra.Command) map[string]bool {
	names := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) { names[f.Name] = true })
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { names[f.Name] = true })
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(c)
	return names
}

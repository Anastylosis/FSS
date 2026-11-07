package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Anastylosis/FSS/internal/i18n"
	"github.com/Anastylosis/FSS/internal/i18n/cobrai18n"
)

// localize installs lang on the shared rootCmd and registers the cleanup that
// puts it back. Every test in this file must go through it — rootCmd is a
// package singleton the rest of the suite drives directly.
func localize(t *testing.T, lang string) {
	t.Helper()
	i18n.SetLanguage(lang)
	restore := cobrai18n.Localize(rootCmd)
	t.Cleanup(func() {
		restore()
		i18n.SetLanguage(i18n.SourceLanguage)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
}

func walkTree(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walkTree(sub, fn)
	}
}

// materialize forces the lazy help command and help/version flags cobra would
// otherwise only build for the command being run. Localize does the same, and
// deliberately does not undo it, so a restore-comparison baseline has to be
// taken from the materialized tree rather than the bare one.
func materialize(root *cobra.Command) {
	root.InitDefaultVersionFlag()
	root.InitDefaultHelpCmd()
	walkTree(root, func(c *cobra.Command) { c.InitDefaultHelpFlag() })
}

// TestLocalizeRestoreRoundTrip is the invariant that makes the shared rootCmd
// safe for every other test in the package.
func TestLocalizeRestoreRoundTrip(t *testing.T) {
	i18n.SetLanguage(i18n.SourceLanguage)
	t.Cleanup(func() { i18n.SetLanguage(i18n.SourceLanguage) })
	materialize(rootCmd)

	type snap struct{ short, long string }
	before := map[*cobra.Command]snap{}
	flagsBefore := map[*pflag.Flag]string{}
	walkTree(rootCmd, func(c *cobra.Command) {
		before[c] = snap{c.Short, c.Long}
		c.Flags().VisitAll(func(f *pflag.Flag) { flagsBefore[f] = f.Usage })
	})
	usageBefore := rootCmd.UsageString()

	i18n.SetLanguage("_pseudo")
	restore := cobrai18n.Localize(rootCmd)
	restore()
	i18n.SetLanguage(i18n.SourceLanguage)

	walkTree(rootCmd, func(c *cobra.Command) {
		if s, ok := before[c]; ok && (c.Short != s.short || c.Long != s.long) {
			t.Errorf("%s: not restored\n short %q -> %q\n long %q -> %q",
				c.CommandPath(), s.short, c.Short, s.long, c.Long)
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if u, ok := flagsBefore[f]; ok && f.Usage != u {
				t.Errorf("%s --%s: usage not restored: %q -> %q", c.CommandPath(), f.Name, u, f.Usage)
			}
		})
	})
	if got := rootCmd.UsageString(); got != usageBefore {
		t.Errorf("root usage not restored:\n--- before ---\n%s\n--- after ---\n%s", usageBefore, got)
	}
}

// TestEveryCatalogRendersHelp renders every command in every shipped language,
// so a stray delimiter or bad placeholder fails here rather than on a user's
// --help.
func TestEveryCatalogRendersHelp(t *testing.T) {
	langs := append(i18n.Available()[1:], "_pseudo")
	for _, lang := range langs {
		t.Run(lang, func(t *testing.T) {
			localize(t, lang)
			if got := i18n.Language(); got != lang {
				t.Fatalf("SetLanguage(%q) installed %q", lang, got)
			}
			var buf bytes.Buffer
			rootCmd.SetOut(&buf)
			rootCmd.SetErr(&buf)
			walkTree(rootCmd, func(c *cobra.Command) {
				if c.UsageString() == "" {
					t.Errorf("%s: empty usage", c.CommandPath())
				}
				if err := c.Help(); err != nil {
					t.Errorf("%s: Help: %v", c.CommandPath(), err)
				}
			})
		})
	}
}

// TestPseudoLocaleCoversChrome proves the chrome key list is complete and the
// parameterized re-insertion works. It asserts presence of the pseudo markers,
// not absence of English — «Flags:» contains "Flags:" as a substring.
func TestPseudoLocaleCoversChrome(t *testing.T) {
	localize(t, "_pseudo")

	root := rootCmd.UsageString()
	for _, want := range []string{
		"«Usage:»",
		"«Available Commands:»",
		"«Flags:»",
		"«help for fss»",
		"«Help about any command»",
		`«Use "fss [command] --help"`,
	} {
		if !strings.Contains(root, want) {
			t.Errorf("root usage is missing %s:\n%s", want, root)
		}
	}

	scrape, _, err := rootCmd.Find([]string{"scrape"})
	if err != nil {
		t.Fatalf("finding scrape: %v", err)
	}
	sub := scrape.UsageString()
	for _, want := range []string{"«Global Flags:»", "«help for scrape»"} {
		if !strings.Contains(sub, want) {
			t.Errorf("scrape usage is missing %s:\n%s", want, sub)
		}
	}
}

// TestCommandNamesUnchangedAfterLocalize is the single most important
// invariant: Name() is Use up to the first space, and Find() resolves user
// input against it.
func TestCommandNamesUnchangedAfterLocalize(t *testing.T) {
	i18n.SetLanguage(i18n.SourceLanguage)
	uses := map[*cobra.Command]string{}
	names := map[*pflag.Flag]string{}
	walkTree(rootCmd, func(c *cobra.Command) {
		uses[c] = c.Use
		c.Flags().VisitAll(func(f *pflag.Flag) { names[f] = f.Name })
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { names[f] = f.Name })
	})

	localize(t, "_pseudo")

	walkTree(rootCmd, func(c *cobra.Command) {
		if u, ok := uses[c]; ok && c.Use != u {
			t.Errorf("Use changed: %q -> %q", u, c.Use)
		}
		check := func(f *pflag.Flag) {
			if n, ok := names[f]; ok && f.Name != n {
				t.Errorf("flag name changed: %q -> %q", n, f.Name)
			}
		}
		c.Flags().VisitAll(check)
		c.PersistentFlags().VisitAll(check)
	})
}

// TestUnknownExplicitLanguageWarns pins the condition Execute warns on: an
// explicit source naming a language with no catalog.
func TestUnknownExplicitLanguageWarns(t *testing.T) {
	t.Setenv("FSS_LANG", "xx")
	i18n.SetLanguage(i18n.SourceLanguage)
	t.Cleanup(func() { i18n.SetLanguage(i18n.SourceLanguage) })

	tag, explicit := resolveLanguage()
	if tag != "xx" || !explicit {
		t.Fatalf("resolveLanguage() = %q, %v; want \"xx\", true", tag, explicit)
	}
	resolved := i18n.SetLanguage(tag)
	warns := warnsUnknownLanguage(tag, explicit, resolved)
	if !warns {
		t.Errorf("warning condition did not hold for %q (resolved %q)", tag, resolved)
	}
}

func TestEnglishRegionsDoNotWarn(t *testing.T) {
	for _, tag := range []string{"en", "en_US", "en-GB", "en_US.UTF-8", "EN"} {
		if warnsUnknownLanguage(tag, true, i18n.SetLanguage(tag)) {
			t.Errorf("%q warned, want no warning", tag)
		}
	}
	if !warnsUnknownLanguage("xx", true, i18n.SourceLanguage) {
		t.Error("xx did not warn")
	}
	if warnsUnknownLanguage("xx", false, i18n.SourceLanguage) {
		t.Error("ambient xx warned")
	}
}

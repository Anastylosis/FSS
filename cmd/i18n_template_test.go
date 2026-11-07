package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/i18n"
	"github.com/Anastylosis/FSS/internal/i18n/cobrai18n"
)

// TestI18nTemplateInSync is the extractor: it regenerates
// internal/i18n/locales/en.json and _pseudo.json from the live command tree
// and fails if they're out of date. A plain test run only compares; set
// FSS_I18N_WRITE=1 (`make i18n-extract` does) to rewrite them, then commit.
func TestI18nTemplateInSync(t *testing.T) {
	i18n.SetLanguage(i18n.SourceLanguage)
	t.Cleanup(func() { i18n.SetLanguage(i18n.SourceLanguage) })

	keys := cobrai18n.Keys(rootCmd)

	// The base file's values are the English source strings, not "". It is
	// never loaded as a catalog (SetLanguage short-circuits on the source
	// language), so this is purely for the humans and the translation
	// platforms reading it: both want a file that shows the string being
	// translated. Weblate additionally requires a monolingual base file to be
	// named for the source language, which is why it is not "_"-prefixed like
	// the pseudo catalog next to it.
	template := map[string]string{}
	pseudo := map[string]string{}
	for _, k := range keys {
		template[k] = k
		pseudo[k] = "«" + k + "»"
	}

	writeIfChanged(t, "../internal/i18n/locales/en.json", marshal(t, template))
	writeIfChanged(t, "../internal/i18n/locales/_pseudo.json", marshal(t, pseudo))
}

func marshal(t *testing.T, m map[string]string) []byte {
	t.Helper()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(b, '\n')
}

func writeIfChanged(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err == nil && normalizeCRLF(got) == normalizeCRLF(want) {
		return
	}
	if os.Getenv("FSS_I18N_WRITE") == "" {
		t.Errorf("%s is out of date; run `make i18n-extract` and commit the result", path)
		return
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	t.Errorf("locales regenerated; commit the changes and re-run")
}

func normalizeCRLF(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

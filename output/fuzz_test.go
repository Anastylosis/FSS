package output

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
)

func FuzzSlugify(f *testing.F) {
	f.Add("https://www.manyvids.com/Profile/1000001/jane-eyre/Store/Videos")
	f.Add("https://evil.com/../../etc/passwd")
	f.Add("")
	f.Add("https://example.com/a?b=c&d=e")
	f.Add("HTTPS://UPPER.COM/PATH")
	// Regression: a URL long enough to push the slug past the 255-byte filename
	// limit made the Flat store fail with ENAMETOOLONG on Save, after the whole
	// scrape had already run.
	f.Add("https://example.com/" + strings.Repeat("a", 500))
	f.Add(strings.Repeat("h", 4000))

	f.Fuzz(func(t *testing.T, rawURL string) {
		slug := Slugify(rawURL)

		if strings.Contains(slug, "..") {
			t.Errorf("Slugify(%q) = %q contains path traversal", rawURL, slug)
		}
		if strings.Contains(slug, "/") {
			t.Errorf("Slugify(%q) = %q contains slash", rawURL, slug)
		}
		if strings.HasPrefix(slug, "-") {
			t.Errorf("Slugify(%q) = %q starts with dash", rawURL, slug)
		}
		if strings.HasSuffix(slug, "-") {
			t.Errorf("Slugify(%q) = %q ends with dash", rawURL, slug)
		}

		// The slug becomes a filename component: the Flat store writes
		// "<slug>.json" and flocks "<slug>.lock". Filenames are capped at 255
		// bytes, so an unbounded slug is not a cosmetic problem — Save fails
		// after the scrape has finished and the run is lost.
		if len(slug) > maxSlugLen {
			t.Errorf("Slugify(%q) returned %d bytes, over the %d-byte cap; "+
				"<slug>.json would exceed the 255-byte filename limit", rawURL, len(slug), maxSlugLen)
		}

		// Never empty, even for input that sanitizes away entirely: an empty
		// stem makes every such studio share the file ".json" and overwrite each
		// other, which is silent data loss rather than an error.
		if slug == "" {
			t.Errorf("Slugify(%q) returned an empty stem", rawURL)
		}

		// Deterministic — the slug *is* the store key, so an unstable one
		// orphans the previous file and restarts the scrape from empty.
		if again := Slugify(rawURL); again != slug {
			t.Errorf("Slugify(%q) is not deterministic: %q then %q", rawURL, slug, again)
		}
	})
}

// FuzzWriteCSVNeverEmitsAFormula exercises the whole WriteCSV path rather than
// escapeCSVFormula alone.
//
// The guard deliberately lives in WriteCSV "so no future column can bypass it"
// (see escapeCSVFormula), and that is a claim about the writer, not about the
// helper — a new column, or a change to sceneToRow's ordering, could route a
// scraped string around it while the helper's own unit tests still pass. This
// asserts the property end to end: the CSV is written, parsed back with
// encoding/csv, and every cell checked.
//
// The fuzzed string is placed in Title *and* Description because both are
// attacker-controlled (they come from scraped pages) and they are formatted
// differently — Description passes through more of sceneToRow.
func FuzzWriteCSVNeverEmitsAFormula(f *testing.F) {
	f.Add("Normal Title")
	f.Add("=cmd|' /C calc'!A0")
	f.Add("+1+1")
	f.Add("-2+3")
	f.Add("@SUM(A1:A9)")
	f.Add("\tleading tab")
	f.Add("\rleading cr")
	f.Add("")
	f.Add("已經")
	f.Add("a\x00b")
	f.Add("line1\nline2")
	f.Add(`quote" and ,comma`)

	f.Fuzz(func(t *testing.T, s string) {
		path := filepath.Join(t.TempDir(), "out.csv")
		scenes := []models.Scene{{
			ID: "1", SiteID: "site", StudioURL: "https://example.com",
			Title: s, Description: s, URL: "https://example.com/1",
			Performers: []string{s}, Tags: []string{s}, Studio: s,
		}}
		if err := WriteCSV(scenes, path); err != nil {
			t.Fatalf("WriteCSV: %v", err)
		}
		fh, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fh.Close() }()

		r := csv.NewReader(fh)
		r.FieldsPerRecord = -1
		records, err := r.ReadAll()
		if err != nil {
			// A written CSV that cannot be read back is itself a defect: the
			// export is the user-facing artefact.
			t.Fatalf("re-reading the CSV we just wrote: %v", err)
		}
		for ri, rec := range records {
			for ci, cell := range rec {
				if cell == "" {
					continue
				}
				switch cell[0] {
				case '=', '+', '-', '@', '\t', '\r':
					t.Errorf("row %d col %d begins with %q — a spreadsheet evaluates this as a "+
						"formula (cell %q, from input %q)", ri, ci, cell[0], cell, s)
				}
			}
		}
	})
}

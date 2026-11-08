package output

import (
	"encoding/csv"
	"net/url"
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
// EscapeCSVFormula alone.
//
// The guard deliberately lives in WriteCSV "so no future column can bypass it"
// (see EscapeCSVFormula), and that is a claim about the writer, not about the
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

func FuzzCanonicalStudioURLIdempotent(f *testing.F) {
	for _, s := range []string{
		"http://x.com//", "https://x.com/a//", "https://x.com/%2F", "https://x.com/a%2F/",
		"HTTP://X.com:443/", "http://x.com:80", "https://x.com:80:443", "https://x.com/?q=1",
		"https://x.com//?q=1#f", "x.com", "", "https://[::1]:443/", "https://x.com:443:443",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		once := CanonicalStudioURL(raw)
		if twice := CanonicalStudioURL(once); twice != once {
			t.Errorf("not idempotent: %q -> %q -> %q", raw, once, twice)
		}
	})
}

// canonicalStudioURLV1 is the single-pass form stored by migration 9.
func canonicalStudioURLV1(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return rawURL
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	u.Host = strings.TrimSuffix(u.Host, ":443")
	u.Host = strings.TrimSuffix(u.Host, ":80")
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	} else {
		u.Path = ""
	}
	return u.String()
}

func FuzzCanonicalStudioURLConvergesFromV1(f *testing.F) {
	for _, s := range []string{"http://x.com//", "https://x.com/a%2Fb/", "https://x.com/a(b)/", "https://x.com/%c3%a9//", "https://x.com/%2F"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		// A trailing slash on a path with a non-default encoding made V1 re-encode
		// it (a(b)/ -> a%28b%29); those keys cannot converge without rewriting
		// encoding, which the contract forbids.
		if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && (u.RawPath != "" || u.Hostname() == "" || strings.HasPrefix(u.Host, ":")) {
			t.Skip()
		}
		if _, err := url.Parse(canonicalStudioURLV1(raw)); err != nil {
			t.Skip()
		}
		if got, want := CanonicalStudioURL(canonicalStudioURLV1(raw)), CanonicalStudioURL(raw); got != want {
			t.Errorf("%q: C(V1(x)) = %q, C(x) = %q", raw, got, want)
		}
	})
}

package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/models"
)

func filterScene(id, studioURL, studio string, performers ...string) models.Scene {
	return models.Scene{
		ID: id, SiteID: "t", StudioURL: studioURL,
		Title: "Scene " + id, Studio: studio, Performers: performers,
	}
}

var filterFixture = []models.Scene{
	filterScene("1", "https://network.example.com", "SLR Originals", "Alice", "Bob"),
	filterScene("2", "https://network.example.com", "perVRt", "Bob"),
	filterScene("3", "https://other.example.com", "Other Studio", "Carol"),
}

func ids(scenes []models.Scene) string {
	var b []string
	for _, s := range scenes {
		b = append(b, s.ID)
	}
	return strings.Join(b, ",")
}

func TestFilterScenesByStudio(t *testing.T) {
	names := map[string]string{"https://network.example.com": "The Network"}

	cases := []struct {
		name    string
		filters []string
		want    string
	}{
		// A studio URL selects everything scraped from it.
		{"by studio URL", []string{"https://network.example.com"}, "1,2"},
		// The studios-table display name does the same.
		{"by studio display name", []string{"The Network"}, "1,2"},
		// The per-scene Studio field is the sub-brand — one level of hierarchy
		// for free, without FSS recording any.
		{"by sub-brand", []string{"SLR Originals"}, "1"},
		// Repeating the flag is OR.
		{"two sub-brands", []string{"SLR Originals", "perVRt"}, "1,2"},
		// Names are matched canonically: stray whitespace and case must not miss.
		{"trailing space", []string{"SLR Originals "}, "1"},
		{"different case", []string{"slr originals"}, "1"},
		{"collapsed spacing", []string{"SLR   Originals"}, "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := filterScenes(filterFixture, c.filters, nil, names)
			if err != nil {
				t.Fatal(err)
			}
			if ids(got) != c.want {
				t.Errorf("got scenes [%s], want [%s]", ids(got), c.want)
			}
		})
	}
}

func TestFilterScenesByPerformer(t *testing.T) {
	got, err := filterScenes(filterFixture, nil, []string{"bob"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "1,2" {
		t.Errorf("got [%s], want [1,2]", ids(got))
	}

	// Repeated values are OR.
	got, err = filterScenes(filterFixture, nil, []string{"Alice", "Carol"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "1,3" {
		t.Errorf("got [%s], want [1,3]", ids(got))
	}
}

// Across flags the filters are ANDed: studio X *and* performer Y.
func TestFilterScenesCombinesFlagsWithAnd(t *testing.T) {
	names := map[string]string{"https://network.example.com": "The Network"}

	got, err := filterScenes(filterFixture, []string{"The Network"}, []string{"Alice"}, names)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "1" {
		t.Errorf("got [%s], want [1] — scene 2 is in the studio but has no Alice", ids(got))
	}

	// A combination nothing satisfies is an error, not a silent empty set.
	if _, err := filterScenes(filterFixture, []string{"Other Studio"}, []string{"Alice"}, names); err == nil {
		t.Fatal("expected an error when the AND of both filters matches nothing")
	}
}

// A filter that matches nothing must name what *was* available, so a typo is
// obvious rather than looking like an empty catalogue.
func TestFilterScenesUnmatchedListsCandidates(t *testing.T) {
	_, err := filterScenes(filterFixture, []string{"Nonexistent"}, nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"SLR Originals", "perVRt", "Other Studio"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention available studio %q: %v", want, err)
		}
	}

	_, err = filterScenes(filterFixture, nil, []string{"Nobody"}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Alice") {
		t.Errorf("error does not list available performers: %v", err)
	}
}

func TestFilterScenesNoFiltersIsIdentity(t *testing.T) {
	got, err := filterScenes(filterFixture, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(filterFixture) {
		t.Errorf("got %d scenes, want all %d", len(got), len(filterFixture))
	}
}

// The whole point of the DB path is that it produces the same scene set as the
// JSON path — otherwise it is a second, subtly different implementation.
func TestLoadFSSScenesDBAndJSONAgree(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fss.db")
	seedTestDB(t, dbPath, "https://example.com", "Alpha", "Beta")

	jsonDir := t.TempDir()
	writeStudioFile(t, filepath.Join(jsonDir, "s.json"), "Alpha", "Beta")

	withCfg(t, &config.Config{OutDir: jsonDir})
	fromJSON, srcJSON, err := loadFSSScenes(newImportTestCmd(t))
	if err != nil {
		t.Fatal(err)
	}

	c := newImportTestCmd(t)
	setFlag(t, c, "db", dbPath)
	fromDB, srcDB, err := loadFSSScenes(c)
	if err != nil {
		t.Fatal(err)
	}

	if srcJSON.kind != "json" || srcDB.kind != "db" {
		t.Fatalf("source kinds = %q / %q", srcJSON.kind, srcDB.kind)
	}
	if len(fromJSON) != len(fromDB) {
		t.Fatalf("json returned %d scenes, db returned %d", len(fromJSON), len(fromDB))
	}
	titles := func(scenes []models.Scene) map[string]bool {
		out := map[string]bool{}
		for _, s := range scenes {
			out[s.Title] = true
		}
		return out
	}
	jt, dt := titles(fromJSON), titles(fromDB)
	for title := range jt {
		if !dt[title] {
			t.Errorf("scene %q present via JSON but missing via the database", title)
		}
	}
}

// changelogDir must stay independent of where scenes came from. `stash revert`
// reads fss-stashbox-changelog.json out of the out_dir, so loading scenes from
// --db must not move (or lose) the changelog.
func TestChangelogDirIsIndependentOfSceneSource(t *testing.T) {
	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "fss.db")
	seedTestDB(t, dbPath, "https://example.com", "Alpha")
	withCfg(t, &config.Config{OutDir: outDir, DB: config.DBRef(dbPath)})

	t.Run("db source still uses out_dir", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "db", dbPath)
		if got := changelogDir(c); got != outDir {
			t.Errorf("changelogDir = %q, want the out_dir %q", got, outDir)
		}
	})

	t.Run("explicit --dir wins", func(t *testing.T) {
		other := t.TempDir()
		c := newImportTestCmd(t)
		setFlag(t, c, "dir", other)
		if got := changelogDir(c); got != other {
			t.Errorf("changelogDir = %q, want the --dir value %q", got, other)
		}
	})

	t.Run("no flags falls back to out_dir", func(t *testing.T) {
		if got := changelogDir(newImportTestCmd(t)); got != outDir {
			t.Errorf("changelogDir = %q, want %q", got, outDir)
		}
	})
}

// TestFilterScenesSubBrandBeatsDerivedStudioName is the regression this design
// exists for. `fss import` and `fss scrape` both derive a studio's display name
// from its first scene's Studio field, so for a multi-brand catalogue that name
// IS one of the sub-brands. Matching display names before sub-brands therefore
// made --from-studio "<sub-brand>" select the whole network — every scene shares
// the studio URL. Verified against real data: it returned 400 of 400 scenes
// instead of 176.
func TestFilterScenesSubBrandBeatsDerivedStudioName(t *testing.T) {
	const url = "https://network.example.com"
	scenes := []models.Scene{
		filterScene("1", url, "SLR Originals", "Alice"),
		filterScene("2", url, "perVRt", "Bob"),
		filterScene("3", url, "SLR Originals", "Carol"),
	}
	// The derived display name is whatever scene 1 happened to carry.
	names := map[string]string{url: "SLR Originals"}

	got, err := filterScenes(scenes, []string{"SLR Originals"}, nil, names)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "1,3" {
		t.Errorf("got [%s], want [1,3] — the sub-brand must not expand to the whole studio URL", ids(got))
	}

	// The other sub-brand still resolves independently.
	got, err = filterScenes(scenes, []string{"perVRt"}, nil, names)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "2" {
		t.Errorf("got [%s], want [2]", ids(got))
	}

	// And the URL still selects everything.
	got, err = filterScenes(scenes, []string{url}, nil, names)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "1,2,3" {
		t.Errorf("got [%s], want all three", ids(got))
	}
}

// A display name that is genuinely distinct from every sub-brand (a --name
// label) still selects the whole studio.
func TestFilterScenesDistinctDisplayNameSelectsWholeStudio(t *testing.T) {
	const url = "https://network.example.com"
	scenes := []models.Scene{
		filterScene("1", url, "SLR Originals"),
		filterScene("2", url, "perVRt"),
		filterScene("3", "https://other.example.com", "Other"),
	}
	names := map[string]string{url: "The Whole Network"}

	got, err := filterScenes(scenes, []string{"the whole network"}, nil, names)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "1,2" {
		t.Errorf("got [%s], want [1,2]", ids(got))
	}
}

// A bare --db must mean "the database I configured", not a different one at the
// XDG default. pflag gives the valueless flag the sentinel "default", and
// resolving that literally opened an empty database while the operator's own
// path sat in `db:`.
func TestResolveDBPathBareFlagUsesConfiguredPath(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "mine.db")

	t.Run("bare --db with a configured path", func(t *testing.T) {
		withCfg(t, &config.Config{DB: config.DBRef(configured)})
		c := newImportTestCmd(t)
		setFlag(t, c, "db", "default")
		if got := resolveDBPath(c); got != configured {
			t.Errorf("resolveDBPath = %q, want the configured %q", got, configured)
		}
	})

	t.Run("explicit path still wins", func(t *testing.T) {
		withCfg(t, &config.Config{DB: config.DBRef(configured)})
		other := filepath.Join(t.TempDir(), "other.db")
		c := newImportTestCmd(t)
		setFlag(t, c, "db", other)
		if got := resolveDBPath(c); got != other {
			t.Errorf("resolveDBPath = %q, want the explicit %q", got, other)
		}
	})

	t.Run("bare --db with nothing configured falls back to the default location", func(t *testing.T) {
		withCfg(t, &config.Config{})
		c := newImportTestCmd(t)
		setFlag(t, c, "db", "default")
		if got := resolveDBPath(c); got == "" || got == "default" {
			t.Errorf("resolveDBPath = %q, want the XDG default path", got)
		}
	})

	t.Run("no flag and an explicit empty db: means no database", func(t *testing.T) {
		withCfg(t, &config.Config{DB: config.DBRef("")})
		if got := resolveDBPath(newImportTestCmd(t)); got != "" {
			t.Errorf("resolveDBPath = %q, want empty", got)
		}
	})
}

// An explicitly empty --db must override a configured database. Without this
// there is no command-line way to say "JSON this time" once `db:` is set, which
// is the only gap a separate `--store` selector would have filled.
func TestResolveDBPathExplicitEmptyOverridesConfig(t *testing.T) {
	withCfg(t, &config.Config{DB: config.DBRef("/configured/fss.db")})

	t.Run("--db= overrides to no database", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "db", "")
		if got := resolveDBPath(c); got != "" {
			t.Errorf("resolveDBPath = %q, want empty (flat store)", got)
		}
	})

	t.Run("not passing --db leaves the config in charge", func(t *testing.T) {
		if got := resolveDBPath(newImportTestCmd(t)); got != "/configured/fss.db" {
			t.Errorf("resolveDBPath = %q, want the configured path", got)
		}
	})
}

func TestFilterScenesByStudioURLSpellings(t *testing.T) {
	scenes := []models.Scene{filterScene("1", "https://www.site.com/models/x", "X")}
	for _, f := range []string{
		"https://www.site.com/models/x/",
		"http://www.site.com/models/x",
		"https://WWW.site.com/models/x",
	} {
		got, err := filterScenes(scenes, []string{f}, nil, nil)
		if err != nil || ids(got) != "1" {
			t.Errorf("%s: got %q, err %v", f, ids(got), err)
		}
	}
}

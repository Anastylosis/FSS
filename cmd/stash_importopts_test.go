package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
)

// newImportTestCmd builds a command carrying the same flags as stashImportCmd,
// derived from stashImportCmd itself rather than re-listed here — a re-listed set
// silently stops covering flags added later, and resolveImportOpts reading a flag
// the test never registered panics rather than failing informatively.
//
// Fresh flags (not AddFlag) because pflag's Changed lives on the *Flag, and
// resolveImportOpts branches on it: sharing pointers would leak a test's
// --resolution-tags into the real command.
func newImportTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "import"}
	stashImportCmd.Flags().VisitAll(func(f *pflag.Flag) {
		switch f.Value.Type() {
		case "bool":
			c.Flags().Bool(f.Name, f.DefValue == "true", f.Usage)
		case "string":
			c.Flags().String(f.Name, f.DefValue, f.Usage)
		case "int":
			c.Flags().Int(f.Name, 0, f.Usage)
		case "stringSlice":
			c.Flags().StringSlice(f.Name, nil, f.Usage)
		case "count":
			// --debug: merged in from root's persistent flags once anything
			// (e.g. cobrai18n.Keys) forces mergePersistentFlags on this command.
			c.Flags().CountP(f.Name, f.Shorthand, f.Usage)
		default:
			t.Fatalf("flag --%s has unhandled type %q; add it here", f.Name, f.Value.Type())
		}
	})
	return c
}

// withCfg swaps the package-level config for the duration of a test.
func withCfg(t *testing.T, c *config.Config) {
	t.Helper()
	prev := cfg
	cfg = c
	t.Cleanup(func() { cfg = prev })
}

func setFlag(t *testing.T, c *cobra.Command, name, val string) {
	t.Helper()
	if err := c.Flags().Set(name, val); err != nil {
		t.Fatalf("setting --%s=%s: %v", name, val, err)
	}
}

// The config fallbacks are the interesting part of resolveImportOpts: two of them
// key off the empty string, but resolution-tags keys off pflag's Changed, because
// `false` is both the zero value and a meaningful choice. Getting that wrong makes
// `--resolution-tags=false` silently lose to a config `true` — the user disables a
// behaviour and it still happens.
func TestResolveImportOptsResolutionTagsFlagBeatsConfig(t *testing.T) {
	withCfg(t, &config.Config{Stash: config.StashConfig{ResolutionTags: true}})

	t.Run("untouched flag takes the config value", func(t *testing.T) {
		o, err := resolveImportOpts(newImportTestCmd(t))
		if err != nil {
			t.Fatal(err)
		}
		if !o.resolutionTags {
			t.Error("config ResolutionTags=true was not picked up when the flag was untouched")
		}
	})

	t.Run("explicit false beats config true", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "resolution-tags", "false")
		o, err := resolveImportOpts(c)
		if err != nil {
			t.Fatal(err)
		}
		if o.resolutionTags {
			t.Error("--resolution-tags=false lost to the config value; an explicit flag must win")
		}
	})

	t.Run("explicit true with config false", func(t *testing.T) {
		withCfg(t, &config.Config{Stash: config.StashConfig{ResolutionTags: false}})
		c := newImportTestCmd(t)
		setFlag(t, c, "resolution-tags", "true")
		o, err := resolveImportOpts(c)
		if err != nil {
			t.Fatal(err)
		}
		if !o.resolutionTags {
			t.Error("--resolution-tags=true was not honoured")
		}
	})
}

// The two tag names end up on scenes in Stash and `stash revert` keys off the
// stashbox one, so an empty value silently falling through to no tag at all would
// make a later revert find nothing to undo.
func TestResolveImportOptsTagFallbacks(t *testing.T) {
	withCfg(t, &config.Config{Stash: config.StashConfig{
		Tag:         "cfg-tag",
		StashboxTag: "cfg-sbox",
	}})

	o, err := resolveImportOpts(newImportTestCmd(t))
	if err != nil {
		t.Fatal(err)
	}
	if o.tagName != "cfg-tag" {
		t.Errorf("tagName = %q, want the config value", o.tagName)
	}
	if o.stashboxTag != "cfg-sbox" {
		t.Errorf("stashboxTag = %q, want the config value", o.stashboxTag)
	}

	c := newImportTestCmd(t)
	setFlag(t, c, "tag", "flag-tag")
	setFlag(t, c, "stashbox-tag", "flag-sbox")
	o, err = resolveImportOpts(c)
	if err != nil {
		t.Fatal(err)
	}
	if o.tagName != "flag-tag" || o.stashboxTag != "flag-sbox" {
		t.Errorf("flags did not override config: tag=%q stashboxTag=%q", o.tagName, o.stashboxTag)
	}
}

// --fields is the blast-radius control: whatever it does not name must not be
// written. Cover is the special case — naming it in --fields enables cover
// setting without --cover, so the two paths have to agree.
func TestResolveImportOptsFields(t *testing.T) {
	withCfg(t, &config.Config{})

	t.Run("no --fields means all fields and no cover", func(t *testing.T) {
		o, err := resolveImportOpts(newImportTestCmd(t))
		if err != nil {
			t.Fatal(err)
		}
		if o.allowedFields != nil {
			t.Errorf("allowedFields = %v, want nil (nil means every field is allowed)", o.allowedFields)
		}
		if o.setCover {
			t.Error("setCover true without --cover or --fields cover")
		}
	})

	t.Run("--fields cover implies setCover", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "fields", "cover")
		o, err := resolveImportOpts(c)
		if err != nil {
			t.Fatal(err)
		}
		if !o.setCover {
			t.Error("naming cover in --fields did not enable cover setting")
		}
	})

	t.Run("--fields title does not imply setCover", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "fields", "title")
		o, err := resolveImportOpts(c)
		if err != nil {
			t.Fatal(err)
		}
		if o.setCover {
			t.Error("setCover enabled by --fields title; covers would be written when only titles were asked for")
		}
		if !o.allowedFields["title"] || o.allowedFields["tags"] {
			t.Errorf("allowedFields = %v, want only title", o.allowedFields)
		}
	})

	t.Run("--cover alone enables it", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "cover", "true")
		setFlag(t, c, "fields", "title")
		o, err := resolveImportOpts(c)
		if err != nil {
			t.Fatal(err)
		}
		if !o.setCover {
			t.Error("--cover was not honoured alongside --fields")
		}
	})

	t.Run("an unknown field is rejected, not ignored", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "fields", "title,bogus")
		if _, err := resolveImportOpts(c); err == nil {
			t.Fatal("expected an error for an unknown field; silently ignoring it would " +
				"widen the write set past what the user asked for")
		}
	})
}

// The remaining flags are plain pass-throughs, checked together so a renamed flag
// or a copy-pasted lookup (reading "studio" into pathFilter, say) is caught.
func TestResolveImportOptsPassThroughFlags(t *testing.T) {
	withCfg(t, &config.Config{})
	c := newImportTestCmd(t)
	setFlag(t, c, "apply", "true")
	setFlag(t, c, "cover-allow-private", "true")
	setFlag(t, c, "include-stashbox", "true")
	setFlag(t, c, "organized", "true")
	setFlag(t, c, "performer", "Jane Doe")
	setFlag(t, c, "studio", "Some Studio")
	setFlag(t, c, "filter", "/mnt/media")
	setFlag(t, c, "top", "7")

	o, err := resolveImportOpts(c)
	if err != nil {
		t.Fatal(err)
	}
	if !o.apply || !o.coverAllowPrivate || !o.includeStashbox || !o.organized {
		t.Errorf("a boolean flag did not reach opts: %+v", o)
	}
	if o.performer != "Jane Doe" || o.studio != "Some Studio" || o.pathFilter != "/mnt/media" {
		t.Errorf("string flags crossed over: performer=%q studio=%q pathFilter=%q",
			o.performer, o.studio, o.pathFilter)
	}
	if o.top != 7 {
		t.Errorf("top = %d, want 7", o.top)
	}
}

func writeStudioFile(t *testing.T, path string, titles ...string) {
	t.Helper()
	sf := models.StudioFile{StudioURL: "https://example.com"}
	for i, title := range titles {
		sf.Scenes = append(sf.Scenes, models.Scene{
			ID:        string(rune('a' + i)),
			SiteID:    "example",
			StudioURL: "https://example.com",
			Title:     title,
			URL:       "https://example.com/scene/1",
			ScrapedAt: time.Now().UTC(),
		})
	}
	b, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// loadFSSScenes picks the data every later decision is made from. The
// --json/--db/--dir/config precedence is the part worth pinning: silently
// reading the config's out_dir when the user pointed --dir elsewhere would
// import from the wrong catalogue.
func TestLoadFSSScenesSourcePrecedence(t *testing.T) {
	cfgDir := t.TempDir()
	flagDir := t.TempDir()
	writeStudioFile(t, filepath.Join(cfgDir, "a.json"), "From Config Dir")
	writeStudioFile(t, filepath.Join(flagDir, "b.json"), "From Flag Dir", "Second")
	withCfg(t, &config.Config{OutDir: cfgDir})

	t.Run("no flags falls back to config out_dir", func(t *testing.T) {
		scenes, src, err := loadFSSScenes(newImportTestCmd(t))
		if err != nil {
			t.Fatal(err)
		}
		if src.desc != cfgDir {
			t.Errorf("source = %q, want the config out_dir %q", src.desc, cfgDir)
		}
		if len(scenes) != 1 {
			t.Errorf("got %d scenes, want 1", len(scenes))
		}
	})

	t.Run("--dir wins over config", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "dir", flagDir)
		scenes, src, err := loadFSSScenes(c)
		if err != nil {
			t.Fatal(err)
		}
		if src.desc != flagDir {
			t.Errorf("source = %q, want the --dir value %q", src.desc, flagDir)
		}
		if len(scenes) != 2 {
			t.Errorf("got %d scenes, want 2", len(scenes))
		}
	})

	t.Run("--json wins over both", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "dir", flagDir)
		setFlag(t, c, "json", filepath.Join(cfgDir, "a.json"))
		scenes, src, err := loadFSSScenes(c)
		if err != nil {
			t.Fatal(err)
		}
		if src.kind != "json" || len(scenes) != 1 {
			t.Errorf("source = %+v, %d scenes; want the single --json file", src, len(scenes))
		}
	})

	t.Run("--db and --json together is an error", func(t *testing.T) {
		c := newImportTestCmd(t)
		setFlag(t, c, "db", "default")
		setFlag(t, c, "json", filepath.Join(cfgDir, "a.json"))
		if _, _, err := loadFSSScenes(c); err == nil {
			t.Fatal("expected an error when --db is combined with --json")
		}
	})
}

// A configured `db:` must NOT silently become the default source. It says where
// `fss scrape` writes, not where these commands read; reading the database is
// always an explicit --db.
func TestLoadFSSScenesConfiguredDBDoesNotOverrideJSON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fss.db")
	seedTestDB(t, dbPath, "https://example.com", "DB Scene")
	writeStudioFile(t, filepath.Join(dir, "a.json"), "JSON Scene")
	withCfg(t, &config.Config{OutDir: dir, DB: config.DBRef(dbPath)})

	scenes, src, err := loadFSSScenes(newImportTestCmd(t))
	if err != nil {
		t.Fatal(err)
	}
	if src.kind != "json" {
		t.Fatalf("source kind = %q, want json — a configured db: must not change the source", src.kind)
	}
	if len(scenes) != 1 || scenes[0].Title != "JSON Scene" {
		t.Errorf("got %+v, want the scene from JSON", scenes)
	}

	// ...but an explicit --db still reaches the database.
	c := newImportTestCmd(t)
	setFlag(t, c, "db", dbPath)
	scenes, src, err = loadFSSScenes(c)
	if err != nil {
		t.Fatal(err)
	}
	if src.kind != "db" || len(scenes) != 1 || scenes[0].Title != "DB Scene" {
		t.Errorf("explicit --db did not read the database: kind=%q scenes=%+v", src.kind, scenes)
	}
}

// An empty source is an error rather than an empty import: proceeding would
// walk every scene in Stash, match nothing, and report "0 changes" as if the
// library were already up to date.
func TestLoadFSSScenesEmptySourceIsAnError(t *testing.T) {
	empty := t.TempDir()
	withCfg(t, &config.Config{OutDir: empty})

	_, src, err := loadFSSScenes(newImportTestCmd(t))
	if err == nil {
		t.Fatal("expected an error for a directory with no JSON files")
	}
	if src.desc != empty {
		t.Errorf("source = %q, want %q — the error message names this path", src.desc, empty)
	}
}

func TestLoadFSSScenesMissingDirIsAnError(t *testing.T) {
	withCfg(t, &config.Config{OutDir: filepath.Join(t.TempDir(), "does-not-exist")})
	if _, _, err := loadFSSScenes(newImportTestCmd(t)); err == nil {
		t.Fatal("expected an error for a nonexistent directory")
	}
}

// seedTestDB creates a SQLite store at path holding one studio's scenes.
func seedTestDB(t *testing.T, path, studioURL string, titles ...string) {
	t.Helper()
	db, err := store.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var scenes []models.Scene
	for i, title := range titles {
		scenes = append(scenes, models.Scene{
			ID: string(rune('a' + i)), SiteID: "example", StudioURL: studioURL,
			Title: title, URL: studioURL + "/scene/1", ScrapedAt: time.Now().UTC(),
		})
	}
	if err := db.Save(studioURL, scenes); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertStudio(models.Studio{
		URL: studioURL, SiteID: "example", Name: "Example Studio", AddedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

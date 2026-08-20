package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/internal/creators"
	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/match"
	"github.com/Anastylosis/FSS/models"
)

// sceneSource describes where scenes were loaded from, so commands can say so.
type sceneSource struct {
	kind string // "json" or "db"
	desc string // directory, file count, or database path
}

func (s sceneSource) String() string { return s.desc }

// addSceneSourceFlags registers the flags every scene-consuming command shares.
func addSceneSourceFlags(cmd *cobra.Command) {
	cmd.Flags().StringSlice("json", nil, "specific JSON files to load")
	cmd.Flags().String("dir", "", "directory containing FSS JSON files (default: config out_dir)")
	cmd.Flags().String("db", "", "load scenes from the SQLite store instead of JSON (no value = the configured db, or the default location)")
	cmd.Flags().Lookup("db").NoOptDefVal = "default"
	cmd.Flags().StringSlice("from-studio", nil,
		"only use scenes from these studios — a studio URL, a studio name, or a per-scene studio/sub-brand name (repeatable: any match)")
	cmd.Flags().StringSlice("from-performer", nil,
		"only use scenes featuring these performers (repeatable: any match)")
	cmd.Flags().StringSlice("from-creator", nil,
		"only use scenes from this creator's storefronts, as defined in creators.d (repeatable: any match)")
	cmd.Flags().String("creators-dir", "",
		"directory of creator YAML files (default: config creators_dir, else ~/.config/fss/creators.d)")
}

// creatorStudioURLs expands --from-creator into studio URLs, which the studio
// filter already knows how to match. A creator is a named set of studio URLs,
// so it resolves to exactly the same thing as listing them by hand.
func creatorStudioURLs(cmd *cobra.Command) ([]string, error) {
	names, _ := cmd.Flags().GetStringSlice("from-creator")
	if len(names) == 0 {
		return nil, nil
	}
	list, err := loadCreators(cmd)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, n := range names {
		c, err := creators.Find(list, n)
		if err != nil {
			return nil, err
		}
		urls = append(urls, c.URLs()...)
	}
	return urls, nil
}

// resolveDBPath returns the database path for a command. An empty result means
// no database — the flat JSON store.
//
//	(absent)      the config's `db:` decides
//	--db=""       no database, explicitly — overrides the config
//	--db          the database named in `db:`, or the default location
//	--db=/path    exactly that file
//
// Passing `--db=""` is distinguished from not passing `--db` at all, mirroring
// the same distinction in the config (see config.DBSetting). Without it there is
// no way to say "not this time" on the command line once `db:` is set, which is
// the one thing a separate `--store` selector would have been needed for.
//
// A bare `--db` means "the database I configured": pflag gives the valueless
// flag the sentinel "default", and resolving that straight to the XDG location
// would open a different — probably empty — database than the one in `db:`.
func resolveDBPath(cmd *cobra.Command) string {
	flag, _ := cmd.Flags().GetString("db")
	if cmd.Flags().Changed("db") && flag == "" {
		return "" // explicit opt-out
	}
	configured, _ := cfg.DBSetting()
	if (flag == "" || flag == "default") && configured != "" {
		flag = configured
	}
	return config.ResolveDBPath(flag)
}

// loadFSSScenes resolves where scenes come from and returns them filtered.
//
// Precedence: --json, then --db, then --dir, and finally the config's out_dir.
// Explicitly combining --db with --json/--dir is an error rather than a silent
// winner, since which one wins is not guessable from the command line.
//
// A configured `db:` deliberately does NOT make the database the default source
// here. It says where `fss scrape` writes, not where these commands read from,
// and inferring one from the other would silently change what an existing
// invocation returns. Reading the database is an explicit `--db`.
//
// There is no pending switch: the flat store remains the default and SQLite is
// a first-class opt-in. See docs/storage.md.
func loadFSSScenes(cmd *cobra.Command) ([]models.Scene, sceneSource, error) {
	jsonFiles, _ := cmd.Flags().GetStringSlice("json")
	dirFlag, _ := cmd.Flags().GetString("dir")
	dbExplicit := cmd.Flags().Changed("db")

	if dbExplicit && (len(jsonFiles) > 0 || dirFlag != "") {
		return nil, sceneSource{}, fmt.Errorf("--db cannot be combined with --json or --dir — pick one source")
	}

	var (
		scenes      []models.Scene
		src         sceneSource
		studioNames map[string]string
		err         error
	)
	switch {
	case len(jsonFiles) > 0:
		src = sceneSource{kind: "json", desc: fmt.Sprintf("%d file(s)", len(jsonFiles))}
		scenes, err = match.LoadJSONFiles(jsonFiles)
	case dbExplicit:
		src = sceneSource{kind: "db", desc: resolveDBPath(cmd)}
		scenes, studioNames, err = loadScenesFromDB(src.desc)
	case dirFlag != "":
		src = sceneSource{kind: "json", desc: dirFlag}
		scenes, err = match.LoadJSONDir(dirFlag)
	default:
		dir := ""
		if cfg != nil {
			dir = cfg.OutDir
		}
		src = sceneSource{kind: "json", desc: dir}
		scenes, err = match.LoadJSONDir(dir)
	}
	if err != nil {
		return nil, src, fmt.Errorf("loading FSS data: %w", err)
	}
	if len(scenes) == 0 {
		return nil, src, fmt.Errorf("no FSS scenes found in %s", src.desc)
	}

	studios, _ := cmd.Flags().GetStringSlice("from-studio")
	performers, _ := cmd.Flags().GetStringSlice("from-performer")
	creatorURLs, err := creatorStudioURLs(cmd)
	if err != nil {
		return nil, src, err
	}
	studios = append(studios, creatorURLs...)
	scenes, err = filterScenes(scenes, studios, performers, studioNames)
	if err != nil {
		return nil, src, err
	}
	return scenes, src, nil
}

// loadScenesFromDB reads every tracked studio out of the SQLite store. It also
// returns each studio URL's display name, which --from-studio can match on.
func loadScenesFromDB(path string) ([]models.Scene, map[string]string, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("--db is required (pass --db for the default location, or --db=/path/to/file.db)")
	}
	db, err := store.NewSQLite(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = db.Close() }()

	studios, err := db.ListStudios()
	if err != nil {
		return nil, nil, err
	}
	if len(studios) == 0 {
		return nil, nil, fmt.Errorf(
			"no studios tracked in %s — scrape with --db, or load existing JSON with `fss import --db`", path)
	}

	names := make(map[string]string, len(studios))
	var all []models.Scene
	for _, st := range studios {
		names[st.URL] = st.Name
		scenes, err := db.Load(st.URL)
		if err != nil {
			return nil, nil, fmt.Errorf("loading %s: %w", st.URL, err)
		}
		all = append(all, scenes...)
	}
	return all, names, nil
}

// filterScenes narrows the scene set. Within one flag any value matches (OR);
// across flags every flag must match (AND).
//
// Studio values resolve in three steps, most specific first:
//
//  1. a value containing "://" matches StudioURL exactly;
//  2. a value naming a per-scene Studio matches that sub-brand only;
//  3. otherwise it is looked up against the studios table's display names and
//     matches every scene under those URLs.
//
// Step 2 must precede step 3. A studio's display name is often *derived* from
// its first scene's Studio field (see studioFromFile and the --name fallback in
// scrape.go), so it is frequently a sub-brand rather than the network label.
// Matching display names first therefore made `--from-studio "SLR Originals"`
// silently select the entire network — every scene shares that studio URL.
func filterScenes(scenes []models.Scene, studios, performers []string, studioNames map[string]string) ([]models.Scene, error) {
	if len(studios) == 0 && len(performers) == 0 {
		return scenes, nil
	}

	studioURLs, studioKeys := resolveStudioFilters(scenes, studios, studioNames)
	performerKeys := map[string]bool{}
	for _, p := range performers {
		if k := match.NormalizeName(p); k != "" {
			performerKeys[k] = true
		}
	}

	out := make([]models.Scene, 0, len(scenes))
	for _, sc := range scenes {
		if len(studios) > 0 && !sceneMatchesStudio(sc, studioURLs, studioKeys) {
			continue
		}
		if len(performerKeys) > 0 && !sceneMatchesPerformer(sc, performerKeys) {
			continue
		}
		out = append(out, sc)
	}

	if len(out) == 0 {
		return nil, noMatchError(scenes, studios, performers, studioNames)
	}
	return out, nil
}

// resolveStudioFilters turns each --from-studio value into either a set of
// studio URLs or a set of sub-brand keys, applying the precedence documented on
// filterScenes.
func resolveStudioFilters(scenes []models.Scene, values []string, studioNames map[string]string) (urls, subBrands map[string]bool) {
	urls, subBrands = map[string]bool{}, map[string]bool{}

	knownSubBrand := map[string]bool{}
	for _, sc := range scenes {
		if k := match.NormalizeName(sc.Studio); k != "" {
			knownSubBrand[k] = true
		}
	}

	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if strings.Contains(v, "://") {
			urls[v] = true
			continue
		}
		key := match.NormalizeName(v)
		if key == "" {
			continue
		}
		if knownSubBrand[key] {
			subBrands[key] = true
			continue
		}
		for studioURL, name := range studioNames {
			if match.NormalizeName(name) == key {
				urls[studioURL] = true
			}
		}
	}
	return urls, subBrands
}

func sceneMatchesStudio(sc models.Scene, urls, subBrands map[string]bool) bool {
	return urls[sc.StudioURL] || subBrands[match.NormalizeName(sc.Studio)]
}

func sceneMatchesPerformer(sc models.Scene, keys map[string]bool) bool {
	for _, p := range sc.Performers {
		if keys[match.NormalizeName(p)] {
			return true
		}
	}
	return false
}

// noMatchError explains an empty filter result by listing what was actually
// available, so a typo or a stale name is immediately obvious.
func noMatchError(scenes []models.Scene, studios, performers []string, studioNames map[string]string) error {
	if len(studios) > 0 {
		available := map[string]bool{}
		for _, sc := range scenes {
			if sc.Studio != "" {
				available[sc.Studio] = true
			}
			if n := studioNames[sc.StudioURL]; n != "" {
				available[n] = true
			}
		}
		return fmt.Errorf("no scenes matched --from-studio %s\navailable studios: %s",
			strings.Join(studios, ", "), sampleNames(available, 15))
	}
	available := map[string]bool{}
	for _, sc := range scenes {
		for _, p := range sc.Performers {
			available[p] = true
		}
	}
	return fmt.Errorf("no scenes matched --from-performer %s\navailable performers: %s",
		strings.Join(performers, ", "), sampleNames(available, 15))
}

// sampleNames renders up to limit names, sorted, noting how many were elided.
func sampleNames(set map[string]bool, limit int) string {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	if len(names) == 0 {
		return "(none)"
	}
	sort.Strings(names)
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s … and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}

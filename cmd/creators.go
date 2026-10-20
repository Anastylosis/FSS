package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/internal/creators"
	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
)

var creatorsCmd = &cobra.Command{
	Use:   "creators",
	Short: "List the creators defined in creators.d",
	Long: `List the creators defined in creators.d.

A creator binds the several storefronts one person sells the same catalogue on,
so a single command can scrape all of them:

    fss scrape --creator "Jane Eyre"
    fss scrape --all-creators --stale 7d

Definitions live in one YAML file per creator (default ~/.config/fss/creators.d).
They carry no credentials, so a directory of them can be kept in git and shared;
point creators_dir at a clone to use someone else's set.`,
	RunE: runCreators,
}

var creatorsSuggestCmd = &cobra.Command{
	Use:   "suggest",
	Short: "Propose creators.d files by clustering the studios you already scrape",
	Long: `Propose creators.d files by clustering the studios you already scrape.

Studios are grouped when their names overlap (EmmaWoodhouse / Emma Woodhouse Films) or
when they share a dominant performer (Duchess Mina / Mina Harker). Both are
heuristics, so the proposals print to stdout for review; --write commits them.`,
	RunE: runCreatorsSuggest,
}

func init() {
	rootCmd.AddCommand(creatorsCmd)
	creatorsCmd.AddCommand(creatorsSuggestCmd)

	// Local, not persistent: `suggest` gets its own copy from
	// addSceneSourceFlags, and an inherited flag of the same name would shadow
	// it confusingly.
	creatorsCmd.Flags().String("creators-dir", "", "directory of creator YAML files (default: config creators_dir, else ~/.config/fss/creators.d)")

	addSceneSourceFlags(creatorsSuggestCmd)
	creatorsSuggestCmd.Flags().Bool("write", false, "write the proposals into the creators directory instead of printing them")
	creatorsSuggestCmd.Flags().Bool("force", false, "with --write, overwrite creator files that already exist")
	creatorsSuggestCmd.Flags().Bool("include-single", false, "also propose creators whose catalogue is on a single storefront")
}

// resolveCreatorsDir applies the usual precedence: the flag wins, then the
// config's creators_dir, then the conventional location (an empty string, which
// creators.Load resolves).
func resolveCreatorsDir(cmd *cobra.Command) string {
	if cmd != nil {
		// The flag is persistent on `creators`, and defined separately on the
		// commands that consume creators, so look it up defensively.
		if f := cmd.Flags().Lookup("creators-dir"); f != nil && f.Value.String() != "" {
			return f.Value.String()
		}
	}
	return cfg.CreatorsPath()
}

// loadCreators reads the creator set for a command, reporting where it looked
// when the set is empty — an absent directory is the normal state before the
// first `fss creators suggest --write`.
func loadCreators(cmd *cobra.Command) ([]creators.Creator, error) {
	dir := resolveCreatorsDir(cmd)
	list, err := creators.Load(dir)
	if err != nil {
		return nil, err
	}
	return list, nil
}

func runCreators(cmd *cobra.Command, _ []string) error {
	dir := resolveCreatorsDir(cmd)
	list, err := creators.Load(dir)
	if err != nil {
		return err
	}
	shown := dir
	if shown == "" {
		shown = creators.DefaultDir()
	}
	if len(list) == 0 {
		fmt.Printf("No creators defined in %s\n", shown)
		fmt.Println("Bootstrap them from the studios you already scrape:  fss creators suggest")
		return nil
	}

	// Last-scraped times come from the SQLite studios table when there is one;
	// the flat store has no such record, so the column is simply omitted.
	lastScraped := studioLastScraped()

	fmt.Printf("%d creator(s) in %s\n\n", len(list), shown)
	for _, c := range list {
		fmt.Printf("%s", c.Name)
		if len(c.Aliases) > 0 {
			fmt.Printf("  (aka %s)", strings.Join(c.Aliases, ", "))
		}
		fmt.Printf("\n")
		for _, s := range c.Stores {
			marks := make([]string, 0, 3)
			if !s.On() {
				marks = append(marks, "disabled")
			}
			if s.Delay != nil {
				marks = append(marks, fmt.Sprintf("delay %dms", *s.Delay))
			}
			if when, ok := lastScraped[s.URL]; ok {
				marks = append(marks, "scraped "+when.Format("2006-01-02"))
			}
			suffix := ""
			if len(marks) > 0 {
				suffix = "  [" + strings.Join(marks, ", ") + "]"
			}
			fmt.Printf("  %s%s\n", s.URL, suffix)
			if s.Note != "" {
				fmt.Printf("      note: %s\n", s.Note)
			}
		}
		fmt.Println()
	}
	return nil
}

// studioLastScraped returns each studio URL's last scrape time, or an empty map
// when no database is configured.
func studioLastScraped() map[string]time.Time {
	out := map[string]time.Time{}
	path := config.ResolveDBPath(configuredDB())
	if path == "" {
		return out
	}
	db, err := store.NewSQLite(path)
	if err != nil {
		return out
	}
	defer func() { _ = db.Close() }()
	studios, err := db.ListStudios()
	if err != nil {
		return out
	}
	for _, s := range studios {
		if s.LastScrapedAt != nil {
			out[s.URL] = *s.LastScrapedAt
		}
	}
	return out
}

// configuredDB returns the config's `db:` value, ignoring flags. Used by
// read-only conveniences that enrich output when a database happens to exist
// and stay silent when it does not.
func configuredDB() string {
	if cfg == nil {
		return ""
	}
	v, _ := cfg.DBSetting()
	return v
}

// --- suggest ---------------------------------------------------------------

// studioProfile is what suggest knows about one studio URL: how it labels
// itself, and who is in most of its scenes.
type studioProfile struct {
	url       string
	name      string // most common Scene.Studio
	scenes    int
	performer string // performer appearing in at least half the scenes, if any
}

func runCreatorsSuggest(cmd *cobra.Command, _ []string) error {
	scenes, src, err := loadFSSScenes(cmd)
	if err != nil {
		return err
	}

	profiles := profileStudios(scenes)
	if len(profiles) == 0 {
		return fmt.Errorf("no studios found in %s", src)
	}
	groups := clusterStudios(profiles)

	includeSingle, _ := cmd.Flags().GetBool("include-single")
	proposals := make([]creators.Creator, 0, len(groups))
	var singles []string
	for _, g := range groups {
		if len(g) < 2 {
			singles = append(singles, g[0].name)
			if !includeSingle {
				continue
			}
		}
		proposals = append(proposals, proposalFor(g))
	}
	sort.Strings(singles)
	sort.Slice(proposals, func(i, j int) bool {
		return creators.Key(proposals[i].Name) < creators.Key(proposals[j].Name)
	})

	if len(proposals) == 0 {
		fmt.Printf("Nothing to propose: every studio in %s stands alone.\n", src)
		fmt.Println("Pass --include-single to define one creator per studio anyway.")
		return nil
	}

	write, _ := cmd.Flags().GetBool("write")
	if !write {
		return printProposals(proposals, singles, includeSingle)
	}
	return writeProposals(cmd, proposals)
}

func printProposals(proposals []creators.Creator, singles []string, includeSingle bool) error {
	fmt.Printf("# %d creator(s) proposed from your scraped studios.\n", len(proposals))
	fmt.Println("# Review these — grouping is a heuristic. Write them with:  fss creators suggest --write")
	for _, c := range proposals {
		body, err := c.Marshal()
		if err != nil {
			return err
		}
		fmt.Printf("\n# --- %s ---\n%s", creators.Filename(c.Name), body)
	}
	if len(singles) > 0 && !includeSingle {
		// Named, not just counted. A storefront can be unmistakably someone's
		// to a human and invisible to both signals — a shop whose name shares
		// no substring with the others and which credits no performers at all
		// has nothing to match on. Reading the list is how those get found.
		fmt.Printf("\n# %d studio(s) grouped with nothing else and were omitted. If any of these\n"+
			"# belong to a creator above, add the URL to that file by hand:\n", len(singles))
		for _, n := range singles {
			fmt.Printf("#   %s\n", n)
		}
		fmt.Println("# (--include-single proposes them as creators of their own instead.)")
	}
	return nil
}

func writeProposals(cmd *cobra.Command, proposals []creators.Creator) error {
	dir := resolveCreatorsDir(cmd)
	if dir == "" {
		dir = creators.DefaultDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	force, _ := cmd.Flags().GetBool("force")
	written, skipped := 0, 0
	for _, c := range proposals {
		path := filepath.Join(dir, creators.Filename(c.Name))
		if !force {
			if _, err := os.Stat(path); err == nil {
				fmt.Printf("  skip   %s (already exists; --force to overwrite)\n", filepath.Base(path))
				skipped++
				continue
			}
		}
		body, err := c.Marshal()
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Printf("  write  %s (%d stores)\n", filepath.Base(path), len(c.Stores))
		written++
	}
	fmt.Printf("\n%d written, %d skipped in %s\n", written, skipped, dir)
	if written > 0 {
		fmt.Println("Check them over, then:  fss scrape --all-creators --stale 7d")
	}
	return nil
}

// profileStudios reduces the scene set to one profile per studio URL.
func profileStudios(scenes []models.Scene) []studioProfile {
	type acc struct {
		names      map[string]int
		performers map[string]int
		total      int
	}
	byURL := map[string]*acc{}
	for _, sc := range scenes {
		if sc.StudioURL == "" {
			continue
		}
		a := byURL[sc.StudioURL]
		if a == nil {
			a = &acc{names: map[string]int{}, performers: map[string]int{}}
			byURL[sc.StudioURL] = a
		}
		a.total++
		if sc.Studio != "" {
			a.names[sc.Studio]++
		}
		// Count each performer once per scene, so a repeated credit on one
		// scene cannot manufacture dominance.
		seen := map[string]bool{}
		for _, p := range sc.Performers {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			a.performers[p]++
		}
	}

	out := make([]studioProfile, 0, len(byURL))
	for url, a := range byURL {
		p := studioProfile{url: url, scenes: a.total}
		p.name, _ = mostCommon(a.names)
		if p.name == "" {
			p.name = url
		}
		name, n := mostCommon(a.performers)
		// Half the catalogue is a high bar for a multi-performer network and a
		// near-certainty for a solo creator's own storefront, which is exactly
		// the distinction this signal has to make.
		if n*2 >= a.total {
			p.performer = name
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].url < out[j].url })
	return out
}

// mostCommon returns the highest-counted key, breaking ties by the shorter and
// then alphabetically so the result does not depend on map iteration order.
func mostCommon(counts map[string]int) (string, int) {
	best, bestN := "", 0
	for k, n := range counts {
		switch {
		case n > bestN:
			best, bestN = k, n
		case n == bestN && best != "":
			if len(k) < len(best) || (len(k) == len(best) && k < best) {
				best = k
			}
		}
	}
	return best, bestN
}

// minNameOverlap is the shortest studio-name key allowed to merge two studios by
// containment. Below it the signal is noise — a two-letter name is a substring
// of half the catalogue.
const minNameOverlap = 5

// clusterStudios groups studio profiles that plausibly belong to one person.
//
// Two signals, both needed in practice. Name containment catches the common
// case where one person's storefronts label themselves "EmmaWoodhouse", "Emma Woodhouse"
// and "Emma Woodhouse Films". A shared dominant performer catches the case where
// the names have nothing in common — "Duchess Mina" on one site and "Mina Harker"
// on three others.
func clusterStudios(profiles []studioProfile) [][]studioProfile {
	parent := make([]int, len(profiles))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) { parent[find(a)] = find(b) }

	byPerformer := map[string][]int{}
	for i, p := range profiles {
		if p.performer == "" {
			continue
		}
		k := creators.Key(p.performer)
		byPerformer[k] = append(byPerformer[k], i)
	}
	for _, idx := range byPerformer {
		for _, i := range idx[1:] {
			union(idx[0], i)
		}
	}

	for i := range profiles {
		ki := creators.Key(profiles[i].name)
		for j := i + 1; j < len(profiles); j++ {
			kj := creators.Key(profiles[j].name)
			short, long := ki, kj
			if len(short) > len(long) {
				short, long = long, short
			}
			if len(short) >= minNameOverlap && strings.Contains(long, short) {
				union(i, j)
			}
		}
	}

	grouped := map[int][]studioProfile{}
	for i, p := range profiles {
		root := find(i)
		grouped[root] = append(grouped[root], p)
	}
	out := make([][]studioProfile, 0, len(grouped))
	for _, g := range grouped {
		sort.Slice(g, func(a, b int) bool { return g[a].scenes > g[b].scenes })
		out = append(out, g)
	}
	return out
}

// proposalFor turns a cluster into a Creator. The name is the dominant
// performer shared by the group when there is one — that is the person's actual
// name, where a storefront label is often a brand ("Emma Woodhouse Films") or a
// handle ("odettelang").
func proposalFor(group []studioProfile) creators.Creator {
	perfCounts := map[string]int{}
	for _, p := range group {
		if p.performer != "" {
			perfCounts[p.performer]++
		}
	}
	name, _ := mostCommon(perfCounts)
	if name == "" {
		// No dominant performer anywhere: fall back to the shortest studio
		// label, which is the one least likely to carry a brand suffix.
		name = group[0].name
		for _, p := range group[1:] {
			if len(p.name) < len(name) {
				name = p.name
			}
		}
	}

	c := creators.Creator{Name: name}
	seenAlias := map[string]bool{creators.Key(name): true}
	for _, p := range group {
		c.Stores = append(c.Stores, creators.Store{URL: p.url})
		// Every distinct spelling the storefronts use becomes an alias, so
		// --creator matches whichever one the operator remembers.
		if k := creators.Key(p.name); k != "" && !seenAlias[k] {
			seenAlias[k] = true
			c.Aliases = append(c.Aliases, p.name)
		}
	}
	return c
}

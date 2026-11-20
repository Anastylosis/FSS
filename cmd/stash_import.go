package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	stash "github.com/Anastylosis/stash-go"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/mediafetch"
	"github.com/Anastylosis/FSS/match"
)

// coverHTTPClient fetches cover images. Plain and pooled: mediafetch drives
// its own retries.
var coverHTTPClient = httpx.NewClient(30 * time.Second)

var stashImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Match FSS JSON scenes against Stash and push metadata",
	Long: `Match FSS JSON scenes against Stash scenes by filename, then push metadata.

Default is dry-run — shows what would change. Pass --apply to actually write to Stash.`,
	RunE: runStashImport,
}

func init() {
	stashCmd.AddCommand(stashImportCmd)

	addSceneSourceFlags(stashImportCmd)
	stashImportCmd.Flags().String("tag", "", "import marker tag (default from config)")
	stashImportCmd.Flags().Bool("resolution-tags", false, "add resolution tags (4K/FHD/HD Available)")
	stashImportCmd.Flags().Bool("organized", false, "set organized flag on imported scenes")
	stashImportCmd.Flags().Bool("include-stashbox", false, "also process scenes that have StashDB data")
	stashImportCmd.Flags().String("stashbox-tag", "", "tag for stashbox overrides (default from config)")
	stashImportCmd.Flags().Bool("cover", false, "set cover image from FSS thumbnail (also implicitly enabled when 'cover' is in --fields)")
	stashImportCmd.Flags().Bool("cover-allow-private", false, "allow cover URLs that resolve to private/loopback IPs (for local media servers); disabled by default to prevent SSRF when importing third-party JSON")
	stashImportCmd.Flags().Bool("apply", false, "actually write changes (default is dry-run)")
	stashImportCmd.Flags().String("performer", "", "filter Stash scenes by performer name")
	stashImportCmd.Flags().String("studio", "", "filter Stash scenes by studio name")
	stashImportCmd.Flags().String("filter", "", "filter Stash scenes by substring in file path")
	stashImportCmd.Flags().Int("top", 0, "limit number of Stash scenes to process (0 = all)")
	stashImportCmd.Flags().StringSlice("fields", nil, "only update these fields (title,details,date,urls,tags,performers,studio,cover); default: all")
}

type importStats struct {
	total     int
	matched   int
	updated   int
	skipped   int
	ambiguous int
	upToDate  int
	partial   int // UpdateScene succeeded but one or more Ensure*/cover calls failed
	failed    int // UpdateScene itself failed
}

// importFailure records a per-scene operation that did not complete.
// Ensure* failures inside a scene that still updates result in a "partial"
// stat; UpdateScene failures result in a "failed" stat.
type importFailure struct {
	SceneID  string
	Filename string
	Op       string // e.g. "tag", "performer", "studio", "cover", "update"
	Name     string // the name that failed (tag name, performer name, etc.); empty for "update"
	Err      error
}

type changelogEntry struct {
	StashSceneID string                        `json:"stash_scene_id"`
	Timestamp    time.Time                     `json:"timestamp"`
	Filename     string                        `json:"filename"`
	MatchedTo    string                        `json:"matched_to"`
	Changes      map[string]changelogFieldDiff `json:"changes"`
}

type changelogFieldDiff struct {
	From  any      `json:"from,omitempty"`
	To    any      `json:"to,omitempty"`
	Added []string `json:"added,omitempty"`
	// AddedIDs are the Stash entity IDs the import actually added (tags,
	// performers); revert prefers them over the names in Added.
	AddedIDs []string `json:"added_ids,omitempty"`
	// FromID is the ID of the studio the scene had before (studio only).
	FromID string `json:"from_id,omitempty"`
}

type importOpts struct {
	apply             bool
	setCover          bool
	coverAllowPrivate bool
	includeStashbox   bool
	organized         bool
	resolutionTags    bool
	tagName           string
	stashboxTag       string
	allowedFields     map[string]bool
	fieldsList        []string
	performer         string
	studio            string
	pathFilter        string
	top               int
}

func resolveImportOpts(cmd *cobra.Command) (importOpts, error) {
	var o importOpts
	o.apply, _ = cmd.Flags().GetBool("apply")
	o.setCover, _ = cmd.Flags().GetBool("cover")
	o.coverAllowPrivate, _ = cmd.Flags().GetBool("cover-allow-private")
	o.includeStashbox, _ = cmd.Flags().GetBool("include-stashbox")
	o.organized, _ = cmd.Flags().GetBool("organized")

	o.resolutionTags, _ = cmd.Flags().GetBool("resolution-tags")
	if !cmd.Flags().Changed("resolution-tags") {
		o.resolutionTags = cfg.Stash.ResolutionTags
	}

	o.tagName, _ = cmd.Flags().GetString("tag")
	if o.tagName == "" {
		o.tagName = cfg.Stash.Tag
	}

	o.stashboxTag, _ = cmd.Flags().GetString("stashbox-tag")
	if o.stashboxTag == "" {
		o.stashboxTag = cfg.Stash.StashboxTag
	}

	o.fieldsList, _ = cmd.Flags().GetStringSlice("fields")
	var err error
	o.allowedFields, err = parseFieldsFlag(o.fieldsList)
	if err != nil {
		return o, err
	}

	o.setCover = resolveCoverEnabled(o.setCover, o.allowedFields)

	o.performer, _ = cmd.Flags().GetString("performer")
	o.studio, _ = cmd.Flags().GetString("studio")
	o.pathFilter, _ = cmd.Flags().GetString("filter")
	o.top, _ = cmd.Flags().GetInt("top")
	return o, nil
}

func loadFSSIndex(cmd *cobra.Command) (*match.SceneIndex, error) {
	fmt.Print("Loading FSS scenes...")
	scenes, src, err := loadFSSScenes(cmd)
	if err != nil {
		fmt.Println()
		return nil, err
	}
	fmt.Printf(" from %s\n", src)
	fmt.Printf("Loaded %d FSS scenes\n", len(scenes))
	return match.BuildIndex(scenes), nil
}

// changelogDir is where the stashbox changelog is written and where
// `fss stash revert` looks for it. It is deliberately independent of where
// scenes were loaded from: with --db there is no scene directory, but the
// changelog still needs the same stable home revert expects.
func changelogDir(cmd *cobra.Command) string {
	if dir, _ := cmd.Flags().GetString("dir"); dir != "" {
		return dir
	}
	if cfg != nil {
		return cfg.OutDir
	}
	return "."
}

func queryStashScenes(ctx context.Context, client *stash.Client, o importOpts) ([]stash.Scene, error) {
	filter := stash.SceneFilter{
		PerformerName: o.performer,
		StudioName:    o.studio,
		PathContains:  o.pathFilter,
	}
	if !o.includeStashbox {
		unmatched := false
		filter.HasStashID = &unmatched
	}

	fmt.Print("Querying Stash scenes...")
	var scenes []stash.Scene
	var err error
	if o.top > 0 {
		scenes, _, err = client.FindScenes(ctx, filter, 1, o.top)
	} else {
		scenes, err = client.FindAllScenes(ctx, filter, func(fetched, total int) {
			fmt.Printf("\rQuerying Stash scenes... %d / %d", fetched, total)
		})
	}
	fmt.Println()
	if err != nil {
		// A filter naming something Stash does not have is a user error worth
		// stating plainly — it used to look identical to "nothing matched".
		if errors.Is(err, stash.ErrPerformerNotFound) || errors.Is(err, stash.ErrStudioNotFound) {
			return nil, fmt.Errorf("%w (Stash matches names exactly — check spelling and trailing spaces)", err)
		}
		return nil, fmt.Errorf("querying stash scenes: %w", err)
	}
	fmt.Printf("Found %d Stash scenes to process\n", len(scenes))
	return scenes, nil
}

func diffScene(ss stash.Scene, merged match.MergedScene, o importOpts) (map[string]changelogFieldDiff, []string, []string) {
	allTags := append([]string(nil), merged.Tags...)
	allTags = append(allTags, merged.Categories...)
	allTags = append(allTags, o.tagName)
	if ss.HasStashID() {
		allTags = append(allTags, o.stashboxTag)
	}
	if o.resolutionTags {
		allTags = append(allTags, match.ResolutionTags(merged.Width)...)
	}
	allTags = dedupNames(allTags)

	mergedURLs := match.MergeStrings(ss.URLs, merged.URLs)

	changes := buildChanges(ss, merged, mergedURLs, allTags, o.setCover, o.organized)
	if o.allowedFields != nil {
		for field := range changes {
			if !o.allowedFields[field] {
				delete(changes, field)
			}
		}
	}
	return changes, allTags, mergedURLs
}

// sceneResult is what applyScene wrote beyond the failures: the entity IDs it
// added to the scene, for the changelog.
type sceneResult struct {
	failures     []importFailure
	addedTagIDs  []string
	addedPerfIDs []string
}

func applyScene(ctx context.Context, l *entityLookup, ss stash.Scene, merged match.MergedScene,
	allTags []string, mergedURLs []string, importTagID string, o importOpts,
) (sceneResult, error) {
	client := l.client
	filename := stashFilename(ss)
	var res sceneResult
	mark := len(l.created)
	input := stash.SceneUpdate{ID: ss.ID}
	var sceneFailures []importFailure
	hasStashbox := ss.HasStashID()

	if fieldAllowed(o.allowedFields, "title") {
		input.Title = strPtr(merged.Title)
	}
	if fieldAllowed(o.allowedFields, "details") && merged.Description != "" {
		input.Details = strPtr(merged.Description)
	}
	if fieldAllowed(o.allowedFields, "date") && !merged.Date.IsZero() {
		d := merged.Date.Format("2006-01-02")
		input.Date = &d
	}
	if fieldAllowed(o.allowedFields, "urls") {
		input.URLs = mergedURLs
	}
	if fieldAllowed(o.allowedFields, "tags") {
		tagIDs := extractTagIDs(ss.Tags)
		tagIDs = append(tagIDs, importTagID)
		if hasStashbox {
			sbTagID, tagErr := l.ensureTag(o.stashboxTag)
			if tagErr != nil {
				sceneFailures = append(sceneFailures, importFailure{
					SceneID: ss.ID, Filename: filename, Op: "tag (stashbox)", Name: o.stashboxTag, Err: tagErr,
				})
			} else {
				tagIDs = append(tagIDs, sbTagID)
			}
		}
		for _, t := range allTags {
			tid, tagErr := l.ensureTag(t)
			if tagErr != nil {
				sceneFailures = append(sceneFailures, importFailure{
					SceneID: ss.ID, Filename: filename, Op: "tag", Name: t, Err: tagErr,
				})
				continue
			}
			tagIDs = append(tagIDs, tid)
		}
		input.TagIDs = dedup(tagIDs)
		res.addedTagIDs = subtractIDs(input.TagIDs, extractTagIDs(ss.Tags))
	}
	if fieldAllowed(o.allowedFields, "performers") {
		perfIDs := extractPerfIDs(ss.Performers)
		for _, p := range merged.Performers {
			pid, perfErr := l.ensurePerformer(p)
			if perfErr != nil {
				sceneFailures = append(sceneFailures, importFailure{
					SceneID: ss.ID, Filename: filename, Op: "performer", Name: p, Err: perfErr,
				})
				continue
			}
			perfIDs = append(perfIDs, pid)
		}
		input.PerformerIDs = dedup(perfIDs)
		res.addedPerfIDs = subtractIDs(input.PerformerIDs, extractPerfIDs(ss.Performers))
	}
	if fieldAllowed(o.allowedFields, "studio") && merged.Studio != "" && !keepStashboxStudio(ss) {
		sid, studioErr := l.ensureStudio(merged.Studio)
		if studioErr != nil {
			sceneFailures = append(sceneFailures, importFailure{
				SceneID: ss.ID, Filename: filename, Op: "studio", Name: merged.Studio, Err: studioErr,
			})
		} else {
			input.StudioID = &sid
		}
	}
	if o.organized && fieldAllowed(o.allowedFields, "organized") {
		input.Organized = &o.organized
	}
	if fieldAllowed(o.allowedFields, "cover") && o.setCover && merged.Thumbnail != "" {
		coverData, coverErr := mediafetch.DataURI(ctx, coverHTTPClient, merged.Thumbnail, o.coverAllowPrivate)
		if coverErr != nil {
			sceneFailures = append(sceneFailures, importFailure{
				SceneID: ss.ID, Filename: filename, Op: "cover", Name: merged.Thumbnail, Err: coverErr,
			})
		} else {
			input.CoverImage = &coverData
		}
	}

	if err := client.UpdateScene(ctx, input); err != nil {
		l.rollbackCreated(mark)
		return sceneResult{}, err
	}
	res.failures = sceneFailures
	return res, nil
}

func runStashImport(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := newStashClient(cmd)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("connecting to stash: %w", err)
	}
	fmt.Println("Connected to Stash")

	idx, err := loadFSSIndex(cmd)
	if err != nil {
		return err
	}

	o, err := resolveImportOpts(cmd)
	if err != nil {
		return err
	}

	stashScenes, err := queryStashScenes(ctx, client, o)
	if err != nil {
		return err
	}

	if !o.apply {
		fmt.Print("\n--- DRY RUN (pass --apply to write changes) ---\n")
		if o.allowedFields != nil {
			fmt.Printf("Fields: %s\n", strings.Join(o.fieldsList, ", "))
		}
		fmt.Println()
	}

	lookup := newEntityLookup(ctx, client)
	var importTagID string
	if o.apply && fieldAllowed(o.allowedFields, "tags") {
		importTagID, err = lookup.ensureTag(o.tagName)
		if err != nil {
			return fmt.Errorf("ensuring import tag %q: %w", o.tagName, err)
		}
	}

	clw := &changelogWriter{dir: changelogDir(cmd)}
	defer func() { _ = clw.flush() }()
	run := &importRun{
		o: o, idx: idx, lookup: lookup, clw: clw, importTagID: importTagID,
		stashBase: stashURL(cmd), stats: importStats{total: len(stashScenes)},
	}

	for _, ss := range stashScenes {
		if ctx.Err() != nil {
			break
		}
		run.one(ctx, ss)
	}
	stats, failures := run.stats, run.failures

	if err := clw.flush(); err != nil {
		return fmt.Errorf("stashbox changelog not written: %w (changes already applied to Stash are NOT recorded, so revert cannot undo them)", err)
	}

	if o.apply {
		printFailureSummary(failures)
	} else {
		printWouldCreateSummary(lookup)
	}

	fmt.Println()
	if o.apply {
		fmt.Printf("Done: %d matched, %d updated, %d partial, %d failed, %d already up-to-date, %d skipped, %d ambiguous\n",
			stats.matched, stats.updated, stats.partial, stats.failed, stats.upToDate, stats.skipped, stats.ambiguous)
	} else {
		fmt.Printf("Dry-run: %d would match, %d already up-to-date, %d skipped, %d ambiguous\n",
			stats.matched, stats.upToDate, stats.skipped, stats.ambiguous)
	}
	if o.apply && stats.failed > 0 {
		return &exitError{code: ExitFailure, err: fmt.Errorf("%d scene(s) failed to import", stats.failed), reported: true}
	}
	if o.apply && ctx.Err() != nil {
		return &exitError{code: ExitInterrupted, err: errors.New("interrupted"), reported: true}
	}
	return nil
}

// importRun carries the state one stash import accumulates across scenes.
type importRun struct {
	o           importOpts
	idx         *match.SceneIndex
	lookup      *entityLookup
	clw         *changelogWriter
	importTagID string
	stashBase   string
	stats       importStats
	failures    []importFailure
}

// one matches, merges and diffs a single Stash scene, then either prints the
// dry-run plan or applies it and records the changelog entry.
func (r *importRun) one(ctx context.Context, ss stash.Scene) {
	filename := stashFilename(ss)
	if filename == "" {
		r.stats.skipped++
		return
	}

	var fileDuration float64
	if pf := ss.PrimaryFile(); pf != nil {
		fileDuration = pf.Duration
	}
	result := r.idx.Match(filename, fileDuration)

	switch result.Confidence {
	case match.MatchNone:
		r.stats.skipped++
		return
	case match.MatchAmbiguous:
		r.stats.ambiguous++
		fmt.Printf("  AMBIGUOUS  %-50s  →  %d candidates, skipped\n\n", truncate(filename, 50), result.Candidates)
		return
	}

	var existingDate time.Time
	if ss.Date != "" {
		existingDate, _ = time.Parse("2006-01-02", ss.Date)
	}

	merged := match.MergeScenes(result.Scenes, existingDate)
	sites := strings.Join(merged.Sites, " + ")
	fmt.Printf("  %-10s %-50s  →  %q (%s)\n", result.Confidence, truncate(filename, 50), merged.Title, sites)
	fmt.Printf("           %s/scenes/%s\n", r.stashBase, ss.ID)

	changes, allTags, mergedURLs := diffScene(ss, merged, r.o)
	changes = dropAlreadyPresent(changes, ss, r.lookup)
	if len(changes) == 0 {
		r.stats.upToDate++
		return
	}

	r.stats.matched++

	if !r.o.apply {
		for field, diff := range changes {
			if len(diff.Added) > 0 {
				fmt.Printf("    %s: +%v\n", field, diff.Added)
			} else {
				fmt.Printf("    %s: %v → %v\n", field, diff.From, diff.To)
			}
		}
		printMergeConflicts(merged)
		if fieldAllowed(r.o.allowedFields, "tags") {
			for _, t := range allTags {
				r.lookup.checkTag(t)
			}
		}
		if fieldAllowed(r.o.allowedFields, "performers") {
			for _, p := range merged.Performers {
				r.lookup.checkPerformer(p)
			}
		}
		if fieldAllowed(r.o.allowedFields, "studio") && merged.Studio != "" && !keepStashboxStudio(ss) {
			r.lookup.checkStudio(merged.Studio)
		}
		fmt.Println()
		return
	}

	applied, updateErr := applyScene(ctx, r.lookup, ss, merged, allTags, mergedURLs, r.importTagID, r.o)
	sceneFailures := applied.failures
	if updateErr != nil {
		r.stats.failed++
		r.failures = append(r.failures, importFailure{
			SceneID: ss.ID, Filename: filename, Op: "update", Err: updateErr,
		})
		return
	}

	if len(sceneFailures) > 0 {
		r.stats.partial++
		r.failures = append(r.failures, sceneFailures...)
	}

	if ss.HasStashID() {
		// Record what actually landed, not the planned diff: prune tag /
		// performer names whose Ensure* failed so a later revert doesn't
		// strip entities that were never imported.
		recorded := recordAddedIDs(reconcileChanges(changes, sceneFailures), applied)
		if len(recorded) > 0 {
			r.clw.add(changelogEntry{
				StashSceneID: ss.ID,
				Timestamp:    time.Now().UTC(),
				Filename:     filename,
				MatchedTo:    merged.Title,
				Changes:      recorded,
			})
		}
	}

	r.stats.updated++

}

// dropAlreadyPresent removes from a diff the tags, performers and studio that
// the scene already has under a different spelling (a tag alias, or a name
// differing only in case Stash resolves to the same entity).
func dropAlreadyPresent(changes map[string]changelogFieldDiff, ss stash.Scene, l *entityLookup) map[string]changelogFieldDiff {
	if len(changes) == 0 {
		return changes
	}
	haveTags := idSet(extractTagIDs(ss.Tags))
	havePerfs := idSet(extractPerfIDs(ss.Performers))
	for field, diff := range changes {
		switch field {
		case "tags":
			diff.Added = filterNames(diff.Added, func(n string) bool {
				id, found, err := l.findTag(n)
				return err == nil && found && haveTags[id]
			})
		case "performers":
			diff.Added = filterNames(diff.Added, func(n string) bool {
				id, found, err := l.findPerformer(n)
				return err == nil && found && havePerfs[id]
			})
		case "studio":
			if ss.Studio != nil {
				if id, found, err := l.findStudio(stringValue(diff.To)); err == nil && found && id == ss.Studio.ID {
					delete(changes, field)
				}
			}
			continue
		default:
			continue
		}
		if len(diff.Added) == 0 {
			delete(changes, field)
		} else {
			changes[field] = diff
		}
	}
	return changes
}

// filterNames returns names with every element for which drop returns true removed.
func filterNames(names []string, drop func(string) bool) []string {
	var out []string
	for _, n := range names {
		if !drop(n) {
			out = append(out, n)
		}
	}
	return out
}

func idSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// recordAddedIDs stores the entity IDs a scene actually gained, so revert
// removes exactly those. A tags/performers entry that gained nothing is dropped.
func recordAddedIDs(changes map[string]changelogFieldDiff, applied sceneResult) map[string]changelogFieldDiff {
	out := make(map[string]changelogFieldDiff, len(changes))
	for field, diff := range changes {
		switch field {
		case "tags":
			if len(applied.addedTagIDs) == 0 {
				continue
			}
			diff.AddedIDs = applied.addedTagIDs
		case "performers":
			if len(applied.addedPerfIDs) == 0 {
				continue
			}
			diff.AddedIDs = applied.addedPerfIDs
		}
		out[field] = diff
	}
	return out
}

// stashFilename is the primary file's basename as Stash reports it; the path
// is split on both separators only when Stash sent no basename, since a
// Windows server's paths do not split on a Unix client's separator.
func stashFilename(ss stash.Scene) string {
	pf := ss.PrimaryFile()
	if pf == nil {
		return ""
	}
	if pf.Basename != "" {
		return pf.Basename
	}
	p := pf.Path
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// dedupNames drops names whose canonical key was already seen, keeping the first spelling.
func dedupNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		k := match.NormalizeName(n)
		if !seen[k] {
			seen[k] = true
			out = append(out, n)
		}
	}
	return out
}

func buildChanges(ss stash.Scene, merged match.MergedScene, mergedURLs []string, newTags []string, setCover, organized bool) map[string]changelogFieldDiff {
	changes := map[string]changelogFieldDiff{}

	// Emit an "organized" change only when --organized is set and the scene
	// isn't already organized — otherwise an already-up-to-date scene has an
	// empty diff and applyScene (the only place input.Organized is set) is
	// skipped, making the flag a silent no-op.
	if organized && !ss.Organized {
		changes["organized"] = changelogFieldDiff{From: ss.Organized, To: true}
	}

	if merged.Title != "" && merged.Title != ss.Title {
		changes["title"] = changelogFieldDiff{From: ss.Title, To: merged.Title}
	}
	if merged.Description != "" && merged.Description != ss.Details {
		changes["details"] = changelogFieldDiff{From: truncate(ss.Details, 60), To: truncate(merged.Description, 60)}
	}

	if !merged.Date.IsZero() {
		newDate := merged.Date.Format("2006-01-02")
		if newDate != ss.Date {
			changes["date"] = changelogFieldDiff{From: ss.Date, To: newDate}
		}
	}

	addedURLs := diffStrings(ss.URLs, mergedURLs)
	if len(addedURLs) > 0 {
		changes["urls"] = changelogFieldDiff{Added: addedURLs}
	}

	if setCover && merged.Thumbnail != "" {
		changes["cover"] = changelogFieldDiff{To: truncate(merged.Thumbnail, 60)}
	}

	existingTagNames := make(map[string]bool, len(ss.Tags))
	for _, t := range ss.Tags {
		existingTagNames[match.NormalizeName(t.Name)] = true
	}
	var addedTags []string
	for _, t := range newTags {
		if !existingTagNames[match.NormalizeName(t)] {
			addedTags = append(addedTags, t)
		}
	}
	if len(addedTags) > 0 {
		changes["tags"] = changelogFieldDiff{Added: addedTags}
	}

	existingPerfNames := make(map[string]bool, len(ss.Performers))
	for _, p := range ss.Performers {
		existingPerfNames[match.NormalizeName(p.Name)] = true
	}
	var addedPerfs []string
	for _, p := range merged.Performers {
		if !existingPerfNames[match.NormalizeName(p)] {
			addedPerfs = append(addedPerfs, p)
		}
	}
	if len(addedPerfs) > 0 {
		changes["performers"] = changelogFieldDiff{Added: addedPerfs}
	}

	if merged.Studio != "" && !keepStashboxStudio(ss) {
		existingStudio, existingStudioID := "", ""
		if ss.Studio != nil {
			existingStudio, existingStudioID = ss.Studio.Name, ss.Studio.ID
		}
		if match.NormalizeName(merged.Studio) != match.NormalizeName(existingStudio) {
			changes["studio"] = changelogFieldDiff{From: existingStudio, To: merged.Studio, FromID: existingStudioID}
		}
	}

	return changes
}

// keepStashboxStudio reports whether a scene's studio must be left alone: it is
// linked to a stash-box, which is the authority on which studio a scene
// belongs to, and it already has one. Storefront data only names the selling
// account - a creator's LoyalFans account "Tara Tainton TV" is not a studio -
// so --include-stashbox may add titles, URLs and tags but must not move the
// scene out of the studio StashDB put it in.
func keepStashboxStudio(ss stash.Scene) bool {
	return ss.HasStashID() && ss.Studio != nil && ss.Studio.Name != ""
}

// reconcileChanges returns the changelog diff that reflects what was actually
// written. Tag/performer names whose Ensure* call failed (captured in failures)
// are dropped from their Added lists, and a field whose Added list empties is
// removed entirely. Scalars and URLs are written atomically by UpdateScene, so
// they pass through unchanged. The input map is not mutated.
func reconcileChanges(changes map[string]changelogFieldDiff, failures []importFailure) map[string]changelogFieldDiff {
	failedTags := map[string]bool{}
	failedPerfs := map[string]bool{}
	for _, f := range failures {
		switch {
		case strings.HasPrefix(f.Op, "tag"): // "tag" and "tag (stashbox)"
			failedTags[f.Name] = true
		case f.Op == "performer":
			failedPerfs[f.Name] = true
		}
	}
	if len(failedTags) == 0 && len(failedPerfs) == 0 {
		return changes
	}

	out := make(map[string]changelogFieldDiff, len(changes))
	for field, diff := range changes {
		switch field {
		case "tags":
			diff.Added = dropNames(diff.Added, failedTags)
			if len(diff.Added) == 0 {
				continue
			}
		case "performers":
			diff.Added = dropNames(diff.Added, failedPerfs)
			if len(diff.Added) == 0 {
				continue
			}
		}
		out[field] = diff
	}
	return out
}

// dropNames returns names with any element present in drop filtered out,
// preserving order. The input slice is not mutated.
func dropNames(names []string, drop map[string]bool) []string {
	if len(drop) == 0 {
		return names
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !drop[n] {
			out = append(out, n)
		}
	}
	return out
}

func diffStrings(existing, merged []string) []string {
	set := make(map[string]bool, len(existing))
	for _, s := range existing {
		set[s] = true
	}
	var added []string
	for _, s := range merged {
		if !set[s] {
			added = append(added, s)
		}
	}
	return added
}

const changelogFlushEvery = 20

// changelogWriter batches entries and writes them every few scenes, so a crash
// partway through a long --apply loses at most one batch rather than the run.
type changelogWriter struct {
	dir     string
	pending []changelogEntry
}

func (w *changelogWriter) add(e changelogEntry) {
	w.pending = append(w.pending, e)
	if len(w.pending) >= changelogFlushEvery {
		if err := w.flush(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write stashbox changelog (will retry): %v\n", err)
		}
	}
}

func (w *changelogWriter) flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	if err := appendChangelog(w.dir, w.pending); err != nil {
		return err
	}
	w.pending = nil
	return nil
}

// changelogMu serialises writers within one process; the file lock covers
// other processes (a no-op on Windows).
var changelogMu sync.Mutex

// appendChangelog adds entries to the changelog under a file lock, creating the
// directory and replacing the file atomically.
func appendChangelog(dir string, entries []changelogEntry) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating changelog directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, "fss-stashbox-changelog.json")

	changelogMu.Lock()
	defer changelogMu.Unlock()
	unlock, err := lockChangelog(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	var existing []changelogEntry
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// First run — no existing changelog, start fresh.
	case err != nil:
		return fmt.Errorf("reading changelog %s: %w", path, err)
	default:
		if err := json.Unmarshal(data, &existing); err != nil {
			backup := filepath.Join(dir, fmt.Sprintf("fss-stashbox-changelog.corrupt-%s.json", time.Now().UTC().Format("20060102-150405")))
			if renameErr := os.Rename(path, backup); renameErr != nil {
				return fmt.Errorf("changelog %s is corrupt and could not be backed up to %s: %w", path, backup, renameErr)
			}
			fmt.Fprintf(os.Stderr, "warning: changelog %s was corrupt (%v); backed up to %s and starting fresh\n", path, err, backup)
			existing = nil
		}
	}

	existing = append(existing, entries...)
	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out, 0o600)
}

// writeFileAtomic writes via a temp file in the same directory and renames it
// over path, so a crash leaves either the old file or the new one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fss-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func extractTagIDs(tags []stash.Tag) []string {
	ids := make([]string, len(tags))
	for i, t := range tags {
		ids[i] = t.ID
	}
	return ids
}

func extractPerfIDs(perfs []stash.Performer) []string {
	ids := make([]string, len(perfs))
	for i, p := range perfs {
		ids[i] = p.ID
	}
	return ids
}

func dedup(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func strPtr(s string) *string { return &s }

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-3]) + "..."
}

var validImportFields = map[string]bool{
	"title": true, "details": true, "date": true, "urls": true,
	"tags": true, "performers": true, "studio": true, "cover": true,
	"organized": true,
}

func parseFieldsFlag(fields []string) (map[string]bool, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	m := make(map[string]bool, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if !validImportFields[f] {
			return nil, fmt.Errorf("unknown field %q (valid: title,details,date,urls,tags,performers,studio,cover)", f)
		}
		m[f] = true
	}
	return m, nil
}

func fieldAllowed(allowed map[string]bool, field string) bool {
	return allowed == nil || allowed[field]
}

// resolveCoverEnabled returns the effective value for the cover-update toggle,
// implicitly enabling it when --fields explicitly lists "cover" so the user
// doesn't have to pass both --cover and --fields cover. The reverse case
// (--cover with --fields not listing cover) still skips cover, because the
// fields filter is a hard allowlist.
func resolveCoverEnabled(setCoverFlag bool, allowedFields map[string]bool) bool {
	if setCoverFlag {
		return true
	}
	return allowedFields != nil && allowedFields["cover"]
}

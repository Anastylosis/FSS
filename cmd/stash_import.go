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
	"sort"
	"strings"
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
	allTags := merged.Tags
	allTags = append(allTags, merged.Categories...)
	allTags = append(allTags, o.tagName)
	if len(ss.StashIDs) > 0 {
		allTags = append(allTags, o.stashboxTag)
	}
	if o.resolutionTags {
		allTags = append(allTags, match.ResolutionTags(merged.Width)...)
	}

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

func applyScene(ctx context.Context, client *stash.Client, ss stash.Scene, merged match.MergedScene,
	allTags []string, mergedURLs []string, importTagID string, o importOpts,
) ([]importFailure, error) {
	filename := filepath.Base(ss.Files[0].Path)
	input := stash.SceneUpdate{ID: ss.ID}
	var sceneFailures []importFailure
	hasStashbox := len(ss.StashIDs) > 0

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
			sbTagID, tagErr := client.EnsureTag(ctx, o.stashboxTag)
			if tagErr != nil {
				sceneFailures = append(sceneFailures, importFailure{
					SceneID: ss.ID, Filename: filename, Op: "tag (stashbox)", Name: o.stashboxTag, Err: tagErr,
				})
			} else {
				tagIDs = append(tagIDs, sbTagID)
			}
		}
		for _, t := range allTags {
			tid, tagErr := client.EnsureTag(ctx, t)
			if tagErr != nil {
				sceneFailures = append(sceneFailures, importFailure{
					SceneID: ss.ID, Filename: filename, Op: "tag", Name: t, Err: tagErr,
				})
				continue
			}
			tagIDs = append(tagIDs, tid)
		}
		input.TagIDs = dedup(tagIDs)
	}
	if fieldAllowed(o.allowedFields, "performers") {
		perfIDs := extractPerfIDs(ss.Performers)
		for _, p := range merged.Performers {
			pid, perfErr := client.EnsurePerformer(ctx, p)
			if perfErr != nil {
				sceneFailures = append(sceneFailures, importFailure{
					SceneID: ss.ID, Filename: filename, Op: "performer", Name: p, Err: perfErr,
				})
				continue
			}
			perfIDs = append(perfIDs, pid)
		}
		input.PerformerIDs = dedup(perfIDs)
	}
	if fieldAllowed(o.allowedFields, "studio") && merged.Studio != "" {
		sid, studioErr := client.EnsureStudio(ctx, merged.Studio)
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
		return nil, err
	}
	return sceneFailures, nil
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

	var importTagID string
	if o.apply && fieldAllowed(o.allowedFields, "tags") {
		importTagID, err = client.EnsureTag(ctx, o.tagName)
		if err != nil {
			return fmt.Errorf("ensuring import tag %q: %w", o.tagName, err)
		}
	}

	var changelog []changelogEntry
	var failures []importFailure
	lookup := newEntityLookup(ctx, client)
	stats := importStats{total: len(stashScenes)}
	stashBase := stashURL(cmd)

	for _, ss := range stashScenes {
		if ctx.Err() != nil {
			break
		}

		filename := ""
		if len(ss.Files) > 0 {
			filename = filepath.Base(ss.Files[0].Path)
		}
		if filename == "" {
			stats.skipped++
			continue
		}

		var fileDuration float64
		if len(ss.Files) > 0 {
			fileDuration = ss.Files[0].Duration
		}
		result := idx.Match(filename, fileDuration)

		switch result.Confidence {
		case match.MatchNone:
			stats.skipped++
			continue
		case match.MatchAmbiguous:
			stats.ambiguous++
			fmt.Printf("  AMBIGUOUS  %-50s  →  %d candidates, skipped\n\n", truncate(filename, 50), result.Candidates)
			continue
		}

		var existingDate time.Time
		if ss.Date != "" {
			existingDate, _ = time.Parse("2006-01-02", ss.Date)
		}

		merged := match.MergeScenes(result.Scenes, existingDate)
		sites := strings.Join(merged.Sites, " + ")
		fmt.Printf("  %-10s %-50s  →  %q (%s)\n", result.Confidence, truncate(filename, 50), merged.Title, sites)
		fmt.Printf("           %s/scenes/%s\n", stashBase, ss.ID)

		changes, allTags, mergedURLs := diffScene(ss, merged, o)
		if len(changes) == 0 {
			stats.upToDate++
			continue
		}

		stats.matched++

		if !o.apply {
			for field, diff := range changes {
				if len(diff.Added) > 0 {
					fmt.Printf("    %s: +%v\n", field, diff.Added)
				} else {
					fmt.Printf("    %s: %v → %v\n", field, diff.From, diff.To)
				}
			}
			printMergeConflicts(merged)
			if fieldAllowed(o.allowedFields, "tags") {
				for _, t := range allTags {
					lookup.checkTag(t)
				}
			}
			if fieldAllowed(o.allowedFields, "performers") {
				for _, p := range merged.Performers {
					lookup.checkPerformer(p)
				}
			}
			if fieldAllowed(o.allowedFields, "studio") && merged.Studio != "" {
				lookup.checkStudio(merged.Studio)
			}
			fmt.Println()
			continue
		}

		sceneFailures, updateErr := applyScene(ctx, client, ss, merged, allTags, mergedURLs, importTagID, o)
		if updateErr != nil {
			stats.failed++
			failures = append(failures, importFailure{
				SceneID: ss.ID, Filename: filename, Op: "update", Err: updateErr,
			})
			continue
		}

		if len(sceneFailures) > 0 {
			stats.partial++
			failures = append(failures, sceneFailures...)
		}

		if len(ss.StashIDs) > 0 {
			// Record what actually landed, not the planned diff: prune tag /
			// performer names whose Ensure* failed so a later revert doesn't
			// strip entities that were never imported.
			recorded := reconcileChanges(changes, sceneFailures)
			if len(recorded) > 0 {
				changelog = append(changelog, changelogEntry{
					StashSceneID: ss.ID,
					Timestamp:    time.Now().UTC(),
					Filename:     filename,
					MatchedTo:    merged.Title,
					Changes:      recorded,
				})
			}
		}

		stats.updated++
	}

	if len(changelog) > 0 {
		if err := appendChangelog(changelogDir(cmd), changelog); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write stashbox changelog: %v\n", err)
		}
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

// entityLookup caches Stash existence checks across scenes so the same tag /
// printMergeConflicts reports scalar fields where the contributing sites
// disagreed, so a dry run shows what the merge chose *against* rather than
// presenting one site's value as if it were uncontested.
func printMergeConflicts(merged match.MergedScene) {
	for _, field := range []string{"title", "description", "date", "studio", "duration", "resolution"} {
		src, ok := merged.Sources[field]
		if !ok || !src.Conflicted() {
			continue
		}
		fmt.Printf("    ~ %s: kept %s, dropped %s\n",
			field, src.Site, strings.Join(truncateEach(src.Discarded, 40), "; "))
	}
}

func truncateEach(values []string, limit int) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = truncate(v, limit)
	}
	return out
}

// performer / studio name isn't queried multiple times during a dry run. Used
// to populate the "would create on apply" summary.
//
// Map values: true = exists in Stash, false = does not exist (would be created).
// A name is absent from the map until checkX is called for it.
type entityLookup struct {
	ctx    context.Context
	client *stash.Client

	tags       map[string]bool
	performers map[string]bool
	studios    map[string]bool
}

func newEntityLookup(ctx context.Context, c *stash.Client) *entityLookup {
	return &entityLookup{
		ctx:        ctx,
		client:     c,
		tags:       map[string]bool{},
		performers: map[string]bool{},
		studios:    map[string]bool{},
	}
}

func (l *entityLookup) checkTag(name string) {
	if _, seen := l.tags[name]; seen {
		return
	}
	_, found, err := l.client.FindTag(l.ctx, name)
	if err == nil && !found {
		_, found, err = l.client.FindTagByAlias(l.ctx, name)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: looking up tag %q: %v\n", name, err)
		l.tags[name] = true // treat as existing so it doesn't appear in the would-create list
		return
	}
	l.tags[name] = found
}

func (l *entityLookup) checkPerformer(name string) {
	if _, seen := l.performers[name]; seen {
		return
	}
	_, found, err := l.client.FindPerformer(l.ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: looking up performer %q: %v\n", name, err)
		l.performers[name] = true
		return
	}
	l.performers[name] = found
}

func (l *entityLookup) checkStudio(name string) {
	if _, seen := l.studios[name]; seen {
		return
	}
	_, found, err := l.client.FindStudio(l.ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: looking up studio %q: %v\n", name, err)
		l.studios[name] = true
		return
	}
	l.studios[name] = found
}

// printWouldCreateSummary writes a sorted, grouped list of entities that
// would be created in Stash on `--apply`. No-op if nothing would be created.
func printWouldCreateSummary(l *entityLookup) {
	tags := wouldCreateNames(l.tags)
	perfs := wouldCreateNames(l.performers)
	studios := wouldCreateNames(l.studios)

	if len(tags) == 0 && len(perfs) == 0 && len(studios) == 0 {
		return
	}

	fmt.Println()
	fmt.Println("Would create on apply:")
	for _, t := range tags {
		fmt.Printf("  + tag       %q\n", t)
	}
	for _, p := range perfs {
		fmt.Printf("  + performer %q\n", p)
	}
	for _, s := range studios {
		fmt.Printf("  + studio    %q\n", s)
	}
}

func wouldCreateNames(m map[string]bool) []string {
	var out []string
	for name, exists := range m {
		if !exists {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// printFailureSummary writes a grouped, scene-by-scene report of all per-scene
// failures collected during apply mode. No-op if there are no failures.
// Output goes to stderr so it stays out of the way when piping.
func printFailureSummary(failures []importFailure) {
	if len(failures) == 0 {
		return
	}

	bySceneID := make(map[string][]importFailure)
	var sceneOrder []string
	for _, f := range failures {
		if _, seen := bySceneID[f.SceneID]; !seen {
			sceneOrder = append(sceneOrder, f.SceneID)
		}
		bySceneID[f.SceneID] = append(bySceneID[f.SceneID], f)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Failures (%d operations across %d scenes):\n", len(failures), len(bySceneID))
	for _, id := range sceneOrder {
		fs := bySceneID[id]
		fmt.Fprintf(os.Stderr, "  scene %s (%s):\n", id, fs[0].Filename)
		for _, f := range fs {
			if f.Name != "" {
				fmt.Fprintf(os.Stderr, "    - %s %q: %v\n", f.Op, f.Name, f.Err)
			} else {
				fmt.Fprintf(os.Stderr, "    - %s: %v\n", f.Op, f.Err)
			}
		}
	}
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
		existingTagNames[t.Name] = true
	}
	var addedTags []string
	for _, t := range newTags {
		if !existingTagNames[t] {
			addedTags = append(addedTags, t)
		}
	}
	if len(addedTags) > 0 {
		changes["tags"] = changelogFieldDiff{Added: addedTags}
	}

	existingPerfNames := make(map[string]bool, len(ss.Performers))
	for _, p := range ss.Performers {
		existingPerfNames[p.Name] = true
	}
	var addedPerfs []string
	for _, p := range merged.Performers {
		if !existingPerfNames[p] {
			addedPerfs = append(addedPerfs, p)
		}
	}
	if len(addedPerfs) > 0 {
		changes["performers"] = changelogFieldDiff{Added: addedPerfs}
	}

	if merged.Studio != "" {
		existingStudio := ""
		if ss.Studio != nil {
			existingStudio = ss.Studio.Name
		}
		if merged.Studio != existingStudio {
			changes["studio"] = changelogFieldDiff{From: existingStudio, To: merged.Studio}
		}
	}

	return changes
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

func appendChangelog(dir string, entries []changelogEntry) error {
	path := filepath.Join(dir, "fss-stashbox-changelog.json")

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
	return os.WriteFile(path, out, 0o600)
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

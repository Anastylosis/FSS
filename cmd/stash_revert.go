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

	"github.com/spf13/cobra"

	stash "github.com/Anastylosis/stash-go"
)

var stashRevertCmd = &cobra.Command{
	Use:   "revert <stash-scene-id> [<stash-scene-id> ...]",
	Short: "Undo a prior --include-stashbox import using the changelog",
	Long: `Undo changes recorded in fss-stashbox-changelog.json for one or more scenes.

Reverts: title, date, urls (the added ones), tags (the added ones), performers
(the added ones), studio and organized. Tags and performers are removed by the
Stash ID recorded at import time (older changelogs fall back to the name, which
is matched case-insensitively and through tag aliases). A list that ends up
empty is emptied, not left unchanged.

NOT reverted (limitations of what the changelog stores):
  - details: only a 60-char preview is recorded; can't restore the original.
  - cover:   the original cover URL was never recorded.

Default is dry-run — pass --apply to actually write changes.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runStashRevert,
}

func init() {
	stashCmd.AddCommand(stashRevertCmd)

	stashRevertCmd.Flags().String("dir", "", "directory containing fss-stashbox-changelog.json (default: config out_dir)")
	stashRevertCmd.Flags().Bool("apply", false, "actually write changes (default is dry-run)")
	stashRevertCmd.Flags().Bool("all", false, "revert every changelog entry for the scene (default: only the most recent)")
	stashRevertCmd.Flags().StringSlice("fields", nil, "only revert these fields (title,date,urls,tags,performers,studio,organized); default: all revertible")
}

// revertableFields is the subset of validImportFields that revert can actually
// restore. details/cover are excluded — see stashRevertCmd.Long.
var revertableFields = map[string]bool{
	"title": true, "date": true, "urls": true, "tags": true, "performers": true,
	"studio": true, "organized": true,
}

func runStashRevert(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir, _ := cmd.Flags().GetString("dir")
	if dir == "" {
		dir = cfg.OutDir
	}
	apply, _ := cmd.Flags().GetBool("apply")
	all, _ := cmd.Flags().GetBool("all")
	fieldsList, _ := cmd.Flags().GetStringSlice("fields")
	allowed, err := parseRevertFields(fieldsList)
	if err != nil {
		return err
	}

	entries, err := loadChangelog(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("changelog at %s is empty", filepath.Join(dir, "fss-stashbox-changelog.json"))
	}

	client := newStashClient(cmd)
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("connecting to stash: %w", err)
	}
	fmt.Println("Connected to Stash")

	if !apply {
		fmt.Print("\n--- DRY RUN (pass --apply to write changes) ---\n\n")
	}

	lookup := newEntityLookup(ctx, client)
	var anyApplied, anyMatched, anyFailed int
	for _, sceneID := range args {
		matches := entriesForScene(entries, sceneID)
		if len(matches) == 0 {
			fmt.Fprintf(os.Stderr, "warning: no changelog entries for scene %s\n", sceneID)
			continue
		}
		ordered := revertOrder(matches, all)

		current, found, err := client.FindScene(ctx, sceneID)
		if err != nil {
			return fmt.Errorf("fetching scene %s: %w", sceneID, err)
		}
		if !found {
			fmt.Fprintf(os.Stderr, "warning: scene %s not found in Stash, skipping\n", sceneID)
			continue
		}
		anyMatched += len(ordered)

		// revertOrder applies the entries newest-first. Each entry is computed
		// against the current (possibly already partially reverted) state.
		for i, entry := range ordered {
			input, plan, skipped := computeRevert(entry, *current, allowed)
			fmt.Printf("scene %s (%s) [entry %s]:\n", sceneID, entry.Filename, entry.Timestamp.Format("2006-01-02T15:04:05Z"))
			if len(plan) == 0 && len(skipped) == 0 {
				fmt.Println("  nothing to revert")
				continue
			}
			for _, line := range plan {
				fmt.Printf("  %s\n", line)
			}
			for _, s := range skipped {
				fmt.Printf("  (skipped) %s\n", s)
			}

			if !apply {
				continue
			}

			if err := applyRevert(ctx, lookup, input, *current); err != nil {
				// Subsequent entries for this scene compose on top of this one
				// (newest-first unwinding), so a failure here leaves stale state
				// — stop this scene rather than revert against it, and surface a
				// non-zero exit at the end. Other scenes are independent.
				fmt.Fprintf(os.Stderr, "error reverting scene %s: %v\n", sceneID, err)
				anyFailed++
				break
			}
			anyApplied++

			// Refresh the in-memory current state for subsequent --all entries.
			refreshed, ok, err := client.FindScene(ctx, sceneID)
			if err != nil || !ok {
				if i < len(ordered)-1 {
					fmt.Fprintf(os.Stderr, "error: re-fetching scene %s after reverting: %v — not reverting its older entries against stale state\n", sceneID, err)
					anyFailed++
				}
				break
			}
			current = refreshed
		}
	}

	fmt.Println()
	if apply {
		fmt.Printf("Done: %d entries matched, %d applied, %d failed\n", anyMatched, anyApplied, anyFailed)
		if anyFailed > 0 {
			return fmt.Errorf("%d scene(s) failed to revert", anyFailed)
		}
	} else {
		fmt.Printf("Dry-run: %d entries would be reverted\n", anyMatched)
	}
	return nil
}

// parseRevertFields validates the --fields list against the revertable set.
// Returns nil to mean "all revertable fields".
func parseRevertFields(fields []string) (map[string]bool, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	out := make(map[string]bool, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if !revertableFields[f] {
			valid := make([]string, 0, len(revertableFields))
			for k := range revertableFields {
				valid = append(valid, k)
			}
			sort.Strings(valid)
			return nil, fmt.Errorf("--fields: %q is not revertable (valid: %s)", f, strings.Join(valid, ","))
		}
		out[f] = true
	}
	return out, nil
}

// loadChangelog returns the changelog entries from the given directory, or an
// error if the file is missing/unreadable. An empty slice is returned for an
// empty file.
func loadChangelog(dir string) ([]changelogEntry, error) {
	path := filepath.Join(dir, "fss-stashbox-changelog.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no changelog at %s — was --include-stashbox ever used here?", path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading changelog %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var entries []changelogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parsing changelog %s: %w", path, err)
	}
	return entries, nil
}

// revertOrder returns the changelog entries to apply for a scene, in the order
// they must be applied. The changelog is stored oldest-first; scalar reverts
// (title/date) write each entry's `From` value, so the OLDEST entry's From must
// be applied LAST to win — i.e. iterate newest-first. Two imports (title A→B,
// then B→C) reverted with --all must end at A, not the intermediate B. Additive
// subtractions (urls/tags/performers) are order-independent, so newest-first is
// correct for them too. When `all` is false only the most recent entry applies.
func revertOrder(matches []changelogEntry, all bool) []changelogEntry {
	if len(matches) == 0 {
		return nil
	}
	if !all {
		return matches[len(matches)-1:]
	}
	out := make([]changelogEntry, len(matches))
	for i, e := range matches {
		out[len(matches)-1-i] = e
	}
	return out
}

func entriesForScene(entries []changelogEntry, sceneID string) []changelogEntry {
	var out []changelogEntry
	for _, e := range entries {
		if e.StashSceneID == sceneID {
			out = append(out, e)
		}
	}
	return out
}

// revertInput packages the scene update plus the name-keyed deletion lists
// that need ID resolution before the update can be issued.
type revertInput struct {
	scene           stash.SceneUpdate
	removeTagNames  []string
	removePerfNames []string
	removeTagIDs    []string
	removePerfIDs   []string
	studioName      string
	clearFields     []string
}

// computeRevert builds the scene update for a single changelog entry.
// Returns the input, a human-readable plan (one line per field), and a list
// of skipped fields (with reason) that the caller should print as warnings.
func computeRevert(entry changelogEntry, current stash.Scene, allowed map[string]bool) (revertInput, []string, []string) {
	in := revertInput{scene: stash.SceneUpdate{ID: current.ID}}
	var plan, skipped []string

	allow := func(field string) bool {
		return allowed == nil || allowed[field]
	}

	for field, diff := range entry.Changes {
		switch field {
		case "title":
			if !allow(field) {
				continue
			}
			from := stringValue(diff.From)
			if from == "" {
				in.clearFields = append(in.clearFields, "title")
				plan = append(plan, fmt.Sprintf("title: %q → (empty)", current.Title))
				continue
			}
			s := from
			in.scene.Title = &s
			plan = append(plan, fmt.Sprintf("title: %q → %q", current.Title, from))

		case "date":
			if !allow(field) {
				continue
			}
			from := stringValue(diff.From)
			if from == "" {
				in.clearFields = append(in.clearFields, "date")
				plan = append(plan, fmt.Sprintf("date: %q → (empty)", current.Date))
				continue
			}
			d := from
			in.scene.Date = &d
			plan = append(plan, fmt.Sprintf("date: %q → %q", current.Date, from))

		case "urls":
			if !allow(field) {
				continue
			}
			if len(diff.Added) == 0 {
				continue
			}
			in.scene.URLs = subtractStrings(current.URLs, diff.Added)
			if len(in.scene.URLs) == 0 {
				in.scene.URLs = nil
				in.clearFields = append(in.clearFields, "urls")
			}
			plan = append(plan, fmt.Sprintf("urls: -%v", diff.Added))

		case "tags":
			if !allow(field) {
				continue
			}
			if len(diff.Added) == 0 {
				continue
			}
			in.removeTagNames, in.removeTagIDs = diff.Added, diff.AddedIDs
			plan = append(plan, fmt.Sprintf("tags: -%v", diff.Added))

		case "performers":
			if !allow(field) {
				continue
			}
			if len(diff.Added) == 0 {
				continue
			}
			in.removePerfNames, in.removePerfIDs = diff.Added, diff.AddedIDs
			plan = append(plan, fmt.Sprintf("performers: -%v", diff.Added))

		case "studio":
			if !allow(field) {
				continue
			}
			from := stringValue(diff.From)
			switch {
			case diff.FromID != "":
				id := diff.FromID
				in.scene.StudioID = &id
			case from != "":
				in.studioName = from
			default:
				skipped = append(skipped, "studio: the scene had no studio before; Stash cannot clear it through an update, leaving current")
				continue
			}
			plan = append(plan, fmt.Sprintf("studio: %q → %q", stringValue(diff.To), from))

		case "organized":
			if !allow(field) {
				continue
			}
			o := false
			in.scene.Organized = &o
			plan = append(plan, "organized: true → false")

		case "details":
			skipped = append(skipped, "details: only a 60-char preview was recorded; can't restore original")

		case "cover":
			skipped = append(skipped, "cover: original URL was never recorded; can't restore")
		}
	}

	// Sort plan/skipped lines for stable output regardless of map iteration order.
	sort.Strings(plan)
	sort.Strings(skipped)
	return in, plan, skipped
}

// stringValue extracts a string from an `any` field that came back via JSON.
// JSON unmarshalling into `any` produces string for JSON strings, so this is
// the common case. Returns "" for nil or non-string types.
func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// subtractStrings returns `from` with any element of `remove` filtered out.
// Order of `from` is preserved.
func subtractStrings(from, remove []string) []string {
	if len(remove) == 0 {
		return from
	}
	skip := make(map[string]bool, len(remove))
	for _, s := range remove {
		skip[s] = true
	}
	out := make([]string, 0, len(from))
	for _, s := range from {
		if !skip[s] {
			out = append(out, s)
		}
	}
	return out
}

// subtractIDs is identical to subtractStrings but named for readability at
// call sites that work with Stash entity IDs.
func subtractIDs(from, remove []string) []string {
	return subtractStrings(from, remove)
}

func currentTagIDs(s stash.Scene) []string {
	ids := make([]string, len(s.Tags))
	for i, t := range s.Tags {
		ids[i] = t.ID
	}
	return ids
}

func currentPerfIDs(s stash.Scene) []string {
	ids := make([]string, len(s.Performers))
	for i, p := range s.Performers {
		ids[i] = p.ID
	}
	return ids
}

// applyRevert writes one computed revert: scalars and URLs through a scene
// update, emptied fields through ClearSceneFields, and tags/performers through
// the bulk REMOVE mutations (an update cannot empty a list).
func applyRevert(ctx context.Context, l *entityLookup, in revertInput, current stash.Scene) error {
	tagIDs := in.removeTagIDs
	if len(tagIDs) == 0 && len(in.removeTagNames) > 0 {
		ids, err := lookupIDs(in.removeTagNames, l.findTag)
		if err != nil {
			return err
		}
		tagIDs = ids
	}
	perfIDs := in.removePerfIDs
	if len(perfIDs) == 0 && len(in.removePerfNames) > 0 {
		ids, err := lookupIDs(in.removePerfNames, l.findPerformer)
		if err != nil {
			return err
		}
		perfIDs = ids
	}
	if in.studioName != "" {
		id, found, err := l.findStudio(in.studioName)
		if err != nil {
			return err
		}
		if !found {
			fmt.Fprintf(os.Stderr, "warning: studio %q no longer exists in Stash, leaving the current studio\n", in.studioName)
		} else {
			in.scene.StudioID = &id
		}
	}

	if sceneUpdateHasChanges(in.scene) {
		if err := l.client.UpdateScene(ctx, in.scene); err != nil {
			return err
		}
	}
	if len(in.clearFields) > 0 {
		if err := l.client.ClearSceneFields(ctx, current.ID, in.clearFields...); err != nil {
			return err
		}
	}
	if err := l.client.RemoveSceneTags(ctx, tagIDs, current.ID); err != nil {
		return err
	}
	return l.client.RemoveScenePerformers(ctx, perfIDs, current.ID)
}

func sceneUpdateHasChanges(u stash.SceneUpdate) bool {
	return u.Title != nil || u.Date != nil || u.URLs != nil || u.StudioID != nil || u.Organized != nil
}

func lookupIDs(names []string, find func(string) (string, bool, error)) ([]string, error) {
	var ids []string
	for _, n := range names {
		id, found, err := find(n)
		if err != nil {
			return nil, err
		}
		if found {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// resolveExistingTagIDs returns the Stash IDs for tags whose names (or
// aliases) are in the list. Names with no matching tag are dropped.
func resolveExistingTagIDs(ctx context.Context, client *stash.Client, names []string) ([]string, error) {
	return lookupIDs(names, newEntityLookup(ctx, client).findTag)
}

func resolveExistingPerfIDs(ctx context.Context, client *stash.Client, names []string) ([]string, error) {
	return lookupIDs(names, newEntityLookup(ctx, client).findPerformer)
}

package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	stash "github.com/Anastylosis/stash-go"

	"github.com/Anastylosis/FSS/match"
)

// entityLookup caches Stash existence checks across scenes so the same tag /
// printMergeConflicts reports scalar fields where the contributing sites
// disagreed, so a dry run shows what the merge chose *against* rather than
// presenting one site's value as if it were uncontested.
func printMergeConflicts(merged match.MergedScene) {
	for _, c := range mergeConflicts(merged) {
		kept := c.Kept
		if kept == "" {
			kept = "the existing Stash value"
		}
		fmt.Printf("    ~ %s: kept %s, dropped %s\n",
			c.Field, kept, strings.Join(truncateEach(c.Dropped, 40), "; "))
	}
}

func truncateEach(values []string, limit int) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = truncate(v, limit)
	}
	return out
}

// performer / studio name isn't queried multiple times during a dry run, and
// that import and revert resolve names the same way (name, then alias for
// tags). Used to populate the "would create on apply" summary.
//
// Map values: true = exists in Stash, false = does not exist (would be created).
// A name is absent from the map until checkX is called for it.
type entityLookup struct {
	ctx    context.Context
	client *stash.Client

	tags       map[string]bool
	performers map[string]bool
	studios    map[string]bool

	tagIDs    map[string]string
	perfIDs   map[string]string
	studioIDs map[string]string

	created []createdEntity
}

// createdEntity records an entity ensureX created this run, so a scene whose
// update then fails can take back what only it caused.
type createdEntity struct {
	kind, id, name string
}

// rollbackCreated deletes the entities created since mark and forgets them, so
// a failed scene update leaves no orphaned tag, performer or studio behind.
func (l *entityLookup) rollbackCreated(mark int) {
	for i := len(l.created) - 1; i >= mark; i-- {
		e := l.created[i]
		var err error
		switch e.kind {
		case "tag":
			err = l.client.DeleteTag(l.ctx, e.id)
			delete(l.tags, e.name)
			delete(l.tagIDs, e.name)
		case "performer":
			err = l.client.DeletePerformer(l.ctx, e.id)
			delete(l.performers, e.name)
			delete(l.perfIDs, e.name)
		case "studio":
			err = l.client.DeleteStudio(l.ctx, e.id)
			delete(l.studios, e.name)
			delete(l.studioIDs, e.name)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove %s %q created for a failed scene update: %v\n", e.kind, e.name, err)
		}
	}
	l.created = l.created[:mark]
}

func newEntityLookup(ctx context.Context, c *stash.Client) *entityLookup {
	return &entityLookup{
		ctx:        ctx,
		client:     c,
		tags:       map[string]bool{},
		performers: map[string]bool{},
		studios:    map[string]bool{},
		tagIDs:     map[string]string{},
		perfIDs:    map[string]string{},
		studioIDs:  map[string]string{},
	}
}

// hasLikeWildcard reports whether a name carries a character Stash's name
// lookup may treat as a pattern; for those the returned entity is re-checked,
// since the server can answer with a different entity that merely matches.
func hasLikeWildcard(name string) bool { return strings.ContainsAny(name, "%_") }

func (l *entityLookup) findTag(name string) (string, bool, error) {
	if found, seen := l.tags[name]; seen {
		return l.tagIDs[name], found, nil
	}
	id, found, err := l.client.FindTag(l.ctx, name)
	if err == nil && found && hasLikeWildcard(name) {
		found, err = l.tagNamed(id, name, false)
	}
	if err == nil && !found {
		id, found, err = l.client.FindTagByAlias(l.ctx, name)
		if err == nil && found && hasLikeWildcard(name) {
			found, err = l.tagNamed(id, name, true)
		}
	}
	if err != nil {
		return "", false, err
	}
	l.tags[name], l.tagIDs[name] = found, id
	return id, found, nil
}

func (l *entityLookup) tagNamed(id, name string, alias bool) (bool, error) {
	t, ok, err := l.client.FindTagByID(l.ctx, id)
	if err != nil || !ok {
		return false, err
	}
	if !alias {
		return strings.EqualFold(t.Name, name), nil
	}
	for _, a := range t.Aliases {
		if strings.EqualFold(a, name) {
			return true, nil
		}
	}
	return false, nil
}

func (l *entityLookup) findPerformer(name string) (string, bool, error) {
	if found, seen := l.performers[name]; seen {
		return l.perfIDs[name], found, nil
	}
	id, found, err := l.client.FindPerformer(l.ctx, name)
	if err == nil && found && hasLikeWildcard(name) {
		var p *stash.Performer
		if p, found, err = l.client.FindPerformerByID(l.ctx, id); err == nil && found {
			found = strings.EqualFold(p.Name, name)
		}
	}
	if err != nil {
		return "", false, err
	}
	l.performers[name], l.perfIDs[name] = found, id
	return id, found, nil
}

func (l *entityLookup) findStudio(name string) (string, bool, error) {
	if found, seen := l.studios[name]; seen {
		return l.studioIDs[name], found, nil
	}
	id, found, err := l.client.FindStudio(l.ctx, name)
	if err == nil && found && hasLikeWildcard(name) {
		var st *stash.Studio
		if st, found, err = l.client.FindStudioByID(l.ctx, id); err == nil && found {
			found = strings.EqualFold(st.Name, name)
		}
	}
	if err != nil {
		return "", false, err
	}
	l.studios[name], l.studioIDs[name] = found, id
	return id, found, nil
}

func (l *entityLookup) ensureTag(name string) (string, error) {
	if id, found, err := l.findTag(name); err != nil || found {
		return id, err
	}
	id, err := l.client.CreateTag(l.ctx, name)
	if err == nil {
		l.tags[name], l.tagIDs[name] = true, id
		l.created = append(l.created, createdEntity{"tag", id, name})
	}
	return id, err
}

func (l *entityLookup) ensurePerformer(name string) (string, error) {
	if id, found, err := l.findPerformer(name); err != nil || found {
		return id, err
	}
	id, err := l.client.CreatePerformer(l.ctx, name)
	if err == nil {
		l.performers[name], l.perfIDs[name] = true, id
		l.created = append(l.created, createdEntity{"performer", id, name})
	}
	return id, err
}

func (l *entityLookup) ensureStudio(name string) (string, error) {
	if id, found, err := l.findStudio(name); err != nil || found {
		return id, err
	}
	id, err := l.client.CreateStudio(l.ctx, name)
	if err == nil {
		l.studios[name], l.studioIDs[name] = true, id
		l.created = append(l.created, createdEntity{"studio", id, name})
	}
	return id, err
}

func (l *entityLookup) checkTag(name string) {
	if _, seen := l.tags[name]; seen {
		return
	}
	if _, _, err := l.findTag(name); err != nil {
		fmt.Fprintf(os.Stderr, "warning: looking up tag %q: %v\n", name, err)
		l.tags[name] = true // treat as existing so it doesn't appear in the would-create list
	}
}

func (l *entityLookup) checkPerformer(name string) {
	if _, seen := l.performers[name]; seen {
		return
	}
	if _, _, err := l.findPerformer(name); err != nil {
		fmt.Fprintf(os.Stderr, "warning: looking up performer %q: %v\n", name, err)
		l.performers[name] = true
	}
}

func (l *entityLookup) checkStudio(name string) {
	if _, seen := l.studios[name]; seen {
		return
	}
	if _, _, err := l.findStudio(name); err != nil {
		fmt.Fprintf(os.Stderr, "warning: looking up studio %q: %v\n", name, err)
		l.studios[name] = true
	}
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

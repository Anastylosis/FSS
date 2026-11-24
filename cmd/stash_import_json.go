package cmd

import (
	"github.com/Anastylosis/FSS/match"
)

// The --format json result of `fss stash import`; see docs/json-output.md.

type importResultJSON struct {
	Apply         bool                   `json:"apply"`
	Stats         importStatsJSON        `json:"stats"`
	Scenes        []importSceneJSON      `json:"scenes"`
	WouldCreate   *importWouldCreateJSON `json:"wouldCreate,omitempty"`
	Failures      []importFailureJSON    `json:"failures"`
	ChangelogPath string                 `json:"changelogPath,omitempty"`
}

type importStatsJSON struct {
	Total     int `json:"total"`
	Matched   int `json:"matched"`
	Updated   int `json:"updated"`
	Skipped   int `json:"skipped"`
	Ambiguous int `json:"ambiguous"`
	UpToDate  int `json:"upToDate"`
	Partial   int `json:"partial"`
	Failed    int `json:"failed"`
}

type importSceneJSON struct {
	StashSceneID string                     `json:"stashSceneId"`
	Filename     string                     `json:"filename"`
	MatchedTo    string                     `json:"matchedTo,omitempty"`
	Status       string                     `json:"status"`
	Candidates   int                        `json:"candidates,omitempty"`
	Changes      map[string]importFieldJSON `json:"changes,omitempty"`
	Conflicts    []importConflictJSON       `json:"conflicts,omitempty"`
}

type importFieldJSON struct {
	From  any      `json:"from,omitempty"`
	To    any      `json:"to,omitempty"`
	Added []string `json:"added,omitempty"`
}

type importConflictJSON struct {
	Field   string   `json:"field"`
	Kept    string   `json:"kept"`
	Dropped []string `json:"dropped"`
}

type importWouldCreateJSON struct {
	Performers []string `json:"performers"`
	Tags       []string `json:"tags"`
	Studios    []string `json:"studios"`
}

type importFailureJSON struct {
	SceneID  string `json:"sceneId"`
	Filename string `json:"filename"`
	Op       string `json:"op"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error"`
}

// mergeConflicts lists the scalar fields where the contributing sites
// disagreed. Kept is empty when the value came from Stash itself.
func mergeConflicts(merged match.MergedScene) []importConflictJSON {
	var out []importConflictJSON
	for _, field := range []string{"title", "description", "date", "studio", "duration", "resolution"} {
		src, ok := merged.Sources[field]
		if !ok || !src.Conflicted() {
			continue
		}
		out = append(out, importConflictJSON{Field: field, Kept: src.Site, Dropped: src.Discarded})
	}
	return out
}

func jsonChanges(changes map[string]changelogFieldDiff) map[string]importFieldJSON {
	if len(changes) == 0 {
		return nil
	}
	out := make(map[string]importFieldJSON, len(changes))
	for field, d := range changes {
		out[field] = importFieldJSON{From: d.From, To: d.To, Added: d.Added}
	}
	return out
}

func newSceneRecord(sceneID, filename string, merged match.MergedScene, changes map[string]changelogFieldDiff) importSceneJSON {
	return importSceneJSON{
		StashSceneID: sceneID,
		Filename:     filename,
		MatchedTo:    merged.Title,
		Changes:      jsonChanges(changes),
		Conflicts:    mergeConflicts(merged),
	}
}

func (r *importRun) result() *importResultJSON {
	res := &importResultJSON{
		Apply: r.o.apply,
		Stats: importStatsJSON{
			Total: r.stats.total, Matched: r.stats.matched, Updated: r.stats.updated,
			Skipped: r.stats.skipped, Ambiguous: r.stats.ambiguous, UpToDate: r.stats.upToDate,
			Partial: r.stats.partial, Failed: r.stats.failed,
		},
		Scenes:   r.records,
		Failures: make([]importFailureJSON, 0, len(r.failures)),
	}
	if res.Scenes == nil {
		res.Scenes = []importSceneJSON{}
	}
	for _, f := range r.failures {
		res.Failures = append(res.Failures, importFailureJSON{
			SceneID: f.SceneID, Filename: f.Filename, Op: f.Op, Name: f.Name, Error: f.Err.Error(),
		})
	}
	if !r.o.apply {
		res.WouldCreate = &importWouldCreateJSON{
			Performers: nonNil(wouldCreateNames(r.lookup.performers)),
			Tags:       nonNil(wouldCreateNames(r.lookup.tags)),
			Studios:    nonNil(wouldCreateNames(r.lookup.studios)),
		}
	}
	if r.clw.wrote {
		res.ChangelogPath = changelogFile(r.clw.dir)
	}
	return res
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

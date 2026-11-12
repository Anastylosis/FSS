package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	stash "github.com/Anastylosis/stash-go"

	"github.com/Anastylosis/FSS/match"
)

// recStash answers GraphQL requests from a per-test function and records
// every request body.
type recStash struct {
	mu   sync.Mutex
	reqs []recReq
	srv  *httptest.Server
}

type recReq struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func newRecStash(t *testing.T, answer func(r recReq) string) *recStash {
	t.Helper()
	s := &recStash{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req recReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.reqs = append(s.reqs, req)
		s.mu.Unlock()
		resp := `{}`
		if answer != nil {
			if a := answer(req); a != "" {
				resp = a
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":%s}`, resp)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *recStash) client() *stash.Client { return stash.NewClient(s.srv.URL) }

func (s *recStash) with(substr string) []recReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recReq
	for _, r := range s.reqs {
		if strings.Contains(r.Query, substr) {
			out = append(out, r)
		}
	}
	return out
}

func TestBuildChanges_caseInsensitiveNamesAreNotAdditions(t *testing.T) {
	ss := stash.Scene{
		Tags:       []stash.Tag{{ID: "1", Name: "JOI"}},
		Performers: []stash.Performer{{ID: "2", Name: "Jane Eyre"}},
		Studio:     &stash.Studio{ID: "3", Name: "Acme"},
	}
	merged := match.MergedScene{Performers: []string{"jane  eyre"}, Studio: "ACME"}
	changes := buildChanges(ss, merged, nil, []string{"joi"}, false, false)
	if len(changes) != 0 {
		t.Fatalf("case/space-only differences produced changes: %+v", changes)
	}
}

func TestBuildChanges_studioRecordsPreviousID(t *testing.T) {
	ss := stash.Scene{Studio: &stash.Studio{ID: "9", Name: "Old"}}
	changes := buildChanges(ss, match.MergedScene{Studio: "New"}, nil, nil, false, false)
	if got := changes["studio"]; got.FromID != "9" || got.From != "Old" {
		t.Fatalf("studio diff = %+v", got)
	}
}

func TestDiffScene_tagAndCategoryWithSameNameListedOnce(t *testing.T) {
	merged := match.MergedScene{Tags: []string{"Anal"}, Categories: []string{"anal", "Teen"}}
	changes, allTags, _ := diffScene(stash.Scene{}, merged, importOpts{tagName: "FSS"})
	if got := changes["tags"].Added; !reflect.DeepEqual(got, []string{"Anal", "Teen", "FSS"}) {
		t.Fatalf("added = %v (allTags %v)", got, allTags)
	}
}

func TestStashFilename(t *testing.T) {
	tests := []struct {
		name string
		file stash.File
		want string
	}{
		{"basename wins", stash.File{Basename: "A.mp4", Path: `D:\v\B.mp4`}, "A.mp4"},
		{"windows path without basename", stash.File{Path: `D:\Videos\Scene Title.mp4`}, "Scene Title.mp4"},
		{"unix path without basename", stash.File{Path: "/v/x.mp4"}, "x.mp4"},
	}
	for _, tc := range tests {
		if got := stashFilename(stash.Scene{Files: []stash.File{tc.file}}); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
	if stashFilename(stash.Scene{}) != "" {
		t.Error("scene without files should have no filename")
	}
}

func TestDropAlreadyPresent_aliasResolvedTagIsNotAnAddition(t *testing.T) {
	s := newRecStash(t, func(r recReq) string {
		v, _ := r.Variables["v"].(string)
		switch {
		case strings.Contains(r.Query, "aliases"):
			if v == "Female Domination" {
				return `{"findTags":{"tags":[{"id":"7","name":"Femdom"}]}}`
			}
			return `{"findTags":{"tags":[]}}`
		case strings.Contains(r.Query, "findTags"):
			return `{"findTags":{"tags":[]}}`
		}
		return ""
	})
	ss := stash.Scene{Tags: []stash.Tag{{ID: "7", Name: "Femdom"}}}
	changes := map[string]changelogFieldDiff{"tags": {Added: []string{"Female Domination"}}}
	got := dropAlreadyPresent(changes, ss, newEntityLookup(context.Background(), s.client()))
	if len(got) != 0 {
		t.Fatalf("alias-only difference still counted: %+v", got)
	}
}

func TestFindTag_rejectsWildcardMatchOfAnotherEntity(t *testing.T) {
	s := newRecStash(t, func(r recReq) string {
		switch {
		case strings.Contains(r.Query, "findTags"):
			return `{"findTags":{"tags":[{"id":"5","name":"A1B"}]}}`
		}
		return ""
	})
	l := newEntityLookup(context.Background(), s.client())
	if _, found, err := l.findTag("a_b"); err != nil || found {
		t.Fatalf("a_b matched a different tag: found=%v err=%v", found, err)
	}
}

func TestRecordAddedIDs(t *testing.T) {
	changes := map[string]changelogFieldDiff{
		"tags":       {Added: []string{"a"}},
		"performers": {Added: []string{"p"}},
		"title":      {From: "x", To: "y"},
	}
	got := recordAddedIDs(changes, sceneResult{addedTagIDs: []string{"10"}})
	if !reflect.DeepEqual(got["tags"].AddedIDs, []string{"10"}) {
		t.Errorf("tags ids = %v", got["tags"].AddedIDs)
	}
	if _, ok := got["performers"]; ok {
		t.Error("performers gained nothing and must not be recorded")
	}
	if _, ok := got["title"]; !ok {
		t.Error("scalar field dropped")
	}
}

func TestComputeRevert_studioAndOrganized(t *testing.T) {
	entry := changelogEntry{Changes: map[string]changelogFieldDiff{
		"studio":    {From: "Old", To: "New", FromID: "9"},
		"organized": {From: false, To: true},
	}}
	in, plan, skipped := computeRevert(entry, stash.Scene{ID: "1"}, nil)
	if in.scene.StudioID == nil || *in.scene.StudioID != "9" {
		t.Errorf("studio id not restored: %+v", in.scene)
	}
	if in.scene.Organized == nil || *in.scene.Organized {
		t.Errorf("organized not reset: %+v", in.scene.Organized)
	}
	if len(plan) != 2 || len(skipped) != 0 {
		t.Errorf("plan=%v skipped=%v", plan, skipped)
	}

	none := changelogEntry{Changes: map[string]changelogFieldDiff{"studio": {To: "New"}}}
	_, _, skipped = computeRevert(none, stash.Scene{ID: "1"}, nil)
	if len(skipped) != 1 {
		t.Errorf("studio with no previous value must be reported, got %v", skipped)
	}
}

func TestComputeRevert_emptiedListsAndScalarsAreCleared(t *testing.T) {
	entry := changelogEntry{Changes: map[string]changelogFieldDiff{
		"urls":  {Added: []string{"https://a"}},
		"title": {To: "T"},
		"date":  {To: "2026-01-01"},
	}}
	cur := stash.Scene{ID: "1", Title: "T", Date: "2026-01-01", URLs: []string{"https://a"}}
	in, _, _ := computeRevert(entry, cur, nil)
	if in.scene.URLs != nil || in.scene.Title != nil || in.scene.Date != nil {
		t.Errorf("empty values must not go through the omitempty update: %+v", in.scene)
	}
	got := append([]string(nil), in.clearFields...)
	if len(got) != 3 {
		t.Errorf("clearFields = %v, want title,date,urls", got)
	}
}

func TestApplyRevert_removesByRecordedIDAndClearsEmptied(t *testing.T) {
	s := newRecStash(t, nil)
	in := revertInput{
		removeTagIDs:  []string{"10"},
		removePerfIDs: []string{"20"},
		clearFields:   []string{"urls"},
	}
	l := newEntityLookup(context.Background(), s.client())
	if err := applyRevert(context.Background(), l, in, stash.Scene{ID: "42"}); err != nil {
		t.Fatal(err)
	}
	if n := len(s.with("bulkSceneUpdate")); n != 2 {
		t.Errorf("bulk updates = %d, want 2 (tags, performers)", n)
	}
	if n := len(s.with("findTags")); n != 0 {
		t.Errorf("recorded IDs must not be re-resolved by name, saw %d lookups", n)
	}
	var sawClear bool
	for _, r := range s.with("sceneUpdate") {
		input, _ := r.Variables["input"].(map[string]any)
		if u, ok := input["urls"].([]any); ok && len(u) == 0 {
			sawClear = true
		}
	}
	if !sawClear {
		t.Error("urls were not cleared with an explicit empty list")
	}
}

func TestApplyRevert_legacyNamesResolveThroughAlias(t *testing.T) {
	s := newRecStash(t, func(r recReq) string {
		if strings.Contains(r.Query, "aliases") {
			return `{"findTags":{"tags":[{"id":"7","name":"Femdom"}]}}`
		}
		if strings.Contains(r.Query, "findTags") {
			return `{"findTags":{"tags":[]}}`
		}
		return ""
	})
	l := newEntityLookup(context.Background(), s.client())
	in := revertInput{removeTagNames: []string{"Female Domination"}}
	if err := applyRevert(context.Background(), l, in, stash.Scene{ID: "42"}); err != nil {
		t.Fatal(err)
	}
	reqs := s.with("bulkSceneUpdate")
	if len(reqs) != 1 {
		t.Fatalf("bulk updates = %d", len(reqs))
	}
	b, _ := json.Marshal(reqs[0].Variables)
	if !strings.Contains(string(b), `"7"`) {
		t.Errorf("alias-resolved tag not removed: %s", b)
	}
}

func TestAppendChangelog_createsDirAtomicallyAndSurvivesConcurrentWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "out")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := appendChangelog(dir, []changelogEntry{{StashSceneID: fmt.Sprint(i)}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	entries, err := loadChangelog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 8 {
		t.Fatalf("lost entries under concurrent writers: %d of 8", len(entries))
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".fss-tmp-*"))
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestAppendChangelog_failureIsAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendChangelog(filepath.Join(file, "sub"), []changelogEntry{{}}); err == nil {
		t.Fatal("expected an error when the directory cannot be created")
	}
}

func TestChangelogWriter_flushesInBatches(t *testing.T) {
	dir := t.TempDir()
	w := &changelogWriter{dir: dir}
	for i := 0; i < changelogFlushEvery; i++ {
		w.add(changelogEntry{StashSceneID: fmt.Sprint(i)})
	}
	if e, _ := loadChangelog(dir); len(e) != changelogFlushEvery {
		t.Fatalf("batch not written before the end of the run: %d", len(e))
	}
	w.add(changelogEntry{StashSceneID: "last"})
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	if e, _ := loadChangelog(dir); len(e) != changelogFlushEvery+1 {
		t.Fatalf("final flush lost entries: %d", len(e))
	}
}

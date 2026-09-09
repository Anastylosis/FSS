package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/match"
	stash "github.com/Anastylosis/stash-go"
)

func TestAppendChangelog_freshStart(t *testing.T) {
	dir := t.TempDir()
	entries := []changelogEntry{
		{StashSceneID: "1", Timestamp: time.Now().UTC(), Filename: "a.mp4", MatchedTo: "Title A"},
	}

	if err := appendChangelog(dir, entries); err != nil {
		t.Fatalf("appendChangelog: %v", err)
	}

	got := readChangelog(t, dir)
	if len(got) != 1 || got[0].StashSceneID != "1" {
		t.Errorf("got %+v", got)
	}
}

func TestAppendChangelog_appendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fss-stashbox-changelog.json")

	first := []changelogEntry{{StashSceneID: "1", Filename: "a.mp4"}}
	writeChangelog(t, path, first)

	second := []changelogEntry{{StashSceneID: "2", Filename: "b.mp4"}}
	if err := appendChangelog(dir, second); err != nil {
		t.Fatalf("appendChangelog: %v", err)
	}

	got := readChangelog(t, dir)
	if len(got) != 2 || got[0].StashSceneID != "1" || got[1].StashSceneID != "2" {
		t.Errorf("got %+v", got)
	}
}

func TestAppendChangelog_corruptFileBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fss-stashbox-changelog.json")

	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := []changelogEntry{{StashSceneID: "new", Filename: "c.mp4"}}
	if err := appendChangelog(dir, entries); err != nil {
		t.Fatalf("appendChangelog: %v", err)
	}

	got := readChangelog(t, dir)
	if len(got) != 1 || got[0].StashSceneID != "new" {
		t.Errorf("expected only the new entry after corrupt backup, got %+v", got)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "fss-stashbox-changelog.corrupt-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one .corrupt-*.json backup, got %v", matches)
	}

	backup, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(backup), "{not valid json") {
		t.Errorf("backup did not preserve original corrupt content: %s", backup)
	}
}

func TestAppendChangelog_emptyFileTreatedAsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fss-stashbox-changelog.json")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := []changelogEntry{{StashSceneID: "x"}}
	if err := appendChangelog(dir, entries); err != nil {
		t.Fatalf("appendChangelog: %v", err)
	}

	got := readChangelog(t, dir)
	if len(got) != 1 {
		t.Errorf("got %+v", got)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "fss-stashbox-changelog.corrupt-*.json"))
	if len(matches) != 1 {
		t.Errorf("expected backup of empty/corrupt file, got %v", matches)
	}
}

func readChangelog(t *testing.T, dir string) []changelogEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fss-stashbox-changelog.json"))
	if err != nil {
		t.Fatalf("reading changelog: %v", err)
	}
	var entries []changelogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshalling changelog: %v\n%s", err, data)
	}
	return entries
}

func writeChangelog(t *testing.T, path string, entries []changelogEntry) {
	t.Helper()
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureStderr mirrors captureStdout; see there for why the pipe is drained
// concurrently rather than read after the writer is closed.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stderr = orig
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := <-done
	_ = r.Close()
	return out
}

func TestPrintFailureSummary_emptyIsNoOp(t *testing.T) {
	out := captureStderr(t, func() {
		printFailureSummary(nil)
		printFailureSummary([]importFailure{})
	})
	if out != "" {
		t.Errorf("expected no output for empty failures, got: %q", out)
	}
}

func TestPrintFailureSummary_groupsByScene(t *testing.T) {
	failures := []importFailure{
		{SceneID: "10", Filename: "a.mp4", Op: "tag", Name: "POV", Err: errors.New("network blip")},
		{SceneID: "10", Filename: "a.mp4", Op: "performer", Name: "Alice", Err: errors.New("alias collision")},
		{SceneID: "20", Filename: "b.mp4", Op: "studio", Name: "SomeStudio", Err: errors.New("not found")},
		{SceneID: "30", Filename: "c.mp4", Op: "update", Err: errors.New("timeout")},
	}

	out := captureStderr(t, func() { printFailureSummary(failures) })

	// Header reflects 4 ops across 3 scenes.
	if !strings.Contains(out, "Failures (4 operations across 3 scenes)") {
		t.Errorf("missing or wrong header: %s", out)
	}
	// Each scene header appears once.
	for _, want := range []string{"scene 10 (a.mp4)", "scene 20 (b.mp4)", "scene 30 (c.mp4)"} {
		if strings.Count(out, want) != 1 {
			t.Errorf("expected exactly one occurrence of %q, got: %s", want, out)
		}
	}
	// Named ops are quoted.
	if !strings.Contains(out, `tag "POV": network blip`) {
		t.Errorf("missing tag failure detail: %s", out)
	}
	if !strings.Contains(out, `performer "Alice": alias collision`) {
		t.Errorf("missing performer failure detail: %s", out)
	}
	// Update op has no Name and should not be quoted.
	if !strings.Contains(out, "- update: timeout") {
		t.Errorf("update failure should appear without quoted name: %s", out)
	}
	// Scene 10's two failures are nested under the same header (no second 'scene 10' line).
	if strings.Count(out, "scene 10") != 1 {
		t.Errorf("scene 10 should be grouped, got duplicated header: %s", out)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
//
// The pipe is drained in a goroutine rather than read after w.Close(): a pipe
// holds ~64KB, so reading only after fn returns deadlocks as soon as fn writes
// more than that. Small callers never noticed, but `list-scrapers` prints
// hundreds of KB for 1634 scrapers and hangs outright.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := <-done
	_ = r.Close()
	return out
}

func TestPrintWouldCreateSummary_emptyIsNoOp(t *testing.T) {
	l := &entityLookup{
		tags:       map[string]bool{},
		performers: map[string]bool{},
		studios:    map[string]bool{},
	}
	out := captureStdout(t, func() { printWouldCreateSummary(l) })
	if out != "" {
		t.Errorf("expected no output, got: %q", out)
	}
}

func TestPrintWouldCreateSummary_skipsExistingShowsMissing(t *testing.T) {
	l := &entityLookup{
		tags: map[string]bool{
			"POV":               true,  // exists, should not appear
			"Female Domination": false, // would create
			"4K Available":      false, // would create
		},
		performers: map[string]bool{
			"Alice": false,
			"Bob":   true,
		},
		studios: map[string]bool{
			"NewStudio":      false,
			"ExistingStudio": true,
		},
	}
	out := captureStdout(t, func() { printWouldCreateSummary(l) })

	// Sections present.
	if !strings.Contains(out, "Would create on apply:") {
		t.Errorf("missing header: %s", out)
	}

	// Sorted alphabetically — "4K Available" comes before "Female Domination".
	idx4K := strings.Index(out, "4K Available")
	idxFD := strings.Index(out, "Female Domination")
	if idx4K == -1 || idxFD == -1 || idx4K > idxFD {
		t.Errorf("tags should be sorted alphabetically: %s", out)
	}

	// Existing entries do not appear.
	for _, banned := range []string{"POV", "Bob", "ExistingStudio"} {
		if strings.Contains(out, banned) {
			t.Errorf("existing entity %q should not appear: %s", banned, out)
		}
	}

	// Each type prefixed correctly.
	if !strings.Contains(out, `+ tag       "4K Available"`) {
		t.Errorf("missing tag line: %s", out)
	}
	if !strings.Contains(out, `+ performer "Alice"`) {
		t.Errorf("missing performer line: %s", out)
	}
	if !strings.Contains(out, `+ studio    "NewStudio"`) {
		t.Errorf("missing studio line: %s", out)
	}
}

func TestResolveCoverEnabled(t *testing.T) {
	cases := []struct {
		name          string
		flag          bool
		allowedFields map[string]bool
		want          bool
	}{
		{"flag set, no fields filter", true, nil, true},
		{"flag set, fields excludes cover", true, map[string]bool{"title": true}, true},
		{"flag set, fields includes cover", true, map[string]bool{"cover": true}, true},
		{"flag unset, no fields filter (legacy default)", false, nil, false},
		{"flag unset, fields excludes cover", false, map[string]bool{"title": true}, false},
		{"flag unset, fields includes cover (implicit enable)", false, map[string]bool{"cover": true}, true},
		{"flag unset, fields includes cover plus others", false, map[string]bool{"cover": true, "tags": true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveCoverEnabled(c.flag, c.allowedFields); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestPrintWouldCreateSummary_onlyExistingIsNoOp(t *testing.T) {
	l := &entityLookup{
		tags:       map[string]bool{"POV": true, "MILF": true},
		performers: map[string]bool{"Alice": true},
		studios:    map[string]bool{"S": true},
	}
	out := captureStdout(t, func() { printWouldCreateSummary(l) })
	if out != "" {
		t.Errorf("expected no output when nothing would be created, got: %q", out)
	}
}

func TestPrintFailureSummary_preservesInsertionOrder(t *testing.T) {
	failures := []importFailure{
		{SceneID: "C", Filename: "c.mp4", Op: "tag", Name: "x", Err: errors.New("e1")},
		{SceneID: "A", Filename: "a.mp4", Op: "tag", Name: "x", Err: errors.New("e2")},
		{SceneID: "B", Filename: "b.mp4", Op: "tag", Name: "x", Err: errors.New("e3")},
	}
	out := captureStderr(t, func() { printFailureSummary(failures) })

	idxC := strings.Index(out, "scene C")
	idxA := strings.Index(out, "scene A")
	idxB := strings.Index(out, "scene B")
	if idxC >= idxA || idxA >= idxB {
		t.Errorf("expected insertion order C → A → B, got positions C=%d A=%d B=%d\noutput: %s", idxC, idxA, idxB, out)
	}
}

func TestReconcileChanges_noFailuresPassThrough(t *testing.T) {
	changes := map[string]changelogFieldDiff{
		"tags": {Added: []string{"POV", "MILF"}},
	}
	got := reconcileChanges(changes, nil)
	if !reflect.DeepEqual(got, changes) {
		t.Errorf("with no failures the map should pass through unchanged, got %v", got)
	}
}

func TestReconcileChanges_prunesFailedTagsAndPerfs(t *testing.T) {
	changes := map[string]changelogFieldDiff{
		"title":      {From: "old", To: "new"},
		"tags":       {Added: []string{"POV", "MILF", "Anal"}},
		"performers": {Added: []string{"Alice", "Bob"}},
		"urls":       {Added: []string{"https://x"}},
	}
	failures := []importFailure{
		{Op: "tag", Name: "MILF"},
		{Op: "tag (stashbox)", Name: "POV"},
		{Op: "performer", Name: "Bob"},
	}
	got := reconcileChanges(changes, failures)

	if !reflect.DeepEqual(got["tags"].Added, []string{"Anal"}) {
		t.Errorf("tags = %v, want [Anal]", got["tags"].Added)
	}
	if !reflect.DeepEqual(got["performers"].Added, []string{"Alice"}) {
		t.Errorf("performers = %v, want [Alice]", got["performers"].Added)
	}
	// Scalars/urls untouched.
	if got["title"].To != "new" || !reflect.DeepEqual(got["urls"].Added, []string{"https://x"}) {
		t.Errorf("scalars/urls altered: %+v", got)
	}
	// Original map not mutated.
	if len(changes["tags"].Added) != 3 {
		t.Errorf("input map mutated: %v", changes["tags"].Added)
	}
}

func TestReconcileChanges_dropsEmptiedField(t *testing.T) {
	changes := map[string]changelogFieldDiff{
		"tags": {Added: []string{"POV"}},
	}
	failures := []importFailure{{Op: "tag", Name: "POV"}}
	got := reconcileChanges(changes, failures)
	if _, ok := got["tags"]; ok {
		t.Errorf("fully-failed tags field should be dropped, got %v", got)
	}
}

func TestDiffStrings(t *testing.T) {
	got := diffStrings([]string{"a", "b", "c"}, []string{"b", "c", "d", "e"})
	want := []string{"d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffStrings_allNew(t *testing.T) {
	got := diffStrings([]string{"a"}, []string{"b", "c"})
	want := []string{"b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiffStrings_noneNew(t *testing.T) {
	got := diffStrings([]string{"a", "b"}, []string{"a", "b"})
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestExtractTagIDs(t *testing.T) {
	tags := []stash.Tag{{ID: "10", Name: "POV"}, {ID: "20", Name: "MILF"}}
	got := extractTagIDs(tags)
	want := []string{"10", "20"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractPerfIDs(t *testing.T) {
	perfs := []stash.Performer{{ID: "1", Name: "Alice"}, {ID: "2", Name: "Bob"}}
	got := extractPerfIDs(perfs)
	want := []string{"1", "2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedup(t *testing.T) {
	got := dedup([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDedup_noDups(t *testing.T) {
	got := dedup([]string{"x", "y", "z"})
	want := []string{"x", "y", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTruncate_short(t *testing.T) {
	got := truncate("hello", 10)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestTruncate_exact(t *testing.T) {
	got := truncate("hello", 5)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestTruncate_long(t *testing.T) {
	got := truncate("hello world", 8)
	if got != "hello..." {
		t.Errorf("got %q, want %q", got, "hello...")
	}
}

func TestParseFieldsFlag_valid(t *testing.T) {
	got, err := parseFieldsFlag([]string{"title", "tags", "cover"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"title": true, "tags": true, "cover": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseFieldsFlag_invalid(t *testing.T) {
	_, err := parseFieldsFlag([]string{"title", "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention the unknown field, got: %v", err)
	}
}

func TestParseFieldsFlag_empty(t *testing.T) {
	got, err := parseFieldsFlag(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestFieldAllowed_nilAllowsAll(t *testing.T) {
	if !fieldAllowed(nil, "anything") {
		t.Error("nil map should allow all fields")
	}
}

func TestFieldAllowed_presentField(t *testing.T) {
	m := map[string]bool{"title": true}
	if !fieldAllowed(m, "title") {
		t.Error("present field should be allowed")
	}
}

func TestFieldAllowed_absentField(t *testing.T) {
	m := map[string]bool{"title": true}
	if fieldAllowed(m, "tags") {
		t.Error("absent field should not be allowed")
	}
}

func TestBuildChanges_titleChange(t *testing.T) {
	ss := stash.Scene{ID: "1", Title: "Old Title"}
	merged := match.MergedScene{Title: "New Title"}
	changes := buildChanges(ss, merged, nil, nil, false, false)
	diff, ok := changes["title"]
	if !ok {
		t.Fatal("expected title change")
	}
	if diff.From != "Old Title" || diff.To != "New Title" {
		t.Errorf("got from=%v to=%v", diff.From, diff.To)
	}
}

func TestBuildChanges_noChanges(t *testing.T) {
	ss := stash.Scene{ID: "1", Title: "Same", Details: "Desc", Date: "2026-01-01"}
	merged := match.MergedScene{
		Title:       "Same",
		Description: "Desc",
		Date:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	changes := buildChanges(ss, merged, nil, nil, false, false)
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %v", changes)
	}
}

func TestBuildChanges_coverEnabled(t *testing.T) {
	ss := stash.Scene{ID: "1"}
	merged := match.MergedScene{Thumbnail: "https://example.com/thumb.jpg"}
	changes := buildChanges(ss, merged, nil, nil, true, false)
	diff, ok := changes["cover"]
	if !ok {
		t.Fatal("expected cover change")
	}
	if diff.To == nil || diff.To == "" {
		t.Error("cover To should be set")
	}
}

func TestBuildChanges_organizedEmitsChange(t *testing.T) {
	ss := stash.Scene{ID: "1", Title: "Same", Organized: false}
	merged := match.MergedScene{Title: "Same"}

	// Without --organized, no change is emitted.
	if changes := buildChanges(ss, merged, nil, nil, false, false); len(changes) != 0 {
		t.Errorf("organized=false should emit nothing, got %v", changes)
	}

	// With --organized on an unorganized scene, emit the change so applyScene runs.
	changes := buildChanges(ss, merged, nil, nil, false, true)
	diff, ok := changes["organized"]
	if !ok {
		t.Fatal("expected organized change")
	}
	if diff.From != false || diff.To != true {
		t.Errorf("got from=%v to=%v", diff.From, diff.To)
	}
}

func TestBuildChanges_organizedAlreadySetNoChange(t *testing.T) {
	ss := stash.Scene{ID: "1", Title: "Same", Organized: true}
	merged := match.MergedScene{Title: "Same"}
	if changes := buildChanges(ss, merged, nil, nil, false, true); len(changes) != 0 {
		t.Errorf("already-organized scene should emit nothing, got %v", changes)
	}
}

func TestBuildChanges_addedTags(t *testing.T) {
	ss := stash.Scene{
		ID:   "1",
		Tags: []stash.Tag{{ID: "10", Name: "POV"}},
	}
	merged := match.MergedScene{}
	changes := buildChanges(ss, merged, nil, []string{"POV", "MILF", "Threesome"}, false, false)
	diff, ok := changes["tags"]
	if !ok {
		t.Fatal("expected tags change")
	}
	want := []string{"MILF", "Threesome"}
	if !reflect.DeepEqual(diff.Added, want) {
		t.Errorf("got added=%v, want %v", diff.Added, want)
	}
}

// --- diffScene ---------------------------------------------------------------
//
// diffScene is the layer above buildChanges: it assembles the tag set, merges
// URLs, and applies the --fields filter. It decides what `stash import --apply`
// writes to a real library, so each of those responsibilities is pinned here.

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func TestDiffScene_assemblesTagsFromMergedSceneAndImportTag(t *testing.T) {
	ss := stash.Scene{ID: "1"}
	merged := match.MergedScene{
		Title:      "T",
		Tags:       []string{"tag-a"},
		Categories: []string{"cat-b"},
	}
	o := importOpts{tagName: "FSS", stashboxTag: "FSS: StashDB"}

	_, allTags, _ := diffScene(ss, merged, o)

	for _, want := range []string{"tag-a", "cat-b", "FSS"} {
		if !hasTag(allTags, want) {
			t.Errorf("allTags %v missing %q", allTags, want)
		}
	}
	// No StashIDs on the scene, so the stashbox tag must not be applied.
	if hasTag(allTags, "FSS: StashDB") {
		t.Errorf("allTags %v contains the stashbox tag for a scene with no StashIDs", allTags)
	}
}

// The stashbox tag marks scenes that already carry StashDB metadata; applying
// it to a scene without StashIDs would mislabel it, and `stash revert` keys off
// that tag.
func TestDiffScene_stashboxTagOnlyWhenSceneHasStashIDs(t *testing.T) {
	ss := stash.Scene{ID: "1", StashIDs: []stash.StashID{{ID: "abc"}}}
	merged := match.MergedScene{Title: "T"}
	o := importOpts{tagName: "FSS", stashboxTag: "FSS: StashDB"}

	_, allTags, _ := diffScene(ss, merged, o)
	if !hasTag(allTags, "FSS: StashDB") {
		t.Errorf("allTags %v missing the stashbox tag for a scene with StashIDs", allTags)
	}
}

func TestDiffScene_resolutionTagsGatedOnFlag(t *testing.T) {
	ss := stash.Scene{ID: "1"}
	merged := match.MergedScene{Title: "T", Width: 3840}

	_, off, _ := diffScene(ss, merged, importOpts{tagName: "FSS"})
	if hasTag(off, "4K Available") {
		t.Errorf("resolution tag applied without --resolution-tags: %v", off)
	}

	_, on, _ := diffScene(ss, merged, importOpts{tagName: "FSS", resolutionTags: true})
	if !hasTag(on, "4K Available") {
		t.Errorf("allTags %v missing the resolution tag", on)
	}
}

func TestDiffScene_mergesURLsWithExisting(t *testing.T) {
	ss := stash.Scene{ID: "1", URLs: []string{"https://a.example/1"}}
	merged := match.MergedScene{Title: "T", URLs: []string{"https://b.example/2", "https://a.example/1"}}

	_, _, urls := diffScene(ss, merged, importOpts{tagName: "FSS"})
	if len(urls) != 2 {
		t.Fatalf("mergedURLs = %v, want the 2-entry union", urls)
	}
	if urls[0] != "https://a.example/1" {
		t.Errorf("mergedURLs = %v, want the existing URL retained first", urls)
	}
}

// --fields is the guard a user reaches for when they only want some metadata
// touched. If the filter leaks, --apply writes fields they explicitly excluded.
func TestDiffScene_fieldFilterDropsDisallowedChanges(t *testing.T) {
	ss := stash.Scene{ID: "1", Title: "Old", Details: "OldDetails"}
	merged := match.MergedScene{Title: "New", Description: "NewDetails"}

	all, _, _ := diffScene(ss, merged, importOpts{tagName: "FSS"})
	if _, ok := all["title"]; !ok {
		t.Fatal("unfiltered diff should contain a title change")
	}
	if _, ok := all["details"]; !ok {
		t.Fatal("unfiltered diff should contain a details change")
	}

	only := importOpts{tagName: "FSS", allowedFields: map[string]bool{"title": true}}
	got, _, _ := diffScene(ss, merged, only)
	if _, ok := got["title"]; !ok {
		t.Error("title was allowed but is missing from the diff")
	}
	if _, ok := got["details"]; ok {
		t.Errorf("details was not in --fields but survived the filter: %v", got)
	}
}

// A nil allowedFields means "no --fields given", which must allow everything
// rather than nothing.
func TestDiffScene_nilFieldFilterAllowsAll(t *testing.T) {
	ss := stash.Scene{ID: "1", Title: "Old"}
	merged := match.MergedScene{Title: "New"}
	got, _, _ := diffScene(ss, merged, importOpts{tagName: "FSS", allowedFields: nil})
	if _, ok := got["title"]; !ok {
		t.Errorf("nil allowedFields dropped a change: %v", got)
	}
}

// --- applyScene --------------------------------------------------------------
//
// applyScene is what `stash import --apply` actually writes to a user's
// library. fakeStash stands in for the GraphQL endpoint so the write path can
// be driven without a live Stash: it answers every find* with "not found",
// every create* with an id, and records which operations were requested.

type fakeStash struct {
	srv  *httptest.Server
	mu   sync.Mutex
	ops  []string
	last map[string]any // variables of the last sceneUpdate
	// existingTags/Performers make find* return a hit for these names, so the
	// "already exists" branches can be exercised as well as the create ones.
	existingTags map[string]string
	existingPerf map[string]string
}

func newFakeStash(t *testing.T) *fakeStash {
	t.Helper()
	f := &fakeStash{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		q := req.Query

		op, resp := "unknown", `{}`
		switch {
		case strings.Contains(q, "findTags"):
			op = "findTags"
			resp = `{"findTags":{"tags":[]}}`
			if id, ok := f.existingTags[lookupValue(req.Variables)]; ok {
				resp = fmt.Sprintf(`{"findTags":{"tags":[{"id":%q,"name":%q}]}}`, id, lookupValue(req.Variables))
			}
		case strings.Contains(q, "tagCreate"):
			op, resp = "tagCreate", `{"tagCreate":{"id":"t1"}}`
		case strings.Contains(q, "findPerformers"):
			op = "findPerformers"
			resp = `{"findPerformers":{"performers":[]}}`
			if id, ok := f.existingPerf[lookupValue(req.Variables)]; ok {
				resp = fmt.Sprintf(`{"findPerformers":{"performers":[{"id":%q,"name":%q}]}}`, id, lookupValue(req.Variables))
			}
		case strings.Contains(q, "performerCreate"):
			op, resp = "performerCreate", `{"performerCreate":{"id":"p1"}}`
		case strings.Contains(q, "findStudios"):
			op, resp = "findStudios", `{"findStudios":{"studios":[]}}`
		case strings.Contains(q, "studioCreate"):
			op, resp = "studioCreate", `{"studioCreate":{"id":"s1"}}`
		case strings.Contains(q, "sceneUpdate"):
			op, resp = "sceneUpdate", `{"sceneUpdate":{"id":"1"}}`
		}

		f.mu.Lock()
		f.ops = append(f.ops, op)
		if op == "sceneUpdate" {
			f.last = req.Variables
		}
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":%s}`, resp)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStash) client() *stash.Client { return stash.NewClient(f.srv.URL) }

// lookupValue reads the variable stash-go binds every name and alias lookup to.
func lookupValue(vars map[string]any) string {
	s, _ := vars["v"].(string)
	return s
}

func (f *fakeStash) sawOp(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.ops {
		if o == name {
			return true
		}
	}
	return false
}

// The whole point of --fields is that nothing outside the listed fields is
// touched. A leak here silently creates tags, performers and studios in a
// library the user asked not to modify.
func TestApplyScene_fieldFilterSuppressesWrites(t *testing.T) {
	f := newFakeStash(t)
	ss := stash.Scene{ID: "1", Files: []stash.File{{Path: "/v/a.mp4"}}}
	merged := match.MergedScene{Title: "T", Performers: []string{"P"}, Studio: "S"}
	o := importOpts{apply: true, allowedFields: map[string]bool{"title": true}}

	failures, err := applyScene(context.Background(), f.client(), ss, merged,
		[]string{"tag-a"}, nil, "imp1", o)
	if err != nil {
		t.Fatalf("applyScene: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %v, want none", failures)
	}
	if !f.sawOp("sceneUpdate") {
		t.Error("scene was never updated")
	}
	for _, op := range []string{"findTags", "tagCreate", "findPerformers", "performerCreate", "findStudios", "studioCreate"} {
		if f.sawOp(op) {
			t.Errorf("%s was called even though --fields allowed only title", op)
		}
	}
}

// With no --fields, tags/performers/studio are all resolved and written.
func TestApplyScene_writesAllFieldsByDefault(t *testing.T) {
	f := newFakeStash(t)
	ss := stash.Scene{ID: "1", Files: []stash.File{{Path: "/v/a.mp4"}}}
	merged := match.MergedScene{Title: "T", Performers: []string{"P"}, Studio: "S"}

	if _, err := applyScene(context.Background(), f.client(), ss, merged,
		[]string{"tag-a"}, nil, "imp1", importOpts{apply: true}); err != nil {
		t.Fatalf("applyScene: %v", err)
	}
	for _, op := range []string{"findTags", "tagCreate", "findPerformers", "performerCreate", "findStudios", "studioCreate", "sceneUpdate"} {
		if !f.sawOp(op) {
			t.Errorf("%s was not called", op)
		}
	}
}

// A scene with StashIDs gets the stashbox tag; `stash revert` keys off it, so
// applying it to the wrong scenes makes a revert overreach.
func TestApplyScene_stashboxTagOnlyForScenesWithStashIDs(t *testing.T) {
	ss := stash.Scene{ID: "1", Files: []stash.File{{Path: "/v/a.mp4"}}}
	merged := match.MergedScene{Title: "T"}
	o := importOpts{apply: true, stashboxTag: "FSS: StashDB"}

	withIDs := newFakeStash(t)
	ssWith := ss
	ssWith.StashIDs = []stash.StashID{{ID: "abc"}}
	if _, err := applyScene(context.Background(), withIDs.client(), ssWith, merged, nil, nil, "imp1", o); err != nil {
		t.Fatalf("applyScene: %v", err)
	}
	if !withIDs.sawOp("findTags") {
		t.Error("stashbox tag was not resolved for a scene with StashIDs")
	}

	without := newFakeStash(t)
	if _, err := applyScene(context.Background(), without.client(), ss, merged, nil, nil, "imp1", o); err != nil {
		t.Fatalf("applyScene: %v", err)
	}
	if without.sawOp("findTags") {
		t.Error("stashbox tag was resolved for a scene with no StashIDs")
	}
}

// --- entityLookup -------------------------------------------------------------
//
// entityLookup decides what `stash import` reports as "would create on apply" —
// i.e. what the operator is told will be added to their library. It was at 0%.
//
// Its error path is a deliberate choice worth pinning: a failed lookup records
// the name as *existing*, so a transient Stash error cannot produce a misleading
// "would create" entry. A future refactor that flipped that to `false` would
// silently start promising to create things that are already there.

// lookupServer answers the tag/performer/studio queries entityLookup makes.
// name/alias sets decide what "exists"; failOn makes a query error.
func lookupServer(t *testing.T, exists map[string]bool, aliases map[string]bool, failOn string) *stash.Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		value := lookupValue(req.Variables)
		byAlias := strings.Contains(req.Query, "aliases:")
		if failOn != "" && value == failOn {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		hit := (byAlias && aliases[value]) || (!byAlias && exists[value])
		body := `{"tags":[]}`
		switch {
		case strings.Contains(req.Query, "findTags"):
			if hit {
				body = `{"findTags":{"tags":[{"id":"1","name":"x"}]}}`
			} else {
				body = `{"findTags":{"tags":[]}}`
			}
		case strings.Contains(req.Query, "findPerformers"):
			if hit {
				body = `{"findPerformers":{"performers":[{"id":"1","name":"x"}]}}`
			} else {
				body = `{"findPerformers":{"performers":[]}}`
			}
		case strings.Contains(req.Query, "findStudios"):
			if hit {
				body = `{"findStudios":{"studios":[{"id":"1","name":"x"}]}}`
			} else {
				body = `{"findStudios":{"studios":[]}}`
			}
		}
		_, _ = fmt.Fprintf(w, `{"data":%s}`, body)
	}))
	t.Cleanup(ts.Close)
	return stash.NewClient(ts.URL)
}

func TestEntityLookupTagByNameAndAlias(t *testing.T) {
	c := lookupServer(t, map[string]bool{"HasName": true}, map[string]bool{"OnlyAlias": true}, "")
	l := newEntityLookup(context.Background(), c)

	l.checkTag("HasName")
	l.checkTag("OnlyAlias") // absent by name, present by alias
	l.checkTag("Missing")

	if !l.tags["HasName"] {
		t.Error("tag found by name recorded as missing")
	}
	if !l.tags["OnlyAlias"] {
		t.Error("tag found by *alias* recorded as missing — the alias fallback did not run")
	}
	if l.tags["Missing"] {
		t.Error("absent tag recorded as existing")
	}
}

// A lookup error must record the name as existing, so it stays out of the
// would-create list rather than promising a creation that may be wrong.
func TestEntityLookupTreatsErrorsAsExisting(t *testing.T) {
	c := lookupServer(t, nil, nil, "Explodes")
	l := newEntityLookup(context.Background(), c)

	stderr := captureStderr(t, func() { l.checkTag("Explodes") })

	if !l.tags["Explodes"] {
		t.Error("a failed lookup was recorded as missing; it must be treated as existing")
	}
	if !strings.Contains(stderr, "Explodes") {
		t.Errorf("no warning naming the failed lookup; stderr = %q", stderr)
	}
}

// Each name is queried once — the map doubles as a cache, and re-querying would
// multiply requests against the operator's Stash on a large import.
func TestEntityLookupCachesByName(t *testing.T) {
	var queries int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		queries++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"findTags":{"tags":[{"id":"1","name":"x"}]}}}`)
	}))
	defer ts.Close()

	l := newEntityLookup(context.Background(), stash.NewClient(ts.URL))
	l.checkTag("Same")
	l.checkTag("Same")
	l.checkTag("Same")
	if queries != 1 {
		t.Errorf("made %d queries for one name, want 1 (the map is the cache)", queries)
	}
}

func TestEntityLookupPerformerAndStudio(t *testing.T) {
	c := lookupServer(t, map[string]bool{"Known": true}, nil, "")
	l := newEntityLookup(context.Background(), c)

	l.checkPerformer("Known")
	l.checkPerformer("Unknown")
	l.checkStudio("Known")
	l.checkStudio("Unknown")

	if !l.performers["Known"] || l.performers["Unknown"] {
		t.Errorf("performers = %v", l.performers)
	}
	if !l.studios["Known"] || l.studios["Unknown"] {
		t.Errorf("studios = %v", l.studios)
	}
}

// Import must never drop a performer Stash already has: the update seeds its
// PerformerIDs from the scene's existing cast and only appends. This matters
// most with `fss scrape --performer`, which replaces the performer list on the
// FSS side — if import were authoritative, relabelling a solo performer would
// strip her co-stars out of Stash rather than merely stop adding them.
func TestApplyScene_keepsPerformersAlreadyOnTheStashScene(t *testing.T) {
	f := newFakeStash(t)
	f.existingPerf = map[string]string{"Jodi West": "p-jodi"}

	ss := stash.Scene{
		ID:    "1",
		Files: []stash.File{{Path: "/v/a.mp4"}},
		// Already attached in Stash, and deliberately absent from merged.
		Performers: []stash.Performer{{ID: "p-costar", Name: "Marcello Bravo"}},
	}
	merged := match.MergedScene{Title: "T", Performers: []string{"Jodi West"}}

	if _, err := applyScene(context.Background(), f.client(), ss, merged,
		nil, nil, "imp1", importOpts{apply: true}); err != nil {
		t.Fatalf("applyScene: %v", err)
	}

	f.mu.Lock()
	vars := f.last
	f.mu.Unlock()
	if vars == nil {
		t.Fatal("no sceneUpdate was sent")
	}

	input, ok := vars["input"].(map[string]any)
	if !ok {
		t.Fatalf("sceneUpdate had no input object: %#v", vars)
	}
	raw, ok := input["performer_ids"].([]any)
	if !ok {
		t.Fatalf("performer_ids missing or not a list: %#v", input["performer_ids"])
	}
	got := map[string]bool{}
	for _, v := range raw {
		got[fmt.Sprint(v)] = true
	}
	if !got["p-costar"] {
		t.Errorf("performer_ids = %v — the co-star already in Stash was dropped", raw)
	}
	if !got["p-jodi"] {
		t.Errorf("performer_ids = %v — the merged performer was not added", raw)
	}
}

// truncateEach maps truncate over a slice, keeping it the same length so the
// caller's columns still line up.
func TestTruncateEach(t *testing.T) {
	got := truncateEach([]string{"hello world", "hi", "another long one"}, 8)
	want := []string{"hello...", "hi", "anoth..."} // limit-3 runes plus the ellipsis
	if !reflect.DeepEqual(got, want) {
		t.Errorf("truncateEach = %q, want %q", got, want)
	}
}

func TestTruncateEachEmpty(t *testing.T) {
	if got := truncateEach(nil, 8); len(got) != 0 {
		t.Errorf("truncateEach(nil) = %q, want empty", got)
	}
}

//go:build integration

package cmd

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	stash "github.com/Anastylosis/stash-go"
	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
)

// These run against a real Stash: `make smoke-stash`, or
// `go test -tags integration ./cmd/`. FSS_STASH_URL defaults to
// http://localhost:9999, FSS_STASH_API_KEY may be empty.
//
// They cover fss's own layer — client construction, the flag-to-filter
// mapping, and the lookups import and revert make. The GraphQL client itself
// is stash-go's to test.
//
// Read-only: nothing here creates, updates or deletes. checkTag and the
// resolveExisting* helpers only query; the Ensure* calls that would create are
// deliberately not exercised.
func liveStash(t *testing.T) *stash.Client {
	t.Helper()

	url := os.Getenv("FSS_STASH_URL")
	if url == "" {
		url = "http://localhost:9999"
	}

	orig := cfg
	t.Cleanup(func() { cfg = orig })
	cfg = &config.Config{Stash: config.StashConfig{URL: url, APIKey: os.Getenv("FSS_STASH_API_KEY")}}

	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("url", "", "")
	cmd.Flags().String("api-key", "", "")

	c := newStashClient(cmd)
	// Short deadline: an unreachable server should skip promptly rather than
	// sit through the transport's retries.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("Stash not reachable at %s: %v", url, err)
	}
	return c
}

func liveScenes(t *testing.T, c *stash.Client, o importOpts) []stash.Scene {
	t.Helper()
	scenes, err := queryStashScenes(context.Background(), c, o, io.Discard)
	if err != nil {
		t.Fatalf("queryStashScenes: %v", err)
	}
	return scenes
}

// The whole client path: config, API key, and fss's retrying transport.
func TestLiveStashClientConnects(t *testing.T) {
	liveStash(t)
}

// import's default query asks for scenes without stash-box metadata. Getting
// the filter mapping wrong silently changes which scenes an import touches.
func TestLiveQueryStashScenesUnmatchedOnly(t *testing.T) {
	c := liveStash(t)

	scenes := liveScenes(t, c, importOpts{top: 20})
	for _, s := range scenes {
		if len(s.StashIDs) > 0 {
			t.Errorf("scene %s has stash_ids but came back from the unmatched query", s.ID)
		}
	}
	t.Logf("%d unmatched scenes", len(scenes))

	all := liveScenes(t, c, importOpts{top: 20, includeStashbox: true})
	if len(all) < len(scenes) {
		t.Errorf("--include-stashbox returned %d scenes, fewer than the %d unmatched", len(all), len(scenes))
	}
}

func TestLiveQueryStashScenesTopLimits(t *testing.T) {
	c := liveStash(t)
	scenes := liveScenes(t, c, importOpts{top: 3, includeStashbox: true})
	if len(scenes) > 3 {
		t.Errorf("--top 3 returned %d scenes", len(scenes))
	}
}

func TestLiveQueryStashScenesFilters(t *testing.T) {
	c := liveStash(t)
	scenes := liveScenes(t, c, importOpts{top: 20, includeStashbox: true})
	if len(scenes) == 0 {
		t.Skip("no scenes in Stash")
	}

	for _, s := range scenes {
		if s.Studio == nil {
			continue
		}
		matched := liveScenes(t, c, importOpts{top: 5, includeStashbox: true, studio: s.Studio.Name})
		if len(matched) == 0 {
			t.Errorf("filtering by studio %q matched nothing, want at least scene %s", s.Studio.Name, s.ID)
		}
		break
	}

	for _, s := range scenes {
		if len(s.Files) == 0 || s.Files[0].Basename == "" {
			continue
		}
		substr := s.Files[0].Basename[:min(10, len(s.Files[0].Basename))]
		matched := liveScenes(t, c, importOpts{top: 5, includeStashbox: true, pathFilter: substr})
		if len(matched) == 0 {
			t.Errorf("filtering by path %q matched nothing, want at least scene %s", substr, s.ID)
		}
		break
	}
}

// A misspelled --performer used to look exactly like "nothing matched" and
// exit 0. It must be an error naming the sentinel.
func TestLiveQueryStashScenesRejectsUnknownFilterTargets(t *testing.T) {
	c := liveStash(t)
	ctx := context.Background()

	for _, o := range []importOpts{
		{performer: "fss_nonexistent_42xyz"},
		{studio: "fss_nonexistent_42xyz"},
	} {
		if _, err := queryStashScenes(ctx, c, o, io.Discard); err == nil {
			t.Errorf("queryStashScenes(%+v) succeeded, want a not-found error", o)
		}
	}
}

// entityLookup drives what `stash import` reports as "would create on apply".
// Against a real library, a name already on a scene must never be reported as
// missing.
func TestLiveEntityLookupFindsWhatScenesAlreadyHave(t *testing.T) {
	c := liveStash(t)
	scenes := liveScenes(t, c, importOpts{top: 20, includeStashbox: true})

	l := newEntityLookup(context.Background(), c)
	var checked int
	for _, s := range scenes {
		for _, tag := range s.Tags {
			l.checkTag(tag.Name)
			if !l.tags[tag.Name] {
				t.Errorf("tag %q is on scene %s but the lookup reported it missing", tag.Name, s.ID)
			}
			checked++
		}
		for _, p := range s.Performers {
			l.checkPerformer(p.Name)
			if !l.performers[p.Name] {
				t.Errorf("performer %q is on scene %s but the lookup reported it missing", p.Name, s.ID)
			}
			checked++
		}
		if s.Studio != nil {
			l.checkStudio(s.Studio.Name)
			if !l.studios[s.Studio.Name] {
				t.Errorf("studio %q is on scene %s but the lookup reported it missing", s.Studio.Name, s.ID)
			}
			checked++
		}
		if checked > 0 {
			break
		}
	}
	if checked == 0 {
		t.Skip("no scene carries a tag, performer or studio")
	}
}

// revert resolves changelog names back to IDs, dropping whatever has been
// deleted from Stash since the import rather than failing.
func TestLiveResolveExistingIDsDropsUnknownNames(t *testing.T) {
	c := liveStash(t)
	ctx := context.Background()
	scenes := liveScenes(t, c, importOpts{top: 20, includeStashbox: true})

	var tagName, perfName string
	for _, s := range scenes {
		if len(s.Tags) > 0 && tagName == "" {
			tagName = s.Tags[0].Name
		}
		if len(s.Performers) > 0 && perfName == "" {
			perfName = s.Performers[0].Name
		}
	}
	if tagName == "" && perfName == "" {
		t.Skip("no scene carries a tag or performer")
	}

	if tagName != "" {
		ids, err := resolveExistingTagIDs(ctx, c, []string{tagName, "fss_deleted_since_import_42xyz"})
		if err != nil {
			t.Fatalf("resolveExistingTagIDs: %v", err)
		}
		if len(ids) != 1 {
			t.Errorf("got %d tag IDs, want only the one that still exists", len(ids))
		}
	}

	if perfName != "" {
		ids, err := resolveExistingPerfIDs(ctx, c, []string{perfName, "FSS Deleted Since Import 42xyz"})
		if err != nil {
			t.Fatalf("resolveExistingPerfIDs: %v", err)
		}
		if len(ids) != 1 {
			t.Errorf("got %d performer IDs, want only the one that still exists", len(ids))
		}
	}
}

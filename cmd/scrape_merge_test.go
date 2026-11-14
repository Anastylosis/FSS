package cmd

import (
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestMergeRunAbsentPolicy(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	gone := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	sc := func(id string, del *time.Time) models.Scene {
		return models.Scene{ID: id, SiteID: "s", Title: id, DeletedAt: del}
	}
	existing := []models.Scene{sc("a", nil), sc("b", nil), sc("c", &gone)}
	fresh := []models.Scene{sc("a", nil), sc("a", nil), sc("n", nil)}

	cases := []struct {
		name       string
		policy     absentPolicy
		incomplete bool
		wantIDs    []string
		deleted    map[string]bool
		newly      int
	}{
		{"drop", absentDrop, false, []string{"a", "n"}, nil, 0},
		{"drop incomplete keeps all", absentDrop, true, []string{"a", "n", "b", "c"}, map[string]bool{"c": true}, 0},
		{"keep", absentKeep, false, []string{"a", "n", "b", "c"}, map[string]bool{"c": true}, 0},
		{"soft delete", absentSoftDelete, false, []string{"a", "n", "b", "c"}, map[string]bool{"b": true, "c": true}, 1},
		{"soft delete incomplete", absentSoftDelete, true, []string{"a", "n", "b", "c"}, map[string]bool{"c": true}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := mergeRun(existing, fresh, traversal{incomplete: c.incomplete}, c.policy, true, now)
			var ids []string
			for _, s := range out.scenes {
				ids = append(ids, s.ID)
				if (s.DeletedAt != nil) != c.deleted[s.ID] {
					t.Errorf("%s: deleted=%v want %v", s.ID, s.DeletedAt != nil, c.deleted[s.ID])
				}
			}
			if len(ids) != len(c.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, c.wantIDs)
			}
			for i := range ids {
				if ids[i] != c.wantIDs[i] {
					t.Fatalf("ids = %v, want %v", ids, c.wantIDs)
				}
			}
			if out.newlyDeleted != c.newly || out.newCount != 1 {
				t.Errorf("newlyDeleted=%d newCount=%d", out.newlyDeleted, out.newCount)
			}
			if out.cov.storedAll != 3 || out.cov.stored != 2 || out.cov.fresh != 2 {
				t.Errorf("cov = %+v", out.cov)
			}
		})
	}
	if existing[1].DeletedAt != nil {
		t.Error("mergeRun mutated its input")
	}
}

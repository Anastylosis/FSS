package cmd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// A-1: a clean empty --refresh of a small studio must refuse, not soft-delete.
func TestScrapeOne_emptyRefreshGuard(t *testing.T) {
	const url = "https://example.com/a1-refresh-guard"
	now := time.Now().UTC().Truncate(time.Second)
	var seed []models.Scene
	for _, id := range []string{"1", "2", "3"} {
		seed = append(seed, models.Scene{ID: id, SiteID: "a1g", StudioURL: url,
			Title: "S" + id, URL: "https://example.com/v/" + id, ScrapedAt: now})
	}
	scraper.Register(&fakeRegistered{id: "a1gempty", url: url})
	st := store.NewFlat(t.TempDir(), []string{"json"})
	if err := st.Save(url, seed); err != nil {
		t.Fatal(err)
	}

	run := func(force bool) {
		t.Helper()
		if err := scrapeOne(context.Background(), st, scrapeTarget{url: url}, "", "", "", []string{"json"},
			false, true, force, true, 1, siteSettings{}, sceneOverrides{}); (err != nil) == force || (err != nil && exitCodeOf(err) != ExitRefused) {
			t.Fatalf("scrapeOne(force=%v): %v", force, err)
		}
	}
	run(false)
	got, _ := st.Load(url)
	for _, s := range got {
		if s.DeletedAt != nil {
			t.Fatalf("scene %s soft-deleted by an empty --refresh without --force", s.ID)
		}
	}
	run(true)
	got, _ = st.Load(url)
	for _, s := range got {
		if s.DeletedAt == nil {
			t.Errorf("--force should soft-delete, scene %s is live", s.ID)
		}
	}
}

// A-2: already soft-deleted scenes are not part of the coverage denominator.
func TestCoverageOf_ignoresSoftDeleted(t *testing.T) {
	now := time.Now()
	existing := map[sceneKey]models.Scene{}
	seen := map[sceneKey]bool{}
	for i := 0; i < 20; i++ {
		s := models.Scene{ID: fmt.Sprint(i), SiteID: "x"}
		if i >= 5 {
			s.DeletedAt = &now
		} else {
			seen[keyOf(s)] = true
		}
		existing[keyOf(s)] = s
	}
	cov := coverageOf(existing, seen, traversal{})
	if cov.stored != 5 || cov.seen != 5 || cov.fresh != 5 || cov.collapsed() {
		t.Errorf("got %+v, want stored=5 seen=5 fresh=5 not collapsed", cov)
	}
}

// A-7: soft-deleted IDs must not reach the scraper as KnownIDs.
func TestScrapeIncremental_deletedNotKnown(t *testing.T) {
	const url = "https://example.com/a7"
	now := time.Now().UTC()
	st := store.NewFlat(t.TempDir(), []string{"json"})
	if err := st.Save(url, []models.Scene{
		{ID: "live", SiteID: "a7", StudioURL: url, Title: "L", URL: "u1", ScrapedAt: now},
		{ID: "gone", SiteID: "a7", StudioURL: url, Title: "G", URL: "u2", ScrapedAt: now, DeletedAt: &now},
	}); err != nil {
		t.Fatal(err)
	}
	sc := &knownCapture{}
	if _, _, err := scrapeIncremental(context.Background(), sc, st, url, scraper.ListOpts{Workers: 1}, true, sceneOverrides{}); err != nil {
		t.Fatal(err)
	}
	if !sc.known["live"] || sc.known["gone"] {
		t.Errorf("KnownIDs = %v, want only live", sc.known)
	}
}

type knownCapture struct{ known map[string]bool }

func (k *knownCapture) ID() string             { return "a7" }
func (k *knownCapture) Patterns() []string     { return nil }
func (k *knownCapture) MatchesURL(string) bool { return true }
func (k *knownCapture) ListScenes(_ context.Context, _ string, o scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	k.known = o.KnownIDs
	ch := make(chan scraper.SceneResult)
	close(ch)
	return ch, nil
}

type failSecondLoad struct {
	store.Store
	loads int
}

func (f *failSecondLoad) Load(u string) ([]models.Scene, error) {
	f.loads++
	if f.loads > 1 {
		return nil, fmt.Errorf("boom")
	}
	return f.Store.Load(u)
}

// A-14: a Load failure in the guard refuses instead of saving the empty set.
func TestScrapeOne_guardLoadErrorRefuses(t *testing.T) {
	const url = "https://example.com/a14"
	scraper.Register(&fakeRegistered{id: "a14", url: url})
	base := store.NewFlat(t.TempDir(), []string{"json"})
	if err := base.Save(url, []models.Scene{{ID: "1", SiteID: "a14", StudioURL: url, Title: "x", URL: "u", ScrapedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	st := &failSecondLoad{Store: base}
	err := scrapeOne(context.Background(), st, scrapeTarget{url: url}, "", "", "", []string{"json"},
		true, false, false, true, 1, siteSettings{}, sceneOverrides{})
	if err == nil {
		t.Fatal("expected error from guard Load failure")
	}
	got, _ := base.Load(url)
	if len(got) != 1 {
		t.Errorf("scene count = %d, want 1 untouched", len(got))
	}
}

// D-27 / E-8: soft-deleted scenes are dropped from the shared loader's output.
func TestDropDeleted(t *testing.T) {
	now := time.Now()
	in := []models.Scene{{ID: "a"}, {ID: "b", DeletedAt: &now}, {ID: "c"}}
	out := dropDeleted(in)
	if len(out) != 2 || out[0].ID != "a" || out[1].ID != "c" {
		t.Errorf("got %v", out)
	}
	if len(in) != 3 {
		t.Error("input mutated")
	}
}

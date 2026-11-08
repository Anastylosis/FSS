package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func seedDeleted(t *testing.T, st store.Store, url, site string, n int) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	var seed []models.Scene
	for i := 0; i < n; i++ {
		seed = append(seed, models.Scene{ID: fmt.Sprint(i), SiteID: site, StudioURL: url,
			Title: "S", URL: fmt.Sprintf("https://example.com/v/%d", i), ScrapedAt: now, DeletedAt: &now})
	}
	if err := st.Save(url, seed); err != nil {
		t.Fatal(err)
	}
}

// --full hard-deletes soft-deleted scenes too, so it must refuse on them.
func TestScrapeOne_fullGuardCountsSoftDeleted(t *testing.T) {
	const url = "https://example.com/a1-all-deleted"
	scraper.Register(&fakeRegistered{id: "a1alldel", url: url})
	st := store.NewFlat(t.TempDir(), []string{"json"})
	seedDeleted(t, st, url, "a1alldel", 30)
	if err := scrapeOne(context.Background(), st, scrapeTarget{url: url}, "", "", "", []string{"json"},
		true, false, false, true, 1, siteSettings{}, sceneOverrides{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(url); len(got) != 30 {
		t.Errorf("%d scenes remain, want 30", len(got))
	}
}

// An incremental run that finds nothing of a populated studio still warns.
func TestScrapeOne_incrementalZeroFreshWarns(t *testing.T) {
	const url = "https://example.com/inc-zero"
	scraper.Register(&fakeRegistered{id: "inczero", url: url})
	st := store.NewFlat(t.TempDir(), []string{"json"})
	now := time.Now().UTC()
	var seed []models.Scene
	for i := 0; i < 12; i++ {
		seed = append(seed, models.Scene{ID: fmt.Sprint(i), SiteID: "inczero", StudioURL: url, Title: "S", URL: fmt.Sprint("u", i), ScrapedAt: now})
	}
	if err := st.Save(url, seed); err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() {
		if err := scrapeOne(context.Background(), st, scrapeTarget{url: url}, "", "", "", []string{"json"},
			false, false, false, true, 1, siteSettings{}, sceneOverrides{}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "re-saw 0 of 12") {
		t.Errorf("no collapse warning, stderr: %q", out)
	}
}

func TestDiffStores_zeroLiveFlatStudioInSync(t *testing.T) {
	d := diffStores(studioInventory{"https://a.example/x": 0}, studioInventory{})
	if !d.inSync() {
		t.Errorf("got %+v, want in sync", d)
	}
}

func sourceCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "x"}
	addSceneSourceFlags(c)
	if err := c.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoadFSSScenes_includeDeleted(t *testing.T) {
	const url = "https://example.com/lf"
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.NewFlat(dir, []string{"json"}).Save(url, []models.Scene{
		{ID: "1", SiteID: "lf", StudioURL: url, Title: "live", URL: "u1", ScrapedAt: now},
		{ID: "2", SiteID: "lf", StudioURL: url, Title: "gone", URL: "u2", ScrapedAt: now, DeletedAt: &now},
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	out := captureStderr(t, func() {
		sc, _, err := loadFSSScenes(sourceCmd(t, "--dir", dir))
		if err != nil {
			t.Error(err)
		}
		n = len(sc)
	})
	if n != 1 || !strings.Contains(out, "skipped 1 soft-deleted") {
		t.Errorf("default: n=%d stderr=%q", n, out)
	}
	sc, _, err := loadFSSScenes(sourceCmd(t, "--dir", dir, "--include-deleted"))
	if err != nil || len(sc) != 2 {
		t.Errorf("include-deleted: %d scenes, err %v", len(sc), err)
	}

	dir2 := t.TempDir()
	seedDeleted(t, store.NewFlat(dir2, []string{"json"}), url, "lf", 3)
	if _, _, err := loadFSSScenes(sourceCmd(t, "--dir", dir2)); err == nil || !strings.Contains(err.Error(), "marked deleted") {
		t.Errorf("all-deleted: err = %v", err)
	}
}

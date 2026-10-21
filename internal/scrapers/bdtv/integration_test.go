//go:build integration

package bdtv

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/bdtvutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func row(t *testing.T, id string) bdtvutil.SiteConfig {
	t.Helper()
	for _, c := range sites {
		if c.SiteID == id {
			return c
		}
	}
	t.Fatalf("no row %q", id)
	return bdtvutil.SiteConfig{}
}

func TestLiveHarmonyConcepts(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "harmonyconcepts")), "https://harmonyvideo.tv/", 3)
}

func TestLiveHarmonyConceptsStudioOnBDTV(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "harmonyconcepts")), "https://bdtv.com/studio-display?studio_id=66", 3)
}

func TestLiveHarmonyConceptsActor(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "harmonyconcepts")), "https://harmonyvideo.tv/actor/908", 3)
}

func TestLiveHarmonyConceptsCategory(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "harmonyconcepts")), "https://harmonyvideo.tv/display-movies/category/1482", 3)
}

func TestLiveCloseUp(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "closeup")), "https://bdtv.com/", 3)
}

func TestLiveCloseUpStudio(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "closeup")), "https://bdtv.com/studio-display?studio_id=58", 3)
}

func TestLiveCloseUpActor(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "closeup")), "https://bdtv.com/actor/846", 3)
}

func TestLiveCloseUpCategory(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "closeup")), "https://bdtv.com/display-movies/category/1469", 3)
}

func TestLiveCurtisDupontWrestling(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "curtisdupontwrestling")), "https://bdtv.com/studio-display?studio_id=75", 3)
}

func TestLiveHelplessHeroes(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "helplessheroes")), "https://bdtv.com/studio-display?studio_id=71", 3)
}

func TestLiveMatrockWrestling(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "matrockwrestling")), "https://bdtv.com/studio-display?studio_id=69", 3)
}

func TestLiveTaoProductions(t *testing.T) {
	testutil.RunLiveScrape(t, bdtvutil.New(row(t, "taoproductions")), "https://bdtv.com/studio-display?studio_id=59", 3)
}

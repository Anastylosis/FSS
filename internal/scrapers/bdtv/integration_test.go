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

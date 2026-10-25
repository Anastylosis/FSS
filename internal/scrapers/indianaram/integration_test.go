//go:build integration

package indianaram

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func liveScraper(t *testing.T, id string) *Scraper {
	t.Helper()
	for _, cfg := range sites {
		if cfg.id == id {
			return newScraper(cfg)
		}
	}
	t.Fatalf("no site %s", id)
	return nil
}

func TestLiveSlowTeasingHandJobs(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "slowteasinghandjobs"), "https://slowteasinghandjobs.com/sthj_main.htm", 3)
}

func TestLiveSlowTeasingHandJobsModel(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "slowteasinghandjobs"), "https://slowteasinghandjobs.com/sthj_directory/model_pages/sthj_directory_aaron.htm", 3)
}

func TestLiveReluctantYoungMen(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "reluctantyoungmen"), "https://reluctantyoungmen.com/", 3)
}

func TestLiveStraightMenInTrouble(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "straightmenintrouble"), "https://straightmenintrouble.com/", 3)
}

func TestLiveHardUpStraightGuys(t *testing.T) {
	testutil.RunLiveScrape(t, liveScraper(t, "hardupstraightguys"), "https://hardupstraightguys.com/", 3)
}

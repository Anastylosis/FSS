//go:build integration

package ghostpro

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// One live smoke per template variant the network exposes. We don't run a
// per-site smoke for all 9 sister sites: they share the parser and the
// table-driven self-test (TestSitesTable_uniqueIDsAndDomainIsolation) already
// covers per-site config. AsianSuckDolls is the canonical example with full
// payload fields; TussineeGold is a smaller site (~16 scenes) used to
// exercise the small-catalogue / total_pages cutoff path.

func TestLiveAsianSuckDolls(t *testing.T) {
	cfg := findSite(t, "asiansuckdolls")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/videos", 4)
}

func TestLiveTussineeGold(t *testing.T) {
	cfg := findSite(t, "tussineegold")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/videos", 4)
}

func TestLiveJohnnyRapid(t *testing.T) {
	cfg := findSite(t, "johnnyrapid")
	testutil.RunLiveScrape(t, New(cfg), cfg.SiteBase+"/videos", 4)
}

func findSite(t *testing.T, id string) SiteConfig {
	t.Helper()
	for _, cfg := range sites {
		if cfg.ID == id {
			return cfg
		}
	}
	t.Fatalf("no site config registered with ID %q", id)
	return SiteConfig{}
}

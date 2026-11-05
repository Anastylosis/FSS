package videoelements

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 14 {
		t.Errorf("expected 14 sites, got %d", len(sites))
	}
}

func TestUniqueSiteIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.SiteID] {
			t.Errorf("duplicate SiteID: %s", cfg.SiteID)
		}
		seen[cfg.SiteID] = true
	}
}

// Config-only wrapper keyed on bare hostnames: parsing lives in the *util it
// delegates to, and the SiteBase is built as "https://www." + domain. What is
// not covered elsewhere is this table's integrity — see
// testutil.CheckSiteDomainTable.
func TestSiteTableIntegrity(t *testing.T) {
	rows := make([]testutil.DomainRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.DomainRow{ID: c.SiteID, Domain: c.Domain, Studio: c.StudioName})
	}
	testutil.CheckSiteDomainTable(t, rows)
}

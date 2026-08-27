package aylo

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 56 {
		t.Errorf("expected 56 sites, got %d", len(sites))
	}
}

func TestScraperInterface(t *testing.T) {
	for _, cfg := range sites {
		_ = cfg
		var _ scraper.StudioScraper = &siteScraper{}
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

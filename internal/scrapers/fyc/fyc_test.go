package fyc

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 21 {
		t.Errorf("expected 21 sites, got %d", len(sites))
	}
}

func TestScraperInterface(t *testing.T) {
	for _, cfg := range sites {
		s, err := scraper.ForID(cfg.SiteID)
		if err != nil {
			t.Errorf("ForID(%q): %v", cfg.SiteID, err)
			continue
		}
		_ = s
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

func TestUniqueDomains(t *testing.T) {
	seen := map[string]bool{}
	for _, cfg := range sites {
		if seen[cfg.Domain] {
			t.Errorf("duplicate domain: %s", cfg.Domain)
		}
		seen[cfg.Domain] = true
	}
}

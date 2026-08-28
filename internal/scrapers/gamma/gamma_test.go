package gamma

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 179 {
		t.Errorf("expected 179 sites, got %d", len(sites))
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

// TestPrideStudiosSites pins the Pride Studios family added to the ASGMAX
// segment. The brands split into own-domain sites and sites that redirect to
// pridestudios.com; the latter need RefererBase pinned to the hub or the
// Referer-restricted Algolia key is rejected with HTTP 403.
func TestPrideStudiosSites(t *testing.T) {
	byID := map[string]siteConfig{}
	for _, cfg := range sites {
		byID[cfg.SiteID] = cfg
	}

	ownDomain := []string{"extrabigdicks", "menover30", "familycreep", "pridestudios"}
	redirecting := []string{
		"circlejerkboys", "boyzparty", "highperformancemen",
		"dylanlucas", "cockvirgins", "bearback",
	}

	for _, id := range append(append([]string{}, ownDomain...), redirecting...) {
		cfg, ok := byID[id]
		if !ok {
			t.Errorf("missing Pride Studios site %q", id)
			continue
		}
		// SiteName is the Algolia availableOnSite filter. Leaving it empty
		// would drop the filter and return the whole 13k-scene asgmax
		// segment instead of this brand.
		if cfg.SiteName != id {
			t.Errorf("%s: SiteName = %q, want %q", id, cfg.SiteName, id)
		}
		if cfg.StudioName == "" {
			t.Errorf("%s: StudioName is empty", id)
		}
	}

	for _, id := range ownDomain {
		if got := byID[id].RefererBase; got != "" {
			t.Errorf("%s serves its own /en/videos, so RefererBase should be empty, got %q", id, got)
		}
	}
	for _, id := range redirecting {
		if got := byID[id].RefererBase; got != "https://www.pridestudios.com" {
			t.Errorf("%s redirects to the hub, so RefererBase must be pinned there, got %q", id, got)
		}
	}
}

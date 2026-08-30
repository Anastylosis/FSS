package brandnewamateurs

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/adultdoorwayclassicutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteTable(t *testing.T) {
	ids := map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.ID] {
			t.Errorf("duplicate site id %q", cfg.ID)
		}
		ids[cfg.ID] = true
		if cfg.Studio == "" || cfg.MatchRe == nil {
			t.Errorf("%s: incomplete config %+v", cfg.ID, cfg)
		}
		// All three mount the tour at the site root.
		if cfg.TourPrefix != "" {
			t.Errorf("%s: TourPrefix = %q, want empty", cfg.ID, cfg.TourPrefix)
		}
		got, err := scraper.ForURL(cfg.SiteBase + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.SiteBase, err)
			continue
		}
		if got.ID() != cfg.ID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.SiteBase, got.ID(), cfg.ID)
		}
	}
	if len(sites) != 3 {
		t.Errorf("expected 3 sites, got %d", len(sites))
	}
}

// The siblings each publish their own catalogue, so a URL must resolve to its
// own scraper rather than to the network's flagship.
func TestSiblingsDoNotClaimEachOther(t *testing.T) {
	flagship := adultdoorwayclassicutil.New(sites[0])
	if flagship.MatchesURL("https://footjobvirgin.com/") || flagship.MatchesURL("https://jackoffgirls.com/") {
		t.Error("brandnewamateurs must not match its siblings")
	}
	if flagship.MatchesURL("https://notbrandnewamateurs.com/") {
		t.Error("a look-alike host must not match")
	}
}

package paysitemanager

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/paysitemanagerutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteTable(t *testing.T) {
	ids := map[string]bool{}
	bases := map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.SiteID] {
			t.Errorf("duplicate site id %q", cfg.SiteID)
		}
		if bases[cfg.SiteBase] {
			t.Errorf("duplicate site base %q", cfg.SiteBase)
		}
		ids[cfg.SiteID] = true
		bases[cfg.SiteBase] = true
		if cfg.StudioName == "" {
			t.Errorf("%s: empty studio name", cfg.SiteID)
		}
		got, err := scraper.ForURL(cfg.SiteBase + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.SiteBase, err)
			continue
		}
		if got.ID() != cfg.SiteID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.SiteBase, got.ID(), cfg.SiteID)
		}
	}
}

func TestSitesDoNotClaimEachOther(t *testing.T) {
	first := paysitemanagerutil.New(sites[0])
	for _, cfg := range sites[1:] {
		if first.MatchesURL(cfg.SiteBase + "/") {
			t.Errorf("%s matched %s", sites[0].SiteID, cfg.SiteBase)
		}
	}
}

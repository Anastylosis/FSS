package gayroom

import (
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/fycutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteCount(t *testing.T) {
	if len(sites) != 14 {
		t.Errorf("expected 14 sites, got %d", len(sites))
	}
}

func TestSitesAreRegisteredAndDistinct(t *testing.T) {
	ids := map[string]bool{}
	domains := map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.SiteID] {
			t.Errorf("duplicate SiteID: %s", cfg.SiteID)
		}
		if domains[cfg.Domain] {
			t.Errorf("duplicate domain: %s", cfg.Domain)
		}
		ids[cfg.SiteID] = true
		domains[cfg.Domain] = true
		if cfg.StudioName == "" {
			t.Errorf("%s: empty studio name", cfg.SiteID)
		}
		if strings.ToLower(cfg.SiteID) != cfg.SiteID {
			t.Errorf("%s: site id must be lowercase", cfg.SiteID)
		}
		if _, err := scraper.ForID(cfg.SiteID); err != nil {
			t.Errorf("ForID(%q): %v", cfg.SiteID, err)
		}
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := fycutil.New(sites[0]) // gayroom.com
	if !s.MatchesURL("https://gayroom.com/") || !s.MatchesURL("https://www.gayroom.com/models/some-guy") {
		t.Error("gayroom.com should match")
	}
	if s.MatchesURL("https://manroyale.com/") {
		t.Error("a sibling site must not match")
	}
	if s.MatchesURL("https://notgayroom.com/") {
		t.Error("a look-alike host must not match")
	}
}

func TestEveryNetworkURLResolvesToItsOwnScraper(t *testing.T) {
	for _, cfg := range sites {
		got, err := scraper.ForURL("https://" + cfg.Domain + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.Domain, err)
			continue
		}
		if got.ID() != cfg.SiteID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.Domain, got.ID(), cfg.SiteID)
		}
	}
}

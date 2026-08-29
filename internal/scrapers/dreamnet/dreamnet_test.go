package dreamnet

import (
	"strings"
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
		if cfg.Studio == "" || cfg.SiteBase == "" || cfg.MatchRe == nil {
			t.Errorf("%s: incomplete config %+v", cfg.ID, cfg)
		}
		// Each site mounts the same tour under its own prefix; that prefix is
		// the only per-site difference and must be reflected in the patterns.
		if cfg.TourPrefix != "" && !strings.Contains(strings.Join(cfg.Patterns, " "), cfg.TourPrefix+"/categories/") {
			t.Errorf("%s: patterns %v do not mention the %q prefix", cfg.ID, cfg.Patterns, cfg.TourPrefix)
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
	if len(sites) != 4 {
		t.Errorf("expected 4 sites, got %d", len(sites))
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := adultdoorwayclassicutil.New(sites[0]) // blowbanggirls
	for _, u := range []string{"https://blowbanggirls.com/", "https://www.blowbanggirls.com/v3/", "http://blowbanggirls.com"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://blowbanggirls.net/", "https://notblowbanggirls.com/", "https://example.com/blowbanggirls.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// girls.dreamnet.com is a subdomain, so its matcher must not accept the bare
// dreamnet.com — that host is a WordPress support site with no catalogue.
func TestSubdomainSiteDoesNotClaimTheApex(t *testing.T) {
	var girls adultdoorwayclassicutil.SiteConfig
	for _, cfg := range sites {
		if cfg.ID == "girlsdreamnet" {
			girls = cfg
		}
	}
	s := adultdoorwayclassicutil.New(girls)
	if !s.MatchesURL("https://girls.dreamnet.com/tour/") {
		t.Error("its own host should match")
	}
	if s.MatchesURL("https://www.dreamnet.com/") || s.MatchesURL("https://dreamnet.com/") {
		t.Error("the apex must not match")
	}
}

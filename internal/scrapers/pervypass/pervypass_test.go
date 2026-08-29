package pervypass

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteTable(t *testing.T) {
	ids := map[string]bool{}
	for _, cfg := range sites {
		if ids[cfg.SiteID] {
			t.Errorf("duplicate site id %q", cfg.SiteID)
		}
		ids[cfg.SiteID] = true
		if cfg.StudioName == "" || cfg.Domain == "" {
			t.Errorf("%s: incomplete config %+v", cfg.SiteID, cfg)
		}
		if cfg.TourPrefix != "/tour" {
			t.Errorf("%s: TourPrefix = %q, want /tour", cfg.SiteID, cfg.TourPrefix)
		}
		got, err := scraper.ForURL("https://www." + cfg.Domain + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.Domain, err)
			continue
		}
		if got.ID() != cfg.SiteID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.Domain, got.ID(), cfg.SiteID)
		}
	}
	if len(sites) != 3 {
		t.Errorf("expected 3 sites, got %d", len(sites))
	}
}

func TestAliasResolvesToItsSite(t *testing.T) {
	// justpov.com redirects to the pawged tour, so it is an alias.
	got, err := scraper.ForURL("https://www.justpov.com/tour/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "pawged" {
		t.Errorf("ForURL(justpov) = %s, want pawged", got.ID())
	}
}

func TestNewFor(t *testing.T) {
	if newFor("onlybbc") == nil {
		t.Error("newFor(onlybbc) = nil")
	}
	if newFor("nope") != nil {
		t.Error("newFor of an unknown id should be nil")
	}
}

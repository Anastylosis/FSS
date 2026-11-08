package grooby

import "testing"

func TestSiteCount(t *testing.T) {
	if len(sites) != 46 {
		t.Errorf("got %d sites, want 46", len(sites))
	}
}

func TestUniqueSiteIDs(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range sites {
		if seen[s.SiteID] {
			t.Errorf("duplicate site ID: %s", s.SiteID)
		}
		seen[s.SiteID] = true
	}
}

func TestUniqueDomains(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range sites {
		if seen[s.Domain] {
			t.Errorf("duplicate domain: %s", s.Domain)
		}
		seen[s.Domain] = true
	}
}

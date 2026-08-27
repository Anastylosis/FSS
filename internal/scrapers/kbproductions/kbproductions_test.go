package kbproductions

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/paysiteutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestMatchesURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://melina-may.com/", "melinamay"},
		{"https://www.passionpov.com/videos", "passionpov"},
		{"https://shehergirls.com", "shehergirls"},
		{"https://vrallure.com/videos", "vrallure"},
		{"https://www.manpuppy.com/", "manpuppy"},
		{"https://milflicious.com/videos", "milflicious"},
	}
	for _, tt := range tests {
		found := false
		for _, cfg := range sites {
			s := paysiteutil.New(cfg)
			if s.MatchesURL(tt.url) {
				if s.ID() != tt.want {
					t.Errorf("MatchesURL(%q) matched %q, want %q", tt.url, s.ID(), tt.want)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no scraper matched %q", tt.url)
		}
	}
}

// Domain-keyed config table — see testutil.CheckSiteDomainTable.
func TestSiteTableIntegrity(t *testing.T) {
	rows := make([]testutil.DomainRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.DomainRow{ID: c.SiteID, Domain: c.Domain, Studio: c.StudioName})
	}
	testutil.CheckSiteDomainTable(t, rows)
}

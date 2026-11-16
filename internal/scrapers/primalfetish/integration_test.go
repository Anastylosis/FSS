//go:build integration

package primalfetish

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestLiveScrape(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		wantSiteID string
	}{
		{"taboo", "https://primalfetishnetwork.com/paysite/primals-taboo-relations/", "primalstaboofamily"},
		{"cosplay", "https://primalfetishnetwork.com/paysite/cosplay/", "primalscosplay"},
		{"wrestling", "https://primalfetishnetwork.com/paysite/primals-wrestling-sex/", "primalswrestlingsex"},
		{"eroticmassage", "https://primalfetishnetwork.com/paysite/erotic-massage-master/", "primaleroticmassage"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := scraper.ForURL(tc.url)
			if err != nil {
				t.Fatalf("ForURL(%s): %v", tc.url, err)
			}
			if s.ID() != tc.wantSiteID {
				t.Fatalf("ForURL matched %q, want %q", s.ID(), tc.wantSiteID)
			}
			testutil.RunLiveScrape(t, s, tc.url, 3)
		})
	}
}

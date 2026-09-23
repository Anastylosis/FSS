//go:build integration

package peterskingdom

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		t.Run(cfg.SiteID, func(t *testing.T) {
			s := New(cfg)
			testutil.RunLiveScrape(t, s, s.base+"/", 3)
		})
	}
}

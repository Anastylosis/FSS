//go:build integration

package dreamnet

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/adultdoorwayclassicutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// The tour path, not the bare domain, is the URL under test: these sites put
// an age splash on the root, so a probe of the root cannot tell a CMS outage
// from an empty catalogue. The scraper accepts either spelling.
func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		cfg := cfg
		t.Run(cfg.ID, func(t *testing.T) {
			testutil.RunLiveScrape(t, adultdoorwayclassicutil.New(cfg), cfg.SiteBase+cfg.TourPrefix+"/", 2)
		})
	}
}

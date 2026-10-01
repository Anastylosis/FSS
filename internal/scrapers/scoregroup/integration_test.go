//go:build integration

package scoregroup

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrapeAll(t *testing.T) {
	// All 93 sites sit behind one CDN, which resets HTTP/2 streams when the
	// whole table runs at -parallel's default width — ~40 of them skipped as
	// "site could not be reached" while each returned 200 on its own.
	gate := testutil.NewGate(4)
	for _, cfg := range sites {
		t.Run(cfg.SiteID, func(t *testing.T) {
			t.Parallel()
			gate.Enter(t)
			s := newScraper(cfg)
			testutil.RunLiveScrape(t, s, cfg.SiteBase+"/", 2)
		})
	}
}

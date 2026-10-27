//go:build integration

package dreamboyplus

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		u := "https://dreamboyplus.com/"
		if cfg.Domain != "" {
			u = "https://www." + cfg.Domain + "/"
		}
		t.Run(cfg.SiteID, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(cfg), u, 2)
		})
	}
}

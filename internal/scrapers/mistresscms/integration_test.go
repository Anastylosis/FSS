//go:build integration

package mistresscms

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		t.Run(cfg.SiteID, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(cfg), "https://www."+cfg.Domain+"/", 3)
		})
	}
}

//go:build integration

package ladyboygold

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/natscmsutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveLadyboyGold(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://ladyboygold.com", 3)
}

func TestLiveSiblings(t *testing.T) {
	for _, cfg := range siblings {
		cfg := cfg
		t.Run(cfg.ID, func(t *testing.T) {
			testutil.RunLiveScrape(t, natscmsutil.New(withDefaults(cfg)), cfg.SiteBase+"/", 2)
		})
	}
}

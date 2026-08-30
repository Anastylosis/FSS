//go:build integration

package brandnewamateurs

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/adultdoorwayclassicutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		cfg := cfg
		t.Run(cfg.ID, func(t *testing.T) {
			testutil.RunLiveScrape(t, adultdoorwayclassicutil.New(cfg), cfg.SiteBase+"/", 2)
		})
	}
}

//go:build integration

package paysitemanager

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/paysitemanagerutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		cfg := cfg
		t.Run(cfg.SiteID, func(t *testing.T) {
			testutil.RunLiveScrape(t, paysitemanagerutil.New(cfg), cfg.SiteBase+"/", 3)
		})
	}
}

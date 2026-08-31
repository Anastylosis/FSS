//go:build integration

package mmpnetwork

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, cfg := range sites {
		cfg := cfg
		t.Run(cfg.SiteID, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(cfg), "https://"+cfg.Domain+"/", 3)
		})
	}
}

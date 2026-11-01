//go:build integration

package mooduniverse

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMoodUniverse(t *testing.T) {
	for _, cfg := range sites {
		t.Run(cfg.SiteID, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(cfg), "https://"+cfg.Domain+"/movies.php", 3)
		})
	}
}

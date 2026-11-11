//go:build integration

package dollshub

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrapeAll(t *testing.T) {
	for _, cfg := range sites {
		t.Run(cfg.SiteID, func(t *testing.T) {
			t.Parallel()
			testutil.RunLiveScrape(t, New(cfg), "https://"+cfg.Domain+"/scenes", 3)
		})
	}
}

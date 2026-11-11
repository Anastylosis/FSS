//go:build integration

package only3xnetwork

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxplaylistutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrapeAll(t *testing.T) {
	for _, cfg := range sites {
		t.Run(cfg.SiteID, func(t *testing.T) {
			t.Parallel()
			testutil.RunLiveScrape(t, elxplaylistutil.New(cfg), "https://"+cfg.Domain+"/categories/movies.html", 3)
		})
	}
}

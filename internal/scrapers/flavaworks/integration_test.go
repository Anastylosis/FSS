//go:build integration

package flavaworks

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

// A model page is the same grid filtered, and the cross-promo cards on it
// point at another site and must not be collected.
func TestLiveScrapeModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://www.thugboy.com/model/263", 3)
}

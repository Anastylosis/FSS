//go:build integration

package ukcentrefolds

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveUKCentrefolds(t *testing.T) {
	urls := map[string]string{
		"staceypoole":         "https://staceypoole.co.uk/",
		"alicebrookes":        "https://www.alicebrookes.com/",
		"melissadebling":      "https://melissad.co.uk/",
		"glamourvilla":        "https://www.glamourvilla.com/",
		"officialsammybraddy": "https://www.officialsammybraddy.com/",
		"officialrae":         "https://www.officialrae.com/",
	}
	for _, cfg := range sites {
		t.Run(cfg.id, func(t *testing.T) {
			testutil.RunLiveScrape(t, newScraper(cfg), urls[cfg.id], 3)
		})
	}
}

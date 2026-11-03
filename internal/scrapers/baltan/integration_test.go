//go:build integration

package baltan

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveBaltan(t *testing.T) {
	for _, c := range sites {
		t.Run(c.id, func(t *testing.T) {
			testutil.RunLiveScrape(t, newScraper(c), "https://"+c.domain+"/items/search", 3)
		})
	}
}

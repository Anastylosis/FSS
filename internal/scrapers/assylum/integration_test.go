//go:build integration

package assylum

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

// A single session URL skips the id sweep, which is the cheap live check.
func TestLiveAssylumSession(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.assylum.com/session//780", 1)
}

// The full sweep probes every id up to the highest one the index views reach.
func TestLiveAssylum(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.assylum.com/", 2)
}

func TestLiveSlavemouth(t *testing.T) {
	testutil.RunLiveScrape(t, newFor(sites[1]), "https://www.slavemouth.com/", 2)
}

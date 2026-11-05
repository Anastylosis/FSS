//go:build integration

package asianstreetmeat

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveAsianStreetMeat(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://asianstreetmeat.com/", 3)
}

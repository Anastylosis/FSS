//go:build integration

package velvetveronica

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveVelvetVeronica(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://velvetveronica.com/", 3)
}

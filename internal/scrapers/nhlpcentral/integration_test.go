//go:build integration

package nhlpcentral

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveNHLPCentral(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://nhlpcentral.com", 3)
}

func TestLiveNHLPCentralModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://nhlpcentral.com/bio.php?name=Chloe%20Toy", 3)
}

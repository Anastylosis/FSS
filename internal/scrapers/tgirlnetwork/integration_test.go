//go:build integration

package tgirlnetwork

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveTGirlNetwork(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.tgirl-network.com/", 2)
}

// A solo site is scraped through its network model page.
func TestLiveTGirlNetworkSoloSite(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.mandy-mitchell.com/", 2)
}
